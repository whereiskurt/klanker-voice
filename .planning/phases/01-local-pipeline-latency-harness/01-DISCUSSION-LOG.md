# Phase 1: Local Pipeline & Latency Harness - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-07-04
**Phase:** 1-Local Pipeline & Latency Harness
**Areas discussed:** Voice identity & vibe, Conversation behavior, Local dev experience, Harness output & verdicts

---

## Voice identity & vibe

| Option | Description | Selected |
|--------|-------------|----------|
| Warm, quick-witted female | "Brilliant conference host" energy | |
| Calm, dry male | "Cool bartender who knows your infra" | |
| Cheeky hacker energy | DEF CON-native, riskier | |
| Audition shortlist | 3-voice side-by-side during tuning, pick by ear | ✓ |

**User's choice:** Audition shortlist
**Notes:** Agent name via "Other": initials **KPH**, goes by **"K"** (pronounced "kay"). Pace: fast & punchy (recommended option).

---

## Conversation behavior

| Option | Description | Selected |
|--------|-------------|----------|
| K greets first | Short opener on connect | ✓ |
| Punchy, offer depth | 1–2 sentences with a "want more?" hook | ✓ |
| Roll with it, steer back | Answers off-topic gamely, steers back after a turn or two | ✓ |
| Playful with teeth | Witty, cheeky, gentle roasts if invited; no unprompted profanity | ✓ |

**User's choice:** All recommended options (greeting-first, punchy verbosity, roll-with-it off-topic, playful-with-teeth sass).

---

## Local dev experience

| Option | Description | Selected |
|--------|-------------|----------|
| Both run modes | Localhost web page (prod-parity WebRTC) + terminal mic mode | ✓ |
| TOML + env for secrets | pipeline.toml for knobs, .env for keys | ✓ |
| SSM→.env script | Reads /kmk/bootstrap/* via klanker-application profile | ✓ |

**User's choice:** All recommended options.

---

## Harness output & verdicts

| Option | Description | Selected |
|--------|-------------|----------|
| Console table + JSON artifact | Per-stage p50/p95 + diffable JSON per run | ✓ |
| docs/TUNING.md | A/B tables + reasoning live in the repo | ✓ |
| Informational now, gate later | ✅/⚠️ in Phase 1; CI gate at Phase 5 freeze | ✓ |

**User's choice:** All recommended options.

## Claude's Discretion

- Exact TOML schema, harness CLI shape, test-script content
- ElevenLabs audition shortlist (3 voices fitting fast-punchy brief)
- Barge-in test scenario design
- Repo layout details within the ARCHITECTURE.md monorepo shape

## Deferred Ideas

None — discussion stayed within phase scope.
