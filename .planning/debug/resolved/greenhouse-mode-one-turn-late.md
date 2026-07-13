---
status: resolved
trigger: "Greenhouse recruiting mode engages ONE TURN LATE on PSTN calls: the caller says the magic word 'greenhouse', KPH answers that turn in the normal persona, and the sticky interview mode (LLM opener asking what role) only fires on the FOLLOWING turn. User wants it to engage on the trigger turn — 'boom next turn'."
created: 2026-07-13
updated: 2026-07-13
---

## Symptoms

**Expected behavior:**
When an utterance containing the exact keyword "greenhouse" (weight-3, hidden+sticky topic in `knowledge/router/topic-map.yaml`) reaches the router, the recruiting pack should swap in and THAT turn's LLM response should already be the greenhouse-mode opener (first-person, assumes recruiting, asks what role). Ack is deliberately suppressed (`ack: ""`) — the LLM opener is the sole output.

**Actual behavior:**
On real PSTN calls (2026-07-13, two separate calls), the response to the greenhouse utterance came from the NORMAL persona; the interview-mode opener only fired on the turn after. The mode does engage and stick — just one turn late.

**Error messages:**
None. No crashes; purely a timing/ordering behavior.

**Timeline:**
Observed on the first two real greenhouse attempts over PSTN (telephony edge, Phase 12). Greenhouse mode was built for the WebRTC path (PR #12/#13) where it reportedly felt immediate; PSTN is where the lag shows.

**Reproduction:**
Live: call a DID, unlock gate, say "…I'm from Greenhouse." Response is normal persona; next utterance gets the recruiting opener. Offline: likely reproducible in unit tests by driving the pipeline/router with FRAGMENTED final TranscriptionFrames (see evidence) — the caller's utterance arrives as multiple frames and the aggregator/LLM turn races the router's block1 rebuild.

## Evidence (from CloudWatch logs, log group /ecs/telephony-edge-telephony-edge-use1-kmv, 2026-07-13 02:37 and 02:58 UTC)

- **STT is NOT the problem:** Deepgram transcribed "Greenhouse" correctly (single word, capital G) in both calls.
- **Call 1 (02:37):** user content aggregated as fragments `[{'Wait.'}, {'Wait.'}, ...]` then `[{'Wait. Wait. Wait. Wait.'}, {'from Greenhouse.'}]` → assistant replied in NORMAL persona ("Yeah — Kurt actually works at Greenhouse Software…"). Next user turn ("So I said I'm recruiter from Greenhouse.") → assistant then produced the mode opener ("That's the magic word… What kind of role are you hiring for?").
- **Call 2 (02:58):** user content `[{"Hey. My name's"}, {"Mike, and I'm from Greenhouse."}]` → assistant replied normal persona ("…What brings you by the booth?"). Next user turn ("I'm doing a little recruiting.") → assistant produced the opener ("Alright — I'll take that as a recruiting cue. What kind of role are you hiring for?").
- Both greenhouse utterances arrived as MULTIPLE text parts in the user message (list-of-text content) — PSTN endpointing fragments utterances far more than the browser mic.

## Context / suspects

- `KnowledgeRouterProcessor` (`apps/voice/src/klanker_voice/knowledge/router.py`) sits between `stt` and `user_aggregator` in `pipeline.build_pipeline`. It classifies transcript text and, on a genuine topic switch, rebuilds block1 and applies it via the LLM service's `Settings.system_instruction` (see Phase-7 07-01 notes — `apply_system_blocks` sets the system array directly on the service because pipecat's AnthropicLLMAdapter flattens list-content system messages).
- Working hypothesis: with fragmented utterances, the aggregator triggers the LLM turn (on UserStoppedSpeaking/aggregation timeout) BEFORE the router has classified the fragment containing the keyword and/or before the system_instruction swap lands — so the in-flight turn still runs the old persona. On the next turn the swap is already in place → opener fires.
- Alternative/secondary hypotheses to check: router classifies only on certain frames (final TranscriptionFrame) and the keyword fragment ordering relative to UserStoppedSpeakingFrame; router's swap happens on "deep turn" detection that requires the aggregated turn to complete first; sticky/hidden topic handling path differs from normal topics (`_maybe_*` greenhouse branches in router.py lines ~260-330).
- Constraint: fix must NOT regress the WebRTC path (greenhouse is live in prod there — PR #12/#13), must not break router ack behavior for normal topics, must keep the ack suppressed for greenhouse, and must not add cross-turn latency (≤1.2s voice-to-voice budget). Full suite currently 422 passed/53 skipped.
- Test surface: `apps/voice/tests/` has router tests (Phase 7) and telephony tests; a regression test should drive fragmented final transcripts through the router+aggregator ordering and assert the greenhouse system blocks are in effect for the SAME turn's LLM call.

## Evidence

## Eliminated

- hypothesis: Deepgram mis-transcribes "greenhouse" over 8kHz PSTN ("green house") so the keyword never matches
  evidence: CloudWatch logs show "Greenhouse" transcribed correctly in both calls; the mode DID engage — just one turn late
  timestamp: 2026-07-13

## Current Focus

hypothesis (refined after source trace): PSTN STT fragments one spoken turn into MULTIPLE finalized TranscriptionFrames. The pipecat user-aggregator stop strategy (TurnAnalyzerUserTurnStopStrategy, smart_turn_v3) triggers an LLM inference on the FIRST finalized fragment (which lacks "greenhouse") — that inference runs with the normal persona. The router correctly applies the greenhouse block-swap when it later processes the keyword fragment, but that fragment only STARTS a fresh pending user turn (TranscriptionUserTurnStartStrategy); its inference does not fire until the NEXT end-of-turn signal (user speaks again or the stop-timeout watchdog). So the opener appears one turn late. On WebRTC the whole utterance arrives as ONE clean final frame → single inference AFTER the swap → opener is immediate. Router classifies each fragment in ISOLATION and has no notion of "keyword arrived mid-turn, re-run this turn."
test: Build offline reproduction: real KnowledgeRouterProcessor + real LLMContextAggregatorPair user aggregator + fake LLM recording (system_instruction snapshot, context) per context frame. Drive a fragmented turn (finalized "Wait." then finalized "...from Greenhouse.") via manual VAD + TranscriptionFrame(finalized=True) frames. Assert the FIRST inference after the keyword utterance runs with greenhouse blocks (currently expected to FAIL — it runs normal blocks).
expecting: reproduction shows inference #1 (pre-keyword fragment) fires with normal system_instruction; greenhouse blocks apply only after, and no inference fires for the keyword fragment until a later turn boundary.
next_action: confirm the linear-pipeline ordering assumption empirically with the reproduction test; then design a fix that fires the greenhouse opener on the SAME turn without double-firing on the clean WebRTC path.

## Evidence (source trace, 2026-07-12)

- timestamp: 2026-07-12
  checked: pipecat AnthropicLLMService._process_context (llm.py:300-342) + process_frame (521-539)
  found: The LLM reads the SHARED, mutable `self._settings.system_instruction` at GENERATION time when it processes an LLMContextFrame — not captured when the context frame was created. apply_system_blocks writes exactly this field (prompt_assembly.py:247).
  implication: The router's block-swap and the answering inference are only causally ordered if the keyword frame is processed by the router BEFORE the aggregator emits that turn's context frame. With PSTN fragmentation this ordering breaks.

- timestamp: 2026-07-12
  checked: llm_response_universal.py user aggregator + TurnAnalyzerUserTurnStopStrategy (turn_analyzer stop). _handle_transcription triggers user_turn_stopped on a finalized transcript when turn is COMPLETE; _on_user_turn_inference_triggered → push_aggregation → context frame → LLM. Multiple finalized transcripts per turn = multiple inferences.
  found: On PSTN one spoken turn fragments into several finalized transcripts. An EARLIER fragment (no keyword) can fire an inference before the keyword fragment is processed/swapped. The keyword fragment then applies greenhouse blocks and merely STARTS a fresh pending turn whose inference doesn't fire until the next end-of-turn (user speaks again / stop-timeout). WebRTC delivers ONE clean final per turn → single inference after the swap → immediate.
  implication: Root cause is a swap-vs-inference race amplified by PSTN utterance fragmentation, NOT a router classification bug (router classifies each final correctly).

- timestamp: 2026-07-12
  checked: DeepgramSTTService default Settings (stt.py:358 interim_results=True); InterimTranscriptionFrame class (distinct from TranscriptionFrame, has .text); router.process_frame (only acts on final TranscriptionFrame); gate.py (forwards all frames once unlocked); duplex.py (forwards InterimTranscriptionFrame, only swallows FINAL during suppression).
  found: Deepgram streams interim transcripts continuously DURING speech, well before any final/turn-stop inference. They reach the router untouched today (router ignores them) on both WebRTC and telephony (post-unlock) paths.
  implication: Detecting the greenhouse magic word on an INTERIM lets the block-swap land BEFORE the premature inference — closing the race. Restricting the interim path to hidden+sticky keyword topics keeps noisy interims from ever switching normal topics or firing the Haiku fallback.

## Reasoning checkpoint

reasoning_checkpoint:
  hypothesis: "Greenhouse fires one turn late because the block-swap is applied only when the router processes the FINAL 'greenhouse' transcript, but PSTN fragmentation makes the aggregator fire that turn's LLM inference on an earlier fragment before the swap lands (the LLM reads the shared system_instruction at generation time). WebRTC's single clean final avoids the race."
  confirming_evidence:
    - "AnthropicLLMService reads the shared _settings.system_instruction at generation time (llm.py:300-342), decoupled from when the context frame was created."
    - "TurnAnalyzerUserTurnStopStrategy fires an inference per finalized transcript when the turn is COMPLETE; PSTN produces multiple finalized transcripts per spoken turn (CloudWatch: list-of-text user content in both live calls)."
    - "Router acts only on final TranscriptionFrame; interims (emitted continuously by Deepgram, interim_results=True default) currently pass through untouched."
  falsification_test: "If driving an InterimTranscriptionFrame containing 'greenhouse' through the router does NOT apply the greenhouse block1 to the LLM settings (i.e. interims can't carry the keyword to the router), the interim-early-lock fix is invalid."
  fix_rationale: "Early-lock the hidden+sticky greenhouse keyword on interim transcripts so the swap precedes the turn's inference — mirroring WebRTC timing. Scoped to hidden+sticky topics so normal-topic switching/Haiku fallback stay final-only (no regression). Sticky state makes the subsequent final a no-op → exactly one opener on both paths."
  blind_spots: "Full offline reproduction of the aggregator turn-race needs audio/smart-turn; validated the race by source trace instead. Rare interim ASR false-positive on 'greenhouse' could early-lock recruiting mode (acceptable for a sticky easter egg with an exit phrase)."

## Resolution

root_cause: "PSTN STT fragments one spoken turn into multiple finalized transcripts. The user-aggregator's turn-stop strategy fires an LLM inference on an earlier fragment (before the 'greenhouse' fragment reaches the router), and the Anthropic service reads the shared system_instruction at generation time — so that inference answers in the normal persona. The router's block-swap (applied when it finally processes the keyword FINAL) lands after, and the keyword fragment only starts a fresh pending turn whose inference fires on the NEXT end-of-turn → the greenhouse opener is one turn late. WebRTC delivers a single clean final per turn, so its one inference is causally after the swap → immediate."
fix: "Detect the hidden+sticky greenhouse magic word on INTERIM transcripts in KnowledgeRouterProcessor and commit the sticky switch early (block-swap + ambience), so the swap precedes the turn's premature inference. Restricted to hidden+sticky topics (never normal-topic switching or Haiku fallback on noisy interims); sticky state makes the later final a no-op (no double-opener)."
verification: |
  Self-verified offline:
  - New regression test test_interim_greenhouse_early_locks_before_any_final drives an InterimTranscriptionFrame('...greenhouse') through the REAL router via run_test and asserts block1 swaps to the greenhouse pack with NO final sent. RED-checked: temporarily disabling the interim branch makes exactly this test FAIL (proves it catches the bug); restored → passes.
  - test_interim_lock_then_final_greenhouse_is_a_sticky_noop: after early-lock, the eventual FINAL 'greenhouse' pushes NO frames (no ack, no second swap) → exactly one opener, no double-response.
  - test_interim_never_early_locks_a_normal_topic: a normal-topic keyword in an interim never switches and NEVER calls the Haiku fallback (fallback raises if invoked) → no regression to normal-topic acks / latency.
  - test_interim_early_lock_candidate_only_matches_hidden_sticky: candidate selector matches greenhouse, rejects normal topics, and never re-nominates the active sticky topic.
  - Full suite: 426 passed / 53 skipped (was 422/53; +4 new tests, no regressions). cd apps/voice && .venv/bin/pytest tests/ -q
  PENDING human verification: live PSTN call — say 'greenhouse', confirm the recruiting opener fires on the SAME/next turn (not delayed a full turn).
files_changed: apps/voice/src/klanker_voice/knowledge/router.py, apps/voice/tests/test_greenhouse_hidden.py, infra/terraform/live/site/services/telephony-edge/service.hcl
  - apps/voice/src/klanker_voice/knowledge/router.py: import InterimTranscriptionFrame; add _early_lock_candidate() (hidden+sticky-only keyword scan) and _early_lock_via_interim(); handle InterimTranscriptionFrame in process_frame to commit the sticky switch early.
  - apps/voice/tests/test_greenhouse_hidden.py: 4 new regression tests + imports (InterimTranscriptionFrame, TranscriptionFrame, run_test).
