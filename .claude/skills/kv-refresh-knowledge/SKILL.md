---
name: kv-refresh-knowledge
description: Regenerate KPH's per-topic knowledge packs from the curated manifest (repos + optional transcripts) for the klanker-voice concierge — the Phase 07 knowledge-refresh workflow. Use when the user wants to refresh, rebuild, or regenerate KPH's knowledge, update the concierge's facts about km/defcon.run.34/meshtk/tiogo/kvmlab, ingest new transcripts into the style layer, or invokes /kv-refresh-knowledge. Wraps apps/voice/scripts/refresh_knowledge.py via `make -C apps/voice knowledge` / `kv knowledge refresh`, enforces the do-not-say scrubber and the ≥4096-token cache threshold, and gates on a human git-diff review before commit (D-09).
---

# Refresh KPH Knowledge Packs

Regenerate the versioned per-topic knowledge packs that KPH (the klanker-voice concierge) speaks from. The refresh is a **script run, not a manual edit** (Phase 07 / D-07): it reads a curated manifest, distills each public source into a hook + long-version pack, applies the do-not-say scrubber, checks the cache-token threshold, and writes into the tracked `apps/voice/knowledge/` tree for review.

## 0. Preflight — is Phase 07 shipped?

This workflow depends on Phase 07 (KPH Knowledge Base) having been executed. Check first:

```bash
ls apps/voice/scripts/refresh_knowledge.py apps/voice/knowledge/manifest.yaml 2>/dev/null
```

**If either path is missing**, Phase 07 has not been executed yet. Stop and tell the user:

> KPH's knowledge system doesn't exist in the repo yet — Phase 07 (KPH Knowledge Base) hasn't been executed. Run `/gsd-execute-phase 7` first, then this refresh will work. (Plans live in `.planning/phases/07-kph-knowledge-base/`.)

Do not attempt to create the script or packs by hand — that is Phase 07's job.

## 1. Confirm sources (manifest + optional transcripts)

`apps/voice/knowledge/manifest.yaml` is the **only** source list — repos, public flags, priority order, and an optional transcript slot. Read it before running:

```bash
cat apps/voice/knowledge/manifest.yaml
```

- If the user is adding **Kurt's transcripts** (the diagram-walkthrough recordings that ground the *style* layer, Amendment 2), confirm the manifest's transcript slot points at the transcript file(s) before running. Transcripts are optional — without them the style layer regenerates from persona + corpus tone; with them it regenerates richer.
- Do not add private/non-public sources. The corpus is public-only (D-02); the scrubber is a backstop, not a license to include secrets.

## 2. Run the refresh

The script needs API keys (Anthropic, same-vendor distillation). Prefer the operator command:

```bash
make -C apps/voice knowledge
```

Equivalent thin dispatcher: `kv knowledge refresh`. Direct: `cd apps/voice && python scripts/refresh_knowledge.py`.

The script runs survey → distill (hook + long version, voice-friendly) → style pass → **`scrub.scan()` on every output (refuses on any landmine finding)** → token check (stable prefix crosses 4096 so prompt-caching engages; each deep pack within budget) → writes packs into `apps/voice/knowledge/`.

**If it exits complaining about `.env` / missing keys:** run `make -C apps/voice env` (per its own guidance) and retry.

**If the scrubber refuses output** (a landmine — private key, AWS account id, SOPS/ARN/internal-secret/hostname marker, node key, coordinates): it names the offending source and finding. Fix the source or manifest so the flagged content is excluded, then rerun. **Never bypass or stub the scrubber to force output through** — it is the phase's real secret-leakage control.

## 3. Review before commit (D-09 git-diff gate)

Packs are tracked and human-reviewable by design. **Do not auto-commit.** Surface the diff:

```bash
git status --short apps/voice/knowledge/
git diff -- apps/voice/knowledge/
```

Verify with the user:
- The regenerated packs read correctly and stay voice-friendly.
- No do-not-say content slipped in (the scrubber passed, but a human skim on the diff is the D-09 gate).
- The `manifest.yaml` version bumped if the sources changed.

Commit **only after the user confirms** the diff. Suggested message:

```
chore(voice): refresh KPH knowledge packs
```

## Notes

- Raw corpus digests staged during Phase 07 planning live in `.planning/phases/07-kph-knowledge-base/corpus/*.md` — historical input, not the live packs. The live packs are under `apps/voice/knowledge/`.
- Launch topic set is km / defcon.run.34 / meshtk (primary); tiogo / kvmlab are additive manifest entries. Adding or removing a topic is a `manifest.yaml` + `router/topic-map.yaml` edit, then a refresh.
