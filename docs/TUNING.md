# klanker-voice — Endpointing A/B Tuning Verdicts (D-12)

**Pipeline:** pipecat-ai `~=1.5.0` (pinned) · cascaded Nova-3 → Claude Haiku 4.5 → ElevenLabs Flash v2.5
**Measured:** 2026-07-05, local laptop, `bot.py -t eval` over the plan-03 five-scenario suite
(greeting, memory, bargein_early, bargein_mid, bargein_monologue) — identical scripted turns per arm.
**Clock:** all numbers are `UserBotLatencyObserver` observer-clock milliseconds. They are **never**
comparable with the eval-harness `within_ms` budgets (a different anchor — Pitfall 7). Verdicts are
**informational only (D-13)**: no run, report, or CLI ever exits nonzero on a threshold.

> **STATUS: closed (2026-07-05) — accepted + scoped to a later phase.**
> Round 0 chose the endpointing winner (Nova-3 + SmartTurn v3); the round-0 escalation over the 1.2 s
> ceiling led the user to choose **"tune further now"**, and a tuning round followed (Flux-native observer +
> measurement, persona trim, eager-EOT test, stop_secs analysis). The winner did not meaningfully change
> (p50 ~1402 ms, still over the ceiling — Haiku LLM TTFT is the dominant, un-knobbable cost). On the
> re-escalation the user chose **accept + scope a later phase**: ~1402 ms p50 is the accepted Phase-1
> number, and ≤1.2 s / ~800 ms becomes a committed later-phase goal via PIPE-08 ack-masking and related
> levers. Full record and the recorded decision are in
> **[Tuning round 1](#tuning-round-1--2026-07-05)** at the end of this document. Verdicts stayed
> informational (D-13) throughout — no tool exited nonzero; the escalation was an execution decision, not a gate.
>
> **The sections between here and the tuning round are the round-0 record, preserved as written.**

---

## The three arms

| Arm | STT | Turn detection | Config |
|-----|-----|----------------|--------|
| A | Deepgram Nova-3 (`nova-3-general`) | local Silero VAD + `SpeechTimeoutUserTurnStopStrategy` (`vad_timeout`) | `apps/voice/configs/arm-a.toml` |
| B | Deepgram Nova-3 (`nova-3-general`) | `TurnAnalyzerUserTurnStopStrategy` + `LocalSmartTurnAnalyzerV3` (`smart_turn_v3`) — the pipecat 1.5.0 default | `apps/voice/configs/arm-b.toml` |
| C | Deepgram Flux (`flux-general-en`) | server-side, `ExternalUserTurnStrategies` (Flux owns EOT) | `apps/voice/configs/arm-c.toml` |

Each arm config is a full `pipeline.toml` clone differing only in the `[stt]`/`[turn]` sections, so the
runs are diffable. LLM (Haiku 4.5) and TTS (Flash v2.5, speed 1.1) are held constant across all arms.

**Arm-integrity checks (RESEARCH Pitfalls 2–3), from the run logs:**
- Arm A ran `SpeechTimeoutUserTurnStopStrategy` and the Smart Turn model-load line was **absent** — the
  VAD-timeout arm did not silently measure SmartTurn (Pitfall 2 clear).
- Arm B ran `TurnAnalyzerUserTurnStopStrategy` (SmartTurn v3 active), as intended.
- Arm C emitted a **balanced 20 start / 20 stop** turn signals — exactly one end-of-turn per turn, no
  double endpointing (Pitfall 3 clear).

---

## Per-arm latency (`harness compare`, observer clock)

Rendered by `python -m klanker_voice.harness compare docs/tuning/arm-a.json arm-b.json arm-c.json`:

| stage | A — Nova-3 + VAD (`vad_timeout`) | **B — Nova-3 + SmartTurn v3** | C — Flux |
|-------|----------------------------------|-------------------------------|----------|
| vad_stop        | 801.2 / 802.1 (10)   | **401.2 / 413.9 (10)**   | — (null) |
| stt_final       | 238.2 / 274.2 (2)    | 266.5 / 266.5 (1)        | — (null) |
| llm_ttft        | 603.9 / 2727.9 (10)  | 586.8 / 1433.0 (10)      | — (null) |
| tts_first_audio | 156.6 / 220.8 (10)   | 163.7 / 212.4 (10)       | — (null) |
| **voice_to_voice** | 1799.8 / 4057.9 (10) | **1460.9 / 2210.7 (10)** | — (null) |

*(p50 / p95 (n), milliseconds.)* `stt_final` is usually null by design — streaming STT reports TTFB at
the first partial while the user is still speaking, outside the user-stop→bot-start window; that wait is
already inside `vad_stop` (plan-03 anchor contract).

**Note on Arm A's p95:** the `voice_to_voice` p95 of 4057.9 ms is inflated by a **single LLM-TTFT spike**
(one turn at `llm_ttft` 4370 ms — a Haiku/network tail, not an endpointing effect). The cleaner
stop_secs=0.3 sweep run (below) shows Arm A's endpointing p95 at ~2096 ms with no such outlier. Arm A is
slower than Arm B on the endpointing that this plan is tuning regardless of the outlier: its `vad_stop`
p50 is **801 ms vs Arm B's 401 ms**.

### Arm A stop_secs sweep

Three short Arm-A runs, editing only `vad_stop_secs`; endpointing silence scales ~linearly with it:

| `vad_stop_secs` | vad_stop p50 (ms) | voice_to_voice p50 (ms) | voice_to_voice p95 (ms) | note |
|-----------------|-------------------|--------------------------|--------------------------|------|
| **0.2** | 801.2 | **1799.8** | 4057.9 | committed Arm-A value (fastest p50); p95 carries one LLM tail |
| 0.3 | 901.5 | 1836.9 | 2096.0 | cleanest run, no LLM outlier |
| 0.5 | 1102.2 | 2071.7 | 2601.8 | slowest — extra fixed silence buys nothing here |

Verdict within Arm A: **stop_secs 0.2** is the fastest and is the committed `arm-a.toml` value. Even so,
Arm A's best is ~340 ms slower at p50 than Arm B.

---

## Verdicts

### Endpointing A/B winner: **Arm B — Nova-3 + SmartTurn v3**

Among the arms the harness can score, **SmartTurn v3 wins decisively**: `voice_to_voice` p50 **1460.9 ms
vs 1799.8 ms** for the best VAD-timeout config. The entire delta comes from turn release — `vad_stop` p50
**401 ms vs 801 ms**. SmartTurn releases the turn as soon as it detects a semantic end-of-turn, instead of
waiting a fixed VAD silence timeout, so it reclaims ~400 ms of dead air every turn. LLM/TTS stages are
statistically identical across the two arms (same providers, same scripted turns), confirming the win is
purely endpointing.

This is landed in `apps/voice/pipeline.toml` (which was already on `smart_turn_v3` as the walking-skeleton
default — the A/B confirms that default is the right one). Greeting + mid barge-in were re-confirmed under
the final `pipeline.toml` and **passed** (judge said yes; barge-in stopped playback within one TTS word).

### SmartTurn v3 verdict: **KEEP**

- It is the pipecat 1.5.0 **default** stop strategy, so keeping it is zero-friction.
- It is `LocalSmartTurnAnalyzerV3` — an **8 MB int8 ONNX** model, ~12 ms inference on a modern CPU / ~60 ms
  on a cheap cloud CPU, **no torch/transformers**. The "~2 GB torch bloat" caution in the project stack
  notes refers to the *older* smart-turn; it does **not** apply to v3. There is no meaningful image-size or
  latency cost to carrying it into the Phase 4 Fargate image.
- It measurably beats the VAD-timeout alternative and needs no per-utterance silence tuning.

### Deepgram Flux verdict: **promising but UNMEASURED on this harness — deferred**

Flux ran correctly end-to-end: the bot spoke, barge-in fired through Flux's `StartOfTurn`, and turn
signals were balanced (no double endpointing). But the plan-03 harness recorded **zero turns** for Flux,
so it has **no comparable latency number**.

**Why (RESEARCH Open Question 1, now resolved):** the harness deliberately subclasses pipecat's
`UserBotLatencyObserver` rather than hand-roll frame timing (plan-03 "Don't Hand-Roll"). That observer
anchors *every* turn measurement on the local VAD-stop frame (`VADUserStoppedSpeakingFrame`). Flux replaces
local VAD with server-side turn detection (`ExternalUserTurnStrategies`), so that frame **never fires** —
and with no start anchor, `on_latency_measured` never emits a turn. `vad_stop` is therefore null under
Flux, and so is `voice_to_voice`. The `docs/tuning/arm-c.json` artifact records this in its `anchors`.

**Consequence:** Flux **cannot be crowned the endpointing winner on measured evidence** — we cannot show it
is faster because we cannot measure it here. The vendor claims median EOT < 300 ms (vs SmartTurn's measured
~400 ms `vad_stop`), which would make Flux the arm to beat if realized. Scoring it requires a **Flux-native
observer** that anchors on Flux's `StartOfTurn`/`EndOfTurn` frames. That is a future lever (ties to the
PIPE-08 latency work), not this plan.

### Eager-EOT decision: **DISABLED (`eager_eot_threshold = 0.0`)**

Not measurable on this harness for the same root cause as the Flux `voice_to_voice` gap — Flux emits no
observer turns, so an eager-vs-non-eager delta is unavailable here. Kept **off** as the conservative
default: eager EOT trades a large fraction of speculative (often-discarded) LLM calls for latency
(Pitfall 4), and there is no measured saving to justify that spend. Revisit alongside a Flux-native
observer.

---

## Final knob values (landed in `apps/voice/pipeline.toml`)

```toml
[stt]
provider = "deepgram-nova3"
model    = "nova-3-general"

[turn]
strategy            = "smart_turn_v3"   # A/B winner
vad_stop_secs       = 0.2
user_speech_timeout = 0.6
```

LLM (`claude-haiku-4-5`) and TTS (`eleven_flash_v2_5`, speed 1.1) are unchanged. The `[stt.flux]` knobs
(`eot_threshold = 0.7`, `eager_eot_threshold = 0.0`) remain in the schema, unused by the winner, ready for
a future Flux evaluation.

---

## Latency vs the 1.2 s ceiling (D-13, PIPE-01)

Informational assessment of the winning config (Arm B) against the roadmap targets:

| Metric | Measured (winner) | 1.2 s ceiling | ~800 ms target | Verdict |
|--------|-------------------|---------------|----------------|---------|
| voice_to_voice p50 | 1460.9 ms | 1200 ms | 800 ms | ⚠️ over |
| voice_to_voice p95 | 2210.7 ms | 1200 ms | 800 ms | ⚠️ over |

Even the winning arm sits **above the 1.2 s ceiling at both p50 and p95**, and well above the ~800 ms
stretch target. Decomposing the p50 (~1461 ms): `vad_stop` ~401 ms + Haiku `llm_ttft` ~587 ms + Flash
`tts_first_audio` ~164 ms + first-sentence aggregation/transport ≈ 300 ms. With SmartTurn already reclaiming
the turn-release time, the **LLM TTFT is now the dominant remaining cost** (and its p95 tail, 1433 ms, is
what pushes voice-to-voice p95 past 2 s). RESEARCH **Assumption A4** (that ~800 ms is reachable with this
cascade untuned) is in tension with this measured floor.

Levers that could close the gap (informational — **not** scoped or planned here):
- **Flux server-side EOT** measured with a Flux-native observer (could replace the ~401 ms `vad_stop`).
- **PIPE-08 ack-masking** — mask perceived latency with an immediate short filler (the design's named v2 lever).
- **Context/persona trimming** to cut Haiku TTFT and tame its p95 tail.
- **Eager EOT** (speculative LLM), once measurable.

### ESCALATION — decision required (plan-04 rule)

Because the winner's measured voice-to-voice p95 (2210.7 ms) exceeds the 1.2 s ceiling, execution is
**paused for an explicit human tuning/scope decision** rather than closing the phase silently over the
ceiling. This preserves D-13 exactly: no CI gate is added and no tool exits nonzero — the pause is an
execution decision, not a tooling failure. Options:

1. **Tune further in this plan** — add a Flux-native observer and measure Arm C; trim persona/context to
   cut Haiku TTFT; test lower `stop_secs` / eager EOT.
2. **Accept the number** with recorded reasoning — the cascaded-pipeline floor with hosted APIs; barge-in
   feels slick subjectively; conference-demo tolerance — and treat ~800 ms as a v2 goal.
3. **Scope a later phase** for the PIPE-08 ack-masking lever and/or the Flux-native measurement.

**Decision (2026-07-05): option 1 — _tune further now._** See the tuning round below.

---

## Tuning round 1 — 2026-07-05

**Decision recorded:** the user chose **"tune further now"** on the round-0 escalation. Scope: (1) build a
Flux-native observer so Arm C is actually measurable and measure it; (2) trim persona/context to attack
Haiku TTFT; (3) test lower `stop_secs` / eager EOT. Ground rules unchanged (minimal live runs, D-13
preserved). Persona is now **v2** (trimmed) for every run in this round.

### Lever 1 — Flux is now measurable (Open Question 1 fully resolved)

Round 0 recorded Arm C as *unmeasurable*: pipecat's `UserBotLatencyObserver` anchors every turn on the
local `VADUserStoppedSpeakingFrame`, which Flux never emits. The observer now seeds that anchor on Flux's
**EndOfTurn** (`UserStoppedSpeakingFrame`), after the parent processes it — so `vad_stop` stays null (no
locally observable turn wait) while `voice_to_voice` is measured as the **post-endpointing processing
latency** (LLM + aggregation + TTS). This EXCLUDES Flux's server-side EOT detection wait, so it compares
to the local arms' *(voice_to_voice − vad_stop)*, not to their full voice-to-voice. (Code:
`observers.py`; regression tests: `tests/test_observers.py`, no live API.)

Measured Arm C (`docs/tuning/arm-c.json`, persona v2, eager off), post-endpointing:

| stage | Flux (eager off) | Flux (eager 0.5) |
|-------|------------------|------------------|
| vad_stop        | null (server-side)      | null (server-side)      |
| llm_ttft        | 735.9 / 1444.7 (10)     | 548.4 / 836.3 (9)       |
| tts_first_audio | 158.6 / 173.1 (10)      | 167.0 / 286.1 (9)       |
| **voice_to_voice** | **1779.4 / 2389.3 (10)** | **1589.7 / 4073.2 (9)** |

*(p50 / p95 (n), ms. Both EXCLUDE the Flux server EOT wait; add Deepgram's documented median EOT ~260–300 ms
for a full-pipeline estimate.)*

**Flux verdict: measurable, and it LOSES — deferred with measured evidence.** Flux's post-endpointing
processing (p50 1779 ms, or 1590 ms with eager) is *higher than the SmartTurn winner's full voice-to-voice*
(1401.7 ms), and Flux's number does not even include its server-side EOT wait. Root cause, verified in the
run logs: a **consistent ~503 ms gap** between Flux's EndOfTurn and the LLM start. That gap is
`ExternalUserTurnStopStrategy(timeout=0.5)` — a fallback hold hard-coded into
`ExternalUserTurnStrategies.__post_init__`, which Flux's `service_metadata_frame()` auto-installs with
defaults. There is **no supported path** to lower it without passing our own `user_turn_strategies` to the
aggregator — the exact double-endpointing override the factory forbids (Pitfall 3). So on pipecat 1.5.0,
Flux carries a built-in ~0.5 s endpointing tax that keeps it behind SmartTurn. Revisit if a future pipecat
exposes that timeout through Flux's metadata, or if a later phase deliberately accepts the double-endpointing
wiring to reclaim it.

### Lever 2 — persona trim (kept for hygiene; latency delta is within noise, not a win)

`concierge.md` v1 → v2, compressed ~22 % (802 → 618 tokens) with every scenario-exercised fact and rule
preserved. Measured on the SmartTurn winner (`docs/tuning/arm-b-trimmed.json`) vs the round-0 baseline
(`docs/tuning/arm-b.json`):

| stage | Arm B baseline (persona v1) | Arm B trimmed (persona v2) |
|-------|------------------------------|-----------------------------|
| vad_stop        | 401.2 / 413.9 (10)   | 397.5 / 403.6 (10)   |
| llm_ttft        | 586.8 / 1433.0 (10)  | 542.7 / 818.0 (10)   |
| tts_first_audio | 163.7 / 212.4 (10)   | 155.8 / 172.0 (10)   |
| **voice_to_voice** | 1460.9 / 2210.7 (10) | 1401.7 / 3877.5 (10) |

**Honest read: the measured deltas are within run-to-run noise, not a demonstrated latency win.** The
apparent `voice_to_voice` p50 −59 ms and `llm_ttft` p50 −44 ms were measured in a *different session* from
the baseline (so they also carry time-of-day API variance), and — verified against the Anthropic API
reference — at ~600 prefill tokens Haiku TTFT is dominated by model/service latency, not prefill compute, so
a 184-token trim should not move TTFT meaningfully. Haiku's own per-turn TTFT ranges 457–1597 ms across
these runs, which dwarfs a 44 ms shift on n=10. The trimmed run's `voice_to_voice` p95 of 3877 ms is a
single-turn anomaly (turn 8 measured 5348 ms with all component stages normal — a transport/interrupt stall,
not a persona effect; excluding it the p95 is ~2080 ms). **The trim is kept for prompt hygiene** — a
smaller, cleaner prompt with identical measured behaviour and 5/5 scenarios passing — **not because it
demonstrably lowers latency.** The honest negative result: trimming the persona is not a TTFT lever at this
size.

**Context caveat:** the eval harness runs all five scenarios in one pipeline session, so the LLM context
accumulates to ~3000 prompt tokens — the persona is only ~600–800 of that. A production conversation is a
*fresh* session (a few turns), so its context, and thus its Haiku TTFT, is likely lighter than these
harness numbers. The measured TTFT is therefore a conservative (high) estimate.

### Lever 3 — eager EOT (rejected) and stop_secs (held at 0.2)

- **Eager EOT:** measured (`docs/tuning/arm-c-eager.json`, threshold 0.5). It helps Flux (~190 ms p50, part
  of which is the persona trim), but Flux with eager (1589.7 ms) still loses to the SmartTurn winner
  (1401.7 ms) and eager costs ~50–70 % more (often-discarded) speculative LLM calls (Pitfall 4). **Rejected**
  — no adoption; `eager_eot_threshold` stays 0.0.
- **Lower stop_secs:** **held at 0.2** by analysis, not a burned run. SmartTurn's `vad_stop` is already
  ~400 ms at 0.2, the round-0 Arm-A sweep already showed 0.2 beats 0.3/0.5, and dropping below 0.2 trades the
  barge-in-safe "no premature cutoff" feel (the demo's core value) for ~100 ms that would not clear the
  ceiling anyway (1402 → ~1300 ms, still > 1200). 0.2 is the safe floor.

### Round-1 winner and the ceiling

**Winner: Nova-3 + SmartTurn v3 + persona v2 (trimmed).** This is the default in `pipeline.toml`
(turn config unchanged from round 0; the persona-hygiene change rides in `concierge.md`).

| Metric | Round-0 winner | Round-1 winner | 1.2 s ceiling | ~800 ms target | Verdict |
|--------|----------------|--------------------|---------------|----------------|---------|
| voice_to_voice p50 | 1460.9 ms | 1401.7 ms (≈ baseline, within noise) | 1200 ms | 800 ms | ⚠️ over |
| voice_to_voice p95 | 2210.7 ms | ~2080 ms (ex-outlier; 3877 raw) | 1200 ms | 800 ms | ⚠️ over |

The tuning round did **not** meaningfully close the gap: the winner sits at ~1.4 s p50, statistically the
same as round 0. Decomposition (p50 ~1402 ms): `vad_stop` ~398 + Haiku `llm_ttft` ~543 + Flash `tts` ~156 +
aggregation/transport ≈ 300 ms. The three in-scope levers are now exhausted: Flux carries a 0.5 s built-in
hold, eager costs spend without winning, `stop_secs` is at its safe floor, and the persona trim is
within-noise on latency. **The dominant remaining cost is Haiku LLM TTFT (~543 ms p50, tail to ~1.4 s p95),
and no in-scope lever moves it.**

The remaining headroom is in levers outside this plan's scope:
- **PIPE-08 ack-masking** — mask perceived latency with an immediate short filler (the design's named v2 lever). This is the most promising path: it hides the LLM+TTS wall without needing to shrink it.
- **A faster/lighter LLM turn** — smaller/streamed system context, or a lower-latency model tier — to attack the Haiku TTFT floor directly (a model/architecture decision, not a knob).
- **Accept** the cascade floor as the Phase-1 number with ~800 ms as a v2 goal (RESEARCH Assumption A4).

**Prompt caching is NOT a viable lever here** (verified against the Anthropic API reference):
claude-haiku-4-5's minimum cacheable prefix is **4096 tokens**, and the persona system prompt is only
~600 tokens — a system-prompt cache breakpoint below the minimum silently never caches
(`cache_creation_input_tokens` stays 0, which is exactly what the runs showed). Conversation-history caching
only begins paying off once cumulative context exceeds 4096 tokens — i.e. deep in a long conversation, not
the demo-critical early turns. So caching cannot cut the early-turn Haiku TTFT that dominates this budget.

### RE-ESCALATION — DECISION (2026-07-05): accept + scope a later phase

The round-1 winner `Nova-3 + SmartTurn v3 + persona v2` measures `voice_to_voice` p50 **1401.7 ms** /
p95 ~2080 ms (ex-outlier) — still over the 1.2 s ceiling. The user chose **option 1 + option 2 combined:
accept the current number as the Phase-1 result AND scope the remaining levers into a later phase.** D-13 is
preserved throughout (no gate added, every run/report/CLI exits 0; this was an execution pause for a human
decision, not a tooling failure).

**Accepted Phase-1 number: ~1402 ms p50 / ~2080 ms p95 (ex-outlier).** Reasoning on record:

- The cascaded hosted-API floor has been reached. All three in-scope endpointing levers are exhausted —
  SmartTurn v3 already reclaims the turn-release time; Flux carries a fixed ~0.5 s built-in hold; eager EOT
  loses and costs 50–70 % more LLM spend; `stop_secs` is at its barge-in-safe floor; the persona trim is
  within measurement noise. The dominant remaining cost is Haiku LLM TTFT, which no endpointing knob touches.
- Barge-in feels slick (all five scenarios pass, playback stops within one TTS word), which is the
  demo's core "whoa" property.
- The harness accumulates ~3000 prompt tokens across five scenarios in one session; a *fresh-session*
  production conversation is a few turns, so real early-turn Haiku TTFT — and thus voice-to-voice — is
  likely **lower** than these measured numbers. The accepted figure is a conservative (high) estimate.

**≤1.2 s (and the ~800 ms aspiration) is now a committed later-phase goal, not a v2 vibe.** The scoped
follow-up levers for that phase, in expected-value order:

1. **PIPE-08 ack-masking** — highest expected perceptual value. Mask the LLM+TTS wall with an immediate
   short filler so the user hears a response begin before the real turn is ready. Hides the floor rather
   than shrinking it; the design's named perceived-latency lever.
2. **A faster/lighter LLM turn** — attack the Haiku TTFT floor directly via a smaller/streamed system
   context or a lower-latency model tier. A model/architecture decision, not a knob.
3. **Prompt caching — with an honest cap.** claude-haiku-4-5's minimum cacheable prefix is **4096 tokens**;
   the ~600-token system prompt is far below it, so caching **only engages once cumulative conversation
   context exceeds 4096 tokens.** It is therefore a **long-conversation lever** (helps late turns of an
   extended chat), **not** a fix for the demo-critical first turns that dominate this budget. Scope it with
   that limitation stated, or it will be mis-sold as a TTFT fix (as this plan initially did before the
   correction above).
4. **Flux double-endpointing experiment (optional).** Deliberately accept the Pitfall-3 override to strip
   Flux's ~0.5 s `ExternalUserTurnStopStrategy` hold and re-measure — worth doing **only** if Flux's
   server-side EOT then beats SmartTurn end-to-end (this plan's measurement says it currently does not).

_(The phase-level roadmap item for this later work is owned by the orchestrator; this document records the
decision and the scoped levers so it is self-contained.)_

**Plan 01-04 is closed on this decision.**

---

## Chosen voice

_Stub — completed by plan 01-05 after the D-02 three-voice ElevenLabs audition. `pipeline.toml`
`tts.voice_id` is empty until then; the harness uses an interim premade voice (see plan 01-03)._
