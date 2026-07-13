---
phase: quick-260712-ckd
plan: 01
subsystem: infra
tags: [asterisk, docker-compose, ari, telephony, secrets-rendering]

# Dependency graph
requires:
  - phase: 11-voip-ms-telephony-local-asterisk-edge
    provides: "Asterisk edge dev harness (Plan 02), standalone telephony controller (Plan 07/D-08), §24 gate (Plan 06/D-05)"
provides:
  - "One-command docker compose up bring-up of the full Phase 11 §19-C local softphone harness (config-render -> Asterisk -> controller)"
  - "render_configs.py: stdlib secret renderer for ari.conf/pjsip.conf, secrets never land in tracked/git-visible files"
  - "klanker-telephony compose service reaching ARI via shared Asterisk network namespace, zero ARI host-publish surface"
affects: ["11-voip-ms-telephony-local-asterisk-edge (§19-C live-verify item)"]

tech-stack:
  added: []
  patterns:
    - "Secret rendering via a no-profile compose sidecar (asterisk-config-render) + service_completed_successfully depends_on, writing only into a gitignored output dir"
    - "network_mode: service:<name> to reach an app's private-loopback-bound API from a sibling container with zero new host-published surface"

key-files:
  created:
    - apps/voice/asterisk/render_configs.py
    - apps/voice/asterisk/.gitignore
  modified:
    - apps/voice/asterisk/docker-compose.yml
    - apps/voice/asterisk/README.md

key-decisions:
  - "string.Template.safe_substitute (not sed/envsubst) for secret rendering -- zero shell-escaping surface for passwords containing spaces/$/&/|/quotes"
  - "env_file entries use required: false (path/required object form) so docker compose config validates cleanly before a human has run cp .env.example .env -- the controller itself fails loudly with ConfigError if creds are still unset (restart: \"no\")"
  - "Removed the asterisk service's dead 8088:8088 host publish entirely (it forwarded to a non-listening loopback) rather than leaving it for forward compatibility -- klanker-telephony's shared netns makes it unnecessary and its removal is a strict tightening of §18/§25.C"

patterns-established:
  - "Pattern: gitignored .rendered/ dir for secret-bearing config copies, tracked templates keep ${VAR} placeholders forever so structural-invariant lints (test_asterisk_configs.py) never need real secrets to pass"

requirements-completed: ["§19-C", "D-05", "D-08", "§18", "§25"]

coverage:
  - id: D1
    description: "render_configs.py substitutes real secrets from .env into gitignored .rendered/{ari,pjsip}.conf (string.Template.safe_substitute, handles shell-special-char passwords); tracked templates keep ${VAR} placeholders unchanged"
    requirement: "D-09 (secrets never hardcoded; adjacent to §19-C)"
    verification:
      - kind: other
        ref: "python3 apps/voice/asterisk/render_configs.py --templates apps/voice/asterisk --out <dir> with a password containing spaces/$/&/| -- no ${ left in output"
        status: pass
      - kind: unit
        ref: "apps/voice/tests/test_asterisk_configs.py (9 tests, tracked-template lint unaffected)"
        status: pass
    human_judgment: false
  - id: D2
    description: "klanker-telephony compose service reaches ARI at 127.0.0.1:8088 via network_mode: service:asterisk (no ports:); asterisk service no longer publishes 8088 on the host at all"
    requirement: "§18/§25.C (private-only ARI)"
    verification:
      - kind: other
        ref: "docker compose config --no-interpolate | grep klanker-telephony block for network_mode: service:asterisk and absence of ports:"
        status: pass
      - kind: other
        ref: "docker compose config --no-interpolate | asterisk service block has zero occurrences of 8088"
        status: pass
      - kind: unit
        ref: "git diff --stat -- src/klanker_voice/webrtc.py server.py (empty -- browser path byte-unchanged)"
        status: pass
    human_judgment: false
  - id: D3
    description: "README's Manual §19-C softphone proof section rewritten to the one-command docker compose up flow; both former Known-limitation callouts marked RESOLVED; stale manual sed substitution step removed; CI-vs-manual boundary preserved"
    verification:
      - kind: other
        ref: "grep checks in apps/voice/asterisk/README.md: 'docker compose up', 'dev-softphone', 'CI-vs-manual boundary' present; zero non-comment 'sed -i' lines"
        status: pass
    human_judgment: false
  - id: D4
    description: "A human with Docker Desktop + a SIP softphone actually runs `docker compose up` and completes the §19-C recipe (gate -> greet -> barge-in -> hangup, fail-closed path, no-leak check) end-to-end"
    verification: []
    human_judgment: true
    rationale: "This authoring sandbox has no running Docker daemon (docker info fails to connect to the daemon socket); greeting-clipping and barge-in feel are inherently perceptual judgments no automated test can make. This was already the single outstanding human-verify item from Phase 11 Plan 07 and remains so after this quick task -- only the mechanics of getting to that live moment were simplified to one command."

duration: 35min
completed: 2026-07-12
status: complete
---

# Quick Task 260712-ckd: One-Command §19-C Softphone Harness Summary

**Turned the Phase 11 §19-C local softphone proof from an 8-step manual recipe into a single `docker compose up`: a new `asterisk-config-render` sidecar substitutes real secrets from `.env` into gitignored rendered configs, and a new `klanker-telephony` service reaches ARI by sharing Asterisk's network namespace instead of a published port.**

## Performance

- **Duration:** ~35 min
- **Tasks:** 3/3 completed
- **Files modified:** 4 (2 created, 2 modified)

## Accomplishments

- Closed the "`.conf` `${VAR}` placeholders are documentation-only" blocker flagged by 11-02/11-07: `render_configs.py` (dependency-free stdlib, `string.Template.safe_substitute`) renders `ari.conf`/`pjsip.conf` into gitignored `.rendered/` at container start via a new `asterisk-config-render` compose service, with `depends_on: { condition: service_completed_successfully }` gating Asterisk's start. Tracked templates stay placeholder-only forever, so `test_asterisk_configs.py`'s 9-test lint keeps passing unchanged.
- Closed the "ARI loopback-vs-published-port mismatch" blocker: a new `klanker-telephony` compose service builds from the existing app image and runs the standalone controller (`python -m klanker_voice.telephony.controller`) with `network_mode: "service:asterisk"`, reaching `http://127.0.0.1:8088` inside Asterisk's own network namespace. The `asterisk` service's now-dead `8088:8088` host publish (it forwarded to a non-listening container-loopback bindaddr and reached nothing) was removed entirely — ARI has zero host-published surface, a strict tightening of §18/§25.C, not a relaxation.
- Rewrote `apps/voice/asterisk/README.md`'s "Manual §19-C softphone proof" section: both former "Known limitation" callouts are now marked RESOLVED with the mechanism that fixed them; the recipe shrank from 8 steps to 6 (dropped the manual `sed`-substitution workaround and the "pick option (a) or (b) to resolve ARI reachability" step); the CI-vs-manual boundary, fail-closed check, and no-resource-leak check are all preserved verbatim.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add the config-render step** - `afb0c96` (feat)
2. **Task 2: Add the klanker-telephony controller service** - `8a5f18a` (feat)
3. **Task 3: Rewrite the README §19-C recipe** - `a1c8750` (docs)

## Files Created/Modified

- `apps/voice/asterisk/render_configs.py` - stdlib secret renderer (ari.conf/pjsip.conf `${VAR}` -> real values from `os.environ`), no secret hardcoded, writes only to `--out`
- `apps/voice/asterisk/.gitignore` - ignores `.rendered/` (rendered configs carry real secrets)
- `apps/voice/asterisk/docker-compose.yml` - new `asterisk-config-render` service, `asterisk` service repointed to consume `.rendered/{ari,pjsip}.conf` (secrets removed from its own `environment:`), new `klanker-telephony` service (shared netns, no `ports:`), dead `8088:8088` publish removed from `asterisk`
- `apps/voice/asterisk/README.md` - Bring-up/Secrets/Files sections and the full "Manual §19-C softphone proof" recipe rewritten for the one-command flow

## Decisions Made

- Used `string.Template.safe_substitute` instead of `sed`/`envsubst` for secret rendering — zero shell-escaping surface for passwords containing spaces, `$`, `&`, `|`, or quotes; proven against a deliberately hostile test password (`p@ss w0rd|#&`) during Task 1 verification.
- `env_file` entries for `klanker-telephony` use the `{path, required: false}` object form (not bare path strings) so `docker compose config` validates cleanly on a fresh checkout, before a human has run `cp .env.example .env` in either `apps/voice/` or `apps/voice/asterisk/`. The controller still fails loudly with a `ConfigError` at runtime if `ASTERISK_ARI_USERNAME`/`ASTERISK_ARI_PASSWORD` are unset (`restart: "no"` prevents a crash-loop).
- Removed the `asterisk` service's `8088:8088` host publish entirely rather than leaving it "for forward compatibility" (as the pre-existing comment said) — it forwarded to a container-loopback-bound ARI server and reached nothing; `klanker-telephony`'s shared-netns approach makes it fully unnecessary, and removing it is a strict tightening of the §18/§25.C private-only guarantee.

## Deviations from Plan

**1. [Rule 3 - Blocking] `docker compose config` requires `env_file` targets to exist**

- **Found during:** Task 2 verification
- **Issue:** The plan specified `env_file: [../.env, .env]` as bare path strings. `apps/voice/asterisk/.env` does not exist in a fresh checkout (only `.env.example` is tracked — `.env` is user-created per the plan's own `user_setup` block). `docker compose config` with bare-string `env_file` entries hard-fails if the referenced file is missing, which would break the plan's own daemon-free verify gate on a machine that hasn't yet run `cp .env.example .env`.
- **Fix:** Changed both `env_file` entries to the compose-spec object form (`{path: ..., required: false}`), which Docker Compose v5.2.0 supports. `docker compose config` now validates cleanly whether or not `.env` exists yet; the controller's own `ConfigError` (already in `telephony/__main__.py`, unmodified) remains the actual runtime enforcement that ARI creds are set.
- **Files modified:** `apps/voice/asterisk/docker-compose.yml` (part of Task 2's own scope, no extra file touched)
- **Verification:** `docker compose config --no-interpolate` exits 0 on a checkout with `apps/voice/.env` present but `apps/voice/asterisk/.env` absent (this repo's actual state).
- **Committed in:** `8a5f18a` (Task 2 commit)

**2. [Process note, not a plan deviation] `docker compose config` (without `--no-interpolate`) prints resolved `env_file` contents in plaintext**

- **Found during:** Task 2 verification, first invocation
- **Issue:** Running plain `docker compose config` resolves `env_file` values and prints them into the `environment:` block of its YAML output — this dumped real `ANTHROPIC_API_KEY`/`DEEPGRAM_API_KEY`/`ELEVENLABS_API_KEY` values (from the pre-existing, legitimate `apps/voice/.env`) into a captured shell-output file. This is inherent `docker compose config` behavior, not a bug introduced by this plan's changes — the pre-existing compose file had no `env_file` directive at all, so this exposure surface is new as a side effect of Task 2's `env_file` addition.
- **Fix:** Immediately overwrote the temp file that had captured the output (could not `rm` it directly — this sandbox's `rm` requires interactive confirmation the tool cannot supply, so a `Write` of redacted placeholder content was used instead). All subsequent verification in this session used `docker compose config --no-interpolate`, which shows the `env_file` list without resolving its contents and produces no secret-bearing output. This is a genuine sharp edge worth flagging: any human debugging this compose file with plain `docker compose config` should expect real secrets in the output and handle it accordingly (never pipe to a world-readable file, never paste into chat/logs).
- **Files modified:** none (process-only; no code or config change was needed)
- **Verification:** `docker compose config --no-interpolate` confirmed to validate cleanly with zero secret-bearing output for all of this task's remaining verify gates.
- **Committed in:** n/a (no code change)

---

**Total deviations:** 1 auto-fixed (1 blocking / compose-spec compatibility), 1 process note (no code impact)
**Impact on plan:** The `required: false` change was necessary for the plan's own daemon-free verify gate to actually pass on a fresh/partial checkout — no scope creep, stays within Task 2's declared file (`docker-compose.yml`). The `docker compose config` secret-exposure note is an operational caveat for future humans running this harness, not a change to shipped behavior.

## Issues Encountered

None beyond the two items documented above (both resolved within the session).

## User Setup Required

None new. The plan's own `user_setup` block (fill `apps/voice/asterisk/.env` from `.env.example`, and `apps/voice/.env` via `make -C apps/voice env`) is unchanged from before this quick task — it is the same live-proof prerequisite Phase 11 Plan 07 already documented, just now exercised through one `docker compose up` instead of an 8-step manual recipe.

## Next Phase Readiness

- Phase 11's single outstanding item (the §19-C live softphone proof, tracked in `.planning/STATE.md`) is now mechanically a one-command exercise: `cd apps/voice/asterisk && cp .env.example .env` (fill values) `&& docker compose up`, then register a SIP softphone and work through the README's 6-step recipe.
- No blockers introduced. `webrtc.py`/`server.py` (the browser path) verified byte-unchanged throughout (`git diff --stat` empty on both files after every task).
- The actual live run (Docker daemon + real SIP softphone + a human ear judging greeting-clipping/barge-in feel) remains explicitly deferred — this quick task did not and could not close that item, only simplified the path to it.

---
*Quick task: 260712-ckd*
*Completed: 2026-07-12*

## Self-Check: PASSED

All 4 created/modified files confirmed present on disk; all 3 task commit hashes (`afb0c96`, `8a5f18a`, `a1c8750`) confirmed present in `git log`.
