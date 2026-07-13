---
phase: 11-voip-ms-telephony-local-asterisk-edge
reviewed: 2026-07-12T00:00:00Z
depth: standard
files_reviewed: 23
files_reviewed_list:
  - apps/voice/src/klanker_voice/config.py
  - apps/voice/src/klanker_voice/telephony/config.py
  - apps/voice/src/klanker_voice/telephony/rtp_socket.py
  - apps/voice/src/klanker_voice/telephony/ari.py
  - apps/voice/src/klanker_voice/telephony/controller.py
  - apps/voice/src/klanker_voice/telephony/gate.py
  - apps/voice/src/klanker_voice/telephony/__main__.py
  - apps/voice/src/klanker_voice/pipeline.py
  - apps/voice/src/klanker_voice/call_runtime.py
  - apps/voice/src/klanker_voice/session.py
  - apps/voice/asterisk/ari.conf
  - apps/voice/asterisk/http.conf
  - apps/voice/asterisk/pjsip.conf
  - apps/voice/asterisk/extensions.conf
  - apps/voice/asterisk/rtp.conf
  - apps/voice/asterisk/docker-compose.yml
  - apps/voice/tests/test_telephony_gate.py
  - apps/voice/tests/test_telephony_lifecycle.py
  - apps/voice/tests/test_telephony_ari.py
  - apps/voice/tests/test_telephony_rtp_socket.py
  - apps/voice/tests/test_telephony_config.py
  - apps/voice/tests/test_asterisk_configs.py
  - apps/voice/tests/test_telephony_integration.py
findings:
  critical: 2
  warning: 5
  info: 2
  total: 9
status: issues_found
---

# Phase 11: Code Review Report

**Reviewed:** 2026-07-12T00:00:00Z
**Depth:** standard
**Files Reviewed:** 23
**Status:** issues_found

## Summary

The §24 silent answer-gate itself (`gate.py`) is well-built: the redaction
boundary is structurally sound (locked-state frames are never forwarded, not
merely dropped after logging), the unlock/fail-closed race is correctly
resolved by a synchronous check-and-set with no intervening `await`, logging
discipline (D-05e) is verified by a dedicated test, and the idempotent
teardown path in `controller.py` genuinely tears down each call's resources
exactly once even under simulated concurrent racing triggers (verified by
`test_simultaneous_close_calls_release_exactly_once`). No path found that
grants a tier before a passphrase/PIN match succeeds.

However, two real gaps surfaced under adversarial reading, both squarely
inside this review's stated focus areas:

1. **No concurrency cap before the gated pipeline (incl. live STT) is
   allocated** — `max_concurrent_calls`/`quota.start_gate` is not consulted
   until *after* unlock in the gated (production-default) flow, so an
   attacker who can place multiple simultaneous inbound calls can spin up
   unlimited concurrent, billable Deepgram STT connections for the duration
   of each `gate_window_seconds`, unconstrained by the configured cap —
   directly undermining the project's "every session must be quota-gated"
   security requirement and its budget/kill-switch design goal.
2. **No RTP peer-source validation**, combined with a wide-open `0.0.0.0`
   bind default — `rtp_socket.py`'s symmetric-RTP "first packet wins, every
   packet re-learns" design has no check that inbound datagrams actually
   originate from Asterisk, unlike every other Phase-11 surface (ARI HTTP,
   dialplan) which was deliberately hardened to a private/loopback-only
   posture.

Five further warnings (weak PIN/passphrase brute-force resistance, two dead
config fields, an unguarded `require_gate=False` escape hatch, and an
unresolved docker-compose ARI-reachability gap the code's own comment flags)
and two info-level notes round out the findings below.

## Critical Issues

### CR-01: Gated telephony flow allocates a full live-STT pipeline per call with no concurrency cap before quota is checked

**File:** `apps/voice/src/klanker_voice/telephony/controller.py:274-369, 459-557`
**Issue:**
In `on_stasis_start` -> `_finish_stasis_start_gated`, every inbound call from
the expected context is answered, bound to a socket, bridged, and handed a
**fully-built, immediately-running `CallSession`** (via `create_call_session`
-> `build_pipeline` -> `build_stt(cfg)` -> a real Deepgram streaming
connection) — all *before* `quota.start_gate` is ever called. The real
`quota.start_gate` (which is what enforces `telephony_cfg.max_concurrent_calls`
via `per_task_max_sessions`) is only invoked from `_gate_unlock`, which fires
on a successful passphrase/DTMF match — i.e. potentially never, for a caller
who just stays silent for the whole `gate_window_seconds` (default 10s,
`TelephonyConfig.gate_window_seconds`).

This is confirmed by the test suite itself:
`test_gated_stasis_start_stays_locked_no_quota_no_greet`
(`tests/test_telephony_lifecycle.py:429-460`) explicitly asserts
`start_gate_calls == []` immediately after `on_stasis_start` returns for the
gated flow — proving no capacity/quota check happens at allocation time.

Consequence: any caller who can place N simultaneous inbound calls to the
trunk number gets N concurrent, real, billable STT connections running for
up to `gate_window_seconds` each, entirely unbounded by
`max_concurrent_calls`. This is exactly the "public mic wired to metered
APIs" scenario the project's own CLAUDE.md flags as a hard security
requirement ("every session must be quota-gated") and budget constraint
("quotas and kill-switch bound API burn") — for PSTN, this is trivially
automatable (a SIP softphone/dialer script placing repeat/concurrent calls),
easier than the WebRTC-side OIDC-token-gated path this constraint was
written for.

Contrast with the codebase's own established pattern: `call_runtime.py`'s
module docstring states the quota gate deliberately "stays at the HTTP layer
... ahead of any transport/pipeline construction" for WebRTC. The gated
telephony flow inverts this ordering by design (to let STT observe the
caller during the gate window) but never re-adds a lightweight capacity
check to compensate.

**Fix:**
Add an explicit `len(self.calls) >= self._telephony_cfg.max_concurrent_calls`
(or equivalent atomic reservation) check in `on_stasis_start`, *before*
`_open_media_session`/`create_external_media`/`create_bridge`/pipeline
construction, for both the gated and ungated flows. A caller over the cap
should be answered, told (or just silently) hung up, with no socket/bridge/
STT allocation at all — mirroring the "quota-denied leaves no bridge"
principle already implemented for the *post-gate* rejection path, just moved
earlier:

```python
if len(self.calls) >= self._telephony_cfg.max_concurrent_calls:
    logger.warning(f"on_stasis_start: at capacity, channel={sip_channel_id!r}")
    await self._safe_ari(self._ari.hangup(sip_channel_id), "hangup (at capacity)")
    return
```

### CR-02: Symmetric-RTP peer learning has no source-address validation, and the socket binds `0.0.0.0` by default

**File:** `apps/voice/src/klanker_voice/telephony/rtp_socket.py:56-61`, `apps/voice/src/klanker_voice/telephony/controller.py:226`
**Issue:**
`_AsteriskRtpProtocol.datagram_received` unconditionally does
`self.peer = addr` for **every** received datagram, with no check that
`addr[0]` (the source IP) is Asterisk's address:

```python
def datagram_received(self, data: bytes, addr: tuple[str, int]) -> None:
    try:
        self.peer = addr  # symmetric-RTP source learning -- every packet
        self.queue.put_nowait(data)
```

`write_packet` then always sends the bot's next audio frame to whatever
`(ip, port)` sent the *most recent* datagram — not just the first. Combined
with `AsteriskCallController.__init__`'s default
`rtp_bind_host: str = "0.0.0.0"` (i.e. the Klanker-side UDP listener binds
every interface, not a private/loopback address the way `http.conf`/
`ari.conf` deliberately do for ARI), any host that can reach the bound
ephemeral port can:

- **Race the real Asterisk datagram** to become the learned peer first
  (classic symmetric-RTP/UDP-spoofing weakness) and receive the call's
  outbound audio instead of the real caller, or
- **Keep re-winning** the "most recent packet" race throughout the call
  (since `peer` is overwritten on *every* packet, not just the first),
  sustaining a hijack rather than a one-shot race, or
- Inject arbitrary RTP payloads that get queued and read by
  `TelephonyInputTransport` as if they were the real caller's audio (audio
  injection into a live/gated call).

This is exactly the "spoofing/injection surface" this review was asked to
scrutinize, and unlike the ARI/HTTP surface (which the phase deliberately
locked to `127.0.0.1`), no equivalent hardening exists here at the code
level — the module docstring discusses hostile *malformed* datagrams
(T-11-03-01, never crash) but not hostile *spoofed-source* datagrams.

**Fix:** At minimum, only learn `peer` once (on the *first* datagram) rather
than re-learning on every packet, closing the "sustained re-hijack" window:

```python
def datagram_received(self, data: bytes, addr: tuple[str, int]) -> None:
    try:
        if self.peer is None:
            self.peer = addr  # learn once; do not let a later spoofed
                               # datagram redirect an already-established call
        self.queue.put_nowait(data)
```

Better: pass the expected Asterisk source IP (known once `create_external_media`
completes, or resolvable from the docker-compose/host network config) into
`SocketRtpMediaSession`/`_AsteriskRtpProtocol` and reject/log-and-drop any
datagram whose `addr[0]` doesn't match it, rather than trusting first-packet
(or every-packet) source learning unconditionally. If the current
network-boundary-only mitigation (Phase 14, per `http.conf`'s own comment)
is the intended long-term fix, that should be stated explicitly here too —
today the code offers no defense-in-depth at this layer at all.

## Warnings

### WR-01: PIN/passphrase matching is not constant-time and has no attempt-rate limiting

**File:** `apps/voice/src/klanker_voice/telephony/gate.py:109-136`
**Issue:** `accumulate_dtmf` compares via plain `new_buffer == pin` and
`match_passphrase` via `secret_words.issubset(accumulated)` — neither is
constant-time (`hmac.compare_digest` is the standard mitigation). In
practice this is low-severity here because the "oracle" an attacker gets is
already the call's own behavior (goes from silence to a greeting) rather
than a measurable timing delta, so a timing side-channel adds little over
what's already observable. More concretely exploitable: there is no
attempt-count cap or lockout on the DTMF path — `on_channel_dtmf_received`
(`controller.py:616-635`) accepts and tests every digit against the trailing
`len(pin)` window for the entire `gate_window_seconds`, with no per-call
rate limit. A caller (or a compromised/malicious upstream SIP leg) that can
inject DTMF digits fast enough could attempt a meaningful fraction of a
short numeric PIN's keyspace within the default 10s window.
**Fix:** Use `hmac.compare_digest` for both comparisons as defense-in-depth,
and consider capping the number of DTMF digits/attempts processed per gate
window (e.g. stop accumulating after N digits with no match) so a longer
PIN can't be brute-forced purely by DTMF injection speed.

### WR-02: `answer_timeout_seconds` and `hangup_on_pipeline_error` are parsed and validated but never consumed

**File:** `apps/voice/src/klanker_voice/telephony/config.py:53-56, 79-80, 124-125`
**Issue:** `TelephonyConfig.answer_timeout_seconds` is documented as "How
long the controller waits for the External Media channel + bridge to become
ready before treating the call as failed", and `hangup_on_pipeline_error` as
"an unhandled pipeline error tears the call down". Neither field is
referenced anywhere in `controller.py` (confirmed via repo-wide grep — the
only hits are the config definition/parsing and its own tests). The
`on_stasis_start` allocation sequence (`answer` -> `create_external_media`
-> `create_bridge` -> two `add_channel` calls) has no timeout wrapping at
all, so a hung ARI REST call blocks call setup for however long
`aiohttp`'s default client timeout allows, not `answer_timeout_seconds`.
This is a silently-broken documented safety behavior, not just an unused
field.
**Fix:** Either wrap the allocation sequence in
`asyncio.wait_for(..., timeout=telephony_cfg.answer_timeout_seconds)` and
wire pipeline-error hangup per `hangup_on_pipeline_error`, or remove the
fields/docstrings until they're implemented so operators don't believe a
protection exists that doesn't.

### WR-03: `require_gate=False` has no runtime safeguard beyond a docstring

**File:** `apps/voice/src/klanker_voice/telephony/config.py:57-60`, `apps/voice/src/klanker_voice/telephony/controller.py:69-75, 347-358`
**Issue:** The entire §24 answer-gate can be disabled by a single
`require_gate = false` line in `pipeline.toml`/`configs/telephony.toml`.
The code correctly treats this as "test/dev-only" in comments, and
`__main__.py` does log the resolved value at startup, but nothing actually
prevents this from shipping to a production config file — there's no
secondary env-var confirmation (e.g. `ALLOW_UNGATED_TELEPHONY=1`) or
config-level guard analogous to the credential-field-rejection pattern this
same phase uses elsewhere (D-09) to make unsafe states hard to reach by
accident.
**Fix:** Require an explicit environment-variable acknowledgement (checked
in `__main__.py` or the controller constructor) before honoring
`require_gate=False`, so disabling the gate can't happen via a single TOML
typo/copy-paste.

### WR-04: docker-compose's published ARI port likely isn't actually reachable as the comment implies, and the gap is undocumented

**File:** `apps/voice/asterisk/docker-compose.yml:18-21`
**Issue:** `http.conf` binds ARI to `127.0.0.1` *inside* the container
(correct, intentional hardening). `docker-compose.yml` then publishes
`8088:8088/tcp` from the container to the host, with a comment stating this
is "not yet reachable from the host; flagged below" — but no further
explanation follows in this file, and `__main__.py`'s documented default
(`ASTERISK_ARI_URL=http://127.0.0.1:8088`, intended for a host-run
controller) will not actually reach a container-loopback-bound ARI server
via Docker's standard NAT/port-publish mechanism (traffic DNAT'd to the
container's own interface does not reach a process bound only to that
container's `127.0.0.1`). This looks like a genuine dev-harness
connectivity gap that the authors were aware of (per the comment) but
didn't finish resolving or documenting a workaround for in-repo.
**Fix:** Either bind ARI to `0.0.0.0` inside the container (relying on the
compose network boundary + `allowed_origins=`/auth for protection, consistent
with how `pjsip.conf`'s SIP transport is already `0.0.0.0`-bound) or document
concretely how the standalone controller is expected to reach ARI in this
harness (e.g. run the controller inside the same compose network namespace).

### WR-05: `unlock_tier_id` isn't validated against a known tier catalog at config-load time

**File:** `apps/voice/src/klanker_voice/telephony/config.py:129`
**Issue:** `unlock_tier_id=str(table.get("unlock_tier_id", "kph-tier"))` accepts
any string with no existence check. A typo'd tier id would parse
successfully and only surface as a failure inside `quota.start_gate` at the
moment of a real caller's gate unlock — i.e. every real call would fail
post-unlock, discovered live rather than at config load/startup.
**Fix:** Validate `unlock_tier_id` against the known tier catalog (wherever
`kph-tier` and its siblings are enumerated for the WebRTC path) at
`load_telephony_config` time, mirroring the fail-fast posture the rest of
this config module already has for `gate_mode`/provider/edge/codec.

## Info

### IN-01: `_close_active_call`'s composed `on_released` hook recursively re-enters `_close_active_call`

**File:** `apps/voice/src/klanker_voice/telephony/controller.py:446-451, 541-546, 675-696`
**Issue:** `_close_active_call` sets `active_call.closed = True`, then calls
`call_session.close()` -> `lifecycle.release()` -> the composed
`on_released` hook, which itself calls `self._close_active_call(active_call,
"hard timeout release")` again. This is safe today only because the
`closed` guard makes the nested call a no-op — but the control flow is
non-obvious (a reader has to trace through `SessionLifecycle.release()` to
realize `_close_active_call` calls itself), and the misleading
`"hard timeout release"` reason string is baked into every close path
(gate-window-expiry, quota-denied-after-unlock, and the real hard timeout
alike) even though it's only ever logged for the genuine hard-timeout case
(the recursive calls return before their `logger.info` line).
**Fix:** Consider decoupling the SIP-channel hangup from
`SessionLifecycle.on_released` and having `_close_active_call` call
`ari.hangup(sip_channel_id)` directly itself (still `_safe_ari`-guarded,
still idempotent against an already-hung-up channel), removing the
recursive re-entry entirely and letting each call site's actual reason
string be logged accurately.

### IN-02: Unverified SIPp image tag left as a TODO

**File:** `apps/voice/asterisk/docker-compose.yml:48-49`
**Issue:** `# TODO(human, before first real --profile integration run): confirm the
current SIPp release tag ... this pin was not verified against a live
network fetch in this sandbox.` This is explicitly called out as
non-blocking for CI (only `docker compose config` runs, no build/pull), but
is a live TODO left in a committed file.
**Fix:** Track this as a follow-up task before the first real
`--profile integration` run, as the comment itself already suggests; no
code change required for this phase.

---

_Reviewed: 2026-07-12T00:00:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
