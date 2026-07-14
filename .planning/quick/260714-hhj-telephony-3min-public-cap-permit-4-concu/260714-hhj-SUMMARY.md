---
phase: quick-260714-hhj
plan: 01
status: complete
date: 2026-07-14
commits:
  - 1f837d0  # feat: telephony.toml unlock_tier_id -> pstn-public-tier + max_concurrent_calls=4
  - be58c68  # docs: phase12-seed-data.md Task 3 operator command
---

# Quick Task 260714-hhj — Telephony 3-min public cap + permit 4 concurrent (config slice)

## What shipped

Config-only slice of the telephony public-call tuning brief. Two file changes, two
atomic commits, no live AWS / no deploy performed.

1. **`apps/voice/configs/telephony.toml`** (commit `1f837d0`)
   - `unlock_tier_id`: `"kph-tier"` → `"pstn-public-tier"` — un-entitled from-anywhere
     callers who unlock the §24 gate now get a bounded public session (180s / 4 concurrent)
     instead of inheriting Kurt's 24h/5-concurrent kph-tier. (Entitled callers still resolve
     to their own tier via the caller-ID→code mint; this is only the fallback.)
   - `max_concurrent_calls`: `1` → `4`.
   - Inline comments updated to record the new public-caller intent + the capacity flag.
2. **`docs/operators/phase12-seed-data.md`** (commit `be58c68`)
   - New "Task 3" section documenting the operator's live `kv tier define pstn-public-tier`
     command, expected values, and the seed-before-deploy ordering constraint.

## ⚠️ ACTION NEEDED — operator, run against live AWS (before the deploy)

The live `pstn-public-tier` DynamoDB row does not exist yet. Run (profile
`klanker-application`, account `052251888500`, `us-east-1`):

```bash
kv tier define pstn-public-tier --group pstn --session-max 180 --period-max 900 --max-concurrent 4
```

**Ordering matters:** the deployed `unlock_tier_id = "pstn-public-tier"` resolves this tier's
limits at unlock via `quota.read_tier`; an **absent** tier fails **closed** (session_max=0 /
no-access). Seed this row **before** the telephony-edge deploy that ships the repointed config.
Thin-token tier rows are live-editable with no redeploy, so seed-first is safe.

## ⚠️ SCOPE FLAG — this permits, but does not yet deliver, truthful 4-concurrent

`max_concurrent_calls=4` + `pstn-public-tier.maxConcurrent=4` only unlock 4 concurrent calls at
the **software** layer. The single telephony-edge Fargate task (`task_cpu=2048`/2 vCPU,
`desired_count=1`) is sized for **one** call — one call already needed a jump to 2 vCPU to
avoid audio garble. Advertising "4 at a time" truthfully still needs the **DEFERRED capacity
slice** (brief Part B / D2–D4):
- `service.hcl` vCPU/memory bump (vertical) — NOT touched here,
- a real **4-call simultaneous load test** watching telephony-edge logs for `base_output`
  underruns / TTS garble / RTP jitter,
- external verification (D3): VoIP.ms sub-account/trunk allows 4 simultaneous inbound,
- (D4) confirm Asterisk `pjsip.conf`/`rtp.conf` have no per-endpoint channel cap.

## Task 2 — quota concurrency + winddown verification (read-only, no code change)

Traced `controller.py` / `quota.py` / `session.py`:

- **Binding cap for distinct public callers is the controller cap, not the per-user tier cap.**
  PSTN callers get distinct subs (`sub = f"tel:{caller_id or sip_channel_id}"`,
  `controller.py:509/798`), so the per-user `count_active_heartbeats(sub) >= tier.max_concurrent`
  check starts at 0 for each fresh caller and never binds across 4 *different* callers. The real
  gate is controller-level: `active_session_count=len(self.calls)` vs
  `per_task_max_sessions=self._telephony_cfg.max_concurrent_calls` (`controller.py:517-518`).
  With `max_concurrent_calls=4`: calls 1–4 pass (`len(self.calls)` 0→3 < 4), a 5th is refused as
  `ERROR_AT_CAPACITY` (503, retryable) — **no false-reject at 4, graceful refusal at 5.** ✅
- **`[quota] per_task_max_sessions=5` is NOT consulted on the telephony path** (it drives
  `server.py`'s WebRTC path). Worth flagging so nobody assumes that knob gates telephony. It is
  ≥4 anyway, so it poses no false-reject risk regardless.
- **Winddown timing fits the 180s window.** `session.py` `_service_timer` fires `on_warning()` at
  `session_max - winddown_warning_seconds` = 180-30 = **~150s** (spoken "wrapping up" warning,
  ~30s runway), hard wind-down/stop at 180s. `goodbye_grace_seconds=5` fits inside that window;
  `user_silence_timeout=50` is an independent early-teardown (resets on user speech, 50s ≪ 180s)
  that can only end a call *early* on genuine silence — never truncates or conflicts with the
  180s cap. ✅

No quota.py defect found; no change made there.

## Verification evidence

- `grep`: `unlock_tier_id = "pstn-public-tier"` ✅, `max_concurrent_calls = 4` ✅, no residual
  `unlock_tier_id = "kph-tier"` ✅, Task-3 `kv tier define` command present in seed doc ✅.
- `uv run pytest tests/test_telephony_config.py -q` → **17 passed** ✅ (config tests use inline
  TOML fixtures independent of the real file, as the planner predicted — nothing broke).
- `uv run pytest tests/test_quota.py -q` → **could not run: 23 setup ERRORS**, all environmental
  — the `local_dynamodb_env` fixture is `None` (no local DynamoDB running in this worktree), so
  setup fails at `PutItem` against botocore. **Unrelated to this change** (no code touched in
  quota.py). Task-2 findings rest on the code trace above; re-run the quota suite in an env with
  local DynamoDB if a regression guard is wanted.

## Deploy path (human, after merge)

`apps/voice/**` change → `build-telephony-edge.yml` → `deploy.yml` (clean, tag-pinned). Deploy
is a human step. Reminder: seed the live `pstn-public-tier` row **before** deploying (see ordering
note above). Also note the standing telephony-edge deploy-revert gotcha — verify the edge image
tag isn't reverted by the shared deploy after this ships.

## Follow-ups / deferred

- **Part B capacity slice** (the real gate on advertising 4-concurrent): service.hcl vCPU bump +
  4-call load test + VoIP.ms trunk concurrency check (D2/D3/D4). Separate plan.
- Operator: run the `kv tier define pstn-public-tier` command (above) before deploy.
- Gate-fail debug-logging task (the other pending telephony todo) is next in this session.
