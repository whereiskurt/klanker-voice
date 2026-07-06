# klanker-voice production image (INFR-03).
#
# Multi-stage (D-03): a Node build stage compiles the Vite+TS+React client
# (apps/voice/client/) to static dist/ assets, which are COPY'd into the
# python:3.12-slim runtime stage below and served by server.py's StaticFiles
# mount (CLIENT_DIST_DIR = apps/voice/client/dist). One image, one deploy;
# CI builds both toolchains. No committed build artifacts — dist/ is always
# produced fresh here from source.
#
# Build context is apps/voice/ (see .github/workflows/build-voice.yml:
# `docker build -t "$IMAGE" apps/voice`), so all COPY paths below are
# relative to apps/voice/, e.g. `client/` not `apps/voice/client/`.
FROM node:22-slim AS client-build
WORKDIR /client

# Public OIDC client config, baked into the built bundle at build time
# (CLNT-08, D-04, client/.env.example). There is NO secret here — this is a
# PKCE public client (client/src/config/oidc.ts) — so defaulting these ARGs
# to the real production values is safe; override via --build-arg for a
# non-production image (e.g. a staging auth issuer).
ARG VITE_OIDC_ISSUER=https://auth.klankermaker.ai/use1/api/oidc
ARG VITE_OIDC_CLIENT_ID=voice
ARG VITE_OIDC_AUDIENCE=https://voice.klankermaker.ai
ARG VITE_OIDC_REDIRECT_URI=https://voice.klankermaker.ai/callback
ENV VITE_OIDC_ISSUER=${VITE_OIDC_ISSUER} \
    VITE_OIDC_CLIENT_ID=${VITE_OIDC_CLIENT_ID} \
    VITE_OIDC_AUDIENCE=${VITE_OIDC_AUDIENCE} \
    VITE_OIDC_REDIRECT_URI=${VITE_OIDC_REDIRECT_URI}

# Dependency layer first (its own cache layer) so app-code edits don't bust
# the slower npm-install cache — same rationale as the uv sync split below.
COPY client/package.json client/package-lock.json ./
RUN npm ci
COPY client/ ./
RUN npm run build

# python:3.12-slim, NOT dailyco/pipecat-base (CLAUDE.md: that base image targets
# Pipecat Cloud's bot.py/process-per-session runtime contract; this is a
# self-hosted Fargate task with its own FastAPI/uvicorn entrypoint — server.py,
# not the pipecat dev runner).
FROM python:3.12-slim

# aiortc/audio system deps (CLAUDE.md container note): opus + vp8/vp9 codec
# libraries for WebRTC media, ffmpeg for PyAV's underlying codec libraries.
#
# libvpx's shared-library package name tracks the codec ABI version and has
# moved past libvpx7 on python:3.12-slim's current (Debian trixie) base —
# it's libvpx9 there. Pinning an exact numbered name would silently break
# again on the next Debian bump, so resolve it via apt-cache at build time
# instead of hardcoding one.
RUN apt-get update \
    && LIBVPX_PKG="$(apt-cache search '^libvpx[0-9]+$' | awk '{print $1}' | sort -V | tail -1)" \
    && apt-get install -y --no-install-recommends \
        libopus0 \
        "${LIBVPX_PKG:?no libvpx shared-library package found}" \
        ffmpeg \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Pinned uv binary via the official distroless image — matches the local dev
# toolchain version, no pip bootstrap needed.
COPY --from=ghcr.io/astral-sh/uv:0.11.26 /uv /uvx /usr/local/bin/

WORKDIR /app

# Install the locked dependency tree first (its own layer) so app-code edits
# don't bust the (much slower) dependency-install cache.
COPY pyproject.toml uv.lock ./
RUN uv sync --locked --no-install-project --no-dev

# Now copy the app itself and install the project (editable src/klanker_voice).
COPY . .
RUN uv sync --locked --no-dev

# D-03: the built client SPA — server.py's StaticFiles mount expects
# apps/voice/client/dist (CLIENT_DIST_DIR), i.e. /app/client/dist here.
COPY --from=client-build /client/dist /app/client/dist

ENV PATH="/app/.venv/bin:${PATH}"
ENV PYTHONUNBUFFERED=1

EXPOSE 7860

# Invoke uvicorn directly from the already-synced .venv (on PATH above) —
# NOT `uv run uvicorn ...`. `uv run` re-syncs against the lockfile's default
# groups on every invocation, which pulls in the `dev` group (pipecat-ai
# [local]'s pyaudio) even though the image was built with `--no-dev`; pyaudio
# has no wheel here and needs gcc/portaudio headers this runtime image
# doesn't carry, so the container fails at startup trying to compile it.
CMD ["uvicorn", "server:app", "--host", "0.0.0.0", "--port", "7860"]
