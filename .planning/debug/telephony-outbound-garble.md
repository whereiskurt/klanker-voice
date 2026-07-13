---
status: awaiting_human_verify
trigger: "Phase 12 telephony: outbound audio to PSTN caller is garbled/unusable. Real cell → DID 613-480-KURT → KPH connects and caller audio reaches the agent fine, but the KPH→caller return path (agent TTS → Asterisk → VoIP.ms → PSTN) is garbled. Prior investigation ruled out CPU starvation and jitter; suspected format/packetization bug in the outbound path (PCMU/RTP media adapter from Phase 10, telephony-edge container with Asterisk). Task container was already bumped to 2vCPU/4GB and garbling persists."
created: 2026-07-12
updated: 2026-07-12
---

## Symptoms

**Expected behavior:**
KPH's synthesized speech (ElevenLabs TTS, 24kHz PCM from the pipeline) should reach the PSTN caller as clear, intelligible audio after the outbound chain: pipeline 24kHz PCM → resample to 8kHz → µ-law (PCMU) encode → RTP packetize → Asterisk externalMedia → VoIP.ms → PSTN. This exact path worked cleanly in the Phase-11 LOCAL softphone proof (§19-C).

**Actual behavior:**
Outbound audio (KPH→caller) is CONSTANT garble — unusable — on real PSTN calls through the deployed Fargate telephony-edge. The garble is constant, not periodic/bursty. Inbound direction (caller→Deepgram) is roughly OK: Deepgram produces coherent transcripts, the LLM responds, quota gating works — the call is otherwise fully functional.

**Error messages:**
None known. No pipeline crashes; the call proceeds normally except audio quality on the return path.

**Timeline:**
Never worked over real PSTN (Phase 12, live-verified 2026-07-12). Same outbound media code worked in Phase-11 local Docker Asterisk + softphone test. Garble persisted through the entire Phase-12 fix chain (SG RTP range fix, local_net/SDP public-IP fix, 2vCPU/4GB bump).

**Reproduction:**
Call the DID 613-480-5878 (613-480-KURT) from a real cell (caller-ID 5197101515 mints kph-tier), pass PIN gate, let KPH speak. Requires a human to place/listen to the call — live verification is a user checkpoint.

## Context (prior investigation — do NOT re-chase)

- **RULED OUT: CPU starvation.** 0.5→2 vCPU/4GB bump made no difference; garble is constant at 2 vCPU.
- **RULED OUT: network jitter/loss.** Constant garble, not periodic; inbound RTP path is fine after SG (10000-10020) and local_net/SDP fixes.
- **Working direction:** inbound caller→Asterisk→externalMedia→pipeline→Deepgram is intelligible.
- **Broken direction:** outbound pipeline→externalMedia→Asterisk→VoIP.ms→caller.

**Prime suspects (all in `apps/voice/src/klanker_voice/telephony/`):**
1. Outbound resample 24000→8000 in `transport.py` TelephonyOutputTransport.write_audio_frame (PIPELINE_OUTPUT_SAMPLE_RATE const) — verify the resampler is actually invoked and the rate math is right (is the pipeline really emitting 24kHz? ElevenLabs/pipecat output rate vs the const).
2. `media.py` ulaw_encode byte correctness at 8kHz (endianness, sample width).
3. RTP packetization in `controller.py` — clock_rate / samples_per_packet / packet_ms from TelephonyConfig (defaults 8000 / 20ms); wrong timestamp increment or samples_per_packet → constant garble.
4. ARI externalMedia format negotiation (fmt="ulaw") vs what the pipeline actually writes (16-bit slin vs µ-law mismatch is a classic constant-garble signature).

**Concrete first moves suggested by prior session:**
- Tap/dump the exact bytes the pipeline writes to the external-media socket; check payload sizes/ptime.
- Enable ECS Exec (`aws ecs update-service --enable-execute-command` + inline ssmmessages policy on task role — resets on terragrunt redeploy, re-enable per session) and use `rtp set debug on` / `pjsip set logger on` inside Asterisk to inspect outbound payload type/size/timestamps.
- A/B packet_ms.
- Compare against the Phase-11 local proof: what differs between local Docker (worked) and Fargate (garbled)? Same image? Same sample-rate env vars/config?

**Environment:** Fargate service `telephony-edge-use1`, cluster `app-use1-kmv`, acct 052251888500 us-east-1, profile klanker-application. Branch `gsd/phase-12-voip-ms-telephony-inbound-did`, PR #30 open. Asterisk in-container (pjsip.conf/rtp.conf), VoIP.ms subaccount 557010_klanker-pbx (ulaw, toronto1 POP).

## Evidence

- timestamp: 2026-07-12 (static analysis pass 1)
  checked: git log of transport.py/media.py; ari.py create_external_media; Asterisk pjsip.conf/rtp.conf/extensions.conf
  found: Outbound media code is BYTE-IDENTICAL to Phase 10 (last change cce6e91, feat 10-02) — no code drift since it was written. externalMedia created with format="ulaw" (symmetric with inbound). Both Asterisk endpoints (softphone + voipms) are disallow=all/allow=ulaw. Inbound direction working PROVES ulaw codec, RTP parse, resampler, symmetric-RTP peer learning, and the Asterisk<->Klanker externalMedia socket all function.
  implication: The bug is NOT code drift and NOT a broken primitive (codec/RTP/resampler all proven by the working inbound path).

- timestamp: 2026-07-12 (FOUNDATIONAL CORRECTION)
  checked: .planning/quick/260712-ckd-.../260712-ckd-SUMMARY.md (D4, human_judgment:true, verification:[]) + STATE.md (stopped_at / last_activity)
  found: The "Phase-11 LOCAL softphone proof" that supposedly proved clean OUTBOUND audio WAS NEVER ACTUALLY RUN. D4 rationale: "This authoring sandbox has no running Docker daemon... The actual live run remains explicitly deferred." STATE.md confirms "Plan 08 (manual cellular proof) remains." No human ever heard clean KPH->caller audio in ANY environment (softphone or PSTN).
  implication: The debug premise "same outbound media code worked in the Phase-11 local proof" is FALSE. Outbound audio has NEVER been verified clean anywhere. This is an ALWAYS-PRESENT, never-tested bug — NOT a Fargate-vs-local regression. Do not spend effort diffing local-vs-Fargate looking for a regression; there is no working baseline.

- timestamp: 2026-07-12 (test coverage gap)
  checked: apps/voice/tests/test_telephony_transport.py::test_output_frame_emits_pcmu_rtp
  found: The ONLY outbound write_audio_frame test feeds _silence_pcm (all-zero PCM) and asserts ONLY RTP structure (stable SSRC, seq monotonic, ts+=160, payload_type). It never decodes the emitted ulaw back to compare against a known input waveform. Silence encodes to a constant ulaw byte, so any resample/encode/format waveform corruption is invisible to this test.
  implication: Outbound audio FIDELITY has zero automated coverage. A waveform-corrupting bug would pass all existing tests. Need an offline fidelity reproduction with a real (non-silent) signal.

## Eliminated

- hypothesis: CPU starvation of the real-time thread causes the garble
  evidence: 2 vCPU/4GB bump (commit 1429b67) — garble unchanged
- hypothesis: network jitter/packet loss
  evidence: garble is constant not periodic; inbound RTP path clean after SG + local_net fixes
- hypothesis: outbound resample 24000->8000 corrupts the waveform (suspect #1)
  evidence: offline repro (scratchpad/repro_outbound.py) pushed a 440Hz sine through the EXACT chain (SOXR resample 24000->8000 -> PcmFramer -> ulaw_encode -> RTP -> decode) and recovered 439.6Hz with correct RMS — fidelity-perfect. Resampler signature confirmed resample(audio, in_rate, out_rate); args are correct.
  timestamp: 2026-07-12
- hypothesis: ulaw_encode byte correctness (suspect #2)
  evidence: same offline repro round-trips cleanly; ulaw_encode is symmetric with ulaw_decode which is proven by the WORKING inbound path.
  timestamp: 2026-07-12
- hypothesis: RTP packetization clock_rate/samples_per_packet/timestamp (suspect #3)
  evidence: existing test_output_frame_emits_pcmu_rtp asserts ts+=160, seq monotonic, stable SSRC, pt=0; offline repro emits 48 packets for ~1s of audio (correct 50/s). Structure is correct.
  timestamp: 2026-07-12
- hypothesis: ARI externalMedia format mismatch slin-vs-ulaw (suspect #4)
  evidence: externalMedia created format="ulaw" (ari.py:127/143); we send ulaw pt=0; inbound ulaw decode works — format is symmetric and correct.
  timestamp: 2026-07-12

## Current Focus

reasoning_checkpoint:
  hypothesis: "Outbound RTP is not real-time paced. TelephonyOutputTransport.write_audio_frame -> SocketRtpMediaSession.write_packet -> socket.sendto() returns instantly (non-blocking UDP). Pipecat's BaseOutputTransport._audio_task_handler (without_mixer path) drains _audio_queue with NO pacing sleep and relies on write_audio_frame to provide real-time back-pressure (as every other pipecat transport does: local audio blocks on the device, Daily sleeps 10ms, WebRTC hands to a media-clock-paced track). The telephony transport provides none, so an entire TTS utterance's RTP packets (dozens/hundreds) are blasted to Asterisk's externalMedia socket in milliseconds instead of one per 20ms. Asterisk has no read-side jitterbuffer on a UnicastRTP externalMedia channel and forwards the burst to VoIP.ms/PSTN -> constant garble on every utterance."
  confirming_evidence:
    - "base_output.py without_mixer() drains _audio_queue via queue.get() with no sleep; handle_audio_frame() enqueues ALL chunks at once (lines 594-602)"
    - "Every other pipecat transport paces INSIDE write_audio_frame: local/audio.py blocks on the sound device; daily/transport.py sleeps 0.01s per 20ms; smallwebrtc AudioStreamTrack does await asyncio.sleep(wait) on a media clock"
    - "telephony write path (transport.py write_audio_frame -> rtp_socket.py write_packet -> transport.sendto) has zero sleep/block/clock — returns instantly"
    - "offline repro proved the audio BYTES are fidelity-correct; only delivery TIMING is wrong"
    - "asymmetry explains inbound-fine/outbound-garbled: Asterisk generates inbound RTP on its own 20ms timer (clean), we generate outbound with no timer (burst)"
    - "explains all ruled-out items: not CPU (2vCPU no change), not jitter (a burst is the opposite of jitter), never worked live (proof never run; offline tests use an in-memory list that ignores timing)"
  falsification_test: "Capture outbound RTP inter-packet arrival on the container (rtp set debug / tcpdump): if gaps are ~0ms it is a burst (confirms); if ~20ms the theory is wrong. Definitive: add real-time pacing and confirm a live caller hears clean audio."
  fix_rationale: "Add real-time pacing to the outbound path so exactly one 20ms RTP packet leaves per 20ms wall-clock — the same real-time back-pressure every other pipecat transport provides. This directly addresses the root cause (delivery timing), not a symptom."
  blind_spots: "Cannot 100% confirm Asterisk's externalMedia read behavior on a burst without the container/live call; but the burst itself is provable offline and pacing is unambiguously missing vs every reference transport. A live PSTN call (human checkpoint) is the final confirmation."

## Resolution

root_cause: "Outbound telephony audio is never real-time paced. pipecat's BaseOutputTransport delegates real-time pacing to each transport's write_audio_frame (local audio blocks on the device, Daily sleeps per-frame, WebRTC uses a media-clock track). TelephonyOutputTransport.write_audio_frame resamples/encodes/packetizes then calls SocketRtpMediaSession.write_packet, which does a non-blocking socket.sendto() and returns instantly — no clock, no sleep, no back-pressure. So a whole TTS utterance's RTP is dumped to Asterisk's externalMedia socket in milliseconds instead of one packet/20ms. Asterisk (no read-side jitterbuffer on UnicastRTP externalMedia) forwards the burst to VoIP.ms/PSTN, so the caller hears constant garble on every utterance. Inbound is clean because Asterisk paces its own RTP transmission. The audio bytes/codec/RTP structure are all provably correct (offline sine round-trip is fidelity-perfect); only delivery timing is wrong."
fix: "Added a real-time send clock to TelephonyOutputTransport (transport.py). write_audio_frame now calls _pace() before each media.write_packet: a monotonic schedule advancing by packet_time_ms (20ms) per packet, resyncing to now after any gap >1 interval (first packet, inter-utterance silence, post-barge-in flush) to avoid catch-up bursts. flush() also resets the clock. This supplies the real-time back-pressure pipecat's BaseOutputTransport expects (mirroring local-audio device blocking / Daily's per-frame sleep / WebRTC's media-clock track) so exactly one 20ms RTP packet reaches Asterisk's externalMedia socket per 20ms wall-clock instead of a whole-utterance burst."
verification: "OFFLINE VERIFIED. (1) New regression test test_output_rtp_is_real_time_paced_not_bursted asserts wall-clock inter-packet gaps are ~20ms (0.010-0.040s), not ~0 — passes. (2) Offline sine round-trip (scratchpad/repro_outbound.py) confirms byte fidelity was always correct — only timing was wrong. (3) Full telephony suite 118/118 pass, no regressions. PENDING: live PSTN call (human checkpoint) — caller must confirm KPH audio is now clear."
files_changed: ["apps/voice/src/klanker_voice/telephony/transport.py", "apps/voice/tests/test_telephony_transport.py"]
