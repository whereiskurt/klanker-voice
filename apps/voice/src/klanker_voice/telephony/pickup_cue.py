"""The PSTN pickup cue -- ring synth + pre-rendered "hey" loader + barge-in
-safe injection (quick task 260713-m9n, Task 2).

On answer the caller today hears near-silence until the §24 gate unlocks
(``telephony.gate.GateProcessor``). This module gives the caller one short
ringback tone followed by a short pre-rendered KPH "hey" prompt (rendered by
``scripts/render_pickup_cue.py`` into ``assets/telephony/kph-hey.wav``),
injected via the SAME ``worker.queue_frames([...])`` seam
``klanker_voice.pipeline.greet_now``/``speak_goodbye`` already use --
downstream from the pipeline source, through every existing stage, to
``transport.output()``.

**Task 2 spike -- confirmed against this repo's installed pipecat 1.5.0
source (frames.py / transports/base_output.py / utils/frame_queue.py) and
this phase's own ``telephony/gate.py`` / ``telephony/transport.py``:**

1. ``OutputAudioRawFrame`` is a ``DataFrame`` (via ``AudioRawFrame``) --
   pipecat's own docstring: "cancelled by user interruptions". It is NOT an
   ``UninterruptibleFrame``. On an ``InterruptionFrame``,
   ``BaseOutputTransport.MediaSender.handle_interruptions`` (absent a mixer
   or any genuinely-uninterruptible frame in flight) cancels the running
   audio task and swaps in a brand-new, empty ``_audio_queue`` -- any
   already-queued-but-unsent ``OutputAudioRawFrame`` (our injected ring/hey)
   is discarded outright, never written to the transport.
   ``TelephonyOutputTransport.process_frame`` (transport.py, D-07) then also
   runs its own ``flush()`` on the same ``InterruptionFrame`` -- dropping
   the RTP framer's unsent PCM tail and resetting the pacing clock. Together
   these two, already-shipped mechanisms are exactly "caller speech mid-cue
   stops the cue immediately" -- no second VAD/endpointing system is added
   here, the existing pipeline ``InterruptionFrame`` stays fully
   authoritative (same discipline as transport.py's own D-07 note).
2. ``telephony.gate.GateProcessor`` swallows ONLY
   ``TranscriptionFrame``/``InterimTranscriptionFrame``/
   ``UserStartedSpeakingFrame``/``UserStoppedSpeakingFrame`` while locked --
   confirmed by reading its ``process_frame``. ``OutputAudioRawFrame`` and
   ``BotStartedSpeakingFrame``/``BotStoppedSpeakingFrame`` are "everything
   else" and flow through the gate untouched in both the locked and
   unlocked state, so the cue plays correctly during the pre-unlock gate
   window (the whole point of this plan).
3. ``BotStartedSpeakingFrame``/``BotStoppedSpeakingFrame`` are ``SystemFrame``s
   (frames.py) -- "not affected by user interruptions", handled with
   priority. They do NOT toggle ``MediaSender._bot_speaking`` for a plain
   ``OutputAudioRawFrame`` (that internal flag is driven only by
   ``TTSAudioRawFrame``/``SpeechOutputAudioRawFrame`` via
   ``MediaSender._handle_bot_speech``) -- so the bracket is not load-bearing
   for the interruption mechanism itself. It IS kept because it flows
   through every stage untouched (point 2) and gives
   ``klanker_voice.duplex.DuplexProcessor``'s own bot-speaking tracker
   (voice2 backchannel-suppression) accurate state for the cue window, at
   zero cost -- and it keeps this injection shape consistent with the
   existing ``greet_now``/``speak_goodbye`` seam.
4. The frame type is deliberately ``OutputAudioRawFrame``, NOT
   ``TTSAudioRawFrame`` -- this audio never touched the TTS provider (it is
   pre-rendered / locally synthesized), and labeling it as TTS output would
   misrepresent the D-05d "no TTS API call before unlock" cost invariant
   this plan must preserve (T-M9N-01).

Redaction/cost discipline (D-05d, T-M9N-01): this module only ever *sends*
audio outbound. It never reads a transcript, never calls the LLM, and never
calls a TTS API -- the gate's "no billed turn until unlock" invariant is
unaffected by playing this cue.
"""

from __future__ import annotations

import math
import struct
import wave
from functools import lru_cache
from pathlib import Path

from loguru import logger
from pipecat.frames.frames import (
    BotStartedSpeakingFrame,
    BotStoppedSpeakingFrame,
    OutputAudioRawFrame,
)

from klanker_voice.config import APP_ROOT

#: apps/voice/assets/telephony/kph-hey.wav -- rendered by
#: scripts/render_pickup_cue.py (a human step, requires ELEVENLABS_API_KEY).
#: Not committed until that render has run -- see load_hey_clip's graceful
#: missing-asset degrade.
HEY_CLIP_PATH = APP_ROOT / "assets" / "telephony" / "kph-hey.wav"

#: US ringback tone dial pair (440 Hz + 480 Hz), one short ring -- not a
#: repeating cadence.
_RINGBACK_TONE_HZ = (440.0, 480.0)
_DEFAULT_SAMPLE_RATE = 24000
_DEFAULT_DURATION_S = 1.2
#: Linear fade in/out, to avoid a click at the start/end of the tone.
_FADE_SECONDS = 0.02
_AMPLITUDE = 0.28  # headroom for the two summed sines


def generate_ringback(
    sample_rate: int = _DEFAULT_SAMPLE_RATE, duration_s: float = _DEFAULT_DURATION_S
) -> bytes:
    """A single short US ringback tone (440 Hz + 480 Hz), 16-bit little-
    endian mono PCM, with a short linear fade-in/out. Pure and deterministic
    for fixed args -- no numpy needed (``math``/``struct`` only)."""
    n_samples = int(sample_rate * duration_s)
    fade_samples = max(1, int(sample_rate * _FADE_SECONDS))
    tone_a, tone_b = _RINGBACK_TONE_HZ
    out = bytearray()
    for i in range(n_samples):
        t = i / sample_rate
        value = _AMPLITUDE * (
            math.sin(2 * math.pi * tone_a * t) + math.sin(2 * math.pi * tone_b * t)
        ) / 2.0
        if i < fade_samples:
            value *= i / fade_samples
        elif i >= n_samples - fade_samples:
            value *= (n_samples - i) / fade_samples
        sample = int(max(-1.0, min(1.0, value)) * 32767)
        out += struct.pack("<h", sample)
    return bytes(out)


@lru_cache(maxsize=1)
def load_hey_clip(path: Path = HEY_CLIP_PATH) -> tuple[bytes, int]:
    """Read the pre-rendered "hey" WAV via stdlib ``wave``, cached (repeated
    calls do not re-read the file). Returns ``(pcm_bytes, sample_rate)``
    matching the file header.

    Never raises: a missing or unreadable file (the render is a human step,
    ``make -C apps/voice pickup-cue``, requiring ELEVENLABS_API_KEY) degrades
    to ``(b"", 24000)`` -- ``play_pickup_cue`` then plays ring-only."""
    path = Path(path)
    if not path.exists():
        logger.warning(f"pickup_cue: hey clip not found at {path} -- degrading to ring-only")
        return b"", _DEFAULT_SAMPLE_RATE
    try:
        with wave.open(str(path), "rb") as wf:
            sample_rate = wf.getframerate()
            pcm = wf.readframes(wf.getnframes())
        return pcm, sample_rate
    except Exception:  # noqa: BLE001 -- any read/parse failure degrades, never crashes call control
        logger.warning(f"pickup_cue: failed to read hey clip at {path} -- degrading to ring-only")
        return b"", _DEFAULT_SAMPLE_RATE


async def play_pickup_cue(worker) -> None:
    """Queue the ring + hey pickup cue on ``worker`` (a
    ``pipecat.pipeline.worker.PipelineWorker``), bracketed as bot speech so
    the pipeline's existing barge-in mechanism applies (see module
    docstring "Task 2 spike"). Omits the hey frame (ring-only) when the
    clip asset is missing -- see :func:`load_hey_clip`."""
    ring = generate_ringback()
    hey_pcm, hey_sample_rate = load_hey_clip()

    frames = [
        BotStartedSpeakingFrame(),
        OutputAudioRawFrame(audio=ring, sample_rate=_DEFAULT_SAMPLE_RATE, num_channels=1),
    ]
    if hey_pcm:
        frames.append(
            OutputAudioRawFrame(audio=hey_pcm, sample_rate=hey_sample_rate, num_channels=1)
        )
    frames.append(BotStoppedSpeakingFrame())

    await worker.queue_frames(frames)
