# klanker-voice telephony-edge production image (Phase 12, D-01/D-04).
#
# Single self-contained Fargate container: Asterisk (PJSIP/ARI/Stasis, the
# VoIP.ms registration trunk) + the standalone `klanker_voice.telephony`
# ARI/Stasis controller, in ONE process group (entrypoint.sh). Renders
# `${VAR}` placeholder configs from SSM-injected env at container start
# (render_configs.py, unmodified from the Phase-11 local dev harness — same
# D-09 pattern: templates stay placeholder-only forever, only the rendered
# copy in /etc/asterisk carries a real secret), then launches Asterisk in
# the background and execs the Python controller in the foreground.
#
# Base: the SAME pinned Asterisk image + version tag the Phase-11 local dev
# harness already proved ships res_ari/res_stasis compiled in
# (apps/voice/asterisk/docker-compose.yml, apps/voice/asterisk/README.md's
# own module smoke check) — Debian 13 "trixie", the SAME release
# python:3.12-slim uses (apps/voice/Dockerfile's own comment).
#
# Build context is apps/voice/ (mirrors apps/voice/Dockerfile: CI builds
# with `docker build -f asterisk/Dockerfile apps/voice`), so all COPY paths
# below are relative to apps/voice/, e.g. `asterisk/render_configs.py`, not
# `apps/voice/asterisk/render_configs.py`.
FROM andrius/asterisk:22.10.1_debian-trixie

USER root

# aiortc/audio system deps (same rationale as apps/voice/Dockerfile): the
# shared klanker_voice package pulls pipecat-ai[webrtc] (aiortc) as an
# installed dependency even though this telephony container uses socket
# RTP, not WebRTC, so these libs are needed at Python import time
# regardless. libvpx's shared-library package name tracks the codec ABI
# version and isn't hardcoded, for the same reason as apps/voice/Dockerfile.
RUN apt-get update \
    && LIBVPX_PKG="$(apt-cache search '^libvpx[0-9]+$' | awk '{print $1}' | sort -V | tail -1)" \
    && apt-get install -y --no-install-recommends \
        libopus0 \
        "${LIBVPX_PKG:?no libvpx shared-library package found}" \
        ffmpeg \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Pinned uv binary — matches apps/voice/Dockerfile's version exactly. uv
# manages its OWN Python 3.12 toolchain download (this Debian trixie base
# ships python3.13 as the only system python3 package; apps/voice/
# .python-version pins 3.12 for parity with the primary voice image — but
# that file is excluded from the build context by apps/voice/.dockerignore
# ["local dev environment"], so the version is pinned explicitly via
# `--python 3.12` below instead of relying on the .python-version convention).
COPY --from=ghcr.io/astral-sh/uv:0.11.26 /uv /uvx /usr/local/bin/

WORKDIR /app

# Install the locked dependency tree first (its own cache layer) so
# app-code edits don't bust the (much slower) dependency-install cache —
# same rationale as apps/voice/Dockerfile.
COPY pyproject.toml uv.lock ./
RUN uv python install 3.12 \
    && uv sync --locked --python 3.12 --no-install-project --no-dev

# Now the app itself (klanker_voice package, configs/knowledge/prompts the
# telephony harness config resolves relative paths against) and install
# the project.
COPY . .
RUN uv sync --locked --python 3.12 --no-dev

ENV PATH="/app/.venv/bin:${PATH}"
ENV PYTHONUNBUFFERED=1

# pipecat's ElevenLabs TTS needs the punkt_tab tokenizer baked in (same as
# apps/voice/Dockerfile — the runtime FS is read-only... except this image
# deliberately runs with a writable root filesystem, see service.hcl's
# `readonly_root_filesystem = false` comment; baking this in at build time
# avoids a flaky runtime download regardless).
ENV NLTK_DATA=/usr/share/nltk_data
RUN python -m nltk.downloader -d /usr/share/nltk_data punkt_tab

# Asterisk config: the tracked placeholder templates (ari.conf/pjsip.conf,
# D-09 — never a real secret) go into their own templates/ dir so the
# entrypoint renders ONLY those two into /etc/asterisk/ without touching
# the other three configs, which carry no placeholder and are copied
# straight into /etc/asterisk/ — the exact same five-file set the local
# dev harness's docker-compose.yml bind-mounts.
COPY asterisk/http.conf asterisk/extensions.conf asterisk/rtp.conf /etc/asterisk/
COPY asterisk/ari.conf asterisk/pjsip.conf /etc/asterisk/templates/
COPY asterisk/render_configs.py /app/asterisk/render_configs.py
COPY asterisk/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh \
    && chown -R asterisk:asterisk /etc/asterisk

# SIP (PJSIP transport-udp) signaling. RTP media (20000-20100/udp, per
# rtp.conf) is not individually EXPOSEd — Fargate awsvpc gives the task its
# own ENI, and the security group (network/v1.0.0's dedicated
# telephony_edge SG, POP-locked) is what actually bounds ingress, not
# per-port EXPOSE metadata. ARI (8088) is intentionally NOT exposed here —
# it is loopback-only, reached by the controller inside this same
# container at 127.0.0.1:8088 with zero host/network surface (D-01,
# §18/§25.C), mirroring the local dev harness's own "ARI never published"
# design.
EXPOSE 5060/udp

ENTRYPOINT ["/entrypoint.sh"]
