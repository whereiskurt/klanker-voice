# Operator Admin Panel — Design Spec

**Status:** Approved (brainstormed 2026-07-06). Not yet planned/built.
**Sequencing:** Its own small GSD phase, to be entered **after** the Phase 5 voice
UI ships. Does not block the voice client. Candidate name: "Operator Admin Panel."
**Related:** `docs/superpowers/specs/2026-07-04-klanker-voice-design.md` (authoritative
project design); Phase 3 auth service; Phase 4 quota/usage; Phase 7 recorded-transcript
design (`.planning/phases/07-kph-knowledge-base/07-DESIGN-NOTES.md`).

## Problem

The operator (KPH) needs to (a) invite a small, trusted audience — his dad and a few
close friends, ≤25 users over time — into the public voice client, and (b) glance at
who's using it and how. Today, issuing invites and inspecting usage is possible only
from the `kv` CLI. There is no clickable surface to hand a code to a friend or to check
"how many sessions has my dad had this week."

## Scope decision (locked)

**Operational visibility now; transcripts later.** The panel exposes *operational* data
only — logins, sessions, minutes, quota trips, errors. It does **not** capture or display
conversation transcripts.

Rationale: transcripts are **not persisted anywhere today** — STT/LLM/TTS turns stream
through the Pipecat pipeline in real time and are discarded; the `kmv-voice-usage` tables
store only quota bookkeeping (heartbeat leases, session rollups). Reading conversations
would therefore be a new capture-and-store workstream with real privacy weight (a public
mic wired to "the operator saves everything you say"), and it overlaps the Phase 7
recorded-transcript design. Deferred there deliberately.

## Approach (locked)

**A `/admin` route group inside the existing auth app (`apps/auth/webapp`).** Rejected
alternatives: a CLI-only extension of `kv` (no glanceable page, terminal-only invites);
a standalone admin app (own deploy + own auth, overkill for one admin).

The auth app already runs at `auth.klankermaker.ai`, already deploys via existing infra,
already authenticates via magic link, and already has DynamoDB access to **both** relevant
tables (`kmv-auth-electro`/`kmv-auth-authjs` for identity/codes/tiers, `kmv-voice-usage`
for sessions). The panel is therefore mostly "a gated web view over data that already
exists," not new plumbing. `kv` remains the scriptable operator path; both front-ends run
the **same** DynamoDB queries and byte-compatible writes.

## Components

### 1. Gating

- New route group, e.g. `src/app/(admin)/admin/...`.
- Auth reuses the existing **magic-link login** — no new auth system, no new secret.
- Authorization = an **`ADMIN_EMAILS` allowlist** (env/SSM), seeded with
  `whereiskurt@gmail.com`. A layout/middleware check reads the next-auth session; if the
  session email is not in the allowlist, respond **404** (do not advertise the route's
  existence with a 403).
- **Admin login bootstrap (locked):** allowlisted admin emails may request a magic link
  **without** an access code. This is a small branch in the existing login route
  (`src/app/api/login/route.ts`), which today requires an `inviteCode`. Avoids the
  ceremony of minting yourself a code just to view the dashboard. (Alternative considered:
  keep an `admin`-tier code for yourself — lower code change, more ceremony. Not chosen.)

### 2. Read views (server components reading DynamoDB directly)

- **Users** — one row per person: email, tier, group, first seen, last seen, total
  sessions, total minutes. Sourced from the identity table joined with `kmv-voice-usage`
  rollups.
- **User detail → Sessions** — per session: start time, duration/minutes, tier at the
  time, quota trips, whether it was force-stopped by the service timer. **No transcripts.**
- **Codes** — the `kv code list` view as a table: code, tier, group,
  redemptions/cap, expiry.

### 3. Write actions (deliberately small)

- **Create code** — form (tier, group, max-redemptions, expiry) performing the same
  DynamoDB write as `kv code create`.
- **Expire code** — same write as `kv code expire` (soft-expire via `expiresAt`, preserves
  redemption history).
- **Kill-switch (locked: included in v1)** — show current state and expose a toggle,
  surfacing the operation `kv killswitch` already performs as the "big red button."
- **Out of scope for v1:** editing a user's tier, deleting users, revoking live sessions.
  Straightforward to add later; not needed for the first look.

### 4. Reuse vs. new

- **Reuses:** the auth app + its deployment, magic-link login, SES, and the existing
  ElectroDB entities (`AccessCode`, `Tier`, `Usage*`, identity/profile).
- **Adds:** one route group; a handful of read queries (identical in shape to what
  `kv users` / `kv sessions` would run); two write forms wrapping existing operations; the
  kill-switch toggle; and the `ADMIN_EMAILS` allowlist gate + the code-free admin-login
  branch.

## Data sources

| View | Table(s) | Notes |
|------|----------|-------|
| Users list | `kmv-auth-authjs` (identity) + `kmv-voice-usage` (rollups) | join per user |
| Sessions | `kmv-voice-usage` | session rollup + heartbeat/timer flags |
| Codes | `kmv-auth-electro` (`AccessCode` GSI1 `accesscodes#`) | same partition `kv code list` reads |
| Kill-switch | wherever `kv killswitch` reads/writes state | surface + toggle |

## Non-goals

- No conversation transcript capture, storage, or display (Phase 7).
- No new identity providers — email-only magic link stays (design decision D-09; Discord/
  GitHub/Strava OAuth remain dropped).
- No user self-service management; this is an operator-only surface.
- No new deployment target or auth system.

## Byte-compatibility discipline

Any write the panel performs (create/expire code, kill-switch) **must** produce the same
DynamoDB item/attribute shape as the corresponding `kv` command, so the Go CLI and the web
panel stay interchangeable — the same rule already enforced between `kv` and the webapp's
ElectroDB entities.

## Open items to resolve at plan time

- Exact `kmv-voice-usage` rollup query for per-user aggregates (reuse Phase 4 entities).
- Whether the sessions view needs pagination at 25 users (likely not; note it).
- Where kill-switch state lives and its exact toggle write (mirror `kv killswitch`).
