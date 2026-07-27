---
phase: quick-260727-qfq
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - apps/auth/webapp/src/app/ctf/otp/route.ts
  - apps/auth/webapp/src/app/ctf/__tests__/ctf-otp-route.test.ts
  - apps/voice/src/klanker_voice/telephony/config.py
  - apps/voice/src/klanker_voice/telephony/controller.py
  - apps/voice/configs/telephony.toml
  - apps/voice/tests/test_telephony_config.py
  - apps/voice/tests/test_telephony_sms.py
  - infra/terraform/live/site/services/auth/service.hcl
autonomous: true
requirements: [QUICK-260727-QFQ]
must_haves:
  truths:
    - "GET /use1/ctf/otp?g=3234 returns a TOTP computed from CTF_OTP_SECRET_3234 — not from the legacy CTF_OTP_SECRET — and the same holds for ?g=3283 and ?g=8283 against their own env vars (D-01)."
    - "GET /use1/ctf/otp with NO g param (or an empty g) behaves byte-identically to today: it computes from CTF_OTP_SECRET, ignoring every per-game env var. This is the cutover-safety property — the live telephony-edge still calls the bare URL until it redeploys (D-03)."
    - "An unknown g, a g whose mapped env var is absent, a bearer mismatch, and an internal error all return the SAME uniform 404 the route returns today — no distinct status, no distinct body, no oracle (D-04)."
    - "A g value that names a JavaScript prototype member (constructor, __proto__, toString) resolves to NO env var and returns the uniform 404 — the map is a real lookup structure with no prototype-chain fallthrough (D-02)."
    - "No request-supplied value is ever interpolated into an env-var lookup or written to a log line: env var NAMES come only from a static in-module allowlist, and the route still has zero log statements (D-02)."
    - "TOTP params stay period=120, digits=6, SHA1 on every path, per-game and legacy alike (D-05)."
    - "Each of the three shipped telephony.toml game entries points at its OWN issuer URL carrying its own g query, so each game's spoken OTP derives from its own seed (D-06)."
    - "Each shipped game entry's mid-call SMS msg1 carries that game's didhtp claim slug (didhtp3234 / didhtp3283 / didhtp8283) alongside the OTP, in the URL shape c=<slug>&v=<code> (D-07)."
    - "An announcement entry with NO sms_claim_url_template produces a msg1 body byte-identical to today's module constant, and msg2 stays the unchanged 'Hack the planet!' beat (D-07)."
    - "A sms_claim_url_template that lacks a {code} placeholder is a hard config error naming the field; the TOML key itself is accepted by the shared D-09 credential-field gate (D-07)."
    - "The auth task definition carries the three new per-game secrets AND still carries the legacy CTF_OTP_SECRET wiring — additive only, safe to deploy before telephony-edge redeploys (D-08)."
  artifacts:
    - apps/auth/webapp/src/app/ctf/otp/route.ts
    - apps/voice/src/klanker_voice/telephony/config.py
    - apps/voice/src/klanker_voice/telephony/controller.py
    - apps/voice/configs/telephony.toml
    - infra/terraform/live/site/services/auth/service.hcl
  key_links:
    - "telephony.toml otp_url ?g=<game> -> auth route game allowlist -> CTF_OTP_SECRET_<game> env -> SSM /kmv/secrets/use1/ctf/otp_secret_<game>: the four names must agree exactly or a game silently 404s and its call tears down with no spoken line."
    - "The route's game key -> env var NAME map is the ONLY bridge between request input and process.env; it is a static structure and the request string is never concatenated into a lookup."
    - "entry.sms_claim_url_template -> _build_sms_claim_body -> _send_sms_sequence msg1: the per-entry template replaces ONLY the URL portion; the 'Here: ' prefix and the msg2 beat are composed outside it, so an absent template reproduces the legacy constant exactly."
    - "auth service.hcl valueFrom -> the three ALREADY-SEEDED SSM params: ECS fails task launch outright on an absent valueFrom parameter, so the wiring is only deploy-safe because the params exist live."
---

<objective>
Close the per-game OTP loop end to end: each phone game's announcement must read a TOTP
computed from ITS OWN seed, and the mid-call SMS must carry ITS OWN claim slug.

Purpose: quick tasks 260727-ohq/-pdh split one shared game into three per-DID games, but
all three still fetch the SAME OTP from the same bare `/ctf/otp` URL and still text the
same slugless claim link — so all three games are, cryptographically, one game. This task
gives the auth issuer a game dimension, points each TOML entry at its own game, and gives
each entry its own claim-URL template.

Output: a `?g=` game dimension on the auth `/ctf/otp` route backed by a static env-name
allowlist, three per-game `otp_url` values and three per-game `sms_claim_url_template`
values in telephony.toml, an optional `sms_claim_url_template` field on `AnnouncementEntry`
with a byte-identical legacy fallback, and three additive `valueFrom` secrets on auth's
service.hcl.
</objective>

<locked_decisions>

All of these come from `260727-qfq-CONTEXT.md` and are NOT open for reinterpretation. The
D-NN ids are assigned by THIS plan for traceability — CONTEXT.md states them as prose under
`<decisions>`; the mapping is one-to-one and nothing is added.

- **D-01 — The game dimension.** `GET /use1/ctf/otp?g=<game>` where `<game>` is one of
  `"3234"`, `"3283"`, `"8283"`, mapping to env vars `CTF_OTP_SECRET_3234`,
  `CTF_OTP_SECRET_3283`, `CTF_OTP_SECRET_8283` respectively (SSM
  `/kmv/secrets/use1/ctf/otp_secret_{3234,3283,8283}` — all three seeded live and
  readback-verified 2026-07-27).
- **D-02 — Static allowlist, no interpolation, no logging.** The mapping is a static
  in-module structure from game key to env var NAME. Request input is NEVER interpolated
  into an env-var lookup and NEVER logged. The route keeps its current zero-log-statement
  posture.
- **D-03 — Legacy path byte-identical.** No `g` param, or an empty `g`, keeps today's
  behavior exactly: compute from `CTF_OTP_SECRET`. This is required for cutover — the live
  telephony-edge still calls the bare URL until it redeploys.
- **D-04 — Uniform 404, no oracle.** Unknown/invalid `g`, a mapped-but-missing secret, a
  bearer mismatch, and any internal error all return the SAME uniform 404 the route returns
  today. The bearer stays the single shared `CTF_OTP_AUTH_TOKEN` — it is NOT per-game.
- **D-05 — TOTP params unchanged.** `period=120`, `digits=6`, SHA1 on every path, matching
  the seeded didhtp3234/3283/8283 DC34 flag rows.
- **D-06 — Per-entry `otp_url` gains its game query.** Each telephony.toml entry's existing
  per-entry `otp_url` becomes `https://auth.klankermaker.ai/use1/ctf/otp?g=<game>`. Pure
  config edit — `otp_url` is already per-entry, no parser change.
- **D-07 — Per-entry `sms_claim_url_template`.** A NEW OPTIONAL per-entry field replaces
  the hardcoded claim URL inside the module SMS constant. Must contain `{code}` when
  present (validated like `line_template`). Shipped values:
  - 3234: `https://q.defcon.run/c?c=didhtp3234&v={code}`
  - 3283: `https://q.defcon.run/c?c=didhtp3283&v={code}`
  - 8283: `https://q.defcon.run/c?c=didhtp8283&v={code}`
  Absent field falls back to today's constant exactly (backward compatible). The `Here: `
  prefix framing and the msg2 `Hack the planet!` beat are UNCHANGED. The value is PUBLIC
  (no secret); the field NAME must not trip the shared credential-key regex — prove it with
  a parse test, exactly as 260727-pdh did for `words_env_var`.
- **D-08 — Auth service.hcl is additive.** Add THREE `valueFrom` secrets
  (`CTF_OTP_SECRET_3234` / `_3283` / `_8283`) pointing at the seeded params. **KEEP** the
  legacy `CTF_OTP_SECRET` entry — the no-`g` path needs it until telephony-edge redeploys
  and didhtp1 retires. telephony-edge's service.hcl gets NO change (`otp_url` is config;
  the bearer is unchanged).
- **D-09 — Out of scope.** The `q.defcon.run/c` verifier is DC34-side and still unbuilt;
  the URL SHAPE here (`c=<slug>&v=<code>`) is the contract the DC34 side must implement —
  flag it in the SUMMARY. NO SSM writes, NO terraform apply, NO deploys, NO DC34 changes.

</locked_decisions>

<constraints_discovered>

Four facts verified against the live tree before planning; they shape the tasks below.

**1. The existing auth test stub has no URL.** `ctf-otp-route.test.ts`'s `makeRequest()`
returns an object carrying ONLY a `headers.get` shim. A naive `request.nextUrl.searchParams`
read therefore throws under every pre-existing test, gets swallowed by the route's own
`catch`, and turns all five legacy tests into 404s — i.e. it would silently destroy the D-03
cutover-safety property while looking like a test-fixture problem. The route MUST read the
param defensively (an absent URL degrades to the legacy path, never to a 404), and the stub
must grow a real `nextUrl` so the per-game cases are exercisable.

**2. `sms_claim_url_template` clears the D-09 credential-field gate.**
`klanker_voice.config._CREDENTIAL_FIELD_RE` refuses a key containing any of
`api_key|key|keys|secret|secrets|token|tokens|password|passwd|credential|credentials|bearer|auth|pin|passphrase|pass_word`
at a compound boundary, plus `apikey` and a bare `words`. The tokens in this field name are
`sms` / `claim` / `url` / `template` — none of them match. Unlike 260727-pdh's
`passphrase_env_var` collision, CONTEXT.md's proposed name is usable as-is. Task 2 ships a
parse test that pins this rather than leaving it to a future reader's inspection.

**3. This worktree has no auth `node_modules`.** `apps/auth/webapp/node_modules` exists in the
main checkout but NOT in the `gameconfig` worktree, so `npm test` fails with a
`vitest: command not found` that looks like a broken test command rather than a cold
environment. Node also defaults to v22.1.0 here while the auth suite needs 23 (`nvm use 23`,
v23.6.0 is installed). Task 1's verify command handles both: it sources nvm, selects 23, and
runs `npm ci` only when `node_modules` is absent. `node_modules` is gitignored, so the install
cannot dirty the tree.

**4. No IAM change is needed for the three new auth secrets.** The ecs-task module's
execution-role SSM policy (`infra/terraform/modules/ecs-task/v1.0.0/main.tf`) grants
`ssm:GetParameter(s)` on `parameter/*` for the whole account/region, so a new `valueFrom`
path needs no policy edit. Matching 260727-pdh's precedent, `site.hcl`'s `secrets.definitions`
`keys` list is ALSO left untouched: these params were seeded out-of-band and are not
terraform-managed; adding them to `definitions` would demand SOPS values and put terraform
in charge of live, already-verified secret material.

</constraints_discovered>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/quick/260727-qfq-per-game-otp-wiring-auth-ctf-otp-game-di/260727-qfq-CONTEXT.md
@.planning/quick/260727-pdh-second-phone-game-on-725-404-8283-per-ga/260727-pdh-SUMMARY.md
@apps/auth/webapp/src/app/ctf/otp/route.ts
@apps/auth/webapp/src/app/ctf/__tests__/ctf-otp-route.test.ts
@apps/voice/src/klanker_voice/telephony/config.py
@apps/voice/src/klanker_voice/telephony/controller.py
@apps/voice/configs/telephony.toml
@infra/terraform/live/site/services/auth/service.hcl
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: auth /ctf/otp game dimension (D-01..D-05) + no-oracle regression tests</name>
  <files>apps/auth/webapp/src/app/ctf/otp/route.ts, apps/auth/webapp/src/app/ctf/__tests__/ctf-otp-route.test.ts</files>
  <behavior>
    Legacy path (D-03) — the cutover-safety property, asserted not assumed:
    - All five pre-existing tests keep passing with their current expectations. The only
      change to them is that `makeRequest()` now supplies a real URL with no `g`.
    - A request whose object carries NO url at all still takes the legacy path and returns
      200 (not 404) — proves the defensive read degrades toward legacy.
    - With the legacy secret valid AND all three per-game env vars holding INVALID base32,
      a no-`g` request still returns 200 with the legacy code. Deterministic proof the
      per-game envs are not consulted: had any been read, computeTotp would throw and the
      route would return its uniform 404.
    - An explicitly empty `?g=` behaves exactly like no `g` at all.

    Per-game path (D-01, D-05):
    - `?g=3234` with `CTF_OTP_SECRET_3234` set returns 200 and a code equal to a locally
      computed `computeTotp(thatSecret, { period: 120, digits: 6 })` in the same tick.
    - Same for `?g=3283` and `?g=8283` against their own env vars.
    - With the per-game secret valid and the LEGACY `CTF_OTP_SECRET` holding INVALID base32,
      `?g=3234` still returns 200 — deterministic proof the legacy secret is not consulted
      on the per-game path (no flaky code-inequality assertion).
    - The response envelope is unchanged on the per-game path: digits 6, period 120,
      expiresIn within 1..120, cache-control no-store.

    Uniform 404 (D-04) — every one of these must equal the missing-secret 404 byte for byte
    (same status AND same body text), compared against a captured baseline, not just
    asserted as `404`:
    - An unknown game (`?g=9999`).
    - A known game whose mapped env var is absent.
    - A wrong or missing bearer while `CTF_OTP_AUTH_TOKEN` is set, WITH a `?g=` present.
    - A known game whose mapped env var holds malformed base32 (the catch path).

    Prototype-safety (D-02):
    - `?g=constructor`, `?g=__proto__`, and `?g=toString` each return the uniform 404 and
      never reach a truthy env-var name.

    Source-level discipline (D-02) — extend the existing source-reading test:
    - The route source still contains no `console.` call.
    - The route source contains no interpolated `process.env[...]` index (no `${` inside an
      env index expression) — the structural encoding of "request input never becomes an
      env-var name".
  </behavior>
  <action>
Extend the route with a game dimension. Keep the file's existing header-comment density and
cite quick task `260727-qfq` in every new comment block, the way `260715-oq0` is cited today.

**The allowlist.** Add a module-level constant above `GET`, holding the three game keys and
the env var NAME each maps to (`3234`->`CTF_OTP_SECRET_3234`, `3283`->`CTF_OTP_SECRET_3283`,
`8283`->`CTF_OTP_SECRET_8283`, per D-01). Build it as a `Map<string, string>` rather than an
object literal: a `Map` lookup has no prototype-chain fallthrough, so a caller sending a
JavaScript member name as the game key gets `undefined` instead of an inherited function —
no `hasOwnProperty` ceremony needed. Document in a comment that this structure is the ONLY
bridge from request input to `process.env`, and that the request string is used solely as a
lookup key, never concatenated into a name (D-02). Keep it inline in `route.ts` (CONTEXT
leaves the location to discretion; inline keeps the whole contract in one reviewable file
and keeps the existing source-reading test meaningful).

**The read.** Inside the existing `try`, AFTER the unchanged bearer check, read the param
defensively so a request object without a URL degrades to the legacy path rather than to a
404 — see constraint 1 above; this is the difference between preserving and destroying D-03.
Read the `g` search param through an optional chain, trim it, and default to the empty
string.

**The branch.** When the trimmed game key is empty, resolve the secret from
`CTF_OTP_SECRET` exactly as today (D-03). Otherwise look the key up in the allowlist; a miss
returns `notFound()` immediately (D-04), and a hit resolves the secret by indexing
`process.env` with the NAME the map returned. Everything downstream — the empty-secret
check, the `computeTotp(secret, { period: 120, digits: 6 })` call with its fixed params
(D-05), the response envelope, the `cache-control: no-store` header, and the catch-all
`notFound()` — stays untouched. Do NOT add a second bearer, a per-game bearer, or a distinct
status code for any failure mode.

**Header comment.** Extend the file's existing docblock with the game dimension: the query
shape, the three keys, the legacy no-`g` contract and WHY it must stay (telephony-edge calls
the bare URL until it redeploys), and the fact that every failure mode still collapses into
the one 404.

**Tests.** Rework `makeRequest` to take an optional search-string second argument and always
build a real `nextUrl` from it, so existing call sites keep working unchanged and per-game
cases become expressible. Add a separate helper that returns a request object with NO url,
for the degradation test. Extend the `afterEach` cleanup to delete the three new env vars
alongside the two existing ones — a leaked per-game secret would silently invalidate the
legacy-path proofs. Then add the cases enumerated in the behavior block above, grouping the
uniform-404 cases so they compare against ONE captured baseline response rather than each
asserting a bare status code.
  </action>
  <verify>
    <automated>source "$HOME/.nvm/nvm.sh" >/dev/null 2>&1; nvm use 23 >/dev/null 2>&1; cd apps/auth/webapp && { [ -d node_modules ] || npm ci; } && npm test -- src/app/ctf/__tests__/ctf-otp-route.test.ts</automated>
  </verify>
  <done>The five pre-existing tests pass unchanged in expectation; per-game requests resolve from their own env vars; every failure mode returns a response byte-identical to the missing-secret 404; prototype member names resolve to nothing; and the source-discipline test proves no interpolated env index and no log statement.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: voice — per-entry sms_claim_url_template + per-game otp_url TOML edits (D-06, D-07)</name>
  <files>apps/voice/src/klanker_voice/telephony/config.py, apps/voice/src/klanker_voice/telephony/controller.py, apps/voice/configs/telephony.toml, apps/voice/tests/test_telephony_config.py, apps/voice/tests/test_telephony_sms.py</files>
  <behavior>
    config.py:
    - An announcement table without the new key parses to `sms_claim_url_template == ""`
      (backward compatible, byte-identical to the pre-qfq entry shape).
    - A present value parses to the exact string, stripped.
    - A present value with no `{code}` placeholder raises `ConfigError` whose message names
      the field (mirrors the `line_template` rule).
    - A TOML carrying the key is ACCEPTED by the shared D-09 credential-field gate — the
      pin for constraint 2 above, mirroring 260727-pdh's `words_env_var` precedent.

    controller.py:
    - `_build_sms_claim_body(entry, code)` for an entry with NO template returns a string
      byte-identical to `ANNOUNCEMENT_SMS_BODY_TEMPLATE.format(code=code)` — the legacy
      fallback proven by equality against the constant itself, not by a duplicated literal.
    - For an entry WITH a template, it returns the `Here: ` prefix followed by that
      template with `{code}` substituted.
    - The composed body for a shipped-style template is pure 7-bit ASCII with no leftover
      brace (the GSM-7 rule that already guards the module constants).
    - The `_gate_announcement` hook: an entry with a template posts msg1 carrying that
      entry's slug and the plain OTP, and msg2 stays exactly `Hack the planet!`; an entry
      without one posts today's msg1 unchanged. Both still post exactly TWO relay calls and
      speak the sms-eligible punchline.

    Shipped config (D-06, D-07):
    - Each of the three entries' `otp_url` carries its own game query — 3234, 3283, 8283 in
      entry order — and the three URLs are distinct.
    - Each entry's `sms_claim_url_template` carries its own didhtp slug, all three contain
      `{code}`, all three are distinct, and all three are pure 7-bit ASCII.
    - Every pre-existing shipped-config assertion (three entries, shared line_template,
      distinct code env var names, per-DID sms_reply_dids, otp_only_dids) still holds.
  </behavior>
  <action>
Give each game its own issuer URL and its own claim URL. Cite quick task `260727-qfq` in
every new docstring/comment block, matching the `260727-pdh` / `260727-ohq` citation style
already in these files.

**config.py (D-07).** Add `sms_claim_url_template: str = ""` to `AnnouncementEntry`, placed
beside the existing `sms_relay_url` field, and document it in the class docstring: it holds
a PUBLIC claim-URL template (no credential — the OTP is substituted at send time and the
value itself is a plain URL, exactly like `otp_url`); it must contain a `{code}`
substitution when present; and an absent/empty value means the module's built-in claim URL
is used, so every pre-qfq entry keeps its exact current SMS body. Note in the docstring that
the field name deliberately carries no credential-regex token, citing the `otp_env_var` and
`words_env_var` rename precedents. In `_parse_announcements`, parse it beside
`sms_relay_url` with the same coerce-and-strip treatment, then — only when the result is
non-empty — enforce the `{code}` presence rule with a `ConfigError` that names the field,
mirroring the `line_template` check directly above. Do NOT make the field required and do
NOT add a non-empty check.

**controller.py (D-07).** Today `ANNOUNCEMENT_SMS_BODY_TEMPLATE` fuses a prefix with a
hardcoded claim URL. Split it WITHOUT changing its resolved value: introduce a prefix
constant holding `Here: ` and a default-claim-URL constant holding the current default URL
template, then define `ANNOUNCEMENT_SMS_BODY_TEMPLATE` as their concatenation so its value
stays exactly what it is today. This matters because `test_telephony_sms.py` imports and
asserts on that constant — keeping it authoritative is what makes the legacy-fallback proof
an equality against the real constant rather than a copy of its text. Carry the existing
GSM-7 warning comment onto whichever constant now holds the wire text, and add a short note
that per-entry templates are subject to the identical 7-bit rule.

Add a module-level helper `_build_sms_claim_body(entry, code)` next to the other module-level
SMS helpers (module-level, not a method, so tests can monkeypatch it the way `_fetch_ctf_otp`
is patched). It picks the entry's template when set and the default claim URL otherwise, then
returns the prefix concatenated with the template rendered against `code`. Document that the
per-entry field replaces ONLY the URL portion — the prefix framing and the separate msg2 beat
are composed outside it and are unchanged (D-07).

At the `_gate_announcement` SMS build site, replace the direct constant `.format(code=code)`
call with a call to the new helper, passing the entry. Leave the two-body tuple, the
eligibility predicate, the `create_task` fire-and-forget shape, the strong-reference parking
on `ActiveCall`, and the logging discipline exactly as they are.

**telephony.toml (D-06, D-07).** For each of the three game entries, append that game's query
to its `otp_url` (3234, 3283, 8283 respectively) and add a `sms_claim_url_template` line
carrying that game's didhtp slug per D-07. Add a comment block above the first entry
explaining, once, that the issuer URL's game query selects which per-game TOTP seed auth
computes from (naming the auth env vars, NOT their values), and that the claim template's
slug is the DC34-side flag identifier — noting that the verifier for those links is not yet
built, so the URL shape is a forward contract. Do not restate that comment on the other two
entries; point at it the way the existing distinct-code-value note does.

**Tests.** Extend `test_telephony_config.py` with the parse cases and the shipped-config
assertions from the behavior block, following the existing
`test_announcement_sms_reply_dids_*` / `test_shipped_telephony_toml_*` naming and docstring
style. Extend `test_telephony_sms.py` with the `_build_sms_claim_body` cases and one hook
test for a templated entry, reusing the existing `_stub_common` / `_sms_entry` /
`_build_controller` helpers rather than new scaffolding. Leave
`test_hook_eligible_posts_two_relay_calls_and_speaks_punchline` untouched — the
`_announcement_entry` fixture has no template, so it is now the standing legacy-fallback
regression guard.
  </action>
  <verify>
    <automated>cd apps/voice && uv run pytest tests/test_telephony_config.py tests/test_telephony_controller.py tests/test_telephony_sms.py tests/test_telephony_lifecycle.py tests/test_telephony_gate.py -q && cd ../../kv && go test ./internal/app/studio/... ./internal/app/cmd/...</automated>
  </verify>
  <done>Full telephony suite green; the Go shipped-config cross-check still passes against the edited TOML; an entry without a template produces a body equal to the untouched module constant; and the three shipped entries carry three distinct game queries and three distinct didhtp claim templates.</done>
</task>

<task type="auto">
  <name>Task 3: auth service.hcl — three additive per-game valueFrom secrets (D-08)</name>
  <files>infra/terraform/live/site/services/auth/service.hcl</files>
  <action>
Add three `valueFrom` entries to the auth container's `secrets` list, immediately after the
existing `CTF_OTP_SECRET` line so the per-game block reads as an extension of it:
`CTF_OTP_SECRET_3234`, `CTF_OTP_SECRET_3283`, and `CTF_OTP_SECRET_8283`, pointing at
`arn:aws:ssm:us-east-1:052251888500:parameter/kmv/secrets/use1/ctf/otp_secret_3234`,
`.../otp_secret_3283`, and `.../otp_secret_8283` respectively. Follow the existing entries'
exact formatting (same one-line shape, same ARN prefix, same trailing-comma convention as
its neighbours).

**KEEP the legacy `CTF_OTP_SECRET` entry (D-08).** Its removal is a POST-cutover step, not
this task — the no-`g` code path Task 1 preserved still resolves from it, and the live
telephony-edge keeps calling the bare URL until it redeploys.

Extend the comment block above `CTF_OTP_SECRET` (do not start a competing one) to explain
the per-game dimension: the route selects a seed by the request's game query against a
static allowlist; each game's DC34 flag row is seeded from its own parameter; all three
parameters are ALREADY seeded live and readback-verified (2026-07-27), which is what makes
this wiring deploy-safe given that ECS fails task launch outright on an absent `valueFrom`
parameter; and the legacy entry stays until cutover completes. Note explicitly that no
execution-role IAM change is needed because the ecs-task module already grants
`ssm:GetParameter(s)` across the account's parameter namespace.

Change NOTHING else: no `task_role_iam_statements` edit, no `environment` entry, no
`site.hcl` change, and no telephony-edge service.hcl change (D-08). `site.hcl`'s
`secrets.definitions` stays untouched for the reason in constraint 4 — these params are
seeded out-of-band and must not become terraform-managed.
  </action>
  <verify>
    <automated>cd infra/terraform/live/site/services/auth && grep -v '^[[:space:]]*#' service.hcl | grep -c 'name = "CTF_OTP_SECRET_' | grep -qx 3 && grep -v '^[[:space:]]*#' service.hcl | grep -q 'name = "CTF_OTP_SECRET", valueFrom' && grep -q 'otp_secret_3234' service.hcl && grep -q 'otp_secret_3283' service.hcl && grep -q 'otp_secret_8283' service.hcl && test -z "$(git status --porcelain -- ':/infra/terraform/live/site/site.hcl' ':/infra/terraform/live/site/services/telephony-edge/service.hcl')" && echo INFRA_OK</automated>
  </verify>
  <done>The auth secrets list carries exactly three new per-game entries pointing at the three seeded parameters, still carries the legacy `CTF_OTP_SECRET` entry, and neither site.hcl nor telephony-edge's service.hcl has any pending change.</done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| PSTN caller -> telephony-edge -> auth `/ctf/otp` | An internal service-to-service call whose URL is now attacker-influenced in shape (a game query), though the query value itself comes from git-backed TOML, not from the caller. |
| Public internet -> auth `/ctf/otp?g=` | The route is internal-only by convention and bearer, but reachable; `g` is the FIRST request-controlled value this route has ever consumed. |
| Request string -> `process.env` lookup | New this task. The only bridge is the static allowlist map. |
| SSM SecureString -> ECS container env | The three new per-game seeds cross here at auth task launch, resolved by the execution role. |
| telephony-edge -> caller's handset (SMS) | The claim URL now carries a per-game slug; the body still carries the live OTP. |

## STRIDE Threat Register

| Threat ID | Category | Component | Severity | Disposition | Mitigation Plan |
|-----------|----------|-----------|----------|-------------|-----------------|
| T-qfq-01 | Information disclosure | `/ctf/otp` game dimension as an enumeration oracle | high | mitigate | A valid-but-unseeded game, an unknown game, a bad bearer, and an internal error all return one response, so a prober cannot learn which games exist or which are seeded (D-04). Task 1 asserts this by comparing each failure against a CAPTURED baseline response (status AND body), not by asserting a bare 404 per case. |
| T-qfq-02 | Elevation of privilege | Prototype-chain fallthrough on the game lookup | medium | mitigate | An object-literal map would resolve `constructor` / `__proto__` / `toString` to inherited members, turning a request string into a truthy "env var name". The allowlist is a `Map`, which has no such fallthrough; Task 1 tests all three member names return the uniform 404 (D-02). |
| T-qfq-03 | Information disclosure | Request input reaching an env-var name or a log line | high | mitigate | Env var NAMES come only from the static map; the request string is a lookup key and is never concatenated into a name (D-02). The route keeps zero log statements. Task 1's source-reading test asserts no `console.` call and no interpolated `process.env` index — a structural gate, not a convention. |
| T-qfq-04 | Denial of service | Cutover break — telephony-edge 404s after the auth deploy | critical | mitigate | If the no-`g` path regressed, EVERY live game would break the moment auth deployed, before telephony-edge could redeploy. Mitigated by the defensive param read (an absent URL degrades to legacy, never to 404 — constraint 1) plus a deterministic legacy-path test that sets all three per-game envs to invalid base32, so consulting any of them would surface as a 404 rather than passing silently (D-03). |
| T-qfq-05 | Denial of service | ECS task launch on a missing SSM parameter | high | mitigate | ECS fails task launch outright when a `valueFrom` parameter is absent, and this task wires three. All three are seeded live and readback-verified BEFORE the wiring lands (2026-07-27, per CONTEXT D-01); recorded as a comment on the new block. No SSM writes in this task. |
| T-qfq-06 | Denial of service | Premature removal of the legacy `CTF_OTP_SECRET` wiring | high | mitigate | Removing it in this task would break the no-`g` path the instant auth deployed, since telephony-edge still calls the bare URL. Mitigated by scoping Task 3 to ADDITIONS only, with the removal recorded as an explicitly post-cutover step in the SUMMARY (D-08). |
| T-qfq-07 | Tampering | Per-game env/SSM/TOML name drift | high | mitigate | A mismatch among the TOML game query, the route's map key, the env var name, and the SSM path silently 404s that game and tears the call down with no spoken line. Mitigated by shipped-config tests asserting each entry's exact game query (Task 2), route tests asserting each key's exact env var (Task 1), and structural greps asserting each SSM path (Task 3) — all four names pinned by an automated check. |
| T-qfq-08 | Information disclosure | SMS body / claim URL | medium | accept | The claim URL carries the live OTP as a query param and now also a public flag slug — unchanged in kind from today's shipped behavior and inherent to a link-redemption CTF. The body is still never logged, and the transport is the existing auth relay. No new exposure is introduced by adding the slug. |
| T-qfq-09 | Tampering | Malformed per-entry claim template reaching the wire | low | mitigate | A template missing `{code}` would text a slug with no code; a non-ASCII character would silently drop the message via UCS-2. Mitigated by a load-time `ConfigError` on a missing `{code}` (Task 2 config validation) and a shipped-config 7-bit ASCII assertion. |
| T-qfq-10 | Tampering | terraform taking ownership of live secret material | medium | mitigate | Adding the new keys to `site.hcl`'s `secrets.definitions` would put terraform in charge of three already-seeded, readback-verified live parameters and demand SOPS values for them. Mitigated by leaving `site.hcl` untouched (constraint 4, 260727-pdh precedent) and by a Task 3 gate asserting no pending change to that file. |
| T-qfq-SC | Tampering | npm/pip/cargo installs | high | mitigate | Not applicable — this task adds NO dependency in any ecosystem. The auth change uses only the existing `next/server` import plus the already-vendored `computeTotp`; the voice change is stdlib plus existing modules; the infra change is data-only. No package-manager install task exists, so no legitimacy checkpoint is required. |
</threat_model>

<verification>
1. `source "$HOME/.nvm/nvm.sh"; nvm use 23; cd apps/auth/webapp && npm test` — the whole auth
   vitest suite green, not just the ctf file.
2. `cd apps/voice && uv run pytest tests/test_telephony_config.py tests/test_telephony_controller.py tests/test_telephony_sms.py tests/test_telephony_lifecycle.py tests/test_telephony_gate.py -q`
   — full telephony suite green.
3. `cd kv && go test ./internal/app/studio/... ./internal/app/cmd/...` — the Go shipped-config
   cross-check still agrees with the edited telephony.toml.
4. Backward compatibility is asserted, not assumed, on BOTH halves: the auth no-`g` path is
   proven against deliberately-invalid per-game secrets, and the SMS legacy body is proven by
   equality against the untouched module constant.
5. `git diff` on `infra/terraform/live/site/services/auth/service.hcl` shows three secrets
   added, the legacy `CTF_OTP_SECRET` line still present, and no IAM/environment change.
6. `git status --porcelain` shows no change to `infra/terraform/live/site/site.hcl` or to
   `infra/terraform/live/site/services/telephony-edge/service.hcl`.
7. The four-name chain is greppable end to end for each game: the TOML game query, the route
   map key, the env var name, and the SSM path all agree for 3234, 3283, and 8283.

Out of scope, to record in the SUMMARY:
- **The `q.defcon.run/c` verifier is DC34-side and still unbuilt (D-09).** The URL shape this
  task ships — `c=<didhtp slug>&v=<otp code>` — is the contract the DC34 side must implement.
- **Cutover choreography (NOT this task).** Deploy auth first (additive, safe), then
  telephony-edge (which starts calling the `?g=` URLs), then enable didhtp3234/3283/8283 and
  disable didhtp1 in DC34, then delete the legacy SSM params and the legacy `CTF_OTP_SECRET`
  wiring — alongside the legacy `announcement_code` param deletion already documented in
  260727-pdh's SUMMARY.
- **No SSM writes, no terraform apply, no deploys, no DC34 changes were performed.**
- **Known doc drift (pre-existing, widened by this task):**
  `infra/terraform/live/site/SECRETS.md` and `site.hcl`'s `secrets.definitions` describe only
  the original three `ctf` keys. They already omit 260727-pdh's four announcement params and
  now also omit these three otp_secret params, all of which are seeded out-of-band. A
  documentation-only follow-up should record that these `ctf/*` parameters are operator-seeded
  rather than SOPS-managed, so nobody applies the secrets unit expecting them to appear.
</verification>

<success_criteria>
- `/use1/ctf/otp?g={3234,3283,8283}` each compute from their own seed; no-`g` still computes
  from the legacy seed; every failure mode is one uniform 404 (D-01..D-05).
- The three shipped telephony.toml entries each point at their own game query and each carry
  their own didhtp claim template; an entry without a template is byte-identical to today
  (D-06, D-07).
- Auth's task definition gains three per-game secrets and keeps the legacy one; nothing else
  in infra changes (D-08).
- All three verify commands pass; the auth, telephony, and kv suites are green.
</success_criteria>

<output>
Create `.planning/quick/260727-qfq-per-game-otp-wiring-auth-ctf-otp-game-di/260727-qfq-SUMMARY.md` when done
</output>
