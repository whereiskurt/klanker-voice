# Per-DID gate policy + CID-prefix tooling — execution spec (post-/clear)

**Date:** 2026-07-17
**Status:** SPEC — ready to execute in a fresh session.
**Branch:** cut a fresh branch off `origin/main` (main tip after PR #74 = `59e8cb5`).
**Enabled by:** Approach C (quick 260717-buf, #71) — `dialed_did` now resolves at the edge from the
per-DID VoIP.ms Caller ID name prefix in `${CALLERID(name)}`. Without it, none of Part A is possible.

---

## Part A (PRIMARY) — per-DID gate behavior: Vegas = OTP-only, everyone else = concierge

### Goal (operator, 2026-07-17)
Right now the concierge unlock (the spoken passphrase + the DTMF access PIN) works on EVERY DID.
Make it per-DID:
- **Las Vegas** `7254043234` / `7254043283` → **OTP-only**: only the `333266` DTMF announcement
  works; the concierge passphrase/PIN do NOTHING. No `333266` within the gate window → the existing
  **fail-closed hangup** (operator-chosen UX; passphrase ignored).
- **NYC `3474803715` + TO/Belleville `6134805878` + Caldwell `9862763234`** → **concierge + OTP**:
  passphrase/PIN unlock the concierge AND `333266` still reads an OTP. Exactly today's behavior.
- **`333266` OTP stays GLOBAL** (any DID, inside the gate window).

### Cheapest model — invert on the already-identifiable set
Only the 2 Vegas DIDs carry a CID prefix (`KVD3234`/`KVD3283`), so only THEY resolve a `dialed_did`
at the edge. NYC/TO/Caldwell have no prefix → `dialed_did=<none>`. So:

> `otp_only_dids = ["7254043234", "7254043283"]` (config). A call is OTP-only IFF its resolved
> `dialed_did` is in that set. Everything else (including unresolved `<none>`) = concierge + OTP.

Zero new prefixes / zero new VoIP.ms work for NYC/TO/Caldwell — they fall into the default
concierge bucket. (If a future DID must be OTP-only, give it a CID prefix + add it to
`otp_only_dids`; see Part B tooling.)

### Implementation (real seams, verified 2026-07-17)
1. **Config** — `apps/voice/configs/telephony.toml` + `TelephonyConfig`
   (`apps/voice/src/klanker_voice/telephony/config.py`): add `otp_only_dids: tuple[str, ...]`
   parsed via the existing `_parse_sms_dids`-style digit-normalizer (reuse it; it's already the
   DID-list parser). Seed `otp_only_dids = ["7254043234", "7254043283"]`.
2. **Resolve the flag** — `controller.py` `on_stasis_start` (~L816): `dialed_did` is already
   resolved here, BEFORE `_finish_stasis_start_gated`. Compute
   `otp_only = dialed_did in self._telephony_cfg.otp_only_dids` and thread it through
   `_finish_stasis_start_gated(..., otp_only=otp_only)` and onto `ActiveCall` (new field
   `otp_only: bool = False`) so the DTMF handler can see it.
3. **Suppress concierge unlock in the gate** — `gate.py` `GateProcessor.__init__`: add a
   `concierge_unlock_enabled: bool = True` flag. When False: (a) `process_frame` must NOT run the
   passphrase `match_passphrase` path (skip it entirely — do not even accumulate toward it); (b)
   `unlock(method)` becomes a no-op for `method in {"passphrase","dtmf"}`. IMPORTANT: leave
   `cancel_for_takeover` UNTOUCHED — the `333266` announcement takeover must still work. Build the
   gate with `concierge_unlock_enabled=not otp_only` in `_finish_stasis_start_gated`.
4. **Suppress the concierge PIN at the controller** — `on_channel_dtmf_received`: when
   `active_call.otp_only`, SKIP the PIN `accumulate_dtmf`/`unlock("dtmf")` branch but KEEP the
   announcement-code branch (`333266` → `cancel_for_takeover` → `_gate_announcement`). Preserve the
   existing strict-priority ordering for non-otp_only calls (PIN before announcement).
5. **Fail-closed unchanged** — no unlock within the window → the existing `_gate_fail_closed`
   timer fires → hangup. This IS the operator-chosen OTP-only UX (no new copy/cue).
6. **Tests** — `test_telephony_config.py` (parse + shipped-toml has the 2 Vegas DIDs),
   `test_telephony_gate.py` (concierge_unlock_enabled=False: passphrase match is a no-op, DTMF
   PIN no-op, announcement takeover STILL fires), `test_telephony_lifecycle.py` /
   `test_telephony_controller.py` (an otp_only call: passphrase/PIN do nothing, 333266 still reads
   OTP, no-code → fail-closed; a non-otp_only call: byte-identical to today).

### Guardrails / gotchas
- The pickup cue still says "say your access phrase" on OTP-only DIDs (operator accepted this;
  passphrase simply does nothing). Do NOT change the cue in this pass.
- Keep the change ADDITIVE: `otp_only_dids` empty ⇒ byte-identical to today (every DID concierge).
- `require_gate=True` (prod) is unchanged; this only narrows WHICH unlock methods are honored per DID.

---

## Part B (SUPPORT) — `kv` CID-prefix tooling

Automate the manual `setDIDInfo` dance (done by hand this session) so enrolling a DID for per-DID
reply / OTP-only is one command. Go build (`kv/`, cobra, mirrors `km`).

- **`kv voipms set-cid-prefix <did> <tag>`**: sets `callerid_prefix=<tag>` on `<did>` via VoIP.ms
  `setDIDInfo`. MUST bake in the hard-won gotchas:
  - `setDIDInfo` is **full-replace**: first `getDIDsInfo <did>`, then re-send EVERY field
    (routing, pop, dialtime, billing_type, failover_*, etc.) + the new `callerid_prefix`.
  - **Force `cnam=0`** (or warn loudly): `cnam=1` makes VoIP.ms overwrite the caller-ID NAME via
    CNAM lookup and the prefix never rides through (live-proven on 3283 — it silently failed until
    cnam was set 0).
  - Verify with a follow-up `getDIDsInfo` (routing preserved + prefix set); the VoIP.ms API is
    flaky from some egress (intermittent Cloudflare 522) → retry.
- **`kv voipms clear-cid-prefix <did>`**: `callerid_prefix=""` (same full-snapshot preserve).
- Reuse the existing `kv voipms` command group + creds resolver (env→SSM `/kmv/secrets/use1/voipms/api_*`).
- Unit-test the setDIDInfo param assembly (fake client) — snapshot-preserve + cnam=0 forcing.

## Part C (NICE-TO-HAVE) — surface in `kv studio` console
Show per-DID `callerid_prefix` and the per-DID gate policy (OTP-only vs concierge, derived from
`otp_only_dids`) in the routing view, alongside the existing DID/code/tier surfaces. Read-only is
fine for v1. (See memory `kv-studio-operator-console`.)

---

## Reference facts (fresh session)
- DID inventory (getDIDsInfo, all `routing=account:557010_klanker-pbx`):
  `3474803715` NYC · `6134805878` Belleville ON (operator's "TO") · `7254043234`/`7254043283` Las
  Vegas (prefixes `KVD3234`/`KVD3283`, cnam=0) · `9862763234` Caldwell ID.
- Unlock code = `333266` (SSM `/kmv/secrets/use1/ctf/announcement_code`). Concierge PIN =
  `TELEPHONY_ACCESS_PIN`; passphrase words in SSM. `require_gate=True` in prod.
- Deploy: merge `apps/voice/**`→main → `build-telephony-edge.yml` → `deploy.yml` (workflow_call)
  auto-applies ecs-task with `TF_VAR_TELEPHONY_EDGE_IMAGE_TAG=$SHA` (NO revert). NEVER trigger
  `terragrunt-apply.yml` (manual) for telephony-edge — it doesn't set the tag → reverts to the
  hardcoded stale default. See memory `telephony-edge-deploy-revert-bug`.
- AWS `--profile klanker-application` (052251888500) us-east-1. ECS cluster `app-use1-kmv`, service
  `telephony-edge-use1`, logs `/ecs/telephony-edge-telephony-edge-use1-kmv`.
- Related memory: `ctf-per-did-sms-reply`, `ctf-phone-otp-announcement-did`, `kv-studio-operator-console`.
- Gate seams: `gate.py` `GateProcessor` (`match_passphrase`, `unlock`, `cancel_for_takeover`,
  `_fire_fail_closed`); `controller.py` `on_stasis_start` / `_finish_stasis_start_gated` /
  `on_channel_dtmf_received` / `_gate_announcement` / `_gate_fail_closed`; `ActiveCall.dialed_did`.
