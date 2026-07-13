---
created: 2026-07-13T14:10:00.000Z
title: Telephony reach — ring/"hey" pickup cue + SMS→voice-memo MMS channel
area: voice/telephony
files:
  - apps/voice/src/klanker_voice/telephony/  (Asterisk dialplan / Stasis answer flow)
  - apps/voice/greetings/  (pre-rendered KPH pickup "hey" clip)
  - apps/voice/  (new inbound-SMS webhook handler + LLM reply + ElevenLabs synth-to-file)
  - infra/terraform/live/site/  (SMS webhook route/service env; ledger-style S3 for MMS media)
  - kv/internal/app/cmd/voipms.go  (operator commands: enable MMS/CNAM, test send)
---

## Problem / intent

Operator wants the PSTN experience to feel less abrupt and to extend the agent to
async text. Two independent but related feature clusters, both riding the existing
VoIP.ms (carrier) + ElevenLabs (KPH voice clone) + LLM/persona substrate. Captured
2026-07-13 from a "what's possible" exploration — feasibility verdicts included so this
is promote-ready.

Reuse map: VoIP.ms is already the carrier (DIDs + API creds in SSM); ElevenLabs already
holds the KPH voice clone (pre-rendered greeting machinery + ffmpeg-splice technique
exist); the LLM + persona/knowledge stack is live; the Phase-15 private-S3 pattern is a
ready analog for hosting MMS media.

## Cluster A — Call pickup feel (SMALL, existing voice path)

**Verdict: easy, fully in our control, no new vendors.** Today Asterisk answers almost
instantly so there is no "ring," and it is hard to tell the call connected.

1. **Ring cue on pickup.** Preferred: answer immediately (as now) and play ONE short
   ringback/ring sample before the greeting, so we keep full audio control and add no
   connect-fail risk. Alternative: delay the SIP 200-OK by ~one ring cycle so the caller
   hears real *network* ringback first (adds latency, no custom audio during it).
2. **Classic KPH "hey" on answer.** A short, punchy, phone-specific pre-rendered KPH clip
   ("hey—") played immediately after the ring cue, both signaling "you're connected" and
   prompting the caller to say the phrase. Reuses the hand-spliced greeting-clip pattern
   already used for the browser client. Barge-in-able.

Sequence: answer → one "bring" ring sample → short KPH "hey" → into conversation.

## Cluster B — SMS in → voice-memo MMS out, in KPH's voice (NEW async channel)

**Verdict: very doable; arguably easier than the live telephony already shipped**
(async request/response, no RTP/timing/barge-in). Loop:

inbound SMS → VoIP.ms SMS-URL webhook → LLM composes reply (reuse persona/knowledge) →
ElevenLabs synthesizes it in KPH's voice to an audio file → host it (S3 object +
presigned/CloudFront URL, Phase-15 private-bucket pattern is the analog) → VoIP.ms MMS
send with the audio attached (a "voice memo"). Optional: also send a **vCard contact
card and/or a controlled image** via the same MMS API.

New code is small: an HTTP webhook handler + a synth-and-upload step + the MMS send call.

**Caveats (none fatal):**
- MMS media must be small + broadly compatible — transcode the ElevenLabs mp3 down, keep
  well under carrier size caps.
- Confirm MMS is enabled on the specific DID(s) at VoIP.ms.
- US A2P at any volume needs **10DLC registration** or carrier spam filtering bites
  (fine for personal/demo as-is).
- Ledger tie-in: decide whether SMS/MMS turns also go in the Phase-15 transcript ledger.

## What's NOT cheaply possible (recorded so we don't chase it)

A logo/image on the caller's **incoming-call screen** is not a VoIP.ms toggle — that is
"branded calling" (Apple Business Connect / RCS Business / Rich Call Data over
STIR/SHAKEN), each requiring verified-business onboarding via a CPaaS partner. The only
lightweight on-call-screen lever is **CNAM**: register a caller-ID *name* for the DID so
some carriers show ~15 chars of text (e.g. "KLANKERMAKER") — text only, no image,
carrier-dependent propagation. The realistic "image I control on their phone" path is
MMS (Cluster B), delivered as a message rather than call-screen branding.

## Suggested shape

Could be one phase with two waves, or two phases:
- Cluster A is a quick win (dialplan/greeting change + one new audio clip) — good
  candidate for `/gsd-quick` or a small wave.
- Cluster B is a proper phase (new webhook surface, LLM/TTS-to-file, S3 media hosting,
  VoIP.ms MMS + optional vCard, plus the 10DLC/MMS-enablement operator setup).

Depends on nothing in flight; Phase 15 (ledger) merge is independent. Related:
[[voipms-telephony-integration]], [[phase12-telephony-live]],
[[voice-greeting-handpicked-take]], [[transcript-ledger-requirement]].
