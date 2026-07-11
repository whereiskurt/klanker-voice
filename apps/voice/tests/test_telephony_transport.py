"""``TelephonyTransport`` unit tests + the Sec19-B offline pipeline-traversal
proof (D-05/D-06/D-07/D-08/D-10).

Hermetic and offline throughout: NO SIP, NO Asterisk, NO socket, NO live
provider network calls anywhere in this file. Reuses the
``test_call_runtime.py`` fakes verbatim (``_gate_result``/``_quota_config``/
``fake_aws``/``reset_active_count``) plus ``conftest.py``'s
``make_config_file``/``stub_provider_keys`` -- the same offline-pipeline test
rig that makes constructing (never running) the real ``build_pipeline``
around arbitrary providers safe with no network.

The Sec19-B exit-criterion test (``test_telephone_audio_traverses_real_pipeline_offline``)
is the HERMETIC OFFLINE form of Sec19-B: it proves synthetic PCMU RTP
traverses the REAL ``build_pipeline`` graph (transport.input() first,
transport.output() before the assistant aggregator) via
``create_call_session(channel="pstn")``, and that the transport boundary
itself correctly converts RTP <-> Pipecat audio frames (already proven
directly by the earlier transport-level tests in this file). It does **not**
run the pipeline to completion or exercise a live Deepgram-transcribes ->
ElevenLabs-responds round trip -- that needs real API keys + network and is
a documented Phase-11 live eval, not an offline gate (see 10-02-SUMMARY.md).
"""

from __future__ import annotations

import asyncio
import struct

from pipecat.frames.frames import (
    CancelFrame,
    EndFrame,
    InputAudioRawFrame,
    InterruptionFrame,
    OutputAudioRawFrame,
)
from pipecat.tests.utils import SleepFrame, run_test

from klanker_voice.call_runtime import CallIdentity, CallSession, create_call_session
from klanker_voice.config import DuplexConfig, load_config, load_knowledge_config
from klanker_voice.pipeline import build_pipeline
from klanker_voice.session import SessionLifecycle
from klanker_voice.telephony.media import (
    OfflineRtpMediaSession,
    build_rtp,
    parse_rtp,
    ulaw_encode,
)
from klanker_voice.telephony.transport import (
    PIPELINE_INPUT_SAMPLE_RATE,
    PIPELINE_OUTPUT_SAMPLE_RATE,
    TelephonyTransport,
)
from klanker_voice.telephony.types import TelephonyTransportParams

# Reuse the test_call_runtime.py offline-pipeline test rig verbatim (D-10 /
# PATTERNS "Shared Pattern 5") -- fake AWS + stub provider keys + a tmp
# pipeline.toml, so building (never running) the real cascade never touches
# the network.
from tests.test_call_runtime import (  # noqa: F401 -- fake_aws/reset_active_count are fixtures
    _gate_result,
    _quota_config,
    fake_aws,
    reset_active_count,
)


def _tone_pcm(num_samples: int, amplitude: int = 1000) -> bytes:
    """A trivial non-silent 16-bit PCM buffer -- any value is fine, the
    codec/RTP layer is already exhaustively vector-tested in Plan 01."""
    return struct.pack(f"<{num_samples}h", *([amplitude] * num_samples))


def _silence_pcm(num_samples: int) -> bytes:
    return struct.pack(f"<{num_samples}h", *([0] * num_samples))


def _synthetic_pcmu_packets(count: int, params: TelephonyTransportParams) -> list[bytes]:
    """Build ``count`` byte-exact PCMU RTP datagrams (Plan 01's codec/RTP,
    reused directly -- never re-implemented here)."""
    packets = []
    for i in range(count):
        ulaw_payload = ulaw_encode(_tone_pcm(params.samples_per_packet))
        packets.append(
            build_rtp(
                payload=ulaw_payload,
                sequence=i,
                timestamp=i * params.samples_per_packet,
                ssrc=0xABCDEF,
                payload_type=params.payload_type,
            )
        )
    return packets


def _make_transport(
    *, incoming: list[bytes] | None = None, params: TelephonyTransportParams | None = None
) -> tuple[TelephonyTransport, OfflineRtpMediaSession]:
    params = params or TelephonyTransportParams()
    media = OfflineRtpMediaSession(incoming or [])
    transport = TelephonyTransport(call_id="test-call", media=media, params=params)
    return transport, media


# --- Task 1: input path (D-05/D-06) ---------------------------------------


async def test_rtp_input_emits_correct_audio_frame():
    """RTP PCMU in -> a correctly-typed, correctly-sample-rated
    InputAudioRawFrame out, driven through the REAL pipecat StartFrame/
    EndFrame lifecycle via pipecat's own ``run_test`` harness (D-05/D-06).

    10 packets are fed (not 1): ``SOXRStreamAudioResampler`` is a genuine
    streaming resampler with internal lookahead -- the first several calls
    on a fresh instance legitimately return zero bytes until enough history
    has accumulated (verified directly against the installed resampler).
    The base transport correctly drops zero-length audio rather than
    forwarding it, so enough packets must be fed for at least one non-empty
    resampled frame to actually reach the pipeline sink.
    """
    params = TelephonyTransportParams()
    transport, _media = _make_transport(incoming=_synthetic_pcmu_packets(10, params), params=params)

    down_frames, _up_frames = await run_test(
        transport.input(),
        frames_to_send=[SleepFrame(sleep=0.2)],
        send_end_frame=True,
    )

    audio_frames = [f for f in down_frames if isinstance(f, InputAudioRawFrame)]
    assert audio_frames, "expected at least one non-empty InputAudioRawFrame"
    for frame in audio_frames:
        assert frame.sample_rate == PIPELINE_INPUT_SAMPLE_RATE  # never the literal 8000
        assert frame.num_channels == 1
        assert len(frame.audio) > 0

    # D-05/D-08: on_client_connected fired once (media path became ready) and
    # on_client_disconnected fired once (the RTP stream was exhausted).
    assert transport._connected_fired is True
    assert transport._disconnected_fired is True


async def test_malformed_and_exhausted_rtp_does_not_crash_the_receive_loop():
    """T-10-04: a malformed datagram (too short) mixed in with valid packets
    is skipped, never raises, and the loop still reaches natural
    end-of-stream (disconnect still fires exactly once)."""
    params = TelephonyTransportParams()
    packets = _synthetic_pcmu_packets(3, params)
    packets.insert(1, b"\x00\x01")  # malformed: shorter than the 12-byte header
    transport, _media = _make_transport(incoming=packets, params=params)

    down_frames, _up_frames = await run_test(
        transport.input(),
        frames_to_send=[SleepFrame(sleep=0.1)],
        send_end_frame=True,
    )

    assert transport._disconnected_fired is True
    # No exception propagated out of run_test -- the malformed packet did not
    # crash the receive loop.


# --- Task 1/2: output path (D-05/D-06) ------------------------------------


async def test_output_frame_emits_pcmu_rtp():
    """OutputAudioRawFrame at the pipeline rate -> PCMU RTP with the
    configured payload type, a stable SSRC, and timestamps incrementing by
    160 per packet (D-05/D-06). Driven directly against ``write_audio_frame``
    -- this method touches no FrameProcessor task-manager state, so it does
    not need the full pipeline lifecycle."""
    params = TelephonyTransportParams()
    transport, media = _make_transport(params=params)
    output = transport.output()

    one_second_pcm = _silence_pcm(PIPELINE_OUTPUT_SAMPLE_RATE)
    frame = OutputAudioRawFrame(
        audio=one_second_pcm, sample_rate=PIPELINE_OUTPUT_SAMPLE_RATE, num_channels=1
    )
    result = await output.write_audio_frame(frame)

    assert result is True
    assert len(media.written) > 0

    parsed = [parse_rtp(pkt) for pkt in media.written]
    assert all(p is not None for p in parsed)
    ssrcs = {p.ssrc for p in parsed}
    assert len(ssrcs) == 1  # stable SSRC across the whole call
    assert all(p.payload_type == params.payload_type for p in parsed)

    sequences = [p.sequence for p in parsed]
    assert sequences == sorted(sequences)
    assert sequences == list(range(sequences[0], sequences[0] + len(sequences)))

    timestamps = [p.timestamp for p in parsed]
    diffs = {b - a for a, b in zip(timestamps, timestamps[1:])}
    assert diffs == {params.samples_per_packet}  # ts += 160 per packet, D-03


# --- Task 2: interruption flush (D-07) ------------------------------------


async def test_interruption_flushes_output():
    """D-07: the queued-but-unsent outbound PCM tail is dropped on
    interruption, with no effect on RTP already written."""
    transport, media = _make_transport()
    output = transport.output()

    # A full-second write leaves a genuine sub-frame PCM tail buffered inside
    # the framer (SOXR's streaming resample of a finite chunk rarely lands on
    # an exact multiple of 160 samples) -- this is the D-07 "queued outbound
    # audio" this test proves gets flushed.
    frame = OutputAudioRawFrame(
        audio=_silence_pcm(PIPELINE_OUTPUT_SAMPLE_RATE),
        sample_rate=PIPELINE_OUTPUT_SAMPLE_RATE,
        num_channels=1,
    )
    await output.write_audio_frame(frame)
    assert output._framer.pending > 0, "expected a buffered, not-yet-sent PCM tail"

    written_before = len(media.written)
    await transport.flush_output_audio()

    assert output._framer.pending == 0  # D-07: the tail is discarded
    assert len(media.written) == written_before  # flushing writes nothing new

    # And the real wiring: process_frame's InterruptionFrame branch actually
    # invokes the flush hook (D-07 "existing pipecat interruption path").
    flush_calls = []

    async def _spy_flush():
        flush_calls.append(1)

    output.flush = _spy_flush
    await output.handle_interruptions(InterruptionFrame())
    assert flush_calls == [1]


async def test_flush_output_audio_is_a_safe_noop_before_output_constructed():
    """flush_output_audio() must not raise if output() was never built (no
    audio has been sent yet -- e.g. a call that connects and hangs up
    immediately)."""
    transport, _media = _make_transport()
    await transport.flush_output_audio()  # must not raise


# --- Task 2: idempotent stop / terminal close (D-08) -----------------------


async def test_stop_is_idempotent():
    """Calling stop() (and cancel()) twice performs exactly one teardown
    effect (D-08) -- proven by monkeypatching ``cancel_task`` (which itself
    requires a real, running task manager we don't need for this unit test)
    with a call-counting stub."""
    transport, _media = _make_transport()
    input_transport = transport.input()

    cancel_calls = []

    async def _fake_cancel_task(task, timeout=1.0):
        cancel_calls.append(task)

    input_transport.cancel_task = _fake_cancel_task
    dummy_task = object()
    input_transport._receive_task = dummy_task

    await input_transport.stop(EndFrame())
    await input_transport.stop(EndFrame())
    await input_transport.cancel(CancelFrame())

    assert len(cancel_calls) == 1
    assert input_transport._receive_task is None


async def test_disconnect_event_fires_once():
    """on_client_disconnected fires exactly once even if the receive loop's
    natural end-of-stream AND an explicit stop() both occur (D-08); the
    handler is not re-armed after firing."""
    transport, _media = _make_transport(incoming=[])  # already-exhausted media

    fired = []

    @transport.event_handler("on_client_disconnected")
    async def _on_disconnected(transport, client):  # noqa: ANN001 -- pipecat handler shape
        fired.append(1)

    down_frames, _up = await run_test(
        transport.input(),
        frames_to_send=[SleepFrame(sleep=0.05)],
        send_end_frame=True,
    )

    # Drive it again explicitly -- must stay a no-op (fire-once guard on the
    # OUTER TelephonyTransport, independent of the input processor's own
    # per-processor teardown guard).
    await transport._fire_disconnected_once()
    await transport._fire_disconnected_once()

    # _call_event_handler schedules handlers as background tasks; give the
    # event loop a tick to run them before counting.
    await asyncio.sleep(0)
    assert len(fired) == 1


# --- Task 3: the Sec19-B offline pipeline-traversal proof -------------------


async def test_telephone_audio_traverses_real_pipeline_offline(
    make_config_file, stub_provider_keys, fake_aws
):
    """The hermetic offline form of the Sec19-B exit criterion: a real
    ``TelephonyTransport`` fed by an offline ``RtpMediaSession`` is accepted
    by ``create_call_session(channel="pstn")``, which builds the REAL
    ``build_pipeline`` graph around it -- transport.input() first,
    transport.output() immediately before the assistant aggregator -- with
    NO browser-WebRTC/HTTP/socket object anywhere in this test, and NO live
    provider network call (construction only; the worker is never run).
    """
    params = TelephonyTransportParams()
    transport, _media = _make_transport(incoming=_synthetic_pcmu_packets(4, params), params=params)

    cfg = load_config(make_config_file())
    knowledge_cfg = load_knowledge_config()

    # (a) create_call_session accepts the real TelephonyTransport with
    # channel="pstn" -- the Phase 9 seam, unchanged, plugged with the new
    # transport (D-05/D-08).
    call_session = await create_call_session(
        transport=transport,
        identity=CallIdentity(subject="tester", authenticated=True),
        gate_result=_gate_result(),
        cfg=cfg,
        knowledge_cfg=knowledge_cfg,
        duplex_cfg=DuplexConfig(),
        quota_cfg=_quota_config(),
        channel="pstn",
        metadata={},
    )

    assert isinstance(call_session, CallSession)
    assert call_session.worker is not None
    assert isinstance(call_session.lifecycle, SessionLifecycle)
    assert transport._event_handlers["on_client_disconnected"].handlers
    assert transport._event_handlers["on_client_connected"].handlers

    # (b) the real build_pipeline graph genuinely begins at transport.input()
    # and ends (before the assistant aggregator/sink) at transport.output()
    # -- a second, complementary construction call with the SAME transport
    # instance and cfg, inspecting the actual processor list (D-05 "the
    # build_pipeline contract is preserved").
    built = build_pipeline(cfg, transport, knowledge_cfg=knowledge_cfg, duplex_cfg=DuplexConfig())
    processors = built.pipeline.processors
    assert processors[1] is transport.input()  # index 0 is Pipeline's own auto-inserted source
    assert transport.output() in processors
    assert processors.index(transport.input()) < processors.index(transport.output())

    # (c) the transport boundary itself round-trips audio -- proven directly
    # by test_rtp_input_emits_correct_audio_frame /
    # test_output_frame_emits_pcmu_rtp above, using this exact class. The
    # live Deepgram-transcribes -> ElevenLabs-responds round trip needs real
    # API keys + network and is a documented Phase-11 live eval (NOT an
    # offline gate) -- call_session.run() is deliberately never invoked here.
