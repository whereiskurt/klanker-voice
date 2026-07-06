# Deferred Items — Phase 05

Out-of-scope discoveries logged during plan execution (not fixed; scope-boundary rule).

## 05-03

- **Pre-existing TS error, unrelated file**: `apps/auth/webapp/src/app/(authlogin)/login/confirm/__tests__/confirm-no-consume.test.ts:19` —
  `error TS2578: Unused '@ts-expect-error' directive.` Confirmed pre-existing (file last touched
  2026-07-05, before this plan's execution; not modified by 05-03). `npx vitest run` for the full
  auth webapp suite still passes 33/33, so this is a `tsc --noEmit`-only finding, not a test
  failure. Out of scope for 05-03 (touches an unrelated auth-webapp test file, not the OIDC client
  config this plan corrected). Someone should either remove the stale `@ts-expect-error` or confirm
  why it was added.
