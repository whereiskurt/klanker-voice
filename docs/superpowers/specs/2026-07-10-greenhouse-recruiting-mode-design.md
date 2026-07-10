# Design: "greenhouse" — KPH recruiting-mode easter egg

**Date:** 2026-07-10
**Status:** Design + scaffold complete; content + wiring gated on Kurt's input (NOT deployed)
**Origin:** backlog idea "knowledge-bound access keys" — realized as a *spoken-keyword* trigger, not an access code.

## Goal

When a visitor says the magic word **"greenhouse"** (recruiting-ATS pun; Tenable —
whom Kurt built `tiogo` for — recruits on Greenhouse), KPH slips into **recruiting
mode**: it represents Kurt as a candidate and talks up his résumé, experience, and
skills — confident and pitch-y, but strictly honest. Hidden until triggered; never
advertised; never in the tour.

## Approach (chosen): a hidden keyword-topic that reuses the Phase-7 router

No new subsystem. `greenhouse` is a normal topic in the existing router stack, with
two properties:

1. **Hidden** (`topic-map.yaml` `hidden: true`): excluded from the block0 knowledge-map
   hooks (`render_topic_hooks` skips it) and from the Haiku fallback candidate set
   (`default_haiku_fallback_classify` filters it). Reachable ONLY by an explicit
   keyword match in `classify`. Not in `manifest.tour_priority`.
2. **Persona shift in the pack:** `block1` is system context, so `topics/greenhouse.md`
   opens with a "Recruiting mode" behavioral block (pivot to candidate advocacy;
   honest; keep KPH's voice) followed by the résumé content. On the next topic switch
   the pack swaps out and normal KPH resumes.

**Trigger flow:** visitor says "greenhouse" → `classify` scores 3 ≥ floor 2 →
`KnowledgeRouterProcessor` fires the dig-in ack (spoken_name "Kurt's background") and
swaps `block1` to `greenhouse.md` → KPH is in recruiting mode.

### Alternatives considered
- **Access-code gate** (original backlog form): heavier (auth/tier plumbing) and not
  what the user asked; keyword is lighter and reuses the router. Rejected for v1.
- **Session-sticky persona mode** (stays in recruiting mode across topic switches):
  more plumbing (a session-level flag). Deferred — topic-scoped is the natural router
  behavior and matches "whenever they say greenhouse."

## Components / changes

- **`knowledge/topics/greenhouse.md`** (new) — the pack: recruiting-mode behavior +
  who-Kurt-is + technical profile + portfolio (all seeded from PUBLIC GitHub) +
  `<<FILL FROM RESUME/LINKEDIN>>` employment/education sections + sample recruiter Q→A
  + do-not-say boundary.
- **`knowledge/router/topic-map.yaml`** (+entry) — `greenhouse`, `hidden: true`,
  keyword `greenhouse` (w3) + a few high-intent recruiting phrases (prunable).
- **`knowledge/manifest.yaml`** (+entry) — hand-authored pack (like klanker-voice),
  NOT in tour_priority; source = `corpus/kurt-resume.md` (paste target).
- **`knowledge/corpus/kurt-resume.md`** (new) — private paste target for résumé/LinkedIn.
- **`src/klanker_voice/knowledge/prompt_assembly.py`** — `render_topic_hooks` skips
  `hidden` topics.
- **`src/klanker_voice/knowledge/router.py`** — `default_haiku_fallback_classify`
  filters out `hidden` topics.
- **Tests** — hidden topic: skipped in hooks, excluded from fallback, still keyword-matched.

## Honesty / safety

- D-12 honest-unknowns is reinforced in the pack: KPH NEVER invents an employer, title,
  date, degree, cert, or metric — absent a fact, it says so and pivots to demonstrated work.
- Public-safe scrub: no comp, no personal contact beyond public LinkedIn (`in/kurthundeck`),
  no fabrication, no disparagement. The pack is hand-authored (never auto-generated from
  raw résumé) so PII can't leak into system[1].

## Not-deployed gate

The topic is wired but the pack has placeholders. Do NOT merge/deploy until the
`<<FILL...>>` sections are completed from Kurt's résumé/LinkedIn (else "greenhouse" in
prod loads a half-empty pitch).

## What we need from Kurt

1. **Résumé(s)** — paste into `corpus/kurt-resume.md`.
2. **Comprehensive LinkedIn** — About, Experience (companies/titles/dates/scope),
   Education, Skills, notable posts.
3. **Targeting** — role/level, stack/domain, remote/location, IC vs lead, problems he
   wants to work on.
4. **Tone dial** — earnest-witty (default) / playful / hard-sell.
5. **Any extra do-not-say** beyond the defaults.
6. **Keyword scope** — keep the secondary recruiting keywords, or make it strictly
   "greenhouse"-only?
7. **Sticky mode?** — should recruiting mode persist across topic switches for the rest
   of the session, or stay topic-scoped (default)?
