# Quick Task 260727-qfq: Per-game OTP wiring - Context

**Gathered:** 2026-07-27
**Status:** Ready for planning

<domain>
## Task Boundary

Complete the per-DID phone-game loop end-to-end: each game's announcement must
read a TOTP computed from ITS OWN seed, and the mid-call SMS must carry the
right claim slug. Builds directly on quick tasks 260727-ohq/-pdh (same branch,
already committed). Three surfaces: the auth `/ctf/otp` route, the voice
telephony config/controller SMS template, and the two service.hcl secret
wirings.

</domain>

<decisions>
## Implementation Decisions

### Auth `/ctf/otp` per-game dimension
- `GET /use1/ctf/otp?g=<game>` where `<game>` ∈ {"3234", "3283", "8283"} maps
  to env `CTF_OTP_SECRET_3234` / `_3283` / `_8283` (SSM
  `/kmv/secrets/use1/ctf/otp_secret_{3234,3283,8283}` — all three seeded live
  and readback-verified 2026-07-27).
- The mapping is a static allowlist dict (game key → env var NAME); NEVER
  interpolate request input into an env-var lookup or log it.
- No `g` param (or empty) keeps the LEGACY behavior byte-identical: computes
  from `CTF_OTP_SECRET`. Required for cutover — the live telephony-edge still
  calls the bare URL until it redeploys.
- Unknown/invalid `g`, missing mapped secret, bearer mismatch, any error →
  the SAME uniform 404 as today (no-oracle contract; mirrors the existing
  route exactly). Bearer stays the single shared `CTF_OTP_AUTH_TOKEN`.
- TOTP params unchanged (period=120, digits=6, SHA1), matching the seeded
  didhtp3234/3283/8283 DC34 flag rows.

### Voice side
- telephony.toml: each entry's `otp_url` gains its game query:
  `https://auth.klankermaker.ai/use1/ctf/otp?g=3234` (etc. per entry).
  `otp_url` is already per-entry — pure config edit, no parser change.
- SMS claim URL: replace the module constant
  `"Here: https://q.defcon.run/c?v={code}"` (controller.py ~line 286) with a
  per-entry OPTIONAL field `sms_claim_url_template` (must contain `{code}`
  when present; validated like line_template). Entry values:
  - 3234: `https://q.defcon.run/c?c=didhtp3234&v={code}`
  - 3283: `https://q.defcon.run/c?c=didhtp3283&v={code}`
  - 8283: `https://q.defcon.run/c?c=didhtp8283&v={code}`
  Absent field → fall back to the current constant (backward compatible; the
  "Here: " prefix framing and msg2 "Hack the planet!" beat unchanged).
- The template value is PUBLIC (no secret); the field name must not trip the
  credential-key regex (contains none of the refused tokens — verify with a
  parse test like 260727-pdh did for words_env_var).

### Infra
- auth service.hcl: add THREE valueFrom secrets `CTF_OTP_SECRET_3234/3283/8283`
  → the seeded params. KEEP legacy `CTF_OTP_SECRET` (cutover: the no-`g` path
  needs it until telephony-edge redeploys and didhtp1 retires; its removal is
  a post-cutover step alongside the legacy announcement_code/otp_secret param
  deletions already documented in 260727-pdh's SUMMARY).
- telephony-edge service.hcl: NO change (otp_url is config, bearer unchanged).

### Out of scope
- The `q.defcon.run/c` verifier itself is DC34-side and still unbuilt — the
  URL SHAPE here (`c=<slug>&v=<code>`) is the contract the DC34 side must
  implement; flag in SUMMARY.
- No SSM writes, no deploys, no DC34 changes in this task.

### Claude's Discretion
- Whether the auth game map lives inline in route.ts or a small lib module.
- Test organization; follow existing patterns
  (ctf-otp-route.test.ts uniform-404 proofs; test_telephony_config/sms).

</decisions>

<specifics>
## Specific Ideas

- Cutover choreography (NOT this task, recorded for the SUMMARY): deploy auth
  first (additive, safe), then telephony-edge (starts calling ?g= URLs), then
  enable didhtp3234/3283/8283 + disable didhtp1 in DC34, then delete legacy
  SSM params + the legacy CTF_OTP_SECRET wiring.

</specifics>

<canonical_refs>
## Canonical References

- apps/auth/webapp/src/app/ctf/otp/route.ts (the route to extend; uniform-404
  + no-log contract documented in its header comment)
- apps/auth/webapp/src/app/ctf/__tests__/ctf-otp-route.test.ts
- apps/voice/src/klanker_voice/telephony/controller.py (~line 286 SMS msg1
  constant, ~1593-1618 _send_sms_sequence call site)
- apps/voice/src/klanker_voice/telephony/config.py (AnnouncementEntry — add
  sms_claim_url_template beside line_template validation)
- apps/voice/configs/telephony.toml (three game entries from 260727-pdh)
- infra/terraform/live/site/services/auth/service.hcl (+ site.hcl if the SSM
  param paths are declared there)
- .planning/quick/260727-pdh-second-phone-game-on-725-404-8283-per-ga/260727-pdh-SUMMARY.md

</canonical_refs>
