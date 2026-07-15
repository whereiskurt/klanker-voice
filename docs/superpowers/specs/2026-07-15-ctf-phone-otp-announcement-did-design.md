# CTF Phone-OTP Announcement DID — Design

**Date:** 2026-07-15
**Status:** Approved (brainstorm) — ready for implementation planning
**Scope repo:** klanker-voice (this repo). meshtk / CTF-award side is a separate spec.

## Summary

A specific inbound DID becomes an **announcement line**: when someone calls it,
the voice edge answers, speaks a short line containing a **live one-time
password**, and hangs up. It does **not** run the concierge speech-to-speech
pipeline and does **not** run the §24 answer-gate.

The mechanic is a **CTF proof-of-phone-call**:

1. A MeshTastic bot DMs a player: *"Call me at 347-…"*.
2. The player calls the announcement DID and hears the current OTP —
   *"Hey! Let me get you that OTP — 1 2 3 4 5 6 — that's 1 2 3 4 5 6. Bye bye."*
3. The player DMs the code back to the bot.
4. The bot verifies the code and awards the flag to that player, attributed by
   **radio id / private-msg-id**, server-side.

The phone can only observe the caller's **phone number (ANI)**, never their
MeshTastic radio id — so **award attribution must live in meshtk**, not on the
phone side. The two halves are joined only by a **shared TOTP secret** and
agreed TOTP parameters.

## Flow

```
MeshTastic bot ──"call 347-…"──▶ player dials announcement DID
                                        │
                          [voice edge / controller.py]
                          answer → skip §24 gate →
                          GET current TOTP from auth /ctf/otp →
                          TTS "…that OTP — 1 2 3 4 5 6 —
                          that's 1 2 3 4 5 6. Bye bye." →
                          play over RTP → HANG UP
                                        │
player DMs code ──▶ MeshTastic bot verifies TOTP with shared secret
                    ──▶ awards flag by radio-id / msg-id   (meshtk, out of scope)
```

## Scope

### In scope (this repo)

1. **auth app — `GET /ctf/otp`** — an internal-only endpoint that returns the
   current TOTP code.
2. **telephony edge — announcement-DID branch** in `controller.py`: a new
   call path that answers, fetches the OTP, speaks it, and hangs up.

### Out of scope (separate spec, meshtk repo)

- Receiving the code from a radio, verifying it, and awarding the flag by
  radio id / private-msg-id. meshtk verifies the TOTP **independently** using
  the same shared secret — no callback to auth. This repo defines the shared
  TOTP contract (below); it does **not** build a verify endpoint.

## Piece 1 — auth `GET /ctf/otp`

Mirrors the existing `apps/auth/webapp/src/app/tel/[e164]/route.ts` almost
verbatim:

- **Internal-only.** Same deploy-time network lock as `/tel`. Only the voice
  edge calls it. A random internet client cannot scrape codes — you must
  actually place the phone call, which is the CTF proof.
- **Optional shared-bearer** defense-in-depth via a **new** env var
  `CTF_OTP_AUTH_TOKEN` (SSM-backed; clean secret separation from the telephony
  mint's `TELEPHONY_ENDPOINT_AUTH_TOKEN`). A missing/wrong bearer returns the
  **same** uniform failure as any other error — never a distinct 401/403
  (no oracle).
- **Uniform 404** on every failure mode, exactly like `/tel`.

Unlike `/tel`, it does **not** mint a JWT. It computes the current TOTP from
`CTF_OTP_SECRET` (SSM, **never** in TOML) and returns:

```json
{ "code": "123456", "digits": 6, "period": 120, "expiresIn": 47 }
```

- `code` — the current TOTP, zero-padded to `digits`.
- `expiresIn` — seconds until the current period ends (informational; the
  edge does not need it, but it aids debugging).
- `cache-control: no-store`.

TOTP parameters are fixed by the shared contract (below); the endpoint uses
those constants, not request input.

## Piece 2 — announcement-DID branch (`controller.py`)

### Config

A new config surface designates which DID(s) are announcement DIDs and how they
behave. Behavior-only fields live in `[telephony]` TOML (per the existing
config discipline — **no secrets in TOML**). Proposed shape (keyed by DID so it
extends to more than one announcement line later):

```toml
[[telephony.announcement]]
did = "7254043234"                       # Las Vegas, NV DID; matches the normalized dialplan exten
otp_url = "https://auth.klankermaker.ai/use1/ctf/otp"
otp_env_var = "CTF_OTP_AUTH_TOKEN"       # NAME only; value from env/SSM
line_template = "Hey! Let me get you that O T P. {code}. That's {code}. Buh bye."
```

> **Config-key note (implementation deviation):** the bearer-env-var field is named
> `otp_env_var`, **not** `otp_auth_env_var`. `config.py`'s `_CREDENTIAL_FIELD_RE`
> rejects any TOML key containing `_auth_`, so `otp_auth_env_var` would be refused
> by the shared credential gate before parsing — even though it only ever holds an
> env-var *name*. `otp_env_var` mirrors the working `tel_mint_env_var` precedent.
> The value it holds is still `CTF_OTP_AUTH_TOKEN`.

- `did` — compared against the normalized dialplan `exten` in `on_stasis_start`.
- `otp_url` — non-secret plain URL, like `tel_mint_url`.
- `otp_env_var` — the **name** of the env var holding the bearer token;
  the value is read from env/SSM at call time, never stored in TOML (mirrors
  `tel_mint_env_var`).
- `line_template` — spoken text with a `{code}` placeholder. The code is
  rendered **digit-spaced** ("1 2 3 4 5 6") so TTS reads it one digit at a
  time, and the template speaks it **twice** for clarity.

`TelephonyConfig` gains an `announcements` field (a tuple of frozen
announcement entries). Absent table → empty tuple → behavior byte-unchanged.

### Control flow

In `on_stasis_start`, after DID normalization and before the §24 gate:

- If the normalized DID matches an announcement entry → route to a new
  `_run_announcement(...)` path and **return** — skipping the gate, mint, STT,
  LLM, and TTS-conversation pipeline entirely.
- Otherwise → the existing gated speech-to-speech path, byte-unchanged.

`_run_announcement`:

1. Answer the channel.
2. `GET otp_url` with the optional bearer. On any failure (non-200, network
   error, malformed body) → hang up immediately (v1 has no spoken error line);
   log the failure by category only, never leaking the code or endpoint detail.
3. Build the line: substitute the digit-spaced code into `line_template`
   (twice, per the template).
4. Synthesize via the **existing ElevenLabs TTS service** → PCMU (8 kHz), and
   play it over the existing RTP playback path (the same machinery
   `pickup_cue.py` / greeting playback already uses).
5. Hang up when playback completes.

No STT, no LLM, no gate — public and one-directional.

## Cross-repo TOTP contract (shared with meshtk)

| Parameter | Value |
|-----------|-------|
| Algorithm | HMAC-SHA1 |
| Digits    | 6 |
| Period    | **120 s** |
| Skew      | **±1 step** (accept the previous, current, and next step) |
| Secret    | `CTF_OTP_SECRET` (SSM SecureString; base32) |

Effective validity for a caller is therefore ~2–4 minutes — enough for the
hear→relay round-trip. meshtk verifies with the **same secret and the same
parameters**, independently. No auth callback.

## Security & cost notes

- **No-oracle posture** on `/ctf/otp` is inherited from `/tel`: uniform 404,
  bearer failure indistinguishable from any other failure, success-only logging
  by nothing caller-identifying.
- The OTP is *intended* to be spoken to any caller; its protection is that the
  HTTP endpoint is internal-only, so possession requires an actual phone call.
- **Cost:** each call synthesizes one short TTS line. Optional future
  optimization (not v1): cache the synthesized audio keyed by `(code,
  template)` for the TOTP period so repeated calls in the same window reuse the
  clip. Left out of v1 as YAGNI.
- The announcement DID deliberately bypasses `require_gate`. This is scoped to
  DIDs explicitly listed in `[[telephony.announcement]]` — no other DID's gate
  posture changes.

## Testing

- **auth:** `/ctf/otp` returns the correct TOTP for a known secret + fixed
  clock; `expiresIn` is within `[1, period]`; bearer enforced when
  `CTF_OTP_AUTH_TOKEN` is set; uniform 404 on bad bearer and on internal error.
- **telephony:** DID-match selects the announcement branch; the §24 gate is
  **not** entered for an announcement DID; the line-builder spaces the digits
  and substitutes twice; hangup follows playback. OTP fetch and TTS are mocked.
- **manual integration:** call the DID on the local Asterisk edge; confirm the
  spoken code equals `/ctf/otp`'s response and an independently computed TOTP
  for the same secret + clock.

## Settled decisions

1. **Bearer token:** new `CTF_OTP_AUTH_TOKEN` (not reusing the telephony mint
   token).
2. **TOTP period:** 120 s, ±1 step skew.
3. **No `/ctf/verify` in this repo** — meshtk verifies with the shared secret.
   Revisit only if meshtk later prefers a verify oracle.
