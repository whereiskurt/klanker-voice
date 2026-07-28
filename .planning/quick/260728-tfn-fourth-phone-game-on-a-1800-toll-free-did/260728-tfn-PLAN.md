---
phase: quick-260728-tfn
plan: 01
subsystem: telephony
tags: [toml, nextjs, totp, terraform, ecs-secrets, go]
requires:
  - phase: quick-260727-pdh
    provides: per-DID [[telephony.announcement]] game entries + service.hcl per-game announcement secrets
  - phase: quick-260727-qfq
    provides: auth /ctf/otp ?g= static allowlist + per-entry sms_claim_url_template
provides:
  - A FOURTH per-DID CTF phone game wired end-to-end for a new 1-800 toll-free DID (game key "1800"), behind a clearly-marked PLACEHOLDER DID (8005550199 -- the reserved-fictional 800-555-0199) until the operator orders the real number
  - Auth /ctf/otp allowlist entry ["1800" -> CTF_OTP_SECRET_1800] + tests
  - Additive valueFrom wiring: CTF_OTP_SECRET_1800 (auth) + CTF_ANNOUNCEMENT_CODE_1800 (telephony-edge), both SEED-BEFORE-APPLY
  - Placeholder-swap-safe shipped-config tests (the 1800 entry is asserted by consistency with cid_prefix_dids["KVD1800"], not by hard-coded digits)
affects: [telephony-edge, auth, kv-cli, dc34-q-defcon-run-c-verifier]
key-decisions:
  - "Placeholder DID 8005550199 (reserved-fictional range): the toll-free number is not ordered yet; the go-live swap is a single telephony.toml edit and no test hard-codes the placeholder digits for the 1800 entry"
  - "sms_reply_dids = [] at launch: US carriers block SMS from unverified toll-free numbers and the operator's documented preference is text-FROM-the-dialed-number or nothing; flip to the real TF digits only after VoIP.ms toll-free SMS verification clears"
  - "Numeric trigger only (no words_env_var), like 3234/3283"
  - "SEED-BEFORE-APPLY divergence from pdh: the two new SSM parameters do NOT exist yet; terraform apply before seeding fails ECS task launch on missing valueFrom -- called out loudly in both service.hcl comments and the PR body"
tasks:
  - id: 1
    name: "telephony.toml fourth game entry + Python shipped-config tests"
    files: [apps/voice/configs/telephony.toml, apps/voice/tests/test_telephony_config.py]
    verify: "cd apps/voice && uv run pytest tests/test_telephony_config.py tests/test_telephony_controller.py tests/test_telephony_sms.py tests/test_telephony_gate.py tests/test_telephony_lifecycle.py -q"
  - id: 2
    name: "auth /ctf/otp allowlist + route tests + auth service.hcl secret"
    files: [apps/auth/webapp/src/app/ctf/otp/route.ts, apps/auth/webapp/src/app/ctf/__tests__/ctf-otp-route.test.ts, infra/terraform/live/site/services/auth/service.hcl]
    verify: "cd apps/auth/webapp && npx vitest run src/app/ctf/__tests__/ctf-otp-route.test.ts"
  - id: 3
    name: "telephony-edge service.hcl announcement secret + kv Go shipped-config test"
    files: [infra/terraform/live/site/services/telephony-edge/service.hcl, kv/internal/app/studio/repofile_adapter_test.go]
    verify: "cd kv && go build ./... && go test ./internal/app/studio/... ./internal/app/cmd/..."
operator-live-steps: documented in SUMMARY + PR body (cancel 613, order TF DID, digits swap, kv route-did, callerid_prefix KVD1800, SSM seeds, terraform apply, DC34 Qr row, TF SMS verification)
---

# Quick Task 260728-tfn: Fourth phone game on a 1-800 toll-free DID

Clone the 725-404-3234 game stack for a new toll-free DID, game key `1800`,
fully wired in repo behind a placeholder DID so go-live is: order number,
swap digits (one TOML edit), seed two SSM params, apply, set VoIP.ms
routing + CID prefix.
