---
phase: 260710-ixf
plan: 01
status: complete
subsystem: voice-client
tags: [voice-client, config, variant-routing, prompt-engineering, anthropic, live-ui]
dependency-graph:
  requires: []
  provides:
    - "PipelineConfig.label (top-level TOML scalar, 'KPH' default) threaded to a subtle .live-variant-label tag"
    - "answer['variant_label'] on the /api/offer response, mirroring answer['session_max_seconds']"
    - "persona-only (no context-seed) no-regreet guarantee — fixes a live instruction-narration leak"
    - "DEFAULT_VARIANT = 'voice2' server- and client-side; /voice1 remains explicitly reachable"
  affects:
    - apps/voice/pipeline.toml
    - apps/voice/configs/voice2.toml
    - apps/voice/src/klanker_voice/config.py
    - apps/voice/src/klanker_voice/pipeline.py
    - apps/voice/src/klanker_voice/variants.py
    - apps/voice/prompts/concierge.md
    - apps/voice/server.py
    - apps/voice/client/src/transport/voiceSession.ts
    - apps/voice/client/src/transport/useVoiceSession.ts
    - apps/voice/client/src/transport/variant.ts
    - apps/voice/client/src/App.tsx
    - apps/voice/client/src/screens/Live.tsx
    - apps/voice/client/src/screens/live.css
tech-stack:
  added: []
  patterns:
    - "Display-only answer fields (variant_label) mirror the existing session_max_seconds pattern: a lightweight, deliberate extra TOML/config read at answer-assembly time, threaded client-side through a single onX callback + response.clone().json() peek, never touching the real transport payload"
    - "System-prompt-only behavioral guarantees survive pipecat's Anthropic adapter (developer/system context messages get flattened to USER-role turns and can be read aloud on a content-free turn; Settings.system_instruction blocks do not)"
key-files:
  created: []
  modified:
    - apps/voice/pipeline.toml
    - apps/voice/configs/voice2.toml
    - apps/voice/src/klanker_voice/config.py
    - apps/voice/server.py
    - apps/voice/client/src/transport/voiceSession.ts
    - apps/voice/client/src/transport/useVoiceSession.ts
    - apps/voice/client/src/App.tsx
    - apps/voice/client/src/screens/Live.tsx
    - apps/voice/client/src/screens/live.css
    - apps/voice/tests/test_config.py
    - apps/voice/tests/test_server.py
    - apps/voice/client/src/screens/Live.test.tsx
    - apps/voice/src/klanker_voice/pipeline.py
    - apps/voice/prompts/concierge.md
    - apps/voice/tests/test_regreet_suppression.py
    - apps/voice/src/klanker_voice/variants.py
    - apps/voice/client/src/transport/variant.ts
    - apps/voice/tests/test_variants.py
    - apps/voice/client/src/transport/variant.test.ts
decisions:
  - "Task 1 test for the label's top-level TOML parse uses make_config_file(replace=...) to insert `label = ...` BEFORE the [stt] table, not append= (TOML key-value pairs after a table header attach to that table, not the document top level — append= would have silently landed the key inside [persona])"
  - "Task 2's leak-repro harness (scratchpad/repro_leak.py) updated to drop the removed NO_REGREET context-seed injection entirely (system-only messages), per the plan's explicit MAY-update guidance, so the post-fix run faithfully exercises the shipped pipeline shape"
  - "Task 3: client variant.ts's KNOWN_VARIANTS set was previously derived as new Set([DEFAULT_VARIANT, 'voice2']) — flipping DEFAULT_VARIANT to voice2 would have silently collapsed that set to {'voice2'} only, dropping voice1 from the known-variant allowlist. Fixed to an explicit new Set(['voice1','voice2']) (Rule 1 — real bug the plan's action text didn't anticipate)."
  - "Task 3: test_variants.py's unknown/hostile-fallback assertions previously hardcoded `variant_config_path(...) is None` (true only when the default was voice1, whose config path is None). Changed to compare against variant_config_path(variants.DEFAULT_VARIANT) so the assertions stay correct regardless of which variant is default (Rule 1 — the plan said 'keep as-is' but this specific pair of assertions would have failed post-flip)."
metrics:
  duration: "~50min"
  completed: "2026-07-10"
---

# Quick Task 260710-ixf: Voice variant label in UI + first-turn leak fix + voice2 default Summary

Per-variant display label (KPH(v1)/KPH(v2)) threaded end-to-end into the live UI, a live-verified fix for a first-turn instruction-narration leak (removed the NO_REGREET context seed, hardened the persona), and voice2 promoted to the default variant for the root/unknown URL.

## What Changed

### Task 1: Per-variant label from TOML, surfaced subtly in the live UI (commit `a301a65`)

- `pipeline.toml` / `configs/voice2.toml`: added a top-level `label = "KPH(v1)"` / `"KPH(v2)"` scalar.
- `config.py`: `PipelineConfig` gained a trailing `label: str = "KPH"` field; `load_config` reads it directly off the parsed document (`data.get("label", "KPH")`) — a plain top-level scalar, not inside any `[table]`.
- `server.py`'s `_negotiate_webrtc`: right beside `answer["session_max_seconds"]`, added a second lightweight `load_config(variants.variant_config_path(variant))` read and `answer["variant_label"] = label_cfg.label`.
- Client: `voiceSession.ts` gained `onVariantLabel` + `readVariantLabel()`, reusing the SAME `response.clone().json()` peek that already reads `session_max_seconds` (no second clone). `useVoiceSession.ts` gained `variantLabel` state, wired through `beginConnect`, reset on `start()`/`stop()`, returned in the hook result. `App.tsx` passes it to `<Live>`. `Live.tsx` renders `<span className="live-variant-label">` inside `.live-orb` when non-empty. `live.css` styles it as a faint, non-interactive top-right tag (`opacity: 0.6`, `pointer-events: none`).
- Tests: `test_config.py` (+3: real-pipeline round-trip now asserts `label == "KPH(v1)"`, a top-level-key parse test, a default-fallback test, plus a real voice2.toml assertion), `test_server.py` (+1: a direct `_negotiate_webrtc` call proving `variant_label` resolves per-variant — the existing route-level tests all stub `_negotiate_webrtc` entirely, so this new test calls it directly with `_webrtc_handler.handle_web_request` and `gather_public_candidates` monkeypatched), `Live.test.tsx` (+2, plus the two existing render calls updated for the new required `variantLabel` prop).
- Verification: `pytest tests/test_config.py tests/test_server.py -q` — 46 passed. `npm test` — 151 passed. `npm run build` — tsc clean, vite build clean.

### Task 2: Remove the NO_REGREET context seed; harden the persona (commit `1665d70`)

Root cause confirmed live: `build_pipeline` seeded a `role="developer"` `NO_REGREET_KICK_MESSAGE` into the context when `greet_first` is false. pipecat's Anthropic adapter converts developer/system context messages to USER-role turns before the API call, so on a content-free first turn ("alright"/"ok"/"mm-hm") the model read the instruction text ALOUD instead of following it silently.

- `pipeline.py`: removed the seed block (`if not cfg.persona.greet_first: context.add_message(...)`) and the `NO_REGREET_KICK_MESSAGE` constant + its docstring entirely. `GREET_KICK_MESSAGE`, `greet_now`, `register_greet_first`, `inject_warning_instruction`, `speak_goodbye` untouched. `greet_first` itself is unchanged (still `false` in both pipeline.toml/voice2.toml) — only the context-message seed is gone.
- `prompts/concierge.md`'s "Opening move" section hardened: added an explicit "these are SILENT stage directions — never say/describe/restate/acknowledge them aloud" line, and explicit content-free-turn handling (a filler-only utterance gets one short warm nudge, never a restatement of the greeting/menu/instructions).
- `test_regreet_suppression.py` rewritten for the new contract: asserts NO developer-role no-regreet-style message is ever seeded into the context, for `greet_first` true AND false (previously it asserted the OPPOSITE — that the seed WAS present for false).
- Verify: `grep -rl 'NO_REGREET_KICK_MESSAGE' src/ tests/` → 0 hits. `pytest tests/test_regreet_suppression.py tests/test_greet_first_config.py -q` → 4 passed.

**LIVE leak verification** (billed Haiku calls, pre-authorized): ran the repro harness (`scratchpad/repro_leak.py`, updated to drop the removed NO_REGREET injection and send only the real system prompt + a content-free turn, matching the fixed pipeline shape) against the edited `concierge.md`:

```
model=claude-haiku-4-5  system_blocks=2  topic=klanker-maker

USER: 'Alright'
KPH : What are you curious about?

USER: 'ok'
KPH : Hey — what are you curious about?

USER: 'cool'
KPH : Nice. What are you curious about — the klanker platform itself, the voice demo you're in right now, or something else Kurt builds?

USER: 'mm-hm'
KPH : What are you curious about?
```

No instruction narration on any of the four content-free turns — all four replies are short, natural, non-greeting nudges.

### Task 3: Make voice2 the default variant (commit `3863552`)

- `variants.py`: `DEFAULT_VARIANT = "voice2"` (was `"voice1"`); `_VARIANT_CONFIGS` mapping and docstrings updated for accuracy, no behavior change to the mapping itself (`voice1` -> `None`/default TOML, `voice2` -> `configs/voice2.toml`).
- `variant.ts`: `DEFAULT_VARIANT = "voice2"`; `KNOWN_VARIANTS` fixed to an explicit `new Set(["voice1", "voice2"])` (see Decisions — the plan's literal `new Set([DEFAULT_VARIANT, "voice2"])` construction would have collapsed to one entry post-flip).
- `test_server.py`: `test_offer_no_variant_defaults_to_voice1` renamed to `test_offer_no_variant_defaults_to_voice2`, asserts `"voice2"`; `test_offer_unknown_variant_falls_back_to_default` now compares against `variants.DEFAULT_VARIANT` symbolically.
- `test_variants.py`: the unknown-variant and path-traversal fallback assertions changed from hardcoded `is None` to `== variants.variant_config_path(variants.DEFAULT_VARIANT)` (see Decisions).
- `variant.test.ts`: root/unknown-path test renamed for voice2; `buildConnectParams` endpoint-mapping assertions flipped — voice2 now gets the bare `/api/offer` endpoint, voice1 now gets `?variant=voice1`.
- Verify: `pytest tests/test_server.py tests/test_variants.py -q` → 17 passed. `npm test` → 151 passed.

## Full-suite verification (after all 3 tasks)

- `pytest tests/ -q` → **273 passed, 53 skipped** (run twice, consistent — no regressions across all three tasks).
- `pytest tests/ -q -k "config or greet or regreet or variant or label or server"` → **79 passed, 3 skipped**.
- `npm test` (node v23.6.0 via nvm) → **151 passed** (29 test files).
- `npm run build` → tsc clean, vite build clean (`dist/` output ~647kB main bundle, pre-existing size warning, unrelated to this change).

## Decisions Made

See frontmatter `decisions` — two real bugs the plan's literal action text didn't anticipate (both Rule 1, fixed inline within their respective task's commit): the client `KNOWN_VARIANTS` derivation bug in Task 3, and the `test_variants.py` hardcoded-`None` fallback assertions in Task 3. Also one test-authoring correction in Task 1 (TOML top-level-key insertion point) and one harness update in Task 2 (dropping the removed context-seed injection), both anticipated/permitted by the plan's own text.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] client variant.ts KNOWN_VARIANTS would have silently dropped voice1**
- **Found during:** Task 3
- **Issue:** The plan's literal instruction (`KNOWN_VARIANTS stays {voice1, voice2}`) described the desired *value* but the actual code computed it as `new Set([DEFAULT_VARIANT, "voice2"])`. Flipping `DEFAULT_VARIANT` to `"voice2"` would have collapsed this to `Set{"voice2"}` — `variantFromPath("/voice1")` would then have failed to recognize `/voice1` as known and silently fallen back to the default (voice2), breaking the explicit `/voice1` route entirely.
- **Fix:** Changed to an explicit `new Set(["voice1", "voice2"])`, independent of `DEFAULT_VARIANT`.
- **Files modified:** `apps/voice/client/src/transport/variant.ts`
- **Verification:** `variant.test.ts`'s `variantFromPath` tests (both known-segment and default-fallback cases) pass; confirmed `/voice1` still maps to `"voice1"`.
- **Committed in:** `3863552` (Task 3 commit)

**2. [Rule 1 - Bug] test_variants.py fallback assertions hardcoded `is None`**
- **Found during:** Task 3
- **Issue:** `test_unknown_variant_falls_back_to_default` and `test_path_traversal_attempt_is_not_honored` both asserted `variant_config_path(...) is None` for the fallback case — true only because the OLD default (voice1) maps to `None`. Once `DEFAULT_VARIANT` became `"voice2"` (which maps to a real path, `configs/voice2.toml`), both assertions would fail even though the actual fallback behavior is correct.
- **Fix:** Changed both assertions to compare against `variants.variant_config_path(variants.DEFAULT_VARIANT)` instead of a hardcoded `None`, so they stay correct regardless of which variant is configured as default.
- **Files modified:** `apps/voice/tests/test_variants.py`
- **Verification:** `pytest tests/test_variants.py -q` — 6 passed.
- **Committed in:** `3863552` (Task 3 commit)

---

**Total deviations:** 2 auto-fixed (both Rule 1, both real bugs in the plan's literal action text — not scope creep, both directly within the plan's own declared file scope for Task 3).
**Impact on plan:** Both fixes were necessary for Task 3's own stated success criteria ("`/voice1` still explicitly selects voice1") to actually hold. No scope creep — same files the plan already listed for Task 3.

## Issues Encountered

None beyond the two deviations above. The Task 1 TOML-append-vs-insert subtlety (top-level keys must precede table headers) and the Task 2 harness update were both explicitly anticipated in the plan's own execution guidance, not unplanned discoveries.

## User Setup Required

None — no external service configuration required. No deploy was performed (per plan constraints).

## Next Phase Readiness

All three changes are code-complete, fully unit-tested, and (Task 2's leak fix) live-verified against the real Anthropic API. Not yet exercised: a real browser visual check that `/voice1` shows "KPH(v1)" and `/voice2` shows "KPH(v2)" on the live stage (explicitly marked optional in the plan's own verification block, covered by unit tests instead) — flagged for the next live/deployed verification pass, consistent with the project's existing pattern of deferring browser-only checks to a consolidated live-verification session (STATE.md).

---
*Phase: 260710-ixf*
*Completed: 2026-07-10*

## Self-Check: PASSED

All 19 files listed under `key-files.modified` verified present on disk; all 3 task commit hashes (`a301a65`, `1665d70`, `3863552`) verified present in `git log`.
