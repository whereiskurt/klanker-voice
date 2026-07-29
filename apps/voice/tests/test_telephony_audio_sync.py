"""Boot-time S3 audio-clip sync (quick task 260729-rck) -- see
``klanker_voice/telephony/audio_sync.py``. Everything here runs against a
fake in-memory S3 client; no AWS call is ever made."""

from __future__ import annotations

from klanker_voice.telephony import audio_sync
from klanker_voice.telephony.audio_sync import MEDIA_PREFIX, sync_s3_audio


class _FakePaginator:
    def __init__(self, pages):
        self._pages = pages

    def paginate(self, **kwargs):
        assert kwargs["Prefix"] == MEDIA_PREFIX
        return iter(self._pages)


class _FakeS3:
    """Just enough of boto3's S3 client: paginated listing + download."""

    def __init__(self, keys, fail_download=False):
        self._pages = [{"Contents": [{"Key": k} for k in keys]}]
        self.downloaded: list[tuple[str, str]] = []
        self._fail_download = fail_download

    def get_paginator(self, name):
        assert name == "list_objects_v2"
        return _FakePaginator(self._pages)

    def download_file(self, bucket, key, dest):
        if self._fail_download:
            raise RuntimeError("boom")
        self.downloaded.append((key, dest))


def test_sync_mirrors_wav_keys_under_dest_root(tmp_path, monkeypatch):
    """wav keys under the prefix land at DEST_ROOT/<subpath>; non-wav and
    traversal-shaped keys are skipped."""
    monkeypatch.setattr(audio_sync, "DEST_ROOT", tmp_path)
    fake = _FakeS3(
        [
            f"{MEDIA_PREFIX}rick/song.wav",
            f"{MEDIA_PREFIX}rick/nested/loop.wav",
            f"{MEDIA_PREFIX}rick/notes.txt",
            f"{MEDIA_PREFIX}../escape.wav",
            MEDIA_PREFIX,  # bare prefix key (S3 folder placeholder)
        ]
    )

    count = sync_s3_audio(s3_client=fake, bucket="test-bucket")

    assert count == 2
    dests = [d for _, d in fake.downloaded]
    assert str(tmp_path / "rick" / "song.wav") in dests
    assert str(tmp_path / "rick" / "nested" / "loop.wav") in dests
    # Parent dirs were created for the nested key.
    assert (tmp_path / "rick" / "nested").is_dir()


def test_sync_skips_when_bucket_env_unset(monkeypatch):
    """Local dev (no KMV_LEDGER_BUCKET) is a silent no-op -- no client is
    ever constructed."""
    monkeypatch.delenv("KMV_LEDGER_BUCKET", raising=False)
    assert sync_s3_audio() == 0


def test_sync_never_raises_on_s3_failure(tmp_path, monkeypatch):
    """Any S3 failure degrades to 0 synced clips -- boot must proceed."""
    monkeypatch.setattr(audio_sync, "DEST_ROOT", tmp_path)
    fake = _FakeS3([f"{MEDIA_PREFIX}rick/song.wav"], fail_download=True)
    assert sync_s3_audio(s3_client=fake, bucket="test-bucket") == 0
