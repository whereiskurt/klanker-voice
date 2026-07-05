# Phase 7: KPH Knowledge Base - Context

**Gathered:** 2026-07-05
**Status:** Ready for planning

<domain>
## Phase Boundary

KPH answers with deep, current knowledge of Kurt's world — klanker-maker, defcon.run,
meshtk, and explicitly-listed public repos/scripts — via a curated, versioned, **cached
knowledge pack in the system prompt**, without breaking the voice-latency feel. This
phase delivers: the knowledge manifest + `knowledge/` content, an LLM-powered digest
generator behind a manual refresh command, persona steering rules (adaptive tour +
time-aware pacing), and a benchmark eval set proving KPH answers correctly.

NOT in this phase: live retrieval / tool-calling (explicitly rejected — see D-13),
latency work (Phase 6), voice cloning (captured todo), any client/deploy changes.

**Origin note:** This phase activates the conditional requirement **PIPE-10**
("RAG/knowledge retrieval — only if the persona outgrows prompt space") from
REQUIREMENTS.md, per the user's Phase-1 sign-off request. The resolution deliberately
STAYS "fat system prompt" — upgraded to a generated, cached knowledge pack — rather
than adopting retrieval. PROJECT.md's out-of-scope entries ("RAG/knowledge retrieval",
"Agent tool-calling") remain honored in letter: no retrieval infra, no tools.

</domain>

<decisions>
## Implementation Decisions

### Sources & boundaries
- **D-01:** Corpus is a **curated manifest file** checked into the repo. The digest
  generator reads ONLY manifest entries — no auto-discovery of public repos.
- **D-02:** **Hard public-only rule.** The manifest may only reference public sources;
  the generator must refuse private ones. The entire pack is treated as world-readable
  by design (public mic ⇒ assume full prompt extraction): no infra details, no account
  IDs/hostnames/key names, no PII beyond Kurt's self-written bio.
- **D-03:** Non-repo materials (bio, scripts, extra color) live in a **`knowledge/`
  directory in the klanker-voice repo** (public), versioned alongside the manifest —
  same pattern as the versioned persona markdown.

### Steering behavior
- **D-04:** **Adaptive steering:** directed questions get answer-then-hook (extends the
  existing "want the long version?" pattern); aimless/quiet visitors get tour mode
  ("want the 60-second tour of what Kurt builds?"). Steering rules live in the persona;
  the topic map lives in the knowledge pack.
- **D-05:** Tour itinerary is **configurable per event**: the manifest carries a topic
  priority order (one-line edit re-pitches KPH — e.g. defcon.run 34 first at DEF CON).
- **D-06:** **Time-aware pacing:** KPH paces to remaining session time — a 2-min demo
  tier gets tight highlights + a closing pointer; longer tiers get depth. Designed to
  pair with the Phase-4 spoken wind-down. (How session-time reaches the LLM context is
  planner territory; the harness/pipeline already tracks session state.)

### Freshness & refresh
- **D-07:** Digest regeneration is a **manual command** (`kv knowledge refresh` or
  `make knowledge` — planner picks the home). Run deliberately (pre-conference, after
  big pushes); no CI/schedule surprises.
- **D-08:** Digests are **LLM-written, voice-friendly** (facts, stories, hooks — tuned
  for being SPOKEN, not read; not README recitation). Cost is cents per refresh.
- **D-09:** **Git diff is the review gate** — regenerated digests are inspected as an
  ordinary diff before commit; nothing changes what KPH says without a human seeing it.

### Depth retrieval shape
- **D-10:** **Pre-digested only.** Each topic gets BOTH a hook-sized digest AND a
  pre-baked "long version" in the pack. Depth = bigger pack, not lookups.
- **D-11:** **No live retrieval, no tool-calling.** The original project decision
  ("round-trip pauses undercut the slick feel") stands unrevised.
- **D-12:** **Unknowns: honest + redirect.** Beyond-the-pack questions get "that's
  deeper than what I've got loaded — the repo has it, or grab Kurt", then steer to an
  adjacent known topic. Never bluff about Kurt's projects on a public mic.

### Caching (design-critical, from 01-04 findings)
- **D-13:** The pack deliberately CROSSES Haiku's 4096-token minimum cacheable prefix,
  flipping prompt caching from ruled-out (persona-only era, docs/TUNING.md) to a win:
  cache reads ≈0.1× cost with fast prefill. Plan for a `cache_control` breakpoint on
  the stable pack and consider session-start pre-warm. Verify with
  `usage.cache_read_input_tokens > 0` (roadmap success criterion 1).

### Claude's Discretion
- Manifest format, digest token budgets per source, pack assembly order, which model
  writes digests, refresh-command implementation home (kv vs make vs script), topic-map
  schema, how session-remaining-time is injected — all planner/implementation territory.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Project & requirements
- `docs/superpowers/specs/2026-07-04-klanker-voice-design.md` — approved system design (persona scope, latency budget, no-tool-calling rationale)
- `.planning/REQUIREMENTS.md` — PIPE-10 (this phase's activating requirement); PIPE-08 (ack-masking, Phase 6 — NOT this phase)
- `.planning/PROJECT.md` — out-of-scope list this phase deliberately works within

### Latency & caching ground truth
- `docs/TUNING.md` — accepted latency baseline (~1402ms p50), RE-ESCALATION record, and the prompt-caching ruled-out-at-600-tokens note that D-13 flips
- `.planning/phases/01-local-pipeline-latency-harness/01-04-SUMMARY.md` — measured TTFT behavior, caching facts (Haiku 4096-token cache minimum)

### Artifacts this phase extends
- `apps/voice/prompts/concierge.md` — persona v3 (KPH self-reference, TTS-safe DEFCON rule); steering rules land here
- `apps/voice/pipeline.toml` — config consumed unchanged by the Phase-4 deployed service; knowledge-pack path likely joins `[persona]`
- `apps/voice/tests/` + eval scenarios — pattern for the benchmark eval set (criterion 4)

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- Versioned-markdown persona pattern (`apps/voice/prompts/concierge.md`) — the knowledge pack follows the same artifact pattern: checked-in, versioned, consumed by the pipeline at load time
- Eval scenario harness (5 named scenarios, judge) — extend for the knowledge benchmark set
- `apps/voice/scripts/audition.py` — precedent for one-shot generator scripts under `apps/voice/scripts/`

### Established Patterns
- `pipeline.toml` is the single config surface; secrets never in TOML; prod consumes these artifacts unchanged (Phase 4)
- Latency harness measures every change — knowledge-pack TTFT impact must be measured against the accepted 1402ms baseline, not assumed

### Integration Points
- System-prompt assembly in `apps/voice/src/klanker_voice/pipeline.py` (persona load) — pack concatenates here with a `cache_control` breakpoint
- Phase 6 (Latency v2) complements but does not block: ack-masking is not needed for this phase since there are no lookups

</code_context>

<specifics>
## Specific Ideas

- User's founding ask (Phase-1 sign-off, verbatim): "massive RAG or something really
  smart that can kind of steer... I'd want KPH to always refer to themselves as KPH,
  and have all of the knowledge of my repos.. and some scripts and stuff I'd train it on."
  Resolved as: fat cached knowledge pack + adaptive steering (not retrieval, not fine-tuning).
- Tour phrasing sketch: "want the 60-second tour of what Kurt builds?"
- Unknown-handling phrasing sketch: "that's deeper than what I've got loaded — the repo
  has it, or grab Kurt if he's around."
- Named topics KPH already name-drops (greeting line): klanker-maker, defcon.run 34,
  meshtk (spoken "mesh T K — the meshtastic toolkit").

</specifics>

<deferred>
## Deferred Ideas

- **Live retrieval tool over full repo content** — explicitly rejected for this phase
  (D-10/D-11); revisit only if the eval set proves the pack can't cover real questions,
  and then alongside Phase-6 ack-masking.
- **Voice clone (Kurt-trained ElevenLabs voice)** — already a captured todo in STATE.md;
  unrelated to knowledge.
- **TTS text-normalization filter** — 01-05 follow-up candidate if pronunciation-sensitive
  tokens accumulate as knowledge grows (persona rule suffices today).

</deferred>

---

*Phase: 7-KPH Knowledge Base*
*Context gathered: 2026-07-05*
