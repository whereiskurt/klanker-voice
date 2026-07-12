---
phase: quick-260712-ckd
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - apps/voice/asterisk/docker-compose.yml
  - apps/voice/asterisk/render_configs.py
  - apps/voice/asterisk/.gitignore
  - apps/voice/asterisk/README.md
autonomous: true
requirements: ["§19-C", "D-05", "D-08", "§18", "§25"]
user_setup:
  - service: local-dev-secrets
    why: "The §19-C proof spends real provider API calls and authenticates a real ARI + SIP session"
    env_vars:
      - name: ASTERISK_ARI_PASSWORD
        source: "apps/voice/asterisk/.env (copy from .env.example, gitignored)"
      - name: SOFTPHONE_SIP_PASSWORD
        source: "apps/voice/asterisk/.env"
      - name: TELEPHONY_ACCESS_PIN
        source: "apps/voice/asterisk/.env"
      - name: TELEPHONY_PASSPHRASE_WORDS
        source: "apps/voice/asterisk/.env (four space-separated words)"
      - name: DEEPGRAM/ANTHROPIC/ELEVENLABS keys
        source: "apps/voice/.env (make -C apps/voice env)"

must_haves:
  truths:
    - "A single `docker compose up` in apps/voice/asterisk brings up Asterisk AND the Klanker telephony controller, already wired to each other over ARI, with no extra manual step."
    - "The controller authenticates against ARI at http://127.0.0.1:8088 by sharing Asterisk's network namespace — ARI is never published on a public host interface (§18/§25.C preserved)."
    - "Real ARI/SIP passwords land in Asterisk's ari.conf/pjsip.conf at container start from .env only, and are never written into any git-tracked file."
    - "The tracked .conf files and test_asterisk_configs.py lint are unchanged and still pass."
    - "webrtc.py and server.py are byte-unchanged (browser path untouched)."
  artifacts:
    - "apps/voice/asterisk/render_configs.py — env→config renderer (string.Template, no secrets)"
    - "apps/voice/asterisk/docker-compose.yml — asterisk-config-render + klanker-telephony services"
    - "apps/voice/asterisk/.gitignore — ignores .rendered/"
    - "apps/voice/asterisk/README.md — simplified one-command §19-C proof"
  key_links:
    - "klanker-telephony network_mode: service:asterisk → 127.0.0.1:8088 ARI reachability"
    - "asterisk-config-render → .rendered/{ari,pjsip}.conf → asterisk service bind mounts"
    - "controller env_file ../.env (provider keys) + .env (ARI/gate secrets)"
---

<objective>
Make the Phase 11 §19-C local softphone proof a one-command harness: after
`docker compose up` in `apps/voice/asterisk/`, Asterisk AND the standalone
Klanker telephony controller come up already wired together, so a human can
point a SIP softphone at the endpoint, dial the inbound extension, and
exercise the §24 gate → greet → barge-in → hangup flow.

This closes the two live-stack prerequisites 11-02/11-07 flagged as manual
"step 3" blockers:
1. ARI loopback-vs-published-port mismatch — solved by running the
   controller inside Asterisk's network namespace (option (a) from the
   11-07 recipe), so `http://127.0.0.1:8088` resolves without exposing ARI
   on the host.
2. `.conf` `${VAR}` placeholders are documentation-only — solved by a small
   compose-run render step that substitutes the real secrets from `.env`
   into gitignored rendered copies at container start.

Purpose: turn a documented-but-fiddly 8-step manual bring-up into a single
`docker compose up`, without weakening the §18/§25 private-only ARI posture
and without ever committing a real secret.

Output: two new compose services (a config renderer + the controller), a
tiny renderer script, a `.gitignore`, and a rewritten §19-C README section.
The actual live call remains the human perceptual proof (CI cannot judge
greeting-clipping / barge-in feel).
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@.claude/CLAUDE.md
@.planning/phases/11-voip-ms-telephony-local-asterisk-edge/11-07-SUMMARY.md
@.planning/phases/11-voip-ms-telephony-local-asterisk-edge/11-02-SUMMARY.md

# The files this plan changes / mirrors
@apps/voice/asterisk/docker-compose.yml
@apps/voice/asterisk/README.md
@apps/voice/asterisk/ari.conf
@apps/voice/asterisk/pjsip.conf
@apps/voice/asterisk/extensions.conf
@apps/voice/asterisk/http.conf
@apps/voice/Dockerfile
@apps/voice/src/klanker_voice/telephony/__main__.py
@apps/voice/configs/telephony.toml
@apps/voice/tests/test_asterisk_configs.py

# Investigation facts (already confirmed — do NOT re-derive):
# - Dockerfile at apps/voice/Dockerfile: python:3.12-slim + uv, installs all
#   provider deps; build context is apps/voice/ (see build-voice.yml). Its
#   .venv is on PATH, so `python -m klanker_voice.telephony.controller` runs
#   the controller directly. apps/voice/.dockerignore already excludes `.env`
#   and `.venv` → no secrets are baked; runtime env is injected by compose.
# - extensions.conf inbound match is `_X.` → any number of 2+ digits (e.g.
#   1000) reaches [from-klanker-inbound] → Answer() → Stasis(klanker).
# - pjsip.conf endpoint `dev-softphone` has an AOR + auth → registration
#   REQUIRED: username `softphone`, password SOFTPHONE_SIP_PASSWORD, at
#   127.0.0.1:5060, endpoint/AOR id `dev-softphone`.
# - The existing `sipp` service shares Asterisk's netns via
#   `network_mode: "service:asterisk"` + `profiles: ["integration"]`.
# - test_asterisk_configs.py reads the TRACKED files by literal name
#   (apps/voice/asterisk/ari.conf etc.) and asserts the ARI password line is
#   non-empty; the literal `${ASTERISK_ARI_PASSWORD}` satisfies it. Leave the
#   tracked files as templates and the lint keeps passing.
</context>

<tasks>

<task type="auto">
  <name>Task 1: Add the config-render step (secrets from .env into gitignored rendered .conf copies)</name>
  <files>apps/voice/asterisk/render_configs.py, apps/voice/asterisk/.gitignore, apps/voice/asterisk/docker-compose.yml</files>
  <action>
Solve the "`.conf` ${VAR} placeholders are documentation-only" blocker (11-02/11-07) with a compose-run render step that lands real secrets from `.env` at container start WITHOUT editing or renaming any tracked file (so test_asterisk_configs.py keeps passing).

1. Create `apps/voice/asterisk/render_configs.py` — a dependency-free stdlib
   script (runnable by a plain `python3`, no uv/venv needed). It takes
   `--templates <dir>` and `--out <dir>`, and for each of exactly the two
   config files that carry a placeholder — `ari.conf` and `pjsip.conf` — it
   reads `<templates>/<name>`, substitutes `${VAR}` from `os.environ` using
   `string.Template(...).safe_substitute(os.environ)` (chosen over sed/shell
   so a password containing any shell-special character renders correctly and
   there is zero escaping surface), and writes the result to `<out>/<name>`.
   It creates `--out` if missing and prints one line per rendered file.
   The other three configs (http/extensions/rtp) have no placeholder and are
   NOT rendered — Asterisk bind-mounts them directly from the tracked files.
   The script contains NO secret and NO hardcoded password — it only moves
   values from the ambient environment into the output dir.
   Header comment must state: renders ONLY into the gitignored output dir,
   never back into the tracked templates (D-09).

2. Create `apps/voice/asterisk/.gitignore` containing `.rendered/` (and a
   short comment: rendered configs carry real secrets, never commit). This
   guarantees the rendered outputs stay out of git even though they live
   next to the tracked templates.

3. In `apps/voice/asterisk/docker-compose.yml` add an `asterisk-config-render`
   service (NO profile — it must run on a plain `docker compose up`):
   - `image: python:3.12-slim` (same base as apps/voice/Dockerfile — matches
     existing pin practice; no new supply-chain surface, T-11Q-SC).
   - `container_name: klanker-asterisk-config-render`.
   - `environment:` passes `ASTERISK_ARI_PASSWORD=${ASTERISK_ARI_PASSWORD:-}`
     and `SOFTPHONE_SIP_PASSWORD=${SOFTPHONE_SIP_PASSWORD:-}` (compose
     interpolates these from apps/voice/asterisk/.env).
   - `volumes:` `./:/templates:ro` and `./.rendered:/out`.
   - `command:` runs `python /templates/render_configs.py --templates
     /templates --out /out`.

4. Repoint the `asterisk` service so it consumes the rendered ari/pjsip:
   - Change ONLY the two mounts that carry secrets:
     `./ari.conf:/etc/asterisk/ari.conf:ro` → `./.rendered/ari.conf:/etc/asterisk/ari.conf:ro`
     and the pjsip one likewise. Leave http/extensions/rtp mounts pointing at
     the tracked `./*.conf` (they have no secret).
   - Add `depends_on:` on `asterisk` requiring
     `asterisk-config-render: { condition: service_completed_successfully }`
     so the rendered files exist on the host before Asterisk starts (avoids
     the empty-file-mounted-as-directory race).
   - Move the two now-vestigial `ASTERISK_ARI_PASSWORD`/`SOFTPHONE_SIP_PASSWORD`
     entries off the `asterisk` service's `environment:` (Asterisk's own
     parser never used them) — they now belong to the render service. Keep
     everything else on the asterisk service (ports for SIP/RTP, extra_hosts)
     unchanged. Do NOT change http.conf's bindaddr (stays 127.0.0.1).

Do not touch ari.conf / pjsip.conf / http.conf / extensions.conf / rtp.conf
themselves — they remain the tracked templates the lint asserts against.
  </action>
  <verify>
    <automated>ASTERISK_ARI_PASSWORD='p@ss w0rd|#&amp;' SOFTPHONE_SIP_PASSWORD='s!p$ecret' python3 apps/voice/asterisk/render_configs.py --templates apps/voice/asterisk --out /tmp/kv-render-check && ! grep -q '\${' /tmp/kv-render-check/ari.conf && ! grep -q '\${' /tmp/kv-render-check/pjsip.conf && grep -q 'p@ss w0rd|#&amp;' /tmp/kv-render-check/ari.conf && echo RENDER_OK</automated>
    <automated>cd apps/voice && uv run pytest tests/test_asterisk_configs.py -q</automated>
    <automated>cd apps/voice/asterisk &amp;&amp; docker compose config >/dev/null &amp;&amp; echo COMPOSE_OK</automated>
  </verify>
  <done>
`render_configs.py` renders ari.conf + pjsip.conf into the out dir with all
`${VAR}` replaced (including secrets with special chars) and no `${` left;
`.gitignore` ignores `.rendered/`; `docker compose config` validates with the
new `asterisk-config-render` service and the repointed ari/pjsip mounts; the
tracked-conf lint still passes green.
  </done>
</task>

<task type="auto">
  <name>Task 2: Add the klanker-telephony controller service (shared netns → ARI, default up)</name>
  <files>apps/voice/asterisk/docker-compose.yml</files>
  <action>
Add the standalone telephony controller to the compose stack so it comes up
wired to Asterisk on a plain `docker compose up` (option (a) from the 11-07
recipe — run the controller inside Asterisk's network namespace).

Add a `klanker-telephony` service (NO profile — part of the default stack):
- Build from the existing app image, do NOT author a new Dockerfile:
  `build: { context: .., dockerfile: Dockerfile }` (context `..` = apps/voice,
  where the Dockerfile and pyproject live).
- `container_name: klanker-telephony-dev`.
- `network_mode: "service:asterisk"` — mirror the existing `sipp` service
  exactly. This is the security-critical choice: the controller reaches ARI
  at 127.0.0.1:8088 INSIDE Asterisk's netns, so nothing new is published on
  the host and the §18/§25.C private-only ARI guarantee holds. A service
  using `network_mode: service:*` must NOT declare its own `ports:` — it
  doesn't need any.
- `depends_on:` require `asterisk` to be started (the shared-netns provider),
  which in turn depends on the render step from Task 1.
- `env_file:` a list — `../.env` (apps/voice/.env: real Deepgram/Anthropic/
  ElevenLabs provider keys) and `.env` (apps/voice/asterisk/.env: ARI creds +
  §24 gate PIN/passphrase). `.dockerignore` already keeps both out of the
  image, so these are injected only at runtime.
- `environment:` set the non-secret run wiring:
  `KLANKER_PIPELINE_CONFIG=configs/telephony.toml`,
  `ASTERISK_ARI_URL=http://127.0.0.1:8088`,
  `ASTERISK_ARI_USERNAME=klanker` (the ari.conf user; the matching password
  comes from the env_file, never here).
- `working_dir: /app` and
  `command: ["python", "-m", "klanker_voice.telephony.controller"]` — the
  canonical entrypoint (telephony/__main__.py's `main()`; the image's .venv
  is on PATH so `python` resolves to it). Do NOT use `uv run` (the Dockerfile
  comment explains uv re-syncs the dev group and breaks on pyaudio).
- `restart: "no"` — if `.env` is unfilled the controller raises ConfigError
  by design (D-09); it must fail loudly once, not crash-loop.

Separately, in the `asterisk` service, REMOVE the dead `8088:8088` host
publish line (ARI binds to the container's own 127.0.0.1 per http.conf, so
that published port forwarded to a non-listening loopback and reached
nothing). Removing it means ARI has zero host surface — a strict tightening
of §18/§25.C, and the controller no longer needs it (shared netns). Keep the
5060/udp SIP and 10000-10020/udp RTP publishes (the softphone needs those).
Leave the `sipp` service and http.conf bindaddr unchanged.

Do NOT modify apps/voice/src/klanker_voice/webrtc.py or apps/voice/server.py.
  </action>
  <verify>
    <automated>cd apps/voice/asterisk &amp;&amp; docker compose config 2>/dev/null | grep -A30 'klanker-telephony' | grep -q 'network_mode: "service:asterisk"' &amp;&amp; echo NETNS_OK</automated>
    <automated>cd apps/voice/asterisk &amp;&amp; docker compose config 2>/dev/null | grep -q 'klanker_voice.telephony.controller' &amp;&amp; echo CMD_OK</automated>
    <automated>cd apps/voice/asterisk &amp;&amp; ! docker compose config 2>/dev/null | awk '/klanker-telephony:/{f=1} f&amp;&amp;/^  [a-z]/{if($0!~/klanker-telephony/)f=0} f&amp;&amp;/8088/{print}' | grep -q 8088 &amp;&amp; echo NO_ARI_PUBLISH_OK</automated>
    <automated>git -C apps/voice diff --stat -- src/klanker_voice/webrtc.py server.py | grep -q . &amp;&amp; echo TOUCHED || echo BROWSER_PATH_UNCHANGED</automated>
  </verify>
  <done>
`docker compose config` shows a `klanker-telephony` service with
`network_mode: "service:asterisk"`, no `ports:`, the controller command, the
`KLANKER_PIPELINE_CONFIG`/`ASTERISK_ARI_URL`/`ASTERISK_ARI_USERNAME` env, and
both env_files; the `asterisk` service no longer publishes 8088 (SIP/RTP
publishes intact); webrtc.py and server.py are byte-unchanged.
  </done>
</task>

<task type="auto">
  <name>Task 3: Rewrite the README "Manual §19-C softphone proof" to the one-command flow</name>
  <files>apps/voice/asterisk/README.md</files>
  <action>
Simplify `apps/voice/asterisk/README.md`'s "Manual §19-C softphone proof"
section to the new flow, and reconcile the two "Known limitation" callouts
that Task 1/Task 2 have now resolved.

1. Update the "Bring-up" section: note that a plain `docker compose up` now
   brings the FULL wired stack (config-render → Asterisk → controller), and
   that `docker compose up asterisk` starts Asterisk alone if you only want
   the edge without the controller.

2. In the two "Known limitation" callouts: change them from open blockers to
   RESOLVED notes. The ARI-loopback callout: state it is now solved by the
   `klanker-telephony` service sharing Asterisk's netns (127.0.0.1:8088
   resolves inside the shared namespace; ARI is no longer published on the
   host at all). The `${VAR}`-substitution callout: state it is now solved by
   the `asterisk-config-render` step writing real secrets from `.env` into
   gitignored `.rendered/{ari,pjsip}.conf` at container start — the tracked
   `.conf` files stay placeholder-only. Preserve the http.conf bindaddr note
   (still container-loopback, unchanged).

3. Rewrite the "### Recipe" to the simplified sequence, keeping the honest
   CI-vs-manual boundary language (do NOT delete the "CI-vs-manual boundary"
   subsection — the fake-media CI test still proves only the mechanical
   lifecycle; the human ear still owns clipping/barge-in feel):
   - Step 1: fill secrets — copy `.env.example` → `.env` and set
     ASTERISK_ARI_PASSWORD / SOFTPHONE_SIP_PASSWORD / TELEPHONY_ACCESS_PIN /
     TELEPHONY_PASSPHRASE_WORDS (four words); ensure apps/voice/.env has real
     Deepgram/Anthropic/ElevenLabs keys (`make -C apps/voice env`). DELETE the
     old manual `sed`/`${VAR}`-substitution instructions entirely (now
     automatic).
   - Step 2: `docker compose up` — one command brings up config-render →
     Asterisk → controller, already wired over ARI. DELETE the old "step 3:
     resolve the ARI-reachability prerequisite / pick option (a) or (b)" — it
     is now automatic (option (a) is baked in).
   - Step 3: register a SIP softphone (Linphone/baresip) as endpoint
     `dev-softphone`: username `softphone`, password = SOFTPHONE_SIP_PASSWORD,
     registrar/domain `127.0.0.1:5060`. Place a call to any 2+ digit number
     (e.g. `1000`) — the `_X.` inbound pattern routes it to Stasis(klanker).
   - Step 4: confirm the gated happy path (silent answer → speak the 4
     passphrase words OR key the DTMF PIN → greeting NOT clipped → short
     multi-turn → barge-in interrupts TTS → clean hangup).
   - Step 5: confirm the fail-closed path (silent past gate_window_seconds →
     static goodbye → clean hangup, no CallSession built).
   - Step 6: confirm no resource leaks (single idempotent teardown log line;
     `docker exec klanker-asterisk-dev asterisk -rx 'core show channels'`
     shows no stale externalMedia channel / bridge after each call).
   - Keep the module smoke check reference and the "Status: not yet run"
     honesty note (update it to say the recipe is now one-command, still
     pending a human with a live Docker daemon + softphone).

Keep it accurate: the softphone auth username is `softphone` (pjsip.conf
`softphone-auth`), the endpoint/AOR id is `dev-softphone`, the dialed number
is any 2+ digit string.
  </action>
  <verify>
    <automated>grep -q 'docker compose up' apps/voice/asterisk/README.md &amp;&amp; grep -q 'dev-softphone' apps/voice/asterisk/README.md &amp;&amp; grep -qi 'CI-vs-manual boundary' apps/voice/asterisk/README.md &amp;&amp; echo README_OK</automated>
    <automated>grep -vE '^\s*(#|;|>|\|)' apps/voice/asterisk/README.md | grep -c "sed -i" | grep -qx 0 &amp;&amp; echo NO_STALE_SED_STEP</automated>
  </verify>
  <done>
The §19-C section documents the one-command `docker compose up` flow, names
the real softphone credentials (endpoint `dev-softphone`, user `softphone`)
and the dialed extension (any 2+ digit number, e.g. 1000), the two former
"Known limitation" blockers are marked resolved, the stale manual `sed`
substitution step is gone, and the honest CI-vs-manual boundary + fail-closed
+ no-leak + not-yet-run-live language is preserved.
  </done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| host → ARI (8088) | ARI is call-control (answer/externalMedia/bridge/hangup); must never be reachable from a public interface (§18/§25.C). |
| SIP softphone → Asterisk (5060) | Untrusted SIP leg; gated behind the §24 silent answer-gate before any LLM/TTS runs (D-05). |
| .env secrets → containers | ARI/SIP passwords + gate PIN/passphrase + provider keys must reach the containers at runtime without entering git or image layers (D-09). |

## STRIDE Threat Register

| Threat ID | Category | Component | Severity | Disposition | Mitigation Plan |
|-----------|----------|-----------|----------|-------------|-----------------|
| T-11Q-01 | Information Disclosure | ARI HTTP server | high | mitigate | Controller reaches ARI via `network_mode: service:asterisk` (127.0.0.1:8088 inside the shared netns); the dead `8088:8088` host publish is removed → ARI has zero host surface. http.conf bindaddr stays 127.0.0.1 (unchanged). |
| T-11Q-02 | Information Disclosure | Rendered secrets | high | mitigate | Real ARI/SIP passwords are substituted only into gitignored `.rendered/{ari,pjsip}.conf` at container start; tracked `.conf` templates keep `${VAR}` placeholders; `.gitignore` ignores `.rendered/`; `.dockerignore` keeps `.env` out of the controller image. |
| T-11Q-03 | Elevation of Privilege | Inbound dialplan | medium | accept | Unchanged from 11-02: extensions.conf stays the single inbound-only `[from-klanker-inbound]` context with no `Dial()`; test_asterisk_configs.py still enforces it (this plan does not touch it). |
| T-11Q-SC | Tampering | render sidecar base image `python:3.12-slim` | low | accept | Matches the existing apps/voice/Dockerfile base pin practice; no new package installs (no npm/pip/cargo add) → no package-legitimacy gate required. Asterisk image stays the 11-02 pinned tag. |
</threat_model>

<verification>
- `python3 render_configs.py` produces secret-substituted ari.conf/pjsip.conf
  with no `${` remaining, for passwords containing shell-special characters.
- `uv run pytest tests/test_asterisk_configs.py -q` stays green (tracked
  templates unchanged).
- `docker compose config` validates the full file (render + asterisk +
  klanker-telephony + sipp) with no daemon required.
- `klanker-telephony` uses `network_mode: "service:asterisk"`, declares no
  `ports:`, and the `asterisk` service no longer publishes 8088.
- webrtc.py and server.py are byte-unchanged.
- README §19-C documents the one-command flow with correct softphone creds
  and dialed extension, keeps the CI-vs-manual boundary, and drops the stale
  manual `sed` step.

Note (authoring env has no Docker daemon): the live `docker compose up`, the
module smoke check, and the actual §19-C call remain the human proof — not a
task gate here. All task gates run without a daemon.
</verification>

<success_criteria>
A human with Docker Desktop + a SIP softphone can, after filling
apps/voice/asterisk/.env and apps/voice/.env, run a single
`docker compose up` in apps/voice/asterisk/ and have Asterisk + the Klanker
telephony controller come up wired over private ARI, register a softphone,
dial the inbound extension, and exercise gate → greet → barge-in → hangup —
with no secret ever committed, the §18/§25 private-only ARI posture intact,
the browser path untouched, and the config lint still green.
</success_criteria>

<output>
Create `.planning/quick/260712-ckd-make-the-phase-11-19-c-local-softphone-p/260712-ckd-SUMMARY.md` when done
</output>
