# Phase 7: KPH Knowledge Base - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-05
**Phase:** 7-KPH Knowledge Base
**Areas discussed:** Sources & boundaries, Steering behavior, Freshness & refresh, Depth retrieval shape

---

## Sources & boundaries

| Option | Description | Selected |
|--------|-------------|----------|
| Named projects + bio | klanker-maker, defcon.run, meshtk + one-time bio | |
| All public whereiskurt repos | Auto-discover every public repo | |
| Curated list I control | Checked-in manifest; generator reads only listed entries | ✓ |

| Option | Description | Selected |
|--------|-------------|----------|
| Hard public-only | Manifest public-only; pack world-readable by design; no infra/PII | ✓ |
| Private allowed w/ review | Private sources with human-reviewed digests | |
| Public + curated extras | Public rule + hand-written (still public) notes | |

| Option | Description | Selected |
|--------|-------------|----------|
| knowledge/ dir in this repo | Bio/scripts/notes versioned beside manifest + persona | ✓ |
| Repos-only, bio in persona | No extra materials | |
| Separate public knowledge repo | Dedicated kph-knowledge repo | |

**User's choices:** Curated manifest / Hard public-only / knowledge/ dir in this repo

---

## Steering behavior

| Option | Description | Selected |
|--------|-------------|----------|
| Answer-then-hook | Answer, end with one thread to pull | |
| Docent mode | Actively drives a tour itinerary | |
| Adaptive | Directed → answer-then-hook; browsers → tour mode | ✓ |

| Option | Description | Selected |
|--------|-------------|----------|
| Configurable per event | Manifest carries topic priority order | ✓ |
| Conference-first, fixed | defcon.run 34 always leads | |
| Flagship-first, fixed | klanker-maker always the headline | |

| Option | Description | Selected |
|--------|-------------|----------|
| Time-aware | Pace to remaining session time; pairs with Phase-4 wind-down | ✓ |
| Same pace regardless | Only Phase-4 warning is clock-aware | |

**User's choices:** Adaptive / Configurable per event / Time-aware

---

## Freshness & refresh

| Option | Description | Selected |
|--------|-------------|----------|
| Manual command | kv knowledge refresh / make knowledge, run deliberately | ✓ |
| Scheduled PR | Weekly Action regenerates + opens PR | |
| Auto on deploy | Regeneration rides every deploy | |

| Option | Description | Selected |
|--------|-------------|----------|
| LLM-digested | Claude writes voice-friendly digests; diff-reviewed | ✓ |
| Deterministic extraction | Scripted README/docs trimming | |
| Hand-written digests | User maintains digests by hand | |

**User's choices:** Manual command / LLM-digested

---

## Depth retrieval shape

| Option | Description | Selected |
|--------|-------------|----------|
| Pre-digested only | Hook digest + pre-baked long version per topic; zero tool calls | ✓ |
| Hybrid: pack + retrieval tool | Retrieval for depth; revises no-tool-calling decision | |
| Retrieval-first | Small pack, live fetch per deep question | |

| Option | Description | Selected |
|--------|-------------|----------|
| Honest + redirect | Admit the limit, point to repo/Kurt, steer to known topic | ✓ |
| Defer to Kurt every time | All unknowns route to the human | |
| Best-effort general knowledge | Haiku answers from training with caveat | |

**User's choices:** Pre-digested only / Honest + redirect

---

## Claude's Discretion

Manifest format, digest token budgets, pack assembly order, digest-writing model,
refresh-command home (kv vs make vs script), topic-map schema, session-time injection
mechanism.

## Deferred Ideas

- Live retrieval tool over full repo content (rejected for this phase; revisit only if
  the eval set proves pack coverage insufficient, alongside Phase-6 ack-masking)
- TTS text-normalization filter (01-05 follow-up candidate)
