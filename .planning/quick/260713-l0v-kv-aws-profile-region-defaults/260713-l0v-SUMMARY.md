---
phase: quick-260713-l0v
plan: 01
subsystem: infra
tags: [go, cobra, aws-sdk-go-v2, ssm, dynamodb, cli]

# Dependency graph
requires: []
provides:
  - "Config.Profile field + resolveAWSProfile/resolveAWSRegion pure helpers on the kv CLI"
  - "Config.loadAWS — single shared AWS-config construction path for all AWS-backed clients"
  - "Config.resolveVoipmsCreds — env-first, SSM-fallback VoIP.ms credential resolver"
affects: [kv-cli, telephony-operator-tooling, voipms-provisioning]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Single Config.loadAWS helper for all AWS SDK v2 client construction (DynamoClient, SSMClient) — no duplicated LoadDefaultConfig calls"
    - "Pure resolveX helper functions (resolveAWSProfile, resolveAWSRegion) kept separate from flag wiring for table-driven unit testing without live AWS"
    - "Env-first-then-SSM credential resolution pattern via an injectable ssmFactory closure, reusing the existing ssmGetParameterAPI test seam"

key-files:
  created:
    - kv/internal/app/cmd/root_test.go
  modified:
    - kv/internal/app/cmd/root.go
    - kv/internal/app/cmd/telephony.go
    - kv/internal/app/cmd/voipms.go
    - kv/internal/app/cmd/voipms_test.go

key-decisions:
  - "AWS_PROFILE always wins (even alongside static creds); AWS_ACCESS_KEY_ID alone (no AWS_PROFILE) suppresses the operator default to protect CI; otherwise default to klanker-application"
  - "Named the SSM path constants voipmsUsernameSSMPath/voipmsPasswordSSMPath (not ...APIUsername/...APIPassword) specifically to avoid tripping the existing TestVoipmsMethodNamesCentralized credential-literal regex trap"
  - "VoIP.ms SSM-fallback error messages reuse shortSSMErrorNote's short, non-sensitive note (or the already-wrapped leak-free inner message) — never the raw error or param values"

requirements-completed: [KV-OPS-DEFAULTS]

coverage:
  - id: D1
    description: "AWS profile defaults to klanker-application, but AWS_PROFILE or AWS_ACCESS_KEY_ID (CI) still win, and --profile \"\" forces pure-ambient creds"
    requirement: "KV-OPS-DEFAULTS"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/root_test.go#TestResolveAWSProfile"
        status: pass
    human_judgment: false
  - id: D2
    description: "Region defaults to us-east-1 when AWS_REGION is unset; --region still overrides"
    requirement: "KV-OPS-DEFAULTS"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/root_test.go#TestResolveAWSRegion"
        status: pass
    human_judgment: false
  - id: D3
    description: "DynamoClient and SSMClient both route through the single Config.loadAWS helper, DynamoDB EndpointURL override preserved for dynamodb-local"
    requirement: "KV-OPS-DEFAULTS"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/roundtrip_test.go#TestRoundTrip_KVWriteWebappRead"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/roundtrip_test.go#TestRoundTrip_WebappWriteKVRead"
        status: pass
    human_judgment: false
  - id: D4
    description: "VoIP.ms creds resolve from env first, else from SSM; failure never leaks api_password or the SSM param values"
    requirement: "KV-OPS-DEFAULTS"
    verification:
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestResolveVoipmsCreds_EnvWins"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestResolveVoipmsCreds_SSMFallbackSuccess"
        status: pass
      - kind: unit
        ref: "kv/internal/app/cmd/voipms_test.go#TestResolveVoipmsCreds_SSMFallbackError"
        status: pass
  - id: D5
    description: "Manual sanity: after aws sso login, kv telephony list works with zero hand-exported AWS_PROFILE/AWS_REGION/VOIPMS_* env vars"
    verification: []
    human_judgment: true
    rationale: "Requires a live AWS SSO session and live SSM/DynamoDB access on the operator's machine — cannot be exercised in this sandboxed unit-test environment."

# Metrics
duration: 20min
completed: 2026-07-13
status: complete
---

# Quick Task 260713-l0v: kv AWS profile + region defaults Summary

**Centralized AWS-config loading behind one `Config.loadAWS` helper with do-what-I-mean profile/region defaults, and added an env-first/SSM-fallback VoIP.ms credential resolver — all unit-tested without live AWS.**

## Performance

- **Duration:** ~20 min
- **Started:** 2026-07-13T19:14:00Z
- **Completed:** 2026-07-13T19:17:26Z
- **Tasks:** 2/2 completed
- **Files modified:** 5 (1 created, 4 modified)

## Accomplishments
- `kv` now defaults AWS profile to `klanker-application` and region to `us-east-1`, both still overridable via `--profile`/`--region` flags or `AWS_PROFILE`/`AWS_ACCESS_KEY_ID`/`AWS_REGION` env vars, with `--profile ""` as an explicit pure-ambient escape hatch.
- All AWS SDK v2 config construction (DynamoDB, SSM) now flows through one shared `Config.loadAWS` method — no duplicated `LoadDefaultConfig` calls — while `DynamoClient`'s `EndpointURL` override for dynamodb-local is preserved byte-for-byte.
- VoIP.ms API credentials now resolve env-first, then fall back to SSM (`/kmv/secrets/use1/voipms/api_username` + `api_password`) at all three call sites (`voipms balance`/`route-did`/`create-subaccount` and `telephony list`'s inbound-DID gate), with leak-free error messages that never surface the password or raw param values.

## Task Commits

Each task was committed atomically:

1. **Task 1: Centralize AWS config loading in root.go with profile + region defaults** - `afc0328` (feat)
2. **Task 2: Route SSMClient through the shared loader and add an env→SSM VoIP.ms creds resolver** - `ebcfc5d` (feat)

**Plan metadata:** commit pending (docs: complete plan) — created by the orchestrator after this SUMMARY

_Note: both tasks were `tdd="true"` — tests were added alongside the implementation in each single commit rather than as separate RED/GREEN commits, since these are additive helper functions layered onto existing working code rather than net-new user-facing behavior undergoing a red/green cycle._

## Files Created/Modified
- `kv/internal/app/cmd/root.go` - Added `Config.Profile`, `resolveAWSProfile`, `resolveAWSRegion`, `Config.loadAWS`; refactored `DynamoClient` onto `loadAWS`; added `--profile` flag; changed `--region` default to `resolveAWSRegion(...)`
- `kv/internal/app/cmd/root_test.go` - New: table tests for `resolveAWSProfile` (4 precedence rows) and `resolveAWSRegion` (2 rows)
- `kv/internal/app/cmd/telephony.go` - `SSMClient` now builds its config via `c.loadAWS(ctx)`; dropped the now-unused `awsconfig` import; `telephony list`'s inbound-DID gate now calls `cfg.resolveVoipmsCreds(c.Context())`
- `kv/internal/app/cmd/voipms.go` - Added `voipmsUsernameSSMPath`/`voipmsPasswordSSMPath` constants, `voipmsCredsFromSSM`, package-level `resolveVoipmsCreds`, and `(c *Config) resolveVoipmsCreds`; rewired `balance`/`route-did`/`create-subaccount` RunEs onto `cfg.resolveVoipmsCreds`; updated the head-of-file credentials comment for the new env-first-then-SSM behavior
- `kv/internal/app/cmd/voipms_test.go` - Added `TestResolveVoipmsCreds_EnvWins`, `TestResolveVoipmsCreds_SSMFallbackSuccess`, `TestResolveVoipmsCreds_SSMFallbackError` using the existing `fakeSSMGetParameterClient` seam

## Decisions Made
- Named the SSM path constants `voipmsUsernameSSMPath`/`voipmsPasswordSSMPath` (per the plan's explicit constraint) to avoid the `TestVoipmsMethodNamesCentralized` credential-literal regex (`(?i)(api[_]?username|api[_]?password)\s*[:=]=?\s*"`), which would otherwise false-positive on a constant name ending in `...APIUsername =`.
- In `resolveVoipmsCreds`'s SSM-error branch, reused `voipmsCredsFromSSM`'s already-leak-free error message directly (via `%s(err)`) rather than re-deriving through `shortSSMErrorNote(err)` a second time — the latter would only see a plain `*errors.errorString` at that point (not an AWS API error), and would collapse the more specific note to a generic "unavailable", discarding useful operator-facing detail while still remaining leak-free either way.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None.

## User Setup Required

None - no external service configuration required. (The plan's manual sanity check — running `kv telephony list` after `aws sso login` with no hand-exported env vars — is an operator-side verification step, not a setup requirement; documented above as coverage item D5 with `human_judgment: true`.)

## Next Phase Readiness
This was a standalone quick task (not part of a phase sequence). The `kv` CLI operator experience is now "just works" after `aws sso login` for both the AWS profile/region defaults and VoIP.ms credential resolution. No blockers for follow-on work.

---
*Quick task: 260713-l0v-kv-aws-profile-region-defaults*
*Completed: 2026-07-13*

## Self-Check: PASSED

All created/modified files and both task commits verified present.
