# Telephony tuning — 3-min public call cap + 4 concurrent calls

**Captured:** 2026-07-14 (from KPH, end of the telephony-ledger + admin-redesign session)
**Status:** PARTIALLY SHIPPED — config slice done (quick `260714-hhj`, commit `be58c68`,
branch `spec/telephony-3min-4concurrent`). STILL OPEN (kept in pending): (1) operator runs
`kv tier define pstn-public-tier --group pstn --session-max 180 --period-max 900 --max-concurrent 4`
against live AWS **before** the telephony-edge deploy (absent tier fails closed); (2) telephony-edge
deploy; (3) **Part B capacity** — service.hcl vCPU/mem bump + a real 4-call load test (D2) and the
external VoIP.ms trunk concurrency check (D3) / Asterisk per-endpoint cap check (D4). The config
knobs (D1 = new `pstn-public-tier`; `unlock_tier_id` + `max_concurrent_calls=4`) are DECIDED + LANDED.

---

*(original brief below — decisions D2–D4 in Part B are the remaining open work)*

## Goal (operator's words)

> "telephony calls from anywhere to be mapped to a 3min call instead of 2min, and support up to 4 calls at a time."

Two guardrails for the public PSTN line:
1. **Session length:** any caller ("from anywhere") is capped at **3 minutes** per call.
2. **Concurrency:** the line supports **up to 4 simultaneous calls**.

## ⚠️ Premise correction (verified live 2026-07-14)

The "instead of 2 min" premise does **not** match production. Phone-tier resolution today:

- A caller must unlock the §24 gate (DTMF PIN or passphrase) or the call fail-closes with no session.
- On unlock, `apps/voice/configs/telephony.toml` sets **`unlock_tier_id = "kph-tier"`** — so a from-anywhere caller who unlocks currently gets **kph-tier: `sessionMaxSeconds=86400` (24 h), `maxConcurrent=5`** — UNLESS a caller-ID→code mint (`tel_mint_url`, controller `_mint_tier_from_caller_id`) resolves them to their own entitled tier. On mint failure/unconfigured it falls back to `unlock_tier_id`.
- The controller then **soft-caps at `max_concurrent_calls = 1`** (`telephony.toml`), on a **single** telephony-edge task (`desired_count = 1`).

So today: public phone calls are effectively **unlimited length** and **one-at-a-time** — NOT 2 min. (The 2 min is the *web* `demo-tier`.) **Decision D1 below resolves what "3 min" should apply to.**

## Current-state facts (as of 2026-07-14, verified)

| Knob | Where | Current value |
|------|-------|---------------|
| Tier granted on gate unlock | `telephony.toml` `unlock_tier_id` | `kph-tier` |
| `kph-tier` limits | DynamoDB `kmv-auth-electro` (thin-token) | session 86400s, period 1000000s, concurrent 5 |
| `pstn-baseline-tier` limits (exists, not the unlock default) | same table | session 600s (10m), period 1800s, concurrent 1 |
| `demo-tier` (web public) | same table | session 120s (2m), concurrent 2 |
| Controller simultaneous-call soft cap | `telephony.toml` `max_concurrent_calls` | **1** |
| Per-task session cap | `telephony.toml` `per_task_max_sessions` | 5 |
| Telephony-edge task | `services/telephony-edge/service.hcl` | `desired_count=1`, `task_cpu=2048` (2 vCPU), `task_memory=4096` |
| Why 2 vCPU | service.hcl comment | 0.5 vCPU garbled a **single** call (µ-law transcode + 8k↔24k resample + Deepgram+Claude+ElevenLabs). Bumped for **one** call's headroom. |
| RTP ports | `apps/voice/asterisk/rtp.conf` | 20000–20100 (100 ports; comment says "sized for one call at a time") |

Thin-token design: tier **numbers** (sessionMaxSeconds/maxConcurrent) are DynamoDB rows — editable **live, no redeploy**. `telephony.toml` changes need a telephony-edge **rebuild + deploy**.

## Changes required

### Part A — 3-minute public cap (small, mostly config)

**D1 (decide):** Which tier should a from-anywhere caller get, and does 3 min apply to *all* phone callers or only un-entitled ones?
- **Recommended:** introduce a dedicated **`pstn-public-tier`** (session **180s**, period e.g. 900s, `maxConcurrent=4`) and set `unlock_tier_id = "pstn-public-tier"`. Keeps `kph-tier` (your own 24 h) and `pstn-baseline-tier` untouched, and makes "public phone = 3 min / 4 concurrent" explicit and greppable.
- Alternatives: repurpose `pstn-baseline-tier` (600s→180s, concurrent 1→4) — but that name/row is referenced elsewhere; verify before reusing.

Work:
- Create/adjust the tier row (DynamoDB, live — via `kv` or console; there is a Phase-3 tiers seed path to mirror).
- `telephony.toml`: `unlock_tier_id = "pstn-public-tier"` → telephony-edge rebuild+deploy (clean CI path, `apps/voice/**` change triggers `build-telephony-edge.yml`).
- Confirm the graceful "wrapping up" warning copy (`telephony.toml` `warning_copy`) fires sensibly inside a 3-min window (currently generic).

### Part B — 4 concurrent calls (needs a capacity decision, not just numbers)

The numeric knobs are easy; the **capacity** is the real question.

Numeric (easy):
- `telephony.toml` `max_concurrent_calls` 1 → 4 (deploy).
- Tier `maxConcurrent` = 4 (the tier from D1; live).
- `per_task_max_sessions` already 5 (≥4, ok). Verify the global quota concurrency-slot accounting (`klanker_voice.quota`) counts PSTN sessions and won't false-reject at 4.

**D2 (decide) — capacity for 4 simultaneous full pipelines:**
- One call already needed a jump to 2 vCPU to avoid audio garble. **4 concurrent** = 4× (Asterisk media + µ-law transcode + resample + Deepgram WS + Claude + ElevenLabs WS) on one task. Options:
  - **(a) Vertical:** bump `task_cpu`/`task_memory` (e.g. 4–8 vCPU / 8–16 GB) on the single task; simplest (no trunk/registration changes), but a single point of failure and a coarse cost step.
  - **(b) Horizontal:** multiple telephony-edge tasks. ⚠️ Non-trivial: the task holds the **VoIP.ms registration trunk** (outbound REGISTER, `assign_public_ip`); running N tasks means N registrations / call distribution / the POP-locked SG — needs design. Likely out of scope for a quick pass.
- **Recommended first step:** (a) vertical bump + a **load test of 4 real simultaneous calls**, watching telephony-edge logs for `base_output` underruns / TTS garble and RTP jitter. Only pursue (b) if one beefy task can't hold 4 cleanly.

**D3 (verify — external):** VoIP.ms concurrent-call limits. Does the sub-account / DID / trunk allow **4 simultaneous inbound** calls? Check VoIP.ms account settings (`kv telephony list` shows DID inventory; concurrent-call cap is a VoIP.ms-side setting). If the trunk caps below 4, no amount of task sizing helps.

**D4 (verify):** Asterisk `pjsip.conf`/`rtp.conf` have no artificial per-endpoint channel cap; 20000–20100 (100 ports) handles 4 calls (≈4 ports each) trivially — just confirm no `max_contacts`/`max_audio_streams`-style limit was set to 1.

## Suggested phasing

1. **Quick (config + tier):** Part A + the Part B *numeric* knobs (`max_concurrent_calls=4`, tier `maxConcurrent=4`, `unlock_tier_id`). One telephony-edge deploy. Gets 3-min cap live immediately and *permits* 4 concurrent at the software layer.
2. **Capacity slice:** D2 task sizing + a real 4-call load test (D3/D4 verified first). This is the gate on truthfully advertising "4 at a time."

## Verification / done

- Place a public call → session ends at ~3:00 with the graceful wrap-up, not 24 h / not abrupt.
- 4 simultaneous calls all connect, unlock, and hold clean audio (no garble/underruns in telephony-edge logs); a 5th is refused gracefully (quota / `max_concurrent_calls`).
- All 4 land in the transcripts ledger (the 2026-07-14 ledger fix — each as a 📞 PSTN session).

## Pointers
- `apps/voice/configs/telephony.toml` — `unlock_tier_id`, `max_concurrent_calls`, `per_task_max_sessions`, `warning_copy`, gate knobs
- `apps/voice/src/klanker_voice/telephony/controller.py` — `_mint_tier_from_caller_id`, gate unlock, tier grant
- `apps/voice/src/klanker_voice/quota.py` — concurrency-slot accounting
- `infra/terraform/live/site/services/telephony-edge/service.hcl` — `task_cpu`/`task_memory`/`desired_count`
- Tiers: DynamoDB `kmv-auth-electro`, `pk = tier#<id>` (thin-token; editable live)
- Deploy path: `apps/voice/**` change → `build-telephony-edge.yml` → `deploy.yml` (clean, tag-pinned)
