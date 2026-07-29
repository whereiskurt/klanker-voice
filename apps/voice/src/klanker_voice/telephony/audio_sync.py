"""Boot-time S3 audio-clip sync (quick task 260729-rck).

The RICK playback game's clips are operator-curated audio that must NOT
live in this public repository (they may be material the operator can play
over a phone line but not publish). They live instead under a PRIVATE
prefix of the Phase 15 ledger bucket -- ``media/telephony/...`` -- and this
module downloads them into ``assets/telephony/...`` once at process boot,
before the ARI controller starts. The bucket name arrives via the same
``KMV_LEDGER_BUCKET`` env the in-container ledger writer already uses (no
new terragrunt dependency wiring); read access is the task role's
``TelephonyMediaRead``/``TelephonyMediaList`` statements in
``services/telephony-edge/service.hcl``.

Operator flow: ``make -C apps/voice rick-audio`` normalizes local sources
to 8kHz mono s16 wavs, then upload them::

    aws s3 cp assets/telephony/rick/ \\
        s3://$KMV_LEDGER_BUCKET/media/telephony/rick/ --recursive

and restart the telephony-edge task (no image rebuild needed -- clips are
data, not code).

Discipline: NEVER raises (an S3 problem degrades to whatever clips are
baked into the image -- audio can never stop call control from starting),
downloads ``.wav`` keys only, and refuses any key whose relative path
escapes the destination root.
"""

from __future__ import annotations

import os
from pathlib import Path

from loguru import logger

from klanker_voice.config import APP_ROOT

#: The private S3 prefix mirrored into assets/telephony/ at boot.
MEDIA_PREFIX = "media/telephony/"

#: Local mirror root -- subpaths under MEDIA_PREFIX land under here, so
#: ``media/telephony/rick/song.wav`` becomes ``assets/telephony/rick/song.wav``
#: (exactly where the shipped ``audio_dir = "assets/telephony/rick"`` entry
#: looks).
DEST_ROOT = APP_ROOT / "assets" / "telephony"

#: Same env var the ledger writer consumes -- injected at the ecs-task unit
#: via its `dependency "ledger"` block (random-suffixed bucket name).
BUCKET_ENV_VAR = "KMV_LEDGER_BUCKET"


def sync_s3_audio(s3_client=None, bucket: str | None = None) -> int:
    """Mirror every ``.wav`` under ``s3://$KMV_LEDGER_BUCKET/media/telephony/``
    into ``assets/telephony/`` and return how many files landed.

    ``s3_client``/``bucket`` exist for tests; production callers pass
    nothing. An unset bucket env (local dev), a missing prefix, or ANY
    S3/filesystem failure logs and returns without raising -- see the
    module docstring's degrade discipline.
    """
    bucket = bucket if bucket is not None else os.environ.get(BUCKET_ENV_VAR, "")
    if not bucket:
        logger.info("audio_sync: KMV_LEDGER_BUCKET unset -- skipping S3 clip sync")
        return 0
    try:
        if s3_client is None:  # pragma: no cover -- exercised only in a real task
            import boto3

            s3_client = boto3.client("s3")
        count = 0
        paginator = s3_client.get_paginator("list_objects_v2")
        for page in paginator.paginate(Bucket=bucket, Prefix=MEDIA_PREFIX):
            for obj in page.get("Contents", []):
                key = str(obj.get("Key", ""))
                rel = key[len(MEDIA_PREFIX) :]
                if not rel or not rel.endswith(".wav"):
                    continue
                # Refuse anything that could escape DEST_ROOT -- keys are
                # operator-written, but boot code stays paranoid anyway.
                rel_path = Path(rel)
                if rel_path.is_absolute() or ".." in rel_path.parts:
                    logger.warning(f"audio_sync: refusing suspicious key path {key!r}")
                    continue
                dest = DEST_ROOT / rel_path
                dest.parent.mkdir(parents=True, exist_ok=True)
                s3_client.download_file(bucket, key, str(dest))
                count += 1
        if count:
            logger.info(
                f"audio_sync: mirrored {count} clip(s) from s3://{bucket}/{MEDIA_PREFIX}"
            )
        else:
            logger.info(f"audio_sync: no clips under s3://{bucket}/{MEDIA_PREFIX}")
        return count
    except Exception as exc:  # noqa: BLE001 -- boot must proceed on any sync failure
        logger.warning(
            f"audio_sync: S3 clip sync failed ({type(exc).__name__}) -- "
            "continuing with baked-in assets"
        )
        return 0
