---
phase: quick-260714-hhj
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - apps/voice/configs/telephony.toml
  - docs/operators/phase12-seed-data.md
autonomous: true
requirements: []
must_haves:
  truths:
    - "A from-anywhere caller who unlocks the §24 gate is granted pstn-public-tier, not kph-tier."
    - "The telephony controller soft-cap permits 4 simultaneous ARI calls (max_concurrent_calls = 4)."
    - "The operator has a documented, copy-pasteable `kv tier define pstn-public-tier ...` command with the exact live values, run manually (never by the executor)."
  artifacts:
    - apps/voice/configs/telephony.toml
    - docs/operators/phase12-seed-data.md
  key_links:
    - "telephony.toml unlock_tier_id -> pstn-public-tier row in kmv-auth-electro (created live by the operator, not this plan)."
    - "quota.py concurrency-slot accounting (count_active_heartbeats vs tier.max_concurrent) must not false-reject at 4 concurrent PSTN sessions."
---

<objective>
Config-only slice of the telephony public-call tuning brief: cap from-anywhere PSTN
callers at a 3-minute session and *permit* 4 concurrent calls at the software layer, by
pointing the §24 gate unlock at a new dedicated `pstn-public-tier` and raising the
controller soft-cap. Also VERIFY the quota concurrency accounting and winddown timing hold
at these new values, and document the operator's live tier-creation command.

Purpose: make "public phone = 3 min / 4 concurrent" explicit and greppable without
touching kph-tier (Kurt's own 24h tier) or pstn-baseline-tier, and without any live AWS or
deploy action from the executor.

Output: edited `apps/voice/configs/telephony.toml`; a new documented operator step in
`docs/operators/phase12-seed-data.md`; verification findings captured in the SUMMARY.

⚠️ SCOPE FLAG (must be surfaced in the SUMMARY): this change only *permits* 4 concurrent
calls at the software layer. The single telephony-edge Fargate task (`task_cpu=2048` /
2 vCPU, `desired_count=1`) is sized for ONE call's headroom — one call already needed a
jump to 2 vCPU to avoid audio garble. Truthfully advertising "4 concurrent" still requires
the DEFERRED capacity slice (service.hcl vCPU/memory bump + a real 4-call load test —
brief's Part B / D2–D4). Do NOT edit service.hcl in this plan.
</objective>

<execution_context>
@$HOME/.claude/gsd-core/workflows/execute-plan.md
@$HOME/.claude/gsd-core/templates/summary.md
</execution_context>

<context>
@.planning/todos/pending/2026-07-14-telephony-3min-4concurrent.md
@apps/voice/configs/telephony.toml
@apps/voice/src/klanker_voice/quota.py
@docs/operators/phase12-seed-data.md
@kv/internal/app/cmd/tier.go
</context>

<locked_decisions>
1. D1 — new dedicated tier. `pstn-public-tier`: sessionMaxSeconds=180, periodMaxSeconds=900,
   maxConcurrent=4. Set `unlock_tier_id = "pstn-public-tier"`. Leave kph-tier and
   pstn-baseline-tier untouched.
2. Config slice ONLY. Set `max_concurrent_calls = 4`. DEFER Part B capacity (no service.hcl edit).
3. Live tier creation is an OPERATOR action, not a code change. The executor MUST NOT run
   `kv tier define ...` against live AWS — only DOCUMENT it in phase12-seed-data.md and
   surface it to the human.
</locked_decisions>

<tasks>

<task type="auto">
  <name>Task 1: Repoint the §24 gate unlock tier + raise the concurrent-call soft cap in telephony.toml</name>
  <files>apps/voice/configs/telephony.toml</files>
  <action>
    In the `[telephony]` table:
    - Change `unlock_tier_id = "kph-tier"` to `unlock_tier_id = "pstn-public-tier"` (D1). Update
      its inline comment to state this is the dedicated public from-anywhere caller tier
      (3-min session / 4 concurrent, created live in kmv-auth-electro), replacing the prior
      kph-tier grant; keep the existing "(minimal identity seam, D-05a)" intent.
    - Change `max_concurrent_calls = 1` to `max_concurrent_calls = 4` (locked decision 2).
      Update its inline comment to note this only PERMITS 4 concurrent ARI calls at the
      software layer, and that the single 2-vCPU telephony-edge task is still sized for ONE
      call — truthful 4-concurrent capacity requires the deferred service.hcl bump + a real
      4-call load test (brief Part B / D2).
    - Leave every other knob unchanged (per_task_max_sessions=5 already >= 4; do NOT touch
      the [quota] table, gate knobs, or media knobs).
    Do NOT edit any service.hcl, and do NOT run any AWS/kv/deploy command.
  </action>
  <verify>
    <automated>cd apps/voice && grep -q 'unlock_tier_id = "pstn-public-tier"' configs/telephony.toml && grep -q 'max_concurrent_calls = 4' configs/telephony.toml && ! grep -q 'unlock_tier_id = "kph-tier"' configs/telephony.toml && uv run pytest tests/test_telephony_config.py -q</automated>
  </verify>
  <done>
    telephony.toml grants pstn-public-tier on unlock and permits 4 concurrent calls; no
    "kph-tier" unlock remains; comments reflect the new public-caller intent + the capacity
    flag; the telephony config test suite still passes (its inline fixtures are independent
    of the real file, so nothing should break).
  </done>
</task>

<task type="auto">
  <name>Task 2: VERIFY quota concurrency accounting + winddown timing hold at 4 concurrent / 180s</name>
  <files>apps/voice/src/klanker_voice/quota.py</files>
  <action>
    Read-only verification — capture findings in the SUMMARY; do NOT change quota.py unless a
    real defect blocks 4-concurrent (if one is found, note it as a follow-up rather than
    expanding this plan's scope).
    Confirm and record in the SUMMARY:
    - Concurrency-slot accounting path: `start_gate` rejects with ERROR_CONCURRENCY_LIMIT when
      `count_active_heartbeats(identity.sub) >= tier.max_concurrent`. With
      pstn-public-tier.maxConcurrent=4, confirm this permits (not rejects) the 4th concurrent
      slot for a given sub (count goes 0->3 allowed, 4th acquires, a 5th at count==4 is the
      first rejected). Note whether PSTN public callers share one `identity.sub` (so the
      per-user tier.max_concurrent=4 is the binding cap) or get distinct subs (so the
      controller `max_concurrent_calls=4` + `per_task_max_sessions=5` are binding instead) —
      trace the telephony unlock path enough to state which cap actually gates 4 public calls.
    - `per_task_max_sessions=5` (telephony.toml [quota]) is >= 4, so the at-capacity per-task
      gate won't false-reject at 4.
    - Winddown timing: `[quota] winddown_warning_seconds = 30` inside a 180s session fires the
      spoken "wrapping up" warning at ~150s — sensible (well inside the window, ~30s of
      runway). Confirm goodbye_grace_seconds=5 and user_silence_timeout=50 don't conflict with
      a 180s cap.
    Optionally re-run the quota suite as a regression guard (no code change expected).
  </action>
  <verify>
    <automated>cd apps/voice && uv run pytest tests/test_quota.py -q</automated>
  </verify>
  <done>
    SUMMARY records: (a) which cap (tier.max_concurrent vs controller max_concurrent_calls)
    binds 4 public calls and that neither false-rejects at 4; (b) per_task_max_sessions=5 is
    adequate; (c) winddown fires ~150s in a 180s window sensibly. Any real code defect blocking
    4-concurrent is noted as an explicit follow-up (not fixed here); quota suite still green.
  </done>
</task>

<task type="auto">
  <name>Task 3: Document the operator's live `kv tier define pstn-public-tier` command</name>
  <files>docs/operators/phase12-seed-data.md</files>
  <action>
    Append a new task section (e.g. "## Task 3 — Seed pstn-public-tier (public from-anywhere
    3-min / 4-concurrent caller tier)") to docs/operators/phase12-seed-data.md, matching the
    existing Task-2 style (fenced command block + expected live-values table + a short
    pre-check/why note). Document — do NOT run — the exact operator command:
      kv tier define pstn-public-tier --group pstn --session-max 180 --period-max 900 --max-concurrent 4
    Include an expected-values table row: `pstn-public-tier` -> sessionMaxSeconds=180,
    periodMaxSeconds=900, maxConcurrent=4, group=pstn. Note that this is a THIN-TOKEN tier row
    (live-editable in kmv-auth-electro, account 052251888500, region us-east-1, profile
    klanker-application) with NO redeploy required, and that telephony.toml's
    `unlock_tier_id = "pstn-public-tier"` (Task 1) depends on this row existing live. Explicitly
    state this command must be run by a human operator against live AWS — it was NOT run by the
    executor. Leave kph-tier / pstn-baseline-tier / defcon34 entries untouched.
  </action>
  <verify>
    <automated>grep -q 'kv tier define pstn-public-tier --group pstn --session-max 180 --period-max 900 --max-concurrent 4' docs/operators/phase12-seed-data.md && grep -q 'pstn-public-tier' docs/operators/phase12-seed-data.md</automated>
  </verify>
  <done>
    phase12-seed-data.md has a new Task-3-style section with the exact `kv tier define
    pstn-public-tier ...` command, an expected live-values table, and an explicit "operator runs
    this against live AWS, not the executor" note. Existing Task-1/Task-2 content unchanged.
  </done>
</task>

</tasks>

<verification>
- telephony.toml: `unlock_tier_id = "pstn-public-tier"`, `max_concurrent_calls = 4`, no residual
  `kph-tier` unlock; comments updated.
- Telephony config + quota test suites still pass (no code regression).
- phase12-seed-data.md documents the operator tier command with exact values.
- SUMMARY captures the quota/winddown verification findings AND the capacity flag.
</verification>

<success_criteria>
- Public from-anywhere PSTN callers are configured to receive pstn-public-tier (3-min session)
  on gate unlock, and the controller permits 4 concurrent calls — both at the config layer only.
- No service.hcl / infra edit; no live AWS or deploy command executed by the executor.
- The operator has a documented, copy-pasteable command to create the tier live.
- The SUMMARY clearly flags: this permits, but does not yet deliver, truthful 4-concurrent
  capacity (deferred Part B: service.hcl vCPU bump + 4-call load test).
</success_criteria>

<deploy_note>
For the SUMMARY only (NOT an executor action): an `apps/voice/**` change triggers
`build-telephony-edge.yml` -> `deploy.yml`. Deploy is a human step after merge. The live
`pstn-public-tier` DynamoDB row (Task 3's operator command) is independent of deploy —
thin-token tiers are live-editable with no redeploy — but the row MUST exist live before the
deployed telephony.toml's `unlock_tier_id = "pstn-public-tier"` resolves to real limits (an
absent tier fails closed to session_max=0 / no-access via quota.read_tier).
</deploy_note>

<output>
Create `.planning/quick/260714-hhj-telephony-3min-public-cap-permit-4-concu/260714-hhj-SUMMARY.md` when done.
</output>
