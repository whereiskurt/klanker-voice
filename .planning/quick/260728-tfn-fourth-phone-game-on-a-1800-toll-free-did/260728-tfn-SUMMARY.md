---
phase: quick-260728-tfn
plan: 01
subsystem: telephony
tags: [toml, nextjs, totp, terraform, ecs-secrets, go]

requires:
  - phase: quick-260727-pdh
    provides: per-DID [[telephony.announcement]] game entries + per-game announcement secrets on telephony-edge
  - phase: quick-260727-qfq
    provides: auth /ctf/otp ?g= static allowlist + per-entry sms_claim_url_template
provides:
  - A fourth per-DID CTF phone game (game key "1800") wired end-to-end for a toll-free DID, behind the PLACEHOLDER 8005550199 (reserved-fictional 800-555-0199) until the real number is ordered
  - Auth /ctf/otp allowlist entry ["1800" -> CTF_OTP_SECRET_1800] with full per-game/legacy/uniform-404 test extension
  - Additive SEED-BEFORE-APPLY valueFrom wiring: CTF_OTP_SECRET_1800 (auth) + CTF_ANNOUNCEMENT_CODE_1800 (telephony-edge)
  - Placeholder-swap-safe shipped-config tests (Python + Go assert the 1800 entry by consistency/shape, never by literal digits)
affects: [telephony-edge, auth, kv-cli, dc34-q-defcon-run-c-verifier]

tech-stack:
  added: []
  patterns:
    - "Placeholder-DID launch state: a game ships fully wired behind reserved-fictional digits; every shipped-config test asserts the entry via cid_prefix_did_map consistency (Python) or shape-only checks (Go) so the go-live digits swap is a single TOML edit with zero test churn"
    - "SEED-BEFORE-APPLY callout: when a new valueFrom parameter does NOT yet exist in SSM, the service.hcl comment says so loudly at the wiring site (the inverse of pdh's already-seeded state)"

key-files:
  created: []
  modified:
    - apps/voice/configs/telephony.toml
    - apps/voice/tests/test_telephony_config.py
    - apps/auth/webapp/src/app/ctf/otp/route.ts
    - apps/auth/webapp/src/app/ctf/__tests__/ctf-otp-route.test.ts
    - infra/terraform/live/site/services/auth/service.hcl
    - infra/terraform/live/site/services/telephony-edge/service.hcl
    - kv/internal/app/studio/repofile_adapter_test.go

key-decisions:
  - "Placeholder DID 8005550199: the toll-free number is not ordered yet (the operator is trading the 613 DID's account slot for it); no test hard-codes these digits for the 1800 entry"
  - "sms_reply_dids = [] at launch: US carriers block SMS from an unverified toll-free number and the operator's documented preference is text-FROM-the-dialed-number or nothing; relay URL + c1800 claim template stay wired for post-verification"
  - "Numeric-only (no words_env_var), matching 3234/3283"
  - "kv Go code needed NO change -- ParseTelephonyGames/kv telephony list/kv studio are data-driven; only the shipped-config guard test learned about the fourth entry"

requirements-completed: [QUICK-260728-TFN]

coverage:
  - id: D1
    description: "telephony.toml ships a fourth per-DID game entry (KVD1800 cid-prefix row, otp_only_dids extension, g=1800 otp_url, CTF_ANNOUNCEMENT_CODE_1800, c1800 claim slug, SMS off), asserted placeholder-swap-safely"
    requirement: QUICK-260728-TFN
    verification:
      - kind: unit
        ref: "cd apps/voice && uv run pytest tests/test_telephony_config.py tests/test_telephony_controller.py tests/test_telephony_sms.py tests/test_telephony_gate.py tests/test_telephony_lifecycle.py -q (225 passed)"
        status: pass
    human_judgment: false
  - id: D2
    description: "auth /ctf/otp resolves ?g=1800 from CTF_OTP_SECRET_1800; legacy path and uniform-404 no-oracle contract hold across the four-game allowlist"
    requirement: QUICK-260728-TFN
    verification:
      - kind: unit
        ref: "cd apps/auth/webapp && NODE_OPTIONS=--experimental-require-module npx vitest run src/app/ctf/__tests__/ctf-otp-route.test.ts (17 passed)"
        status: pass
    human_judgment: false
  - id: D3
    description: "Both service.hcl files carry the additive per-game valueFrom wirings with SEED-BEFORE-APPLY callouts; kv Go build + studio/cmd suites stay green with the four-game shipped-config guard"
    requirement: QUICK-260728-TFN
    verification:
      - kind: unit
        ref: "cd kv && go build ./... && go test ./internal/app/studio/... ./internal/app/cmd/... (ok)"
        status: pass
    human_judgment: false
  - id: D4
    description: "Live go-live: order the toll-free number, swap the placeholder digits, seed both SSM params, apply, set VoIP.ms routing/CID prefix, live confirm call"
    verification: []
    human_judgment: true
    rationale: "Requires VoIP.ms portal actions (cancel 613, order the TF DID), SSM writes, terraform applies, and a real dial-in -- none exercisable from this sandbox."

duration: ~35min
completed: 2026-07-28
status: complete
---

# Quick Task 260728-tfn: Fourth phone game on a 1-800 toll-free DID Summary

**The 725-404-3234 game stack cloned for a new toll-free DID (game key `1800`), fully wired across voice TOML, auth OTP issuer, and both task definitions — shipped behind the reserved-fictional placeholder 800-555-0199 so the go-live swap after the number is ordered is one TOML edit plus two SSM seeds.**

## Task Commits

1. **Task 1: telephony.toml fourth game entry + Python shipped-config tests** — `8de675f` (feat)
2. **Task 2: auth /ctf/otp game 1800 allowlist + tests + auth service.hcl secret** — `40d8fd6` (feat)
3. **Task 3: telephony-edge announcement secret wiring + Go shipped-config guard** — `2391ed4` (feat)

## Deviations from Plan

- **Local vitest run needed two environment fixes (not code changes):** `npm ci` skipped the rolldown darwin-arm64 native binding (installed explicitly, `--no-save`), and the workstation's node v22.1.0 predates unflagged `require(esm)` — tests run with `NODE_OPTIONS=--experimental-require-module` (or any node ≥22.12). No repo files affected.
- **gsd-tools was not on PATH in this worktree** — the quick workflow's artifacts (PLAN/SUMMARY/STATE row, atomic commits) were produced manually to the same contract.

## User Setup Required (go-live choreography, in order)

1. **VoIP.ms portal: cancel the 613 DID** (operator-approved trade; irreversible — the number returns to the pool) → frees the account's 5-DID slot.
2. **Order the toll-free number** (US+Canada coverage; ~$0.99/mo + ~1.9¢/min US / ~8¢/min CAD inbound; $15 one-time if custom/vanity true-800).
3. **Swap the placeholder**: replace `8005550199` with the real digits in `apps/voice/configs/telephony.toml` (three spots: `otp_only_dids`, `[telephony.cid_prefix_dids]` KVD1800 row, Game 4 `dids`). No test churn by design.
4. **Route + tag in VoIP.ms**: `kv route-did` to the full `557010_klanker-pbx` username; set `callerid_prefix=KVD1800` and `cnam=0` on the DID (Approach C resolution depends on the prefix arriving — verify on the first test call).
5. **Seed SSM BEFORE terraform apply** (ECS fails task launch on a missing valueFrom):
   - `/kmv/secrets/use1/ctf/announcement_code_1800` — DTMF code, DISTINCT from 333266 / 1337 / 696969 (distinct-code-value constraint).
   - `/kmv/secrets/use1/ctf/otp_secret_1800` — base32 TOTP seed (HMAC-SHA1 / 6 digits / 120s); share with the DC34/meshtk verifier.
6. **Apply + deploy**: terragrunt apply for auth + telephony-edge task definitions; redeploy both. Remember the deploy-revert gotcha: any voice/auth CI deploy reverts telephony-edge's image tag — re-pin it after.
7. **DC34 side**: create the `q.defcon.run/c1800` Qr row (forward contract; c3234/c3283/c8283 rows exist, c1800 does not).
8. **Later (optional)**: submit VoIP.ms toll-free SMS verification (business info/use case); once cleared, set the Game 4 entry's `sms_reply_dids` to the real toll-free digits to turn on the mid-call claim SMS.

## Next Phase Readiness

All three verify gates green (Python 225/225, vitest 17/17, Go ok). No live changes were made: no SSM writes, no terraform applies, no VoIP.ms actions.
