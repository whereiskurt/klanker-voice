---
phase: 01-local-pipeline-latency-harness
plan: 05
subsystem: voice-pipeline
tags: [elevenlabs, tts, voice-audition, flash-v2-5, persona, phase-gate, d-02, d-03, d-12]

# Dependency graph
requires:
  - phase: "01-04"
    provides: "tuned pipeline.toml (Nova-3 + SmartTurn v3), docs/TUNING.md endpointing + RE-ESCALATION record, persona v2, accepted v2v p50 ~1402ms"
provides:
  - "K's voice locked by ear (D-02): 'Will - Relaxed Optimist' (bIHbv24MWmeRgasZH58o), speed 1.1 (D-03), in pipeline.toml [tts]"
  - "docs/TUNING.md COMPLETE (D-12): endpointing verdicts + RE-ESCALATION record (untouched) + Chosen voice section with both audition lineups, tiebreak, and the final full knob table"
  - "apps/voice/scripts/audition.py — reusable 3-voice same-script audition renderer (live library query, D-03 brief scoring, one render call per voice)"
  - "persona v3: self-reference is KPH (never 'K'), TTS-safe DEFCON spelling rule"
  - "user conversational-feel sign-off: APPROVED (phase gate closed)"
affects: [phase-4-prod-config, phase-5-hud, PIPE-08]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Voice audition renders each candidate in its own HTTP call — voice settings never flip mid-session/stream (RESEARCH Pitfall 8)"
    - "Pronunciation-sensitive tokens are governed by persona spelling rules (DEFCON uppercase, 'DEFCON dot run'), not pipeline text-normalization code"

key-files:
  created:
    - apps/voice/scripts/audition.py
  modified:
    - apps/voice/pipeline.toml
    - apps/voice/prompts/concierge.md
    - docs/TUNING.md

key-decisions:
  - "D-02 delivered: winner 'Will - Relaxed Optimist' (bIHbv24MWmeRgasZH58o) picked by ear over incumbent Chris in a final tiebreak on the user's own line; speed stays 1.1 (D-03, no tweak requested)"
  - "Round-1 lineup Chris/Jessica/Roger -> user direction: male only, keep Chris, avoid smug/classy/knowing registers (Roger's 'snideness' rejected; also disqualified Eric, the only other conversational male premade)"
  - "Round-2 lineup Chris (incumbent, take reused) + Will + Charlie — the plan's single re-audition loop; Charlie eliminated, Chris-vs-Will tiebreak decided it"
  - "Persona v2 -> v3 at sign-off (user-required): self-reference is always KPH (spoken K-P-H), never 'K'/'Kay'; plus a TTS-safe spelling rule — DEFCON uppercase, 'DEFCON dot run', never glued 'defcon.run'"
  - "Audition renders + manifest stay gitignored per plan (artifacts/); the durable voice record is docs/TUNING.md 'Chosen voice' (winner, runners-up, both lineups, tiebreak, final knob table)"
  - "Phase gate: user APPROVED the conversational feel of the tuned pipeline (greet-first, punchy persona, natural barge-in) with the KPH correction landed"

patterns-established:
  - "Pattern: user-in-the-loop audio decisions run as blocking decision checkpoints with exact afplay commands and per-candidate rationale; the pick lands in config + TUNING.md in the same task"

requirements-completed: [PIPE-01, PIPE-06]

coverage:
  - id: D1
    description: "Three shortlisted ElevenLabs voices rendered the same K script side-by-side; user picked the winner by ear (D-02)"
    requirement: "PIPE-06"
    verification:
      - kind: integration
        ref: "uv run python scripts/audition.py — 3 MP3s + manifest.json in artifacts/audition/ (round 1); round-2 loop rendered 2 new males; Chris-vs-Will tiebreak rendered on the user's line"
        status: pass
      - kind: manual_procedural
        ref: "Task 2 decision checkpoint (round 1 -> re-audition -> round 2 -> tiebreak): user picked Will by ear"
        status: pass
    human_judgment: true
    rationale: "D-02 is explicitly a user-ears deliverable; the pick is the user's, recorded with both lineups and the tiebreak in docs/TUNING.md"
  - id: D2
    description: "Winning voice_id and speed live in pipeline.toml and are recorded with reasoning in docs/TUNING.md (D-02, D-03, D-12)"
    requirement: "PIPE-06"
    verification:
      - kind: unit
        ref: "load_config('pipeline.toml') asserts tts.voice_id non-empty (bIHbv24MWmeRgasZH58o); tests/test_config.py 21 passed"
        status: pass
      - kind: other
        ref: "docs/TUNING.md 'Chosen voice' section: winner + runners-up + user reasoning + final full knob table; endpointing/RE-ESCALATION record untouched"
        status: pass
      - kind: integration
        ref: "greeting scenario 1/1 PASS live under final pipeline.toml through the real WS TTS path (chosen voice)"
        status: pass
    human_judgment: false
  - id: D3
    description: "User held a final conversation with tuned K and signed off on the conversational feel (phase gate)"
    requirement: "PIPE-01"
    verification:
      - kind: manual_procedural
        ref: "Task 4 checkpoint: user ran bot.py -t webrtc, walked the six verification steps, verdict APPROVED (with the KPH persona correction, landed and re-verified live: greeting eval 1/1, bot log shows 'I'm KPH')"
        status: pass
    human_judgment: true
    rationale: "The phase's core value — 'slick' — is a human judgment; the user approved with one persona correction which was landed before close"

# Metrics
duration: ~60min (wall, including three user checkpoints)
completed: 2026-07-05
status: complete
---

# Phase 1 Plan 05: Voice Audition + Phase Gate Summary

**K's voice is "Will — Relaxed Optimist" (`bIHbv24MWmeRgasZH58o`) at speed 1.1 on `eleven_flash_v2_5`, picked by the user's ear through a 3-voice same-script audition, one male-only re-audition loop, and a Chris-vs-Will tiebreak on the user's own line; the winner is landed in `pipeline.toml`, `docs/TUNING.md` is complete (D-12), the persona is v3 (self-reference KPH, TTS-safe DEFCON spelling), and the user APPROVED the conversational feel — Phase 1 execution closes.**

## Performance

- **Duration:** ~60 min wall including three blocking user checkpoints (credential fix, voice picks, sign-off)
- **Completed:** 2026-07-05
- **Tasks:** 4/4 (2 auto + 2 blocking human checkpoints)
- **Live API spend:** 7 short ElevenLabs renders total (3 round-1, 2 round-2, 2 tiebreak — the plan's matrix plus the user-authorized tiebreak; no extra takes) + 2 greeting-eval runs

## Accomplishments

- **`apps/voice/scripts/audition.py` (D-02 renderer):** queries the live ElevenLabs voice library, scores premade voices against the D-03 brief (conversational bonus, narration/ambient/calm penalties, intelligibility nudges), shortlists exactly three, and renders the identical K-register script per candidate — one voice per HTTP call (Pitfall 8), MP3s + manifest.json to gitignored `artifacts/audition/`, key never printed or persisted (T-1-11).
- **The audition itself:** Round 1 (Chris / Jessica / Roger) → user direction: male, keep Chris, no snideness → Round 2 (Chris incumbent + Will + Charlie, only two new renders) → final tiebreak, both finalists speaking the user's own line ("Hey! It's KPH, your concierge…") → **winner: Will**.
- **Config + record landed:** `pipeline.toml` `[tts]` `voice_id = "bIHbv24MWmeRgasZH58o"`, speed 1.1 kept. `docs/TUNING.md` "Chosen voice" completed with both lineups, the user's direction and tiebreak, runners-up, and the final full stt/turn/llm/tts knob table — the endpointing A/B and RE-ESCALATION records above it untouched. TUNING.md is now the complete Phase-1 verdict record (D-12).
- **Persona v3 (sign-off correction):** self-reference is always **KPH** (spoken K-P-H), never "K"/"Kay"; new TTS-safe spelling rule — DEFCON uppercase, "DEFCON dot run", never glued "defcon.run". Verified live: greeting eval 1/1 pass, bot log shows "I'm KPH" through the real WS TTS path.
- **Phase gate:** user held the final conversation and **APPROVED** the conversational feel.

## Task Commits

1. **Task 1: audition renderer (D-02)** - `f9b6d3e` (feat)
2. **Task 2: voice pick** - user decision, no code (winner: Will, `bIHbv24MWmeRgasZH58o`)
3. **Task 3: winner landed + TUNING.md completed (D-12)** - `bdbe557` (feat)
4. **Task 4: sign-off + persona v3 correction** - `7f0c42b` (feat)

## Files Created/Modified

- `apps/voice/scripts/audition.py` - 3-voice same-script audition renderer (library query, brief scoring, per-voice render calls, manifest)
- `apps/voice/pipeline.toml` - `tts.voice_id` = audition winner; speed 1.1 final (Phase-4 prod artifact now complete)
- `apps/voice/prompts/concierge.md` - persona v3: KPH self-reference + DEFCON spelling rule
- `docs/TUNING.md` - "Chosen voice" section completed + final knob table (persona reference updated to v3); prior records untouched

## User Sign-off (Task 4, verbatim record)

**Verdict: APPROVED**, with one required correction (landed as persona v3 before close):

> "I'd like K to refer to themselves as KPH.. K (Kay) doesn't sound right."

Pronunciation observation (addressed with the persona spelling rule):

> "It also said DEFCON right all the time except once, it kinda sead deeeefcon."

Forward-looking note, recorded as an **aspiration, not scope** (the KPH part is fixed in this plan; the knowledge/RAG part is future-phase material owned by the orchestrator):

> "I can see how I will want a massive RAG or something really smart that can kind of steer... I'd want KPH to always refer to themselves as KPH, and have all of the knowledge of my repos.. and some scripts and stuff I'd train it on."

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] SSM bootstrap path broken — .env restored from the main checkout**
- **Found during:** Task 1 setup (fresh worktree has no gitignored `.env`)
- **Issue:** `make -C apps/voice env` fails with **ParameterNotFound** for `/kmv/bootstrap/deepgram_api_key` (profile `klanker-application`, us-east-1) — the D-10 bootstrap parameters do not exist at that path (auth itself succeeded). Side-finding worth fixing: either the parameters were never created or the path/account changed.
- **Fix:** copied the known-good `.env` from the main checkout into the worktree (mode 600). No secrets printed or committed.
- **Files modified:** none (gitignored runtime file only)

**2. [Rule 1 - Bug] Stale bot process caused a false 403 on the KPH verification eval**
- **Found during:** Task 4 (persona v3 re-verification)
- **Issue:** the previous eval run's bot process survived its cleanup (`kill` didn't reach the `uv run` grandchild) and held port 7860, rejecting the new eval's WebSocket with HTTP 403.
- **Fix:** killed the stale PID, re-ran cleanly (1/1 pass). Not a pipeline or persona defect.
- **Files modified:** none

### Notes (plan-conformant, flagged for transparency)

- **Audition manifest is gitignored, not committed** — exactly as the plan specifies (`artifacts/` gitignored; "the manifest travels with them"). The durable, committed voice record is `docs/TUNING.md` "Chosen voice" (both lineups, voice_ids, rationale, tiebreak) plus the committed `audition.py` that regenerates the manifest. Round-1 eliminated takes (Jessica, Roger) remain on local disk as the round-1 record.
- **Tiebreak renders (2 extra takes)** were explicitly user-authorized via the coordinator — not renderer drift.

---

**Total deviations:** 2 auto-fixed (1 Rule-3 environment, 1 Rule-1 transient process). **Impact:** none architectural; no plan-scope change.

## Authentication Gates

- **ElevenLabs key lacked `voices_read` (Task 1):** the scoped key ("klanker-maker-voice-v1") could render TTS but returned 401 `missing_permissions` on the voice-library query. Returned a human-action checkpoint; the user enabled **Voices: Read** in the dashboard; verified 200 on `GET /v1/voices` and resumed. Documented as normal gated flow.

## Follow-up Candidates (not built here)

- **TTS text-normalization filter:** the DEFCON mispronunciation is handled with a persona spelling rule (one line, zero code). If more pronunciation-sensitive tokens accumulate (repo names, "meshtk" → "mesh T K", etc.), a small text-normalization processor in front of TTS is the durable fix — candidate for the PIPE-08 latency/polish phase, not this plan.
- **SSM bootstrap repair:** recreate `/kmv/bootstrap/{deepgram,anthropic,elevenlabs}_api_key` SecureStrings (or fix the script's path) so `make env` works on fresh clones/worktrees — currently only the main checkout's `.env` carries the keys.

## Next Phase Readiness

- **Phase 1 is fully executed:** all five plans complete; Phase-1 artifacts are final — `pipeline.toml` (tuned winner + chosen voice), `prompts/concierge.md` (v3), harness JSON schema, five eval scenarios, completed `docs/TUNING.md`.
- **Phase 4** inherits `pipeline.toml` unchanged as the prod default; **Phase 5** inherits the harness + TUNING.md record; **PIPE-08** (scoped later phase) owns the ≤1.2s work and is the natural home for the text-normalization filter.
- The user's RAG/knowledge aspiration is recorded above for the orchestrator's future-scope capture.

## Self-Check: PASSED

- `apps/voice/scripts/audition.py` exists and is tracked (commit f9b6d3e) — FOUND
- `pipeline.toml` `tts.voice_id` loads as `bIHbv24MWmeRgasZH58o`; 21 config tests pass — FOUND/PASS
- `docs/TUNING.md` contains "Chosen voice" with winner + knob table; endpointing/RE-ESCALATION sections intact — FOUND
- Commits f9b6d3e, bdbe557, 7f0c42b present in git log — FOUND
- Greeting eval 1/1 PASS live under final config with persona v3 ("I'm KPH" in bot log) — PASS
- No secrets in any committed file; renders/manifest gitignored; no forbidden project-name strings in produced files — PASS

---
*Phase: 01-local-pipeline-latency-harness*
*Completed: 2026-07-05*
