"""``klanker_voice.telephony.pickup_cue`` (quick task 260713-m9n, Task 2).

Hermetic and offline throughout: no ElevenLabs call, no real transport --
``play_pickup_cue`` is exercised against a fake ``worker`` recording
``queue_frames`` calls (the same seam ``greet_now``/``speak_goodbye`` use,
``klanker_voice.pipeline``)."""

from __future__ import annotations

import wave
from pathlib import Path

from pipecat.frames.frames import (
    BotStartedSpeakingFrame,
    BotStoppedSpeakingFrame,
    OutputAudioRawFrame,
)

from klanker_voice.telephony import pickup_cue


class _FakeWorker:
    def __init__(self) -> None:
        self.queued: list | None = None

    async def queue_frames(self, frames) -> None:
        self.queued = list(frames)


def _write_wav(path: Path, *, pcm: bytes, sample_rate: int = 24000) -> None:
    with wave.open(str(path), "wb") as wf:
        wf.setnchannels(1)
        wf.setsampwidth(2)
        wf.setframerate(sample_rate)
        wf.writeframes(pcm)


# --- generate_ringback -------------------------------------------------


def test_generate_ringback_correct_length_and_not_silent():
    sample_rate, duration_s = 24000, 1.2
    pcm = pickup_cue.generate_ringback(sample_rate=sample_rate, duration_s=duration_s)
    assert isinstance(pcm, bytes)
    assert len(pcm) == int(sample_rate * duration_s) * 2  # 16-bit mono
    assert any(b != 0 for b in pcm)  # a real tone, not silence


def test_generate_ringback_deterministic_for_fixed_args():
    a = pickup_cue.generate_ringback(sample_rate=24000, duration_s=0.5)
    b = pickup_cue.generate_ringback(sample_rate=24000, duration_s=0.5)
    assert a == b


def test_generate_ringback_different_duration_changes_length():
    short = pickup_cue.generate_ringback(sample_rate=24000, duration_s=0.5)
    long = pickup_cue.generate_ringback(sample_rate=24000, duration_s=1.0)
    assert len(long) == len(short) * 2


# --- load_hey_clip -------------------------------------------------------


def test_load_hey_clip_round_trips_a_real_wav(tmp_path):
    pickup_cue.load_hey_clip.cache_clear()
    pcm = pickup_cue.generate_ringback(sample_rate=24000, duration_s=0.1)
    wav_path = tmp_path / "hey.wav"
    _write_wav(wav_path, pcm=pcm, sample_rate=24000)

    loaded_pcm, sample_rate = pickup_cue.load_hey_clip(wav_path)

    assert loaded_pcm == pcm
    assert sample_rate == 24000


def test_load_hey_clip_missing_file_degrades_to_empty_never_raises(tmp_path):
    pickup_cue.load_hey_clip.cache_clear()
    missing = tmp_path / "does-not-exist.wav"

    pcm, sample_rate = pickup_cue.load_hey_clip(missing)

    assert pcm == b""
    assert sample_rate == 24000


def test_load_hey_clip_is_cached_across_repeated_calls(tmp_path):
    pickup_cue.load_hey_clip.cache_clear()
    pcm = pickup_cue.generate_ringback(sample_rate=24000, duration_s=0.1)
    wav_path = tmp_path / "hey.wav"
    _write_wav(wav_path, pcm=pcm, sample_rate=24000)

    pickup_cue.load_hey_clip(wav_path)
    pickup_cue.load_hey_clip(wav_path)

    info = pickup_cue.load_hey_clip.cache_info()
    assert info.hits == 1
    assert info.misses == 1


# --- play_pickup_cue -------------------------------------------------------


async def test_play_pickup_cue_queues_bracketed_ring_and_hey(monkeypatch):
    monkeypatch.setattr(
        pickup_cue, "load_hey_clip", lambda path=None: (b"hey-pcm-bytes", 24000)
    )
    worker = _FakeWorker()

    await pickup_cue.play_pickup_cue(worker)

    assert worker.queued is not None
    assert [type(f) for f in worker.queued] == [
        BotStartedSpeakingFrame,
        OutputAudioRawFrame,
        OutputAudioRawFrame,
        BotStoppedSpeakingFrame,
    ]
    ring_frame, hey_frame = worker.queued[1], worker.queued[2]
    assert ring_frame.num_channels == 1
    assert len(ring_frame.audio) > 0
    assert hey_frame.audio == b"hey-pcm-bytes"
    assert hey_frame.sample_rate == 24000


async def test_play_pickup_cue_missing_hey_degrades_to_ring_only(monkeypatch):
    monkeypatch.setattr(pickup_cue, "load_hey_clip", lambda path=None: (b"", 24000))
    worker = _FakeWorker()

    await pickup_cue.play_pickup_cue(worker)

    assert [type(f) for f in worker.queued] == [
        BotStartedSpeakingFrame,
        OutputAudioRawFrame,
        BotStoppedSpeakingFrame,
    ]
    assert len(worker.queued[1].audio) > 0  # the ring, never empty
