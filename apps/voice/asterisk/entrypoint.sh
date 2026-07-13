#!/bin/bash
# klanker-voice telephony-edge container entrypoint (Phase 12, D-01/D-04/D-09).
#
# Single-container design (apps/voice/asterisk/Dockerfile): this script is
# PID 1. It:
#   1. Renders ari.conf/pjsip.conf from the SSM-injected environment
#      (VOIPMS_SIP_*, ASTERISK_ARI_*) into /etc/asterisk/ — the SAME
#      substitution the Phase-11 local dev harness's render sidecar
#      performs (render_configs.py, unmodified).
#   2. Starts Asterisk in the background.
#   3. Scrubs the VoIP.ms SIP credential out of THIS shell's environment
#      before handing off to the Python controller (D-04: "the SIP
#      password is consumed only by Asterisk via the rendered config; it
#      is never passed into the Python process"). Asterisk already has it
#      baked into its rendered config file from step 1 and does not need
#      the env var again.
#   4. execs the standalone telephony controller (`python -m
#      klanker_voice.telephony`) as this container's foreground process —
#      a controller crash exits the container and lets ECS restart the
#      task. An Asterisk-only crash is NOT independently detected by this
#      script; that is a known, explicitly documented Phase-14
#      ops-hardening gap (12-RESEARCH.md's own §25 "Phase 14 deferred"
#      table — alarms/health-checks are deferred there too), not fixed
#      here.
set -euo pipefail

TEMPLATES_DIR="/etc/asterisk/templates"
OUT_DIR="/etc/asterisk"

# Fargate: external_media_address/external_signaling_address must be THIS
# task's public IP, which is dynamic per task — discover it via egress echo
# when the env doesn't provide one. Without this the literal
# ${TELEPHONY_MEDIA_ADDRESS} survives rendering and Asterisk black-holes RTP
# (surfaced by the 12-07 post-deploy log check).
if [ -z "${TELEPHONY_MEDIA_ADDRESS:-}" ]; then
    TELEPHONY_MEDIA_ADDRESS="$(python3 -c 'import urllib.request;print(urllib.request.urlopen("https://checkip.amazonaws.com",timeout=5).read().decode().strip())')"
    export TELEPHONY_MEDIA_ADDRESS
    echo "[entrypoint] discovered public media address: ${TELEPHONY_MEDIA_ADDRESS}"
fi

# The softphone endpoint is a Phase-11 local-dev artifact. On the deployed
# edge no one should register to it — when no password is supplied, set an
# unguessable random per-boot value so the rendered pjsip.conf never carries
# the literal ${SOFTPHONE_SIP_PASSWORD} placeholder as its password.
if [ -z "${SOFTPHONE_SIP_PASSWORD:-}" ]; then
    SOFTPHONE_SIP_PASSWORD="$(python3 -c 'import secrets;print(secrets.token_urlsafe(24))')"
    export SOFTPHONE_SIP_PASSWORD
    echo "[entrypoint] softphone endpoint locked with a random per-boot password"
fi

echo "[entrypoint] rendering Asterisk configs from environment..."
python3 /app/asterisk/render_configs.py --templates "$TEMPLATES_DIR" --out "$OUT_DIR"

echo "[entrypoint] starting Asterisk..."
# -f: foreground (no daemonize) so this script can track its PID.
# -T: timestamped console log lines (useful in CloudWatch Logs).
# -U asterisk -p: matches the base image's own default CMD (start as root,
# drop privileges to the asterisk user) — see /usr/local/bin/entrypoint.sh
# in andrius/asterisk:22.10.1_debian-trixie.
asterisk -f -T -U asterisk -p &
ASTERISK_PID=$!

# Give Asterisk a moment to bind its (loopback-only) ARI HTTP listener
# before the controller's first connect attempt. No formal readiness probe
# here — deferred to Phase 14 alongside the rest of the alarms/health-check
# hardening (12-RESEARCH.md §25 table).
sleep 3

if ! kill -0 "$ASTERISK_PID" 2>/dev/null; then
    echo "[entrypoint] FATAL: Asterisk exited during startup" >&2
    exit 1
fi

echo "[entrypoint] scrubbing VoIP.ms SIP credential from the environment before exec'ing the controller (D-04)"
unset VOIPMS_SIP_USERNAME
unset VOIPMS_SIP_PASSWORD

cd /app
echo "[entrypoint] exec'ing the telephony controller..."
exec python3 -m klanker_voice.telephony
