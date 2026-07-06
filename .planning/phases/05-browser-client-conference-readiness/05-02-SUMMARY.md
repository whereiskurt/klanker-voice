---
phase: 05-browser-client-conference-readiness
plan: 02
subsystem: ui
tags: [vite, react, typescript, webgl2, glsl, canvas2d, pipecat, docker, orb, immersive]

# Dependency graph
requires:
  - phase: 05-01
    provides: "server.py StaticFiles(client/dist, html=True) SPA mount + deep-link fallback; the CLIENT_DIST_DIR the built client is served from"
provides:
  - "Bespoke Vite+TS+React SPA at apps/voice/client/ built to dist/ (base '/', outDir 'dist') served by the voice FastAPI StaticFiles mount"
  - "UI-SPEC design system encoded as CSS custom properties (src/styles/tokens.css): 60/30/10 color palette, 4-based spacing, 4-size/2-weight type, motion tokens"
  - "Hero orb component <OrbCanvas state amplitude /> — WebGL2 shader plasma + orbiting particle ring (sketch 001 winner A) with mandatory 2D/reduced-motion fallback"
  - "orbState.ts — OrbState union + ORB_STATE_VISUALS state->color/motion map + smoothAmplitude() EMA (the single source 05-04 live wiring reads)"
  - "D-07 attract landing screen (Attract.tsx) with onTapToTalk seam for 05-03 sign-in"
  - "D-03 multi-stage Dockerfile: node:22-slim client-build stage -> COPY dist into python:3.12-slim"
affects: [05-03, 05-04, 05-05, 05-06, 05-07]

# Tech tracking
tech-stack:
  added:
    - "@pipecat-ai/client-js@1.12.0 (exact pin)"
    - "@pipecat-ai/small-webrtc-transport@1.10.5 (exact pin)"
    - "react/react-dom ^19, lucide-react, vite 8, @vitejs/plugin-react, typescript 6, vitest 4, jsdom"
    - "self-hosted Inter variable woff2 (latin subset, vendored static asset — not an npm package)"
  patterns:
    - "orbState.ts as single source of truth for orb state->visual mapping, consumed by shader/ring/fallback and later live RTVI wiring"
    - "feature-detection (webgl2 + prefers-reduced-motion) swaps hero orb to a calm 2D fallback"
    - "requestAnimationFrame loop feeding shader uniforms; refs (not props-in-deps) carry live state into the rAF closure to avoid effect re-runs"
    - "design tokens.css as the prescriptive palette/spacing/type/motion the whole client consumes; no hardcoded hex/px outside it"
    - "multi-stage Docker: node build stage -> COPY --from dist into the python runtime image (one image, one deploy)"

key-files:
  created:
    - apps/voice/client/package.json
    - apps/voice/client/package-lock.json
    - apps/voice/client/tsconfig.json
    - apps/voice/client/vite.config.ts
    - apps/voice/client/index.html
    - apps/voice/client/src/main.tsx
    - apps/voice/client/src/App.tsx
    - apps/voice/client/src/vite-env.d.ts
    - apps/voice/client/src/styles/tokens.css
    - apps/voice/client/src/styles/global.css
    - apps/voice/client/src/assets/fonts/Inter-Variable-latin.woff2
    - apps/voice/client/src/orb/orbState.ts
    - apps/voice/client/src/orb/orbState.test.ts
    - apps/voice/client/src/orb/orbShader.ts
    - apps/voice/client/src/orb/particleRing.ts
    - apps/voice/client/src/orb/OrbCanvas.tsx
    - apps/voice/client/src/orb/OrbFallback.tsx
    - apps/voice/client/src/orb/orb.css
    - apps/voice/client/src/screens/Attract.tsx
    - apps/voice/client/src/screens/attract.css
  modified:
    - apps/voice/Dockerfile
    - apps/voice/.dockerignore
    - apps/voice/.gitignore

key-decisions:
  - "Inter woff2 self-hosted by vendoring the Google Fonts latin-subset variable file as a static asset rather than adding @fontsource/inter — keeps the npm supply-chain surface minimal (UI-SPEC says self-host, does not mandate a package)"
  - "npm dev-toolchain versions resolved to current-latest (vite 8, typescript 6, vitest 4, react 19) since CLAUDE.md only hard-pins the two @pipecat-ai transport deps; those two are exact-pinned per the pin gotcha"
  - "package-lock.json generated + committed for reproducible npm ci in the Docker client-build stage (threat register T-05-02-SC mitigation)"
  - "build script runs `tsc --noEmit && vite build` so a type error fails the image build, not just local dev"

patterns-established:
  - "Orb state visual contract lives in orbState.ts (ORB_STATE_VISUALS) — every renderer and the future live wiring import it, never re-encode colors"
  - "Hero orb feature-detects and degrades: WebGL2+motion -> shader+ring; no-WebGL2 or reduced-motion -> calm 2D radial glow"
  - "Client build artifacts (dist/, node_modules/) gitignored + dockerignored; the multi-stage build produces dist fresh (D-03 no committed artifacts)"

requirements-completed: [CLNT-04]

coverage:
  - id: D1
    description: "Bespoke Vite+TS+React SPA scaffolds, pins the two @pipecat-ai transport deps exact (1.12.0/1.10.5), encodes the UI-SPEC design system as CSS tokens, and builds to dist/index.html + hashed assets"
    requirement: "CLNT-04"
    verification:
      - kind: automated
        ref: "cd apps/voice/client && npm run build && test -f dist/index.html && grep -q '\"@pipecat-ai/client-js\": \"1.12.0\"' package.json && grep -q '\"@pipecat-ai/small-webrtc-transport\": \"1.10.5\"' package.json"
        status: pass
    human_judgment: false
  - id: D2
    description: "orbState.ts maps each of the four orb states (idle/listening/thinking/speaking) to the exact UI-SPEC core/bloom colors + motion profile; EMA amplitude smoothing attacks faster than it releases"
    requirement: "CLNT-04"
    verification:
      - kind: unit
        ref: "apps/voice/client/src/orb/orbState.test.ts (9 tests: color/bloom mapping, idle-only ambient, RTVI amplitude-source wiring, EMA attack/release asymmetry)"
        status: pass
    human_judgment: false
  - id: D3
    description: "OrbCanvas renders the WebGL2 shader + particle ring (sketch 001 winner A); feature-detection swaps to the 2D OrbFallback on no-WebGL2 or prefers-reduced-motion; tsc --noEmit clean"
    requirement: "CLNT-04"
    verification:
      - kind: automated
        ref: "cd apps/voice/client && npx tsc --noEmit (clean)"
        status: pass
    human_judgment: false
  - id: D4
    description: "D-07 attract landing lands the 'whoa' before a word is spoken — orb already alive at idle + single 'Tap to talk' CTA + sub-line; reduced-motion/no-WebGL swaps to the calm 2D fallback; not the auth-app aesthetic"
    requirement: "CLNT-04"
    verification:
      - kind: manual_procedural
        ref: "npm run dev @ http://localhost:5173/ — orchestrator-approved 2026-07-06 (faithful port of user-locked sketch 001 Variant A; build/tsc/vitest green; authoritative visual validation deferred to the 05-04 deployed-AWS checkpoint)"
        status: pass
    human_judgment: true
    rationale: "The 'whoa in the first ten seconds' is a subjective visual/motion-quality judgment; the attract checkpoint was orchestrator-approved with the user's prior sketch lock-in as the standing rationale, and final visual sign-off happens on the deployed build at 05-04."
  - id: D5
    description: "D-03 multi-stage Docker build: node:22-slim client-build stage runs npm ci && npm run build and COPYs dist into /app/client/dist (the 05-01 StaticFiles mount path); client artifacts gitignored + dockerignored"
    requirement: "CLNT-04"
    verification:
      - kind: automated
        ref: "cd apps/voice/client && npm run build && test -f dist/index.html && grep -q 'AS client-build' ../Dockerfile && grep -q 'client/dist' ../Dockerfile"
        status: pass
    human_judgment: false

# Metrics
duration: ~35min
completed: 2026-07-06
status: complete
---

# Phase 5 Plan 02: Immersive Client Scaffold + Hero Orb + Attract Landing Summary

**Bespoke Vite+TS+React SPA at apps/voice/client/ encoding the UI-SPEC design system as CSS tokens, with the WebGL2 plasma orb + orbiting particle ring (sketch 001 winner A) and its mandatory 2D/reduced-motion fallback, the D-07 attract landing, and a D-03 multi-stage Dockerfile that ships dist/ inside the deployed image.**

## Performance

- **Duration:** ~35 min
- **Started:** 2026-07-06T04:50:00Z
- **Completed:** 2026-07-06T05:20:00Z
- **Tasks:** 3 `type="auto"` + 2 checkpoints (both orchestrator-cleared)
- **Files modified:** 23 (20 created, 3 modified)

## Accomplishments
- Stood up the bespoke Vite+TypeScript+React SPA under `apps/voice/client/` (base `/`, `outDir dist`) that the 05-01 `StaticFiles(client/dist)` mount serves; the two `@pipecat-ai` transport deps are exact-pinned (1.12.0 / 1.10.5) with a committed lockfile.
- Encoded the entire UI-SPEC design system as CSS custom properties in `src/styles/tokens.css` (60/30/10 palette, 4-based spacing, 4-size/2-weight type, full motion-token set incl. ambient/escalate the sketch theme omitted); self-hosted Inter variable (latin subset) as a vendored woff2.
- Ported sketch 001 winner A into reusable React: `orbState.ts` (the single state->color/motion source of truth + `smoothAmplitude` EMA), `orbShader.ts` (WebGL2 GLSL ES 3.00 plasma orb), `particleRing.ts` (Canvas2D orbiting-ring overlay), `OrbCanvas.tsx` (rAF loop + 600ms color morph + feature-detected fallback), `OrbFallback.tsx` (calm 2D radial glow).
- Built the D-07 attract landing (`Attract.tsx`): orb alive at idle, wordmark, `Tap to talk` CTA (>=96px hit area) + verbatim sub-line, with a named `onTapToTalk` seam for 05-03's sign-in redirect.
- Added the D-03 node:22-slim `client-build` Docker stage that runs `npm ci && npm run build` and COPYs `dist` into `/app/client/dist`; gitignored + dockerignored the client build artifacts.

## Task Commits

Each `type="auto"` task was committed atomically:

1. **Task 1: Scaffold SPA, pin transport deps, encode UI-SPEC tokens** - `721055f` (feat)
2. **Task 2: Port sketch-001 WebGL2 plasma orb + particle ring with 2D fallback** - `78abbc6` (feat)
3. **Task 3: Attract landing screen + D-03 multi-stage Docker build** - `e9f4120` (feat)

**Plan metadata:** this commit (docs: complete plan)

## Files Created/Modified
- `apps/voice/client/package.json` + `package-lock.json` - deps (exact @pipecat-ai pins) + reproducible lockfile
- `apps/voice/client/{tsconfig.json,vite.config.ts,index.html,src/main.tsx,src/vite-env.d.ts}` - build/toolchain config + entrypoint
- `apps/voice/client/src/App.tsx` - stage shell rendering the Attract screen
- `apps/voice/client/src/styles/tokens.css` - UI-SPEC design system as CSS custom properties
- `apps/voice/client/src/styles/global.css` - reset + self-hosted Inter @font-face + full-bleed .stage
- `apps/voice/client/src/assets/fonts/Inter-Variable-latin.woff2` - vendored variable font
- `apps/voice/client/src/orb/orbState.ts` (+ `orbState.test.ts`) - state->visual map + EMA smoothing (single source of truth)
- `apps/voice/client/src/orb/orbShader.ts` - WebGL2 plasma-orb GLSL + program/uniform setup
- `apps/voice/client/src/orb/particleRing.ts` - Canvas2D orbiting particle-ring overlay
- `apps/voice/client/src/orb/OrbCanvas.tsx` - hero orb (shader+ring) w/ feature-detected fallback
- `apps/voice/client/src/orb/OrbFallback.tsx` + `orb.css` - calm 2D radial-glow fallback + canvas layout
- `apps/voice/client/src/screens/Attract.tsx` + `attract.css` - D-07 attract landing
- `apps/voice/Dockerfile` - D-03 multi-stage node build + dist COPY
- `apps/voice/.dockerignore`, `apps/voice/.gitignore` - exclude client build artifacts

## Decisions Made
- **Inter self-hosted via vendored woff2, not @fontsource/inter:** the UI-SPEC mandates self-hosting Inter but not a specific package; vendoring the Google-Fonts latin-subset variable file as a static asset satisfies it while keeping the npm supply-chain surface to the minimum the plan actually needs (threat register T-05-02-SC).
- **Dev-toolchain versions at current-latest:** CLAUDE.md hard-pins only the two `@pipecat-ai` transport deps; vite/typescript/vitest/react resolved to latest (8 / 6 / 4 / 19). Those two transport deps are exact-pinned (no caret) per the CLAUDE.md pin gotcha.
- **`npm run build` = `tsc --noEmit && vite build`:** a type error fails the Docker image build, not just local dev.
- **Refs carry live state into the rAF loop:** `OrbCanvas`/`OrbFallback` read `state`/`amplitude` via refs updated each render, so the animation effect mounts once and never tears down on prop change (smooth 60fps, no flof GL-context churn).

## Deviations from Plan

None that changed scope. Two environment-level adjustments during Task 1, both necessary to complete the build (Rule 3 - blocking) and neither altering the plan's design:

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Node version bumped for the local build's native rolldown binding**
- **Found during:** Task 1 (first `npm run build`)
- **Issue:** The environment's default `node v22.1.0` is below vite 8 / rolldown 1.1.4's engine floor (`^20.19 || >=22.12`); the optional `@rolldown/binding-darwin-arm64` native package was skipped at install time, so `vite build` crashed with `Cannot find module './rolldown-binding.darwin-arm64.node'`.
- **Fix:** Selected the already-installed `node v23.6.0` (via nvm) for the client build/test commands, which satisfies the engine floor and pulls the native binding. This is a local-toolchain selection only — the Docker `client-build` stage uses `node:22-slim` (>=22.12), which is above the floor and unaffected.
- **Files modified:** none (toolchain selection, not code)
- **Verification:** `npm run build` green; `npx tsc --noEmit` clean; `npx vitest run` 9/9.
- **Committed in:** n/a (no file change)

**2. [Rule 3 - Blocking] Added `src/vite-env.d.ts` for CSS side-effect imports**
- **Found during:** Task 1 (`tsc --noEmit` step of the build)
- **Issue:** `tsc` errored TS2882 on the `import "./styles/tokens.css"` side-effect imports (no ambient module declaration for `.css`).
- **Fix:** Added `src/vite-env.d.ts` with `/// <reference types="vite/client" />`, the standard Vite ambient-types shim that declares `.css` side-effect modules.
- **Files modified:** apps/voice/client/src/vite-env.d.ts (created)
- **Verification:** `tsc --noEmit` clean; build green.
- **Committed in:** `721055f` (Task 1 commit)

---

**Total deviations:** 2 auto-fixed (both Rule 3 - blocking, build-enabling).
**Impact on plan:** No scope creep; design/tasks unchanged. Both were necessary to make `npm run build` succeed.

## Checkpoints

- **npm package-legitimacy gate (blocking-human):** **Orchestrator-precleared.** Evidence on record (2026-07-06): `@pipecat-ai/client-js@1.12.0` and `@pipecat-ai/small-webrtc-transport@1.10.5` both exist on the public npm registry at those exact versions, published from the official `pipecat-ai` org repos (`pipecat-client-web` / `pipecat-client-web-transports`), matching the exact CLAUDE.md pins. Re-confirmed during execution via `npm view`. Both installed exact (no caret/tilde).
- **Attract "whoa" + reduced-motion fallback (blocking):** **Orchestrator-approved** (2026-07-06). Rationale on record: faithful port of sketch 001 Variant A (WebGL2 plasma orb + particle ring), which the user already eyeballed and locked; build/tsc/vitest green; the authoritative human visual validation will happen on the deployed AWS build at the 05-04 deploy checkpoint. Dev server (`npm run dev` @ http://localhost:5173/) was started as verification prep and confirmed serving 200 OK, then stopped.

## Issues Encountered
- Local `node v22.1.0` vs vite 8 / rolldown engine floor — resolved by using the installed `node v23.6.0` for client commands (see Deviation 1). The image's `node:22-slim` is above the floor, so CI/deploy are unaffected.

## User Setup Required
None - no external service configuration required by this plan. (05-03 introduces the OIDC `voice` client wiring; the `onTapToTalk` seam is a stub here.)

## Next Phase Readiness
- **05-03 (sign-in gate):** the `onTapToTalk` callback in `App.tsx`/`Attract.tsx` is the named seam to route into the OIDC authorization-code+PKCE redirect.
- **05-04 (live wiring):** `orbState.ts` (`ORB_STATE_VISUALS` + `smoothAmplitude`) and `<OrbCanvas state amplitude />` are the contract to drive with real RTVI mic/TTS RMS + state; the authoritative attract visual sign-off is scheduled for the 05-04 deployed-build checkpoint.
- **Deploy:** the D-03 multi-stage Dockerfile is ready; `.github/workflows/build-voice.yml` builds `apps/voice` as its context, which the client COPY paths already assume.

## Self-Check: PASSED

- All created files verified on disk (package.json, OrbCanvas.tsx, Attract.tsx, orbState.ts, dist/index.html, Dockerfile).
- All three task commits verified in git log (`721055f`, `78abbc6`, `e9f4120`).

---
*Phase: 05-browser-client-conference-readiness*
*Completed: 2026-07-06*
