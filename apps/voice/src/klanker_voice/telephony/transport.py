"""``TelephonyTransport(BaseTransport)`` -- the offline "klanker-voice"
telephony transport (spec Phase B, D-05/D-06/D-07/D-08).

Mirrors the browser transport's three-class shape (PATTERNS "transport.py"
section -- the WebRTC transport pipecat ships) on pipecat's
``BaseInputTransport``/``BaseOutputTransport``: this is a transport-specific
leaf module that produces a ``BaseTransport`` exactly as ``server.py``
produces its WebRTC transport for the browser path -- it NEVER branches
shared code (``call_runtime.py``/``pipeline.py``/``factories.py``/
``server.py``/``webrtc.py`` stay byte-unchanged; the ONLY new value threaded
into ``create_call_session`` is ``channel="pstn"``).

Pipeline sample rates (D-06 seam)
----------------------------------
The existing cascade pipeline (``pipeline.build_pipeline`` run inside a
``PipelineWorker``) uses pipecat's own ``PipelineParams``/``StartFrame``
defaults -- ``audio_in_sample_rate=16000`` / ``audio_out_sample_rate=24000``
-- and the browser WebRTC transport (``server.py``) never overrides either
value, so these two constants genuinely ARE "the pipeline rate", not a
telephony-specific choice. ``TelephonyTransport`` targets them
explicitly (:data:`PIPELINE_INPUT_SAMPLE_RATE` / :data:`PIPELINE_OUTPUT_SAMPLE_RATE`)
so Deepgram/ElevenLabs see byte-identical sample-rate metadata whether the
caller is a browser or a phone.

Resampling happens exactly ONCE per direction, at the 8 kHz PCMU boundary,
with one stateful ``SOXRStreamAudioResampler`` instance per direction
(``pipecat.audio.utils.create_stream_resampler``, ``clear_after_secs=None``
-- telephony's irregular packet gaps must not trigger a stale-history clear,
per the resampler's own docstring) -- D-06:

- **Input** (:class:`TelephonyInputTransport._receive_audio`): resamples
  8000 Hz -> ``PIPELINE_INPUT_SAMPLE_RATE`` (16000) BEFORE constructing the
  ``InputAudioRawFrame`` -- the frame's ``sample_rate`` MUST equal the
  resampled rate (spec Sec9 Deepgram), never the literal 8000.
- **Output** (:class:`TelephonyOutputTransport.write_audio_frame`):
  resamples ``PIPELINE_OUTPUT_SAMPLE_RATE`` (24000) -> 8000 itself. The
  pydantic ``TransportParams.audio_out_sample_rate`` is set to 24000 (the
  pipeline rate, matching the TTS's own output), so the base class's own
  ``MediaSender.handle_audio_frame`` resample (``frame.sample_rate ->
  self._sample_rate``) is a documented no-op (both sides are 24000 --
  ``SOXRStreamAudioResampler.resample`` short-circuits when ``in_rate ==
  out_rate``) and the ONLY real down-resample is the one this module
  performs in ``write_audio_frame`` -- this satisfies D-06 "resample once,
  at the boundary" without fighting the base class's own resample-on-ingest
  step (see PATTERNS "Critical Notes" / the "two distinct params objects"
  and "resample once" notes).

Interruption flushing (D-07, spec Sec10)
-----------------------------------------
``BaseOutputTransport`` does NOT expose a directly-overridable
``handle_interruptions`` method on the transport class itself -- that hook
lives on its private inner ``MediaSender`` (dispatched from
``process_frame``'s ``InterruptionFrame`` branch via ``_handle_frame``, per
the installed pipecat 1.5.0 source). ``TelephonyOutputTransport`` therefore
overrides ``process_frame`` to detect ``InterruptionFrame`` AFTER delegating
to ``super().process_frame(...)`` (so the base class's own queued-audio
reset / bot-stopped-speaking bookkeeping still runs first, exactly as D-07
requires), then flushes this module's own outbound telephony buffer (the
queued-but-unsent PCM tail not yet framed/packetized) -- NO second
VAD/endpointing system is added; the existing pipeline ``InterruptionFrame``
(Deepgram/Flux turn strategy) stays fully authoritative. This is a
documented precision-fix over the plan's literal "override
handle_interruptions" phrasing -- see 10-02-SUMMARY.md Deviations.

Lifecycle / terminal close (D-08, spec Sec6.8)
------------------------------------------------
``TelephonyTransport`` registers exactly ``on_client_connected`` /
``on_client_disconnected`` -- the two event names ``create_call_session``
already wires unchanged (Phase 9). ``on_client_connected`` fires once, from
``TelephonyInputTransport.start`` right after ``set_transport_ready`` (this
is also what lets ``register_greet_first`` fire). ``on_client_disconnected``
fires exactly once -- the natural "terminal close" signal for an offline
telephony leg is the RTP media stream itself ending (``media.read_packet()``
returns ``None``, the Phase 11 socket-backed analog of the far end hanging
up) -- and maps unchanged to ``lifecycle.on_transport_disconnected``.
Telephony close is terminal, with NO browser-style reconnect grace added
here (that stays a WebRTC-only concern, Phase 9 D-03/D-06 -- this transport
does not special-case reconnection at all). ``stop()``/``cancel()``/
``cleanup()`` on each processor all route through one idempotent
``_teardown()``.

Deferred to a Phase-11 live eval: the live Deepgram-transcribes ->
ElevenLabs-responds round trip needs real API keys + network and is
explicitly NOT an offline gate (spec Sec19-B; ROADMAP Phase 10 SC4 scope
call -- see 10-02-SUMMARY.md).
"""

from __future__ import annotations

import random

from pipecat.audio.utils import create_stream_resampler
from pipecat.frames.frames import (
    CancelFrame,
    EndFrame,
    Frame,
    InputAudioRawFrame,
    InterruptionFrame,
    OutputAudioRawFrame,
    StartFrame,
)
from pipecat.processors.frame_processor import FrameDirection
from pipecat.transports.base_input import BaseInputTransport
from pipecat.transports.base_output import BaseOutputTransport
from pipecat.transports.base_transport import BaseTransport, TransportParams

from klanker_voice.telephony.media import (
    PcmFramer,
    RtpDepacketizer,
    RtpPacketizer,
    parse_rtp,
    ulaw_decode,
    ulaw_encode,
)
from klanker_voice.telephony.types import RtpMediaSession, TelephonyTransportParams

#: The cascade pipeline's own input/output sample rates (pipecat
#: ``PipelineParams``/``StartFrame`` defaults, unchanged by the browser
#: (``SmallWebRTCTransport``) path -- see module docstring "Pipeline sample
#: rates").
PIPELINE_INPUT_SAMPLE_RATE = 16000
PIPELINE_OUTPUT_SAMPLE_RATE = 24000


class TelephonyInputTransport(BaseInputTransport):
    """RTP PCMU -> pipeline-rate ``InputAudioRawFrame`` (D-05 input path).

    Owns the depacketizer + the ONE stateful input-direction resampler
    (D-06). ``on_ready``/``on_media_end`` are callbacks supplied by the
    owning :class:`TelephonyTransport` so connect/disconnect can be fired
    exactly once from the single place that actually observes readiness
    (``start()``) and stream-end (the receive loop exhausting) -- the outer
    transport owns the fire-once guards (D-08).
    """

    def __init__(
        self,
        media: RtpMediaSession,
        params: TelephonyTransportParams,
        pipecat_params: TransportParams,
        *,
        on_ready,
        on_media_end,
        **kwargs,
    ) -> None:
        super().__init__(pipecat_params, **kwargs)
        self._media = media
        self._telephony_params = params
        self._on_ready = on_ready
        self._on_media_end = on_media_end
        self._depacketizer = RtpDepacketizer(params=params)
        # One stateful resampler for this direction, fixed 8000->pipeline-rate
        # pair (D-06) -- constructed once, never per-frame.
        self._input_resampler = create_stream_resampler(clear_after_secs=None)
        self._receive_task = None
        self._started = False
        self._torn_down = False

    async def start(self, frame: StartFrame) -> None:
        await super().start(frame)
        if self._started:
            return
        self._started = True
        # D-05: on_client_connected becomes fire-once ready here -- this is
        # what lets register_greet_first fire once the media path is ready.
        await self.set_transport_ready(frame)
        await self._on_ready()
        if self._params.audio_in_enabled and self._receive_task is None:
            self._receive_task = self.create_task(self._receive_audio())

    async def _receive_audio(self) -> None:
        """Read RTP -> depacketize -> mu-law decode -> resample -> push.

        Never raises on malformed input (T-10-04): ``parse_rtp`` returns
        ``None`` for a bad datagram and it is simply skipped. When the media
        session is exhausted (``read_packet()`` returns ``None`` -- the
        offline analog of the far end hanging up), this is the terminal
        close signal: fire ``on_client_disconnected`` exactly once (D-08).
        """
        while True:
            packet = await self._media.read_packet()
            if packet is None:
                break
            parsed = parse_rtp(packet)
            if parsed is None:
                continue  # malformed datagram -- skip, never raise (T-10-04)
            for ulaw_payload in self._depacketizer.process(parsed):
                pcm = ulaw_decode(ulaw_payload)
                resampled = await self._input_resampler.resample(
                    pcm, self._telephony_params.clock_rate, PIPELINE_INPUT_SAMPLE_RATE
                )
                await self.push_audio_frame(
                    InputAudioRawFrame(
                        audio=resampled,
                        num_channels=1,
                        sample_rate=PIPELINE_INPUT_SAMPLE_RATE,
                    )
                )
        await self._on_media_end()

    async def _teardown(self) -> None:
        if self._torn_down:
            return
        self._torn_down = True
        if self._receive_task is not None:
            await self.cancel_task(self._receive_task)
            self._receive_task = None
        await self._media.close()

    async def stop(self, frame: EndFrame) -> None:
        await super().stop(frame)
        await self._teardown()

    async def cancel(self, frame: CancelFrame) -> None:
        await super().cancel(frame)
        await self._teardown()

    async def cleanup(self) -> None:
        await self._teardown()
        await super().cleanup()


class TelephonyOutputTransport(BaseOutputTransport):
    """Pipeline-rate ``OutputAudioRawFrame`` -> RTP PCMU (D-05/D-06 output).

    Owns the ONE stateful output-direction resampler, the RTP packetizer,
    and the shallow (single-frame-buffer) ``PcmFramer`` this module treats
    as the flushable outbound "queue" for D-07 (a full audio_out_10ms_chunks
    buffer at the base-class layer is already only 40ms at the default
    ``audio_out_10ms_chunks=4`` -- comfortably inside the 20-60ms D-07
    target; this module adds no further buffering beyond the framer's
    sub-160-sample tail).
    """

    def __init__(
        self,
        media: RtpMediaSession,
        params: TelephonyTransportParams,
        pipecat_params: TransportParams,
        **kwargs,
    ) -> None:
        super().__init__(pipecat_params, **kwargs)
        self._media = media
        self._telephony_params = params
        # One stateful resampler for this direction, fixed pipeline-rate->8000
        # pair (D-06) -- constructed once, never per-frame.
        self._output_resampler = create_stream_resampler(clear_after_secs=None)
        self._framer = PcmFramer(samples_per_packet=params.samples_per_packet)
        self._packetizer = RtpPacketizer(ssrc=random.getrandbits(32), params=params)
        self._torn_down = False

    async def start(self, frame: StartFrame) -> None:
        await super().start(frame)
        await self.set_transport_ready(frame)

    async def write_audio_frame(self, frame: OutputAudioRawFrame) -> bool:
        """The one sink override (D-05/D-06): resample once at the 8 kHz
        boundary, mu-law encode per 160-sample frame, RTP packetize
        (seq/ts/SSRC/payload-type all owned by ``RtpPacketizer``), write."""
        pcm_8k = await self._output_resampler.resample(
            frame.audio, frame.sample_rate, self._telephony_params.clock_rate
        )
        for pcm_frame in self._framer.push(pcm_8k):
            ulaw_frame = ulaw_encode(pcm_frame)
            rtp_packet = self._packetizer.packetize(ulaw_frame)
            await self._media.write_packet(rtp_packet)
        return True

    async def flush(self) -> None:
        """D-07: drop any buffered-but-incomplete outbound PCM tail so stale
        audio doesn't bleed into the caller's next turn after a barge-in.
        The framer is stateful across ``write_audio_frame`` calls (D-02);
        replacing it discards exactly that incomplete tail (never-yet-sent
        audio) with no effect on already-written RTP."""
        self._framer = PcmFramer(samples_per_packet=self._telephony_params.samples_per_packet)

    async def handle_interruptions(self, frame: InterruptionFrame) -> None:
        """D-07 flush hook, invoked from :meth:`process_frame` below.

        Installed pipecat 1.5.0 has NO directly-overridable
        ``handle_interruptions`` on ``BaseOutputTransport`` itself -- that
        hook lives on the private per-destination ``MediaSender`` and is
        already invoked (with its own queued-audio reset / bot-stopped-
        speaking bookkeeping) from inside ``super().process_frame(...)``
        below, BEFORE this method runs. This method only extends that with
        the telephony-specific flush (this module's own outbound PCM tail).
        No extra turn-detection/endpointing logic is introduced -- it only
        reacts to the ``InterruptionFrame`` the pipeline already emits.
        """
        await self.flush()

    async def process_frame(self, frame: Frame, direction: FrameDirection) -> None:
        """Delegate to the base class first (D-07: its own queued-audio
        reset / bot-stopped-speaking bookkeeping for this InterruptionFrame
        runs there), then run the telephony-specific flush hook. No extra
        turn-detection/endpointing logic is introduced here -- this only
        reacts to the ``InterruptionFrame`` the pipeline already emits."""
        await super().process_frame(frame, direction)
        if isinstance(frame, InterruptionFrame):
            await self.handle_interruptions(frame)

    async def _teardown(self) -> None:
        if self._torn_down:
            return
        self._torn_down = True
        await self._media.close()

    async def stop(self, frame: EndFrame) -> None:
        await super().stop(frame)
        await self._teardown()

    async def cancel(self, frame: CancelFrame) -> None:
        await super().cancel(frame)
        await self._teardown()

    async def cleanup(self) -> None:
        await self._teardown()
        await super().cleanup()


class TelephonyTransport(BaseTransport):
    """Pipecat-compatible ``BaseTransport`` for an offline telephony call
    (D-05). ``input()``/``output()`` are lazily constructed and cached,
    exactly mirroring the browser WebRTC transport's shape, so the graph
    ``build_pipeline`` assembles genuinely begins at ``transport.input()``
    and ends at ``transport.output()`` -- the seam this phase proves offline
    via ``create_call_session(channel="pstn")``.

    Registers EXACTLY the two event names ``create_call_session`` already
    wires (``on_client_connected``/``on_client_disconnected``) so the
    existing Phase 9 lifecycle wiring works completely UNCHANGED.
    """

    def __init__(
        self,
        *,
        call_id: str,
        media: RtpMediaSession,
        params: TelephonyTransportParams,
        input_name: str | None = None,
        output_name: str | None = None,
    ) -> None:
        super().__init__(input_name=input_name, output_name=output_name)
        self.call_id = call_id
        self._media = media
        self._telephony_params = params
        # The audio-enable-flags container the In/Out processors need --
        # distinct from TelephonyTransportParams (PATTERNS "two distinct
        # params objects"). Rates are the pipeline's own (module docstring
        # "Pipeline sample rates").
        self._pipecat_params = TransportParams(
            audio_in_enabled=True,
            audio_out_enabled=True,
            audio_in_sample_rate=PIPELINE_INPUT_SAMPLE_RATE,
            audio_out_sample_rate=PIPELINE_OUTPUT_SAMPLE_RATE,
        )
        self._input: TelephonyInputTransport | None = None
        self._output: TelephonyOutputTransport | None = None
        self._connected_fired = False
        self._disconnected_fired = False
        self._register_event_handler("on_client_connected")
        self._register_event_handler("on_client_disconnected")

    def input(self) -> TelephonyInputTransport:
        if self._input is None:
            self._input = TelephonyInputTransport(
                self._media,
                self._telephony_params,
                self._pipecat_params,
                on_ready=self._fire_connected_once,
                on_media_end=self._fire_disconnected_once,
                name=self._input_name,
            )
        return self._input

    def output(self) -> TelephonyOutputTransport:
        if self._output is None:
            self._output = TelephonyOutputTransport(
                self._media,
                self._telephony_params,
                self._pipecat_params,
                name=self._output_name,
            )
        return self._output

    async def _fire_connected_once(self) -> None:
        if self._connected_fired:
            return
        self._connected_fired = True
        await self._call_event_handler("on_client_connected", None)

    async def _fire_disconnected_once(self) -> None:
        if self._disconnected_fired:
            return
        self._disconnected_fired = True
        await self._call_event_handler("on_client_disconnected", None)

    async def flush_output_audio(self) -> None:
        """D-07 signature (spec Sec10): flush the queued outbound telephony
        audio on caller barge-in. A no-op if ``output()`` was never
        constructed (nothing has been sent yet)."""
        if self._output is not None:
            await self._output.flush()
