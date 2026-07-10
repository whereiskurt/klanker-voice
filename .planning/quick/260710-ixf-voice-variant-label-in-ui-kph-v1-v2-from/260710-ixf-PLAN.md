---
phase: 260710-ixf
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
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
autonomous: true
requirements: []
must_haves:
  truths:
    - "The live stage shows a subtle variant label ('KPH(v1)' on /voice1, 'KPH(v2)' on /voice2) sourced from each variant's TOML."
    - "On a content-free first turn ('Alright'/'ok'/'mm-hm'), the concierge no longer narrates its opening-move stage directions aloud."
    - "The root URL and unknown paths resolve to voice2 (full-duplex) on both server and client; /voice1 remains explicitly reachable."
  artifacts:
    - "apps/voice/pipeline.toml and apps/voice/configs/voice2.toml carry a top-level `label`."
    - "PipelineConfig.label parsed by load_config; answer['variant_label'] set in server.py."
    - "Live.tsx renders a .live-variant-label element."
  key_links:
    - "TOML label -> load_config -> _negotiate_webrtc answer['variant_label'] -> voiceSession peek -> useVoiceSession state -> App -> Live render."
    - "Persona system prompt (concierge.md 'Opening move') is now the SOLE no-re-greet guarantee once the NO_REGREET context seed is removed."
---

<objective>
Two atomic, independent changes to the voice app:

1. Surface a per-variant display label ("KPH(v1)" / "KPH(v2)"), defined in each
   variant's TOML, subtly in the live UI — mirroring the existing
   `session_max_seconds` answer-field plumbing end to end.
2. Fix the confirmed first-turn instruction leak: the
   `NO_REGREET_KICK_MESSAGE` context seed is delivered to Anthropic as a
   USER-role turn (pipecat's adapter converts developer/system context messages
   to user role), so on a content-free turn the model reads it aloud. Remove the
   seed; rely on the persona system prompt, lightly hardened.

Purpose: Make the running variant legible at a glance during demos, and stop the
concierge from narrating its own stage directions.
Output: One PLAN, two separate commits.
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md

# Server answer plumbing (the exact pattern to mirror for variant_label)
@apps/voice/server.py
# Config dataclasses + load_config (add the `label` field here)
@apps/voice/src/klanker_voice/config.py
# Variant -> config-path resolution (voice1=default pipeline.toml, voice2=configs/voice2.toml)
@apps/voice/src/klanker_voice/variants.py
# The LEAK source (NO_REGREET context seed) + the persona
@apps/voice/src/klanker_voice/pipeline.py
@apps/voice/prompts/concierge.md
# Client answer-peek + state threading to mirror
@apps/voice/client/src/transport/voiceSession.ts
@apps/voice/client/src/transport/useVoiceSession.ts
@apps/voice/client/src/App.tsx
@apps/voice/client/src/screens/Live.tsx
@apps/voice/client/src/screens/live.css
</context>

<tasks>

<task type="auto">
  <name>Task 1: Per-variant label from TOML, surfaced subtly in the live UI</name>
  <files>apps/voice/pipeline.toml, apps/voice/configs/voice2.toml, apps/voice/src/klanker_voice/config.py, apps/voice/server.py, apps/voice/client/src/transport/voiceSession.ts, apps/voice/client/src/transport/useVoiceSession.ts, apps/voice/client/src/App.tsx, apps/voice/client/src/screens/Live.tsx, apps/voice/client/src/screens/live.css, apps/voice/tests/test_config.py, apps/voice/tests/test_server.py, apps/voice/client/src/screens/Live.test.tsx</files>
  <action>
Add a display label to each variant config and thread it to the live UI, mirroring the existing `session_max_seconds` answer-field path.

TOML (top-level key, NOT inside a table):
- apps/voice/pipeline.toml: add `label = "KPH(v1)"` near the top (e.g. just under the header comment block, before `[stt]`).
- apps/voice/configs/voice2.toml: add `label = "KPH(v2)"` in the same position.
- "label" is safe against `_CREDENTIAL_FIELD_RE` (config.py) — it matches none of the credential tokens, so it will not be rejected by `_reject_credential_fields`.

config.py:
- Add `label: str = "KPH"` as the LAST field of the frozen `PipelineConfig` dataclass (trailing default keeps every existing positional/keyword constructor and fixture working).
- In `load_config`, parse the top-level key: `label = str(data.get("label", "KPH"))` and pass `label=label` into the returned `PipelineConfig(...)`. It is a plain top-level scalar (like nothing else here) — read it directly from `data`, not from a `_require_table`. No range/enum validation needed beyond the str() coercion; `_load_toml_data` already ran `_reject_credential_fields` over the whole document.

server.py (`_negotiate_webrtc`, right beside `answer["session_max_seconds"] = gate_result.session_max_seconds`, ~line 373):
- Resolve the variant's label the SAME way `_run_session` resolves its config (see server.py ~line 170-171): `cfg = load_config(variants.variant_config_path(variant))` then `answer["variant_label"] = cfg.label`. NOTE: at the answer-assembly point the full pipeline config is not yet loaded (that happens later in the fire-and-forget `_run_session`), so this is a deliberate lightweight TOML read purely for the label — a single cheap parse, gated inside the existing `if answer:` block. `load_config` is already imported at server.py line 49; `variants` is already imported. Add the assignment immediately after the `session_max_seconds` line, inside the same `if answer:` guard.

Client — voiceSession.ts:
- Add an optional callback to `CreateVoiceSessionOptions`: `onVariantLabel?: (label: string) => void;` with a short docstring (display-only, sourced from the `/api/offer` answer, mirrors `onSessionMax`).
- Add a helper `readVariantLabel(body)` mirroring `readSessionMaxSeconds`: return `typeof value === "string" ? value : null` for `body.variant_label`.
- In `createVoiceSession`, destructure `onVariantLabel` from options. In the existing `response.ok` peek branch (currently `else if (response.ok && onSessionMax)`), broaden the guard to also fire when `onVariantLabel` is set, and inside the single `.clone().json()` handler read BOTH fields: keep the existing `session_max_seconds` handling and add `const label = readVariantLabel(body); if (label != null && onVariantLabel) onVariantLabel(label);`. Reuse ONE `.clone()` for both reads — do not add a second clone.

Client — useVoiceSession.ts:
- Add state `const [variantLabel, setVariantLabel] = useState<string | null>(null);`.
- Pass `onVariantLabel: setVariantLabel` into the `createVoiceSession({...})` call in `beginConnect` (alongside `onSessionMax`).
- Reset it to `null` in both `start()` and `stop()` (beside the existing `setSessionMaxSeconds(null)` calls).
- Add `variantLabel: string | null;` to `UseVoiceSessionResult` (with a short doc comment) and include `variantLabel` in the returned object.

Client — App.tsx:
- In the `case "live":` branch, pass `variantLabel={voice.variantLabel}` into `<Live .../>` alongside the existing props.

Client — Live.tsx:
- Add `variantLabel: string | null;` to `LiveProps` and destructure it.
- Render it as a SUBTLE, muted, small on-screen detail: when `variantLabel` is non-empty, render `<span className="live-variant-label">{variantLabel}</span>` inside the `.live-orb` header band (so it sits near the orb header, top corner). Keep it unobtrusive; render nothing when null/empty.

Client — live.css:
- Add a `.live-variant-label` rule: absolutely positioned in a top corner of the orb header, small font (e.g. `var(--sz-label)` or smaller), low opacity / muted color, non-interactive (`pointer-events: none`), above the orb canvas (`z-index: 2`). Keep it visually quiet — a faint tag, not a badge.

Tests:
- tests/test_config.py: add a test that `load_config()` (default pipeline.toml) yields `label == "KPH(v1)"`, and a test that a config file WITHOUT a top-level `label` falls back to the `"KPH"` default (construct via a tmp TOML or `load_config(<voice2 path>)` asserting `"KPH(v2)"`, plus a minimal-config default case). Follow the existing test_config.py fixture style.
- tests/test_server.py: extend the offer/answer coverage so `answer["variant_label"]` is present. If the existing suite stubs `_negotiate_webrtc` (it does for variant-routing tests), add a focused test that calls `_negotiate_webrtc` (or asserts the label resolution) for voice1 -> "KPH(v1)" and voice2 -> "KPH(v2)"; reuse the existing monkeypatch/stub seams rather than doing real ICE.
- client Live.test.tsx: add a test that `<Live variantLabel="KPH(v1)" .../>` renders the label text, and that passing `variantLabel={null}` renders no `.live-variant-label`. Update the two EXISTING `render(<Live .../>)` calls to pass `variantLabel={null}` (new required prop).
  </action>
  <verify>
    <automated>cd apps/voice && .venv/bin/python -m pytest tests/test_config.py tests/test_server.py -q -k "label or variant or config or server" && . "$HOME/.nvm/nvm.sh" && nvm use 23 && cd client && npm test && npm run build</automated>
  </verify>
  <done>pipeline.toml/voice2.toml carry `label`; PipelineConfig.label parses with a "KPH" default; server sets answer["variant_label"]; the client threads variant_label from the answer through useVoiceSession into a subtle `.live-variant-label` on the live stage; Python config/server tests and client test+build pass.</done>
</task>

<task type="auto">
  <name>Task 2: Remove the NO_REGREET context seed; harden the persona system prompt</name>
  <files>apps/voice/src/klanker_voice/pipeline.py, apps/voice/prompts/concierge.md, apps/voice/tests/test_regreet_suppression.py</files>
  <action>
Fix the confirmed first-turn instruction leak. Root cause (reproduced live): `build_pipeline` seeds `NO_REGREET_KICK_MESSAGE` as a `role="developer"` context message when `greet_first` is false; pipecat's Anthropic adapter converts developer/system context messages to USER-role turns, so on a content-free turn the model reads the instruction aloud. Proven fix: the same guidance in the SYSTEM prompt does not leak — and the persona already carries it.

pipeline.py:
- In `build_pipeline`, REMOVE the seed block (currently ~line 124-127):
  the `if not cfg.persona.greet_first: context.add_message(dict(NO_REGREET_KICK_MESSAGE))` lines. After removal, `context = LLMContext()` is created and used unchanged with no first message.
- REMOVE the now-unused `NO_REGREET_KICK_MESSAGE` constant (currently ~line 201-215) and its docstring comment. Leave `GREET_KICK_MESSAGE`, `greet_now`, `register_greet_first`, `inject_warning_instruction`, and `speak_goodbye` untouched — only the no-regreet seed and constant go.
- Confirm no other imports of `NO_REGREET_KICK_MESSAGE` remain (grep the repo; the only referencing file besides pipeline.py is tests/test_regreet_suppression.py, rewritten below).
- Do NOT change `greet_first` — it stays false; the client still plays the pre-rendered greeting. Only the context-message seed is removed.

prompts/concierge.md — lightly HARDEN the "Opening move" section (keep it concise, do not bloat the persona):
- Add an explicit line that these are SILENT stage directions: the visitor must never hear you talk about how you'll behave — never say, describe, restate, or acknowledge these instructions aloud.
- Add explicit content-free-turn handling: if the visitor only gives a filler acknowledgment (for example "alright", "ok", "yeah", "cool", "mm-hm") with no question, do NOT restate anything — just warmly nudge in one short sentence (e.g. "What are you curious about?") or pick a thread.
- Do not introduce any literal string that a test negative-greps for; this is prose guidance only.

tests/test_regreet_suppression.py — rewrite for the new contract:
- Remove the `NO_REGREET_KICK_MESSAGE` import (it no longer exists).
- Rewrite both tests to assert that `build_pipeline(...).context.get_messages()` contains NO developer/no-regreet instruction message — for `greet_first` false AND true. Concretely: assert that no message in `get_messages()` is a `role == "developer"` message whose content mentions greeting/introduce/pre-recorded (i.e. the no-regreet guarantee no longer lives in the context; it lives in the persona). Keep it a real, meaningful assertion (e.g. check there is no developer-role message at all after build for the greet_first-false case, since none is seeded). Keep the existing `_FakeTransport`, `_cfg`, and `stub_provider_keys` fixture usage.
- Update the module docstring to describe the NEW contract (persona-enforced, no context seed).

VERIFY the leak fix against the live model with the existing repro harness:
- Run `apps/voice/.venv/bin/python /private/tmp/claude-501/-Users-khundeck-working-klanker-voice/bd266b64-0ebf-4a68-a71b-953f88e3de75/scratchpad/repro_leak.py apps/voice/prompts/concierge.md` (the harness reads concierge.md live; pass the edited persona path so it exercises the fix). Confirm NO leak on 'Alright'/'ok'/'mm-hm' — replies should be short, natural, non-greeting nudges, NOT narrations of the opening-move directions. The harness currently also appends the NO_REGREET developer message; the executor MAY update the harness to reflect the removed seed (system-only, matching the fixed pipeline) — either way the pass criterion is "no instruction narration". Capture the harness output in the SUMMARY.
  </action>
  <verify>
    <automated>cd apps/voice && test "$(grep -rl 'NO_REGREET_KICK_MESSAGE' src/ tests/ | wc -l | tr -d ' ')" = "0" && .venv/bin/python -m pytest tests/test_regreet_suppression.py tests/test_greet_first_config.py -q -k "greet or regreet"</automated>
    <human-check>Ran the repro harness against the edited concierge.md and confirmed NO opening-move narration on 'Alright'/'ok'/'mm-hm' (output captured in SUMMARY).</human-check>
  </verify>
  <done>The `NO_REGREET_KICK_MESSAGE` seed and constant are gone from pipeline.py (no references remain in src/ or tests/); concierge.md's "Opening move" carries the silent-stage-direction + content-free-turn guidance; test_regreet_suppression.py asserts the context has no developer/no-regreet message for greet_first true AND false; the repro harness shows no instruction narration.</done>
</task>

<task type="auto">
  <name>Task 3: Make voice2 the default variant (fallback only; explicit URLs + labels unchanged)</name>
  <files>apps/voice/src/klanker_voice/variants.py, apps/voice/client/src/transport/variant.ts, apps/voice/tests/test_server.py, apps/voice/tests/test_variants.py, apps/voice/client/src/transport/variant.test.ts</files>
  <action>
Flip ONLY the "no variant specified" fallback from voice1 to voice2, on both the server and client. The variant allowlist, the config-path mapping, the explicit `/voice1` and `/voice2` routes, and the per-variant labels from Task 1 all stay exactly as they are — this changes nothing except which variant an un-specified request resolves to.

variants.py (server):
- Line ~31: `DEFAULT_VARIANT = "voice1"` -> `DEFAULT_VARIANT = "voice2"`.
- Do NOT touch `_VARIANT_CONFIGS`: `voice1` still maps to `None` (default pipeline.toml), `voice2` still maps to `"configs/voice2.toml"`. `normalize_variant`, `variant_config_path`, `is_known_variant`, `known_variants` are all unchanged (they resolve unknown/None/"" to `DEFAULT_VARIANT`, which is now voice2).
- Update the nearby doc/comment prose that currently calls voice1 "the live, shipped experience"/"the default" (the `DEFAULT_VARIANT` docstring block ~lines 28-31, and the `_VARIANT_CONFIGS` "voice1 is provably byte-for-byte the current behavior" comment) so it stays accurate: voice2 is now the default fallback; voice1 remains reachable explicitly at `/voice1`. Keep the security note (attacker-controlled name -> allowlist-only) intact.

variant.ts (client):
- Line ~14: `export const DEFAULT_VARIANT = "voice1";` -> `export const DEFAULT_VARIANT = "voice2";`.
- `KNOWN_VARIANTS` stays `{voice1, voice2}`. `variantFromPath` / `currentVariant` logic is unchanged — they already resolve unknown/root paths to `DEFAULT_VARIANT` (now voice2). Update the module docstring line that describes `/voice1` as "the shipped default" so it reads correctly (voice2 is the default; voice1 is the explicit half-duplex route).
- Consequence (no code change needed, just be aware): `buildConnectParams` uses `variant !== DEFAULT_VARIANT` to decide the query string, so voice2 now emits the BARE `/api/offer` and voice1 emits `/api/offer?variant=voice1`. The server re-validates either way.

Tests — test_server.py:
- `test_offer_no_variant_defaults_to_voice1` (~lines 122/125/133): a no-variant `/api/offer` must now negotiate variant `"voice2"`. Update the asserted variant value to `"voice2"` and RENAME the test to `test_offer_no_variant_defaults_to_voice2`. Keep the existing `_offer_capturing_variant` stub seam.
- The existing `test_offer_passes_known_variant_to_negotiation` (voice2 explicit) and `test_offer_unknown_variant_falls_back_to_default` still hold — but confirm the "falls back to default" test asserts the value via `variants.DEFAULT_VARIANT` (or update its expected value to "voice2" if it hardcodes "voice1").

Tests — test_variants.py:
- The `normalize_variant(unknown/None/"") == DEFAULT_VARIANT` assertions still hold symbolically (they compare to `variants.DEFAULT_VARIANT`). If any assertion hardcodes `"voice1"` as the expected fallback, change it to `"voice2"` (or better, reference `variants.DEFAULT_VARIANT`).
- Keep the config-path mapping assertions as-is: `variant_config_path("voice1")` -> None, `variant_config_path("voice2")` -> `configs/voice2.toml`.
- Fix any comment (e.g. ~line 10) that states voice1 is the default.

Tests — client variant.test.ts:
- `describe("variantFromPath")`: the root/unknown-path test compares to `DEFAULT_VARIANT` symbolically and still passes, but its `it("defaults the root and unknown paths to voice1")` description is now misleading — rename to "...to voice2".
- `describe("buildConnectParams variant wiring")`: FLIP the endpoint-mapping assertions to match the new default —
  - the "keeps the bare endpoint for the default variant" test currently uses `buildConnectParams(null, "voice1").endpoint === "/api/offer"`; the default is now voice2, so change it to assert `buildConnectParams(null, "voice2").endpoint === "/api/offer"` (bare) and update the description accordingly.
  - the "appends ?variant= for a non-default variant" test currently uses voice2; change it to assert `buildConnectParams(null, "voice1").endpoint === "/api/offer?variant=voice1"` (voice1 is now the non-default/explicit one).
  - the bearer-header test can keep using either variant; leave its behavior intact.
- No separate voiceSession endpoint test exists — the endpoint-mapping assertions live only in variant.test.ts.
  </action>
  <verify>
    <automated>cd apps/voice && .venv/bin/python -m pytest tests/test_server.py tests/test_variants.py -q && . "$HOME/.nvm/nvm.sh" && nvm use 23 && cd client && npm test</automated>
  </verify>
  <done>DEFAULT_VARIANT is "voice2" server- and client-side; root `/` and unknown paths resolve to voice2 (full-duplex) on both sides; `/voice1` still explicitly selects voice1 (client sends `?variant=voice1`, server serves pipeline.toml); `/voice2` selects voice2 via the bare endpoint; variant labels (Task 1) and `_VARIANT_CONFIGS` mapping are unchanged; test_server.py's no-variant test now asserts voice2 (renamed), test_variants.py + client variant.test.ts descriptions/endpoint assertions updated; `pytest tests/test_server.py tests/test_variants.py` and `npm test` both green.</done>
</task>

</tasks>

<verification>
- Python: `cd apps/voice && .venv/bin/python -m pytest tests/ -q -k "config or greet or regreet or variant or label or server"` passes.
- Client: `cd apps/voice/client && . "$HOME/.nvm/nvm.sh" && nvm use 23 && npm test && npm run build` passes.
- Manual (leak): repro harness output shows short, natural, non-greeting replies on content-free turns.
- Manual (label): a quick visual check that /voice1 shows "KPH(v1)" and /voice2 shows "KPH(v2)" subtly on the live stage (optional — covered by unit tests).
</verification>

<success_criteria>
- Three SEPARATE atomic commits: Task 1 (variant label), Task 2 (leak fix), Task 3 (voice2 default).
- No docs artifacts committed. No deploy.
- All listed Python + client checks pass.
</success_criteria>

<output>
Create `.planning/quick/260710-ixf-voice-variant-label-in-ui-kph-v1-v2-from/260710-ixf-SUMMARY.md` when done (capture the repro-harness output in it).
</output>
