---
quick_id: 260714-0wr
title: Redesign /admin/transcripts as a readable, searchable operator console
status: complete
date: 2026-07-14
commit: a5917b7
---

# Summary — Quick Task 260714-0wr

Replaced the bare, unstyled Phase-15 gate-shell transcripts pages with a
readable dark "operator console" — the deliverable behind "the transcripts
page is pretty gross… make it better, easy to read through, searchable."

## Changes (commit `a5917b7`)

| File | Change |
|------|--------|
| `admin/layout.tsx` | Scoped inline design system (dark, MuseoModerno/Fira Code/Inter via the app's own next/font) + real shell header (brand mark, breadcrumb, signed-in-as). Gate logic unchanged. |
| `admin/transcripts/page.tsx` | Hero + day nav (prev/next + date) + **cross-turn search** (server-side; filters sessions by participant OR any spoken turn, highlights the hit) + session cards with **web/PSTN channel badges**, turn count, time span, match snippet. |
| `admin/transcripts/[sessionId]/page.tsx` | Threaded conversation: role avatars, KPH/Caller labels, timestamps, interruption markers; carried `?q` highlights in place. |
| `admin/**/__tests__/*` | Updated for new markup (bubble classes + role labels; new empty-state copy); added channel-badge + cross-turn-search tests; gate test stubs `@/config/fonts` (next/font can't run under vitest). |

Design decisions honored: lightweight but clearly better; readable + searchable
"modern webby" feel; **no recording-notice in the admin view** (operator: everyone
signed off); stored-XSS guard preserved (text renders only as escaped React
children / `<mark>` string segments, never innerHTML).

## Verification

- `tsc --noEmit`: clean (0 errors in admin/; 1 pre-existing unrelated).
- `npm run build` (== CI Docker `next build`): **exit 0**; both routes compile
  as dynamic (`ƒ /admin/transcripts`, `ƒ /admin/transcripts/[sessionId]`);
  ESLint (run by next build) passed.
- vitest not run locally (config-load ESM quirk in this env); tests are not a
  CI gate for auth (CI = next build only). Assertions hand-matched to new markup.

## Deploy

Auth-app-only change (no IAM) → normal clean CI path: merge to main →
build-auth (docker `next build`) → deploy.yml applies ecs-task/ecs-service with
the new image. None of the telephony-fix infra friction applies here.
