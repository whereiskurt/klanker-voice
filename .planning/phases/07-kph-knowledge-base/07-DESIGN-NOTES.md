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

## Amendment 2 — corpus depth + reply STYLE (2026-07-05, from Kurt mid-planning)

Two new directions from Kurt while Phase 7 was being planned:

**1. Corpus = the FULL codebases + docs, not just READMEs.** The klanker-maker and
defcon.run.34 codebases and docs are "really good" — primary, high-signal knowledge worth a
deep-learn pass to distill per-system packs + the topic map. Local sources:
- **klanker-maker (`km`)** → `/Users/khundeck/working/klankrmkr` (~1,950 md files, has `docs/`)
- **defcon.run.34** → `/Users/khundeck/working/defcon.run.34` (~283 md files, has `docs/`)
- **meshtk** → `/Users/khundeck/working/meshtk` (~123 md files)

  A capable model (Fable) can survey these and produce structured per-system digests + a
  topic map. This is the concrete input to the router's per-topic deep packs (Amendment 1).

**2. Replies must sound like KURT, not just be factually right.** Kurt will provide
**transcripts of himself talking through a diagram**. These ground KPH's *speaking style*
(phrasing, cadence, how he explains) — not only the facts. So the one recording/transcript
now serves **THREE uses**: (a) KPHv1 ElevenLabs voice clone, (b) knowledge corpus,
(c) **reply-style exemplar**.

**Implication for the plan — two axes of grounding:**
- **WHAT** (facts): per-topic factual packs distilled from repos + transcript → the
  swappable deep-context layer the router selects.
- **HOW-IT-SOUNDS** (style): a persona/style layer derived from Kurt's transcripts
  (few-shot style exemplars or a distilled style guide) → lives in the **stable cached
  prefix** alongside the router (it does NOT change per topic, so it stays cache-warm).

  Planner: keep the style layer separate from the per-topic packs; the router prompt +
  style layer are the always-loaded stable prefix, per-topic packs append after.

## Amendment 3 — local retrieval for real depth + per-source corpus prep (2026-07-06, from Kurt: "smart and deep")

Kurt reprioritized Phase 7 to run next, explicitly to make the concierge "smart and deep."
On review, the planned design (curated per-topic packs, no retrieval) has a hard ceiling:
depth = one distilled long-form paragraph per system; it CANNOT answer ad-hoc detail from
the raw repos (km alone is ~1,960 md files). This amendment **deliberately reopens D-10/D-11
(no-retrieval)** to add a bounded local retrieval path.

**A. Retrieval added — local, keyless.** Engine = **SQLite FTS5 with BM25** (stdlib
`sqlite3`, in-process, ~tens of ms over thousands of files). No embeddings, **no 4th vendor**
— respects the three-key constraint (PIPE-07). Full semantic RAG was considered and rejected
for launch (vendor/infra/latency cost).

**B. Flow — two-tier preserved, retrieval is topic-scoped.** Shallow one-liners still answer
instantly from the cached hooks in `system[0]` (no retrieval, no ack). When a question engages
a topic, the router fires the "let me dig in" ack, then the **deep turn** injects into the
swappable `system[1]` block BOTH the curated pack (framing + Kurt style) AND the **top-k
retrieved chunks from the classified topic's corpus** (uses the router's existing topic
classification — retrieval is scoped to that topic, not global). The ack masks the retrieval +
larger-prompt TTFT.

**C. Injection budget.** Default **top-4 chunks (~1.5k tokens), tunable**. Retrieved chunks go
in `system[1]` (uncached) ONLY — never `system[0]` (keeps the cache prefix byte-identical,
Pitfall 3).

**D. Per-source corpus prep (retrieval quality = corpus quality; sources are uneven).**
- **km** — rich docs + a detailed diagram. Index the docs directly (high signal). Ingest the
  diagram as **text** (its mermaid/excalidraw source, or a described legend if it's an image)
  so its structure is searchable.
- **defcon.run.34 + meshtk** — "really the code." Raw source retrieves noisily for voice. Run
  an **offline LLM doc-generation pass** to produce structured explanatory docs FROM the code,
  using **Matt Pocock's `grill-with-docs` skill** (install: `npx skills add mattpocock/skills
  --skill=grill-with-docs` — Kurt installs it before execution; the step stays swappable if a
  different generator is preferred later). Index the **generated docs as the primary corpus**,
  raw code as a **secondary layer** for exact detail.
- **klanker-voice (self)** — document knowledge-first as it's built. **Phase 8 (Documentation
  & Architecture) is the feeder** — its README/architecture docs ARE the concierge's knowledge
  about klanker-voice. One doc effort, two payoffs.

**E. Scrubber demoted — corpus is all public.** The do-not-say scrubber is **no longer a
build-blocking security gate** (reverses its D-01/D-02 "foundational control, refuse-on-finding"
framing). Replaced by a **thin advisory lint**: regexes for AWS account IDs, role ARNs, key
blocks, and internal/`.local`/Cloud Map hostnames that **FLAG** hits in the offline refresh
git-diff for human review — never blocks. Rationale: the LLM doc-gen-over-code path can surface
things that are public-in-repo but shouldn't be volunteered aloud (e.g. account `481723467561`);
flag for the reviewer, don't gate the build. The git-diff human review (D-09) remains.

**F. Cross-system synthesis is OUT for launch.** Topic-scoped retrieval loads one topic, so
questions like "how does km relate to defcon.run's infra?" are not a launch target. Future
lever: multi-topic retrieval or a cross-linked knowledge map.

**G. Offline discipline preserved.** ALL corpus-building — doc-generation, index build, the
advisory lint — is offline (refresh workflow). Runtime = load index + one BM25 query + inject.
The ≤1.2s loop is untouched; the deep-turn cost is ack-masked.

## Amendment 4 — the transcript arrived: style layer is REAL, both-axes corpus (2026-07-07)

Kurt recorded and delivered **14 clips (~82 min, ~13.8k words)** of himself narrating
klanker-maker / DEF CON Run 34 / MeshTK. Transcribed via Deepgram nova-3 (diarize + paragraphs +
**filler_words ON** — cadence preserved as the style ground truth). Raw + proper-noun-normalized
copies live in `apps/voice/knowledge/transcripts/` (`normalized/` has 120 name fixes:
Clanker→Klanker, MeshTK, Terragrunt, DEF CON, km, etc.). This **supersedes the earlier
"transcript-optional v1" framing** — the style layer is no longer a hand-authored stand-in.

- **Both axes from one corpus.** The clips carry real architecture facts AND Kurt's phrasing at
  once. **WHAT** (facts) → feeds per-topic packs + retrieval corpus. **HOW-IT-SOUNDS** (style) →
  distilled into the **stable cached prefix** (does not vary per topic; stays cache-warm).
- **km diagram ingested as text** (Amendment 3-D) → `apps/voice/knowledge/diagrams/km-sandbox-aws.md`,
  a structured legend of the AWS-services diagram. Cross-validated against the transcripts, no
  contradictions — diagram + transcripts + repo docs triangulate the km pack.
- **Coverage is km-heavy** (~9 clips), DEF CON Run 34 (2), MeshTK (1 deep). Style distillation
  can draw from all 14; facts weight toward km for launch depth.
- **Humor/personality source added (2026-07-07):** Kurt's `run.defcon.run` talk deck (36 slides)
  distilled to `apps/voice/knowledge/style/kurt-humor-personality.md` — dry, self-deprecating,
  meme-fluent, POC‖GTFO hacker voice (strikethrough-correction bit, "vibes only", cost punchlines,
  rickroll/"hack the planet", emoji beats). Feeds the STYLE layer. **Carries a persona GUARDRAIL:**
  KPH is a public-mic-to-strangers concierge — capture the wit, but do NOT volunteer crude/edgy
  bits (the "TTP"/🍆 slide, trolls/lulz) unprompted; default PG-13 + self-deprecating, match-and-
  escalate only if the visitor brings that energy first. The deck also holds real defcon.run.34 +
  meshtk FACTS (AWS arch, terraform/terragrunt layout, DKIM/SES, ElectroDB, Meshtastic AES-CCM
  quirk) — add it as a facts-corpus manifest source too.

## Amendment 5 — corpus prep revised: direct code indexing, grill-with-docs DROPPED (2026-07-07)

Reverses Amendment 3-D's `grill-with-docs` doc-generation step. Kurt's call: index the code
**directly**, per-source:

- **km** — `docs/` (rich) + the diagram legend + transcripts. Primary, ready, high-signal.
- **defcon.run.34** (`/Users/khundeck/working/defcon.run.34`) — index **`infra/terraform/`**
  (`live/` = the deployed "services" Kurt refers to, `modules/` = reusable infra, `providers/`)
  and **`apps/`**, as **code** (no generated docs for launch). NOTE: there is no top-level
  `services/` dir — "services" = the live-site terraform deployments under `infra/terraform/live`.
- **MeshTK** (`/Users/khundeck/working/meshtk`) — **`README.md` primary** + Go source
  (`cmd/`, `internal/`, `pkg/`, `protos/`).
- **Google Docs talk** — Kurt has Google Docs of a talk he'll share; ingest as an additional
  facts/style source when provided (treat like the transcripts: distill per-topic).
- **Incoming defcon.run.34 audio (~1 hr, ~2026-07-08)** — Kurt will record another hour of
  defcon.run.34 conversation/detail tomorrow. This deepens defcon on BOTH axes and lands via the
  same transcribe→normalize→refresh path. **Not a planning dependency:** the refresh pipeline is
  re-runnable + manifest-driven (D-01/D-07), so the plan must treat the corpus as GROWING —
  build the machinery now, ship km-deep, and let later `refresh` runs fold in defcon depth +
  the Google Docs talk without re-planning. Design the manifest so adding a source = one edit.

**Retrieval-quality caveat (planner must handle):** raw code retrieves noisier for a *spoken*
answer than generated docs would have. Mitigate with (a) strict per-topic scoping, (b) the
curated pack framing the raw chunks so KPH narrates rather than reads code, (c) preferring
`README`/`docs`/`.md` chunks over source when both match. If evals show code-chunk answers read
badly aloud, revisit generated docs for defcon/meshtk as a fast-follow.

## Status

**Amendments 1–5 are the design of record.** The blocking input (Kurt's corpus) now EXISTS.
Plans 07-01..04 **predate Amendments 3–5** and must be regenerated to add: the retrieval
subsystem (FTS5 index build in `refresh_knowledge.py` — does not exist yet; a retrieval step
in/beside `KnowledgeRouterProcessor` injecting top-k into `system[1]`), **direct per-source
code indexing** (Amendment 5, replacing the doc-gen step), the **transcript-distilled style
layer** in the cached prefix (Amendment 4), the scrubber→advisory-lint demotion, and evals
measuring retrieval DEPTH/coverage. Staged prep on disk: `transcripts/` (+`normalized/`),
`diagrams/km-sandbox-aws.md`, and scratchpad `transcribe.py`/`normalize.py` (to be promoted
into the repo during execution). Next action: `/gsd-plan-phase 7`.
