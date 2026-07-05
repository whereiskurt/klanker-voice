# Phase 7 — Design Evolution Notes

> Captured 2026-07-05 from Kurt, after live demo chatter with KPH (now in KPHv1, his own
> cloned voice). These REVISE parts of 07-CONTEXT.md — read both; reconcile at plan time.

## The router concept (new architecture direction)

Kurt, verbatim intent: *"a router concept where it gets that quick answer but it's also
then using a more focused prompt — I'd build out knowledge for each of the systems like
klanker-maker or defcon.run.34 or meshtk (aka meshtastic toolkit). I'd want a quick kinda
'OK! Let's dig into it.' and while that's happening it's switching to the deeper context
prompt around the specific topic. I'd want to build up a few topics and maybe even a
knowledge map."*

**Shape:** two-tier prompting.
1. **Router turn (fast):** a small, always-loaded router + topic map classifies the
   question to a system/topic and emits a quick acknowledgment ("OK! Let's dig into it.").
2. **Deep turn (focused):** the system swaps in a topic-specific deep-context prompt
   (per-system knowledge pack) and answers from it.
3. **Knowledge map:** a structured topic graph (systems → subtopics → cross-links) the
   router classifies against and KPH steers with ("that connects to X — want to go there?").

**Per-system packs (initial set):** klanker-maker, defcon.run.34, meshtk (meshtastic toolkit).

## Corpus source (new)

Kurt is recording a **several-hour talk** about klanker-maker, defcon.run.34, and meshtk.
That transcript is the **base knowledge corpus**. The SAME recording is the ElevenLabs
training data for KPHv1 (his voice clone) — one recording, two uses: voice + knowledge.

**Implication:** the Phase-7 digest generator's input is no longer just public repo
READMEs — it's Kurt's own conversational transcript (already voice-friendly, story-rich,
public-safe since Kurt controls what he says), distilled per-topic. Raw multi-hour
transcript is the SOURCE, not the pack — it needs ASR cleanup + per-topic distillation.

## How this revises the scoped 07-CONTEXT.md decisions

| Prior decision (07-CONTEXT) | Evolution |
|---|---|
| **D-10 pre-digested only, ONE cached pack** | → **router + per-topic deep packs.** Each turn carries only the relevant topic's deep context (smaller prompt → better Haiku TTFT, more focused answers) instead of one giant always-loaded pack. Reconcile at plan time. |
| **D-11 no live retrieval / no tool-calling** | → the **router is topic-SELECTION, lighter than RAG** (pick a pack, not fetch documents). This nuances "no retrieval" — it's a bounded classify-then-load, not open retrieval. Decide in planning whether the router is keyword/embedding/tiny-Haiku. |
| **D-13 caching: one pack ≥4096 tokens caches** | → **per-topic packs each cache** once warm; pre-warm each at session start to hide first-switch cost. Router prompt is small/cheap (below cache threshold). |
| **Corpus = curated public repos + knowledge/ dir** | → **add Kurt's recorded transcript** as the primary conversational corpus, distilled per-topic. Still public-only (D-02 holds — Kurt controls the recording content). |

## My assessment (thinking-partner)

- **The ack line is the convergence of Phase 6 and Phase 7.** "OK! Let's dig into it."
  is exactly Phase-6 PIPE-08 ack-masking — but it now earns its keep twice: it masks the
  LLM latency AND covers the topic-context swap. One UX affordance, two problems solved.
  This is the strongest argument for building the router this way: the switch cost is
  *free* because the ack line was going to exist anyway.
- **Router > one fat pack, for latency AND quality.** A focused per-topic prompt gives
  Haiku less to read (lower TTFT) and a tighter answer. The fat-pack model we scoped was
  the conservative "no moving parts" choice; the router is better if the switch is masked.
- **One recording, two uses is elegant and on-brand.** KPH knows Kurt AND sounds like Kurt,
  both distilled from the same few hours. It's efficient and it's a great story for the
  booth.
- **Watch-outs for planning:** (1) router misclassification → wrong deep pack → confident
  wrong answer; needs a confidence floor + graceful "which of these did you mean?" fallback.
  (2) transcript cleanup is real work (ASR errors, tangents, cross-talk). (3) the ack line
  must not fire when the router is confident it can answer from the always-loaded layer
  (over-eager "let me dig in" on a one-liner question feels slow, not slick). (4) topic-map
  authoring/maintenance is ongoing — tie it to the manifest refresh command (D-07).

## Status

Not scoped into a plan yet — Phase 7 is still at CONTEXT stage. When Phase 7 is planned,
the planner should treat this note as a design amendment to 07-CONTEXT.md and reconcile
D-10/D-11/D-13 accordingly. No code exists for any of this.
