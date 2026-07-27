# Quick Task 260727-pdh: Second phone game on 725-404-8283 - Context

**Gathered:** 2026-07-27
**Status:** Ready for planning

<domain>
## Task Boundary

Set up 725-404-8283 as its own phone game, distinct from the existing OTP game
(code 333266 on 3234/3283), using the per-DID `dids` scoping shipped in quick
task 260727-ohq. Adds a NEW capability: a per-game spoken-passphrase trigger
(either the numeric DTMF code OR the spoken words fire the game's script), and
operator visibility for game entries in `kv telephony list` and `kv studio`.

</domain>

<decisions>
## Implementation Decisions (operator-confirmed via AskUserQuestion, 2026-07-27)

### 8283 gate behavior
- Game-only, like 3234/3283: add `"7254048283"` to `otp_only_dids` in
  telephony.toml. Concierge passphrase + PIN suppressed on this DID; silent
  until a game factor lands, then the existing fail-closed timer applies.

### Existing 333266 game scoping (AMENDED by operator 2026-07-27)
- 3234 and 3283 SPLIT into separate per-DID entries:
  - Existing entry: `dids = ["7254043234"]` ONLY, keeps
    `code_env_var = "CTF_ANNOUNCEMENT_CODE"` (SSM value 333266, unchanged),
    `sms_reply_dids = ["7254043234"]`.
  - NEW entry for 3283: `dids = ["7254043283"]`,
    `code_env_var = "CTF_ANNOUNCEMENT_CODE_3283"` (SSM param
    `/kmv/secrets/use1/ctf/announcement_code_3283`, operator-seeded value
    1337 — value NEVER in TOML/code), SAME line_template as the 3234 entry
    (same OTP script, different access code), same otp_url/sms_relay_url,
    `sms_reply_dids = ["7254043283"]`, no passphrase_env_var.
- Net effect: 333266 works only when dialing 3234; 1337 only when dialing
  3283; neither works on 8283 or 613.
- service.hcl also wires `CTF_ANNOUNCEMENT_CODE_3283` (third new valueFrom
  secret alongside the two UCTF ones).
- The orchestrator seeds the 3283 SSM param live during this task (operator
  authorized); the two UCTF params remain an operator pre-deploy step.

### Spoken trigger (NEW feature)
- General per-entry field, either-factor semantics: new OPTIONAL
  `passphrase_env_var` on `AnnouncementEntry` (env var NAME only — the words
  VALUE lives in env/SSM, never TOML, per D-09; mirrors `code_env_var`
  exactly). An entry with the env var unset/empty simply has no spoken
  trigger (like code_env_var today).
- EITHER the DTMF code OR the spoken words fire the same script for that
  entry. Same fail-closed per-DID scoping via `_announcement_matches_did`.
- Word matching mirrors the concierge gate's `match_passphrase` semantics
  (lowercased token-set containment, gate.py) — reuse, don't reinvent.
- The gate must accumulate spoken tokens even when `concierge_unlock_enabled`
  is False (today accumulation is skipped on otp_only DIDs — gate.py ~line
  358). Announcement-word matching needs a seam from the GateProcessor
  (pipeline side, sees TranscriptionFrames) to the controller's announcement
  dispatch (ARI side, currently DTMF-only) — e.g. a callback injected into
  GateProcessor, mirroring how DTMF codes reach the dispatch loop.
- D-05e redaction contract still holds: never log heard words, matched words,
  or code values on the success path; pre-unlock transcripts never reach
  LLM/ledger/logs. The existing gate_fail_heard opt-in debug path is
  unchanged.
- Wire the words for the 8283 game first (env var name
  `CTF_ANNOUNCEMENT_WORDS_UCTF`); the existing 333266 entry gets NO
  passphrase_env_var (numeric-only, unchanged behavior).

### New 8283 game entry (telephony.toml)
- Second `[[telephony.announcement]]` block:
  `dids = ["7254048283"]`, `code_env_var = "CTF_ANNOUNCEMENT_CODE_UCTF"`,
  `passphrase_env_var = "CTF_ANNOUNCEMENT_WORDS_UCTF"`,
  `sms_reply_dids = ["7254048283"]`, `sms_relay_url` same as the existing
  entry, `otp_url` same as existing (operator will point it elsewhere later
  if the new game needs a different OTP source).
- `line_template`: SAME OTP gag template as the 3234/3283 entries (AMENDED
  by operator 2026-07-27 — "same OTP gag for now", no placeholder; a
  different script for 8283 can come later as a TOML-only edit).
- SSM: `/kmv/secrets/use1/ctf/announcement_code_uctf` = 696969 SEEDED LIVE
  by the orchestrator (readback-verified 2026-07-27). The words param
  `/kmv/secrets/use1/ctf/announcement_words_uctf` remains UNSEEDED —
  operator hasn't picked the words yet.
- Boot-time safety: an entry whose code env var is unset is dropped from the
  dispatch dict (existing behavior). The same graceful-skip must apply when
  passphrase_env_var is unset/unresolvable: the entry stays live via its
  code, just with no spoken trigger — this is exactly the 8283 launch state
  (code seeded, words not).
- service.hcl caution: since the words param is unseeded, its valueFrom
  wiring must NOT go live before the param exists (ECS fails task launch on
  a missing valueFrom). Either seed a sentinel/empty-behaving value as part
  of this task's operator notes, or gate the words wiring accordingly —
  document the chosen approach in the SUMMARY as a pre-deploy step.

### Infra wiring
- Add the two new env vars (`CTF_ANNOUNCEMENT_CODE_UCTF`,
  `CTF_ANNOUNCEMENT_WORDS_UCTF`) as SSM valueFrom secrets in telephony-edge's
  service.hcl, pointing at `/kmv/secrets/use1/ctf/announcement_code_uctf` and
  `/kmv/secrets/use1/ctf/announcement_words_uctf`.
- CAUTION: ECS fails task launch on a missing valueFrom param — so the SSM
  params MUST be seeded before this deploys. Document this ordering in the
  SUMMARY as an explicit operator pre-deploy step (seeding the actual secret
  values is OUT of scope for this task; operator picks the code digits and
  words).

### UI surface (both)
- `kv telephony list`: add a game-entries section — one row per
  `[[telephony.announcement]]` entry: dids scope (or "global"), code env var
  name + set/unset status, passphrase env var name + set/unset status,
  sms_reply_dids. NEVER print secret values — env var NAMES and set/unset
  only (matches how the existing gate-secret status display works).
- `kv studio`: a games/announcements panel showing the same data, following
  studio's existing degradation pattern (missing data never blocks the rest
  of the console).
- Both read the entries from apps/voice/configs/telephony.toml (git-backed
  source of truth). Follow however `kv telephony list` already sources its
  gate-config view of that file.

### Claude's Discretion
- Exact seam shape between GateProcessor and the controller for spoken
  announcement matching (callback vs. queue) — pick what mirrors existing
  patterns best.
- Go-side TOML parsing approach for the announcement entries (reuse whatever
  kv already uses to read telephony.toml, if anything).
- Studio panel layout details.

</decisions>

<specifics>
## Specific Ideas

- "Hack the planet" was cited as the EXAMPLE of a spoken trigger (it's the
  concierge passphrase style) — the actual game words are operator secrets,
  not decided here.
- One-block-per-game layout is the goal: DID(s) + code + words + script +
  SMS reply, all in one TOML block per game.

</specifics>

<canonical_refs>
## Canonical References

- .planning/quick/260727-ohq-add-per-did-scoping-to-telephony-announc/ (the
  `dids` scoping this builds on — PLAN + SUMMARY)
- apps/voice/src/klanker_voice/telephony/gate.py (match_passphrase, D-05e
  redaction contract, concierge_unlock_enabled guard)
- apps/voice/src/klanker_voice/telephony/controller.py (announcement dispatch
  `_announcements_by_code` ~line 826/1557, `_announcement_matches_did`)
- apps/voice/configs/telephony.toml (live game + cid_prefix + otp_only_dids)
- kv/internal/app/cmd/telephony.go (kv telephony list — existing display)
- kv/internal/app/studio/ (studio server + panels)
- infra/ telephony-edge service.hcl (SSM valueFrom secrets block)

</canonical_refs>
