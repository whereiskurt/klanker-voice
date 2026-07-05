# klanker-voice — Endpointing A/B Tuning Verdicts (D-12)

**Pipeline:** pipecat-ai `~=1.5.0` (pinned) · cascaded Nova-3 → Claude Haiku 4.5 → ElevenLabs Flash v2.5
**Measured:** 2026-07-05, local laptop, `bot.py -t eval` over the plan-03 five-scenario suite
(greeting, memory, bargein_early, bargein_mid, bargein_monologue) — identical scripted turns per arm.
**Clock:** all numbers are `UserBotLatencyObserver` observer-clock milliseconds. They are **never**
comparable with the eval-harness `within_ms` budgets (a different anchor — Pitfall 7). Verdicts are
**informational only (D-13)**: no run, report, or CLI ever exits nonzero on a threshold.

> **STATUS: ESCALATED — see [Latency vs the 1.2 s ceiling](#latency-vs-the-12s-ceiling-d-13-pipe-01).**
> The winning configuration's measured voice-to-voice p95 (2210.7 ms) exceeds the 1.2 s roadmap
> ceiling. Per the plan-04 escalation rule this is surfaced for an explicit tuning/scope decision;
> it is **not** a tooling gate and does **not** fail the phase. The decision is **pending**.

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

**Decision: _pending_.** Record the chosen option and its reasoning here once made.

---

## Chosen voice

_Stub — completed by plan 01-05 after the D-02 three-voice ElevenLabs audition. `pipeline.toml`
`tts.voice_id` is empty until then; the harness uses an interim premade voice (see plan 01-03)._
