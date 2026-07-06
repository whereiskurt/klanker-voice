# Slick Start Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Project note:** this repo enforces the GSD workflow for code edits. Either execute via GSD (`/gsd-plan-phase` can ingest this as the phase design, then `/gsd-execute-phase`), or via the superpowers executor — but keep the atomic-commit + STATE.md discipline either way. Commit messages end with the repo's `Co-Authored-By:` trailer.

**Goal:** Make the voice session start feel like one slick tap — a returning user taps once and immediately hears a warm, randomly-chosen KPH greeting while the stream connects underneath.

**Architecture:** Two independent workstreams. (A) Silent SSO on load: a returning-user breadcrumb triggers one top-level `prompt=none` OIDC bounce during initial load, so the user is authenticated before their tap. (B) Instant greeting: a small set of pre-rendered KPH greetings (rendered from the configured TTS voice) plays the instant the tap gesture fires; the server's greet-first is turned off so KPH doesn't greet twice.

**Tech Stack:** React 19 + Vite + vitest (client, `apps/voice/client`); Python 3.12 + pytest + Pipecat (server, `apps/voice`); ElevenLabs HTTP TTS (`eleven_flash_v2_5`); oidc-provider + next-auth (auth server, `apps/auth`).

**Design spec:** `docs/superpowers/specs/2026-07-06-slick-start-design.md` (read it first).

## Global Constraints

- **Voice:** greeting clips MUST be rendered from the `voice_id` in `apps/voice/pipeline.toml` (currently `UgBBYS2sOqTuMpoF3BR0` "Mark"), model `eleven_flash_v2_5`, speed `1.1` — same as live TTS. Re-render on any voice swap.
- **Audio format:** greeting clips are **MP3** (universal iOS Safari `<audio>` support — not opus).
- **Token:** never persist the access token. Workstream A adds only a boolean `localStorage` breadcrumb — no token, no PII.
- **Copy for the ear:** greeting text has no emojis/markdown/lists; "DEFCON dot run" spelled out.
- **iOS Safari is the primary target.** Silent auth is top-level navigation only (never an iframe). Mic + audio unlock happen inside the tap gesture.
- **Greeting set (approved, 3):**
  1. "Hey! I'm KPH — a useful concierge assistant that sounds a lot like Kurt. Let's dig into some of my projects, feel free to interrupt, and just be yourself."
  2. "Hey! I'm KPH — a concierge that sounds a lot like Kurt and knows his whole world. Let's dig into some of the projects. Feel free to interrupt me anytime, and just be yourself."
  3. "Hey there — I'm KPH, your KlankerMaker concierge. Curious about Kurt, the platform, or DEFCON dot run? Just start talking, and cut me off anytime."

---

## Task 0: `prompt=none` feasibility spike (BLOCKING gate for Workstream A)

Workstream A is worthless if the live issuer doesn't honor `prompt=none`. Verify before building A. This is a manual verification task, not code.

- [ ] **Step 1: Craft a silent authorize URL by hand**

Read the client's live OIDC config values (they're baked into the deployed bundle as `VITE_OIDC_*`; the issuer is like `https://auth.klankermaker.ai/use1/api/oidc`). Build:
`{issuer}/auth?response_type=code&client_id={clientId}&redirect_uri={redirectUri}&scope=voice&resource={audience}&state=spike&code_challenge={anyS256}&code_challenge_method=S256&prompt=none`

- [ ] **Step 2: Test WITH a valid session**

In a browser already signed in at `auth.klankermaker.ai`, visit the URL. Expected: an immediate 302 back to `redirect_uri` with `?code=…` and NO login UI.

- [ ] **Step 3: Test WITHOUT a session**

In a private window (no auth cookie), visit the same URL. Expected: a 302 back to `redirect_uri` with `?error=login_required` (or `interaction_required`) — NOT a login page render.

- [ ] **Step 4: Record the outcome**

If both hold → proceed with Workstream A as written. If the issuer instead renders a login page (no `prompt=none` support), STOP Workstream A and open a follow-up to configure oidc-provider's interaction policy for `prompt=none`; Workstream B is unaffected and can ship alone.

---

## Task 1: Returning-user breadcrumb store

**Files:**
- Create: `apps/voice/client/src/auth/returningStore.ts`
- Test: `apps/voice/client/src/auth/returningStore.test.ts`

**Interfaces:**
- Produces: `markReturningUser(): void`, `isReturningUser(): boolean`, `clearReturningUser(): void`, `markSilentTried(): void`, `wasSilentTried(): boolean` — the breadcrumb (`localStorage kmv_returning`) and the per-load loop guard (`sessionStorage kmv_silent_tried`).

- [ ] **Step 1: Write the failing tests**

```typescript
// apps/voice/client/src/auth/returningStore.test.ts
import { afterEach, describe, expect, it } from "vitest";
import {
  markReturningUser, isReturningUser, clearReturningUser,
  markSilentTried, wasSilentTried,
} from "./returningStore";

afterEach(() => {
  localStorage.clear();
  sessionStorage.clear();
});

describe("returning-user breadcrumb", () => {
  it("is false before any sign-in", () => {
    expect(isReturningUser()).toBe(false);
  });
  it("is true after marking, false after clearing", () => {
    markReturningUser();
    expect(isReturningUser()).toBe(true);
    clearReturningUser();
    expect(isReturningUser()).toBe(false);
  });
  it("stores no token — only a boolean flag", () => {
    markReturningUser();
    expect(localStorage.getItem("kmv_returning")).toBe("1");
  });
});

describe("silent-tried per-load guard", () => {
  it("is false until marked, then true", () => {
    expect(wasSilentTried()).toBe(false);
    markSilentTried();
    expect(wasSilentTried()).toBe(true);
  });
  it("lives in sessionStorage (per tab/load), not localStorage", () => {
    markSilentTried();
    expect(sessionStorage.getItem("kmv_silent_tried")).toBe("1");
    expect(localStorage.getItem("kmv_silent_tried")).toBeNull();
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd apps/voice/client && npx vitest run src/auth/returningStore.test.ts`
Expected: FAIL (module not found).

- [ ] **Step 3: Implement**

```typescript
// apps/voice/client/src/auth/returningStore.ts
/**
 * "Returning user" breadcrumb (Workstream A, slick-start). Holds NO token —
 * only a boolean that says this device has completed an interactive sign-in
 * before, so the app may attempt a silent top-level prompt=none SSO on load.
 * The per-load `silent_tried` guard prevents a redirect loop (load -> silent
 * -> /callback -> load -> silent ...).
 */
const RETURNING_KEY = "kmv_returning";
const SILENT_TRIED_KEY = "kmv_silent_tried";

export function markReturningUser(): void {
  try { localStorage.setItem(RETURNING_KEY, "1"); } catch { /* storage disabled: silent SSO just won't trigger */ }
}
export function isReturningUser(): boolean {
  try { return localStorage.getItem(RETURNING_KEY) === "1"; } catch { return false; }
}
export function clearReturningUser(): void {
  try { localStorage.removeItem(RETURNING_KEY); } catch { /* no-op */ }
}
export function markSilentTried(): void {
  try { sessionStorage.setItem(SILENT_TRIED_KEY, "1"); } catch { /* no-op */ }
}
export function wasSilentTried(): boolean {
  try { return sessionStorage.getItem(SILENT_TRIED_KEY) === "1"; } catch { return false; }
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd apps/voice/client && npx vitest run src/auth/returningStore.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/voice/client/src/auth/returningStore.ts apps/voice/client/src/auth/returningStore.test.ts
git commit -m "feat(voice-client): returning-user breadcrumb + silent-tried guard"
```

---

## Task 2: `prompt=none` support in the authorize URL builder

**Files:**
- Modify: `apps/voice/client/src/auth/oidcClient.ts` (add optional `prompt` to `buildAuthorizeUrl`)
- Test: `apps/voice/client/src/auth/oidcClient.test.ts` (create if absent)

**Interfaces:**
- Consumes: `OidcConfig` from `config/oidc`.
- Produces: `buildAuthorizeUrl(config, { verifier, state, prompt? })` — when `prompt` is set, the returned URL includes `&prompt=none`.

- [ ] **Step 1: Write the failing test**

```typescript
// apps/voice/client/src/auth/oidcClient.test.ts
import { describe, expect, it } from "vitest";
import { buildAuthorizeUrl } from "./oidcClient";

const CONFIG = {
  issuer: "https://auth.example/use1/api/oidc",
  clientId: "voice",
  audience: "https://voice.example/api",
  redirectUri: "https://voice.example/callback",
};

describe("buildAuthorizeUrl prompt", () => {
  it("omits prompt by default (interactive sign-in)", async () => {
    const url = await buildAuthorizeUrl(CONFIG, { verifier: "v".repeat(43), state: "s" });
    expect(new URL(url).searchParams.has("prompt")).toBe(false);
  });
  it("adds prompt=none when requested (silent SSO)", async () => {
    const url = await buildAuthorizeUrl(CONFIG, { verifier: "v".repeat(43), state: "s", prompt: "none" });
    expect(new URL(url).searchParams.get("prompt")).toBe("none");
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd apps/voice/client && npx vitest run src/auth/oidcClient.test.ts`
Expected: FAIL (`prompt` not applied).

- [ ] **Step 3: Implement (edit `oidcClient.ts`)**

Add `prompt?: "none"` to `AuthorizeParams`, and in `buildAuthorizeUrl` after the existing `searchParams.set` calls:

```typescript
export interface AuthorizeParams {
  verifier: string;
  state: string;
  /** When "none", requests a silent (no-UI) authorization — top-level only. */
  prompt?: "none";
}
```
```typescript
  // ...existing url.searchParams.set(...) calls...
  if (prompt) url.searchParams.set("prompt", prompt);
  return url.toString();
```
(Destructure `prompt` from the params argument alongside `verifier, state`.)

- [ ] **Step 4: Run to verify it passes**

Run: `cd apps/voice/client && npx vitest run src/auth/oidcClient.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/voice/client/src/auth/oidcClient.ts apps/voice/client/src/auth/oidcClient.test.ts
git commit -m "feat(voice-client): optional prompt=none in buildAuthorizeUrl"
```

---

## Task 3: Silent SSO trigger in `useAuth` + interactive breadcrumb

**Files:**
- Modify: `apps/voice/client/src/auth/useAuth.ts`

**Interfaces:**
- Consumes: `returningStore` (Task 1), `buildAuthorizeUrl(..., { prompt: "none" })` (Task 2).
- Produces: `attemptSilentSso(): void` on the `useAuth()` result — a no-op unless (returning user) AND (not authenticated) AND (not already tried this load); otherwise stashes PKCE, marks silent-tried, and does a **top-level** `window.location.assign` to the `prompt=none` authorize URL. Also: `beginSignIn()` and the successful callback path now set the breadcrumb.

- [ ] **Step 1: Add `attemptSilentSso` to `useAuth`**

In `useAuth.ts`, import the store and add the callback (mirrors `beginSignIn`, but guarded and silent):

```typescript
import {
  isReturningUser, markSilentTried, wasSilentTried, markReturningUser, clearReturningUser,
} from "./returningStore";
```
```typescript
  const attemptSilentSso = useCallback(async () => {
    if (tokenIsAuthenticated() || !isReturningUser() || wasSilentTried()) return;
    markSilentTried();
    const verifier = generateCodeVerifier();
    const state = generateState();
    stashPkce(verifier, state);
    const url = await buildAuthorizeUrl(getOidcConfig(), { verifier, state, prompt: "none" });
    window.location.assign(url); // top-level — iOS-safe first-party cookie
  }, []);
```

Export `attemptSilentSso`, `markReturningUser`, `clearReturningUser` in the returned object. In `signOut`, also call `clearReturningUser()`.

- [ ] **Step 2: Test the guard logic**

```typescript
// apps/voice/client/src/auth/useAuth.silent.test.ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { useAuth } from "./useAuth";
import { markReturningUser, markSilentTried } from "./returningStore";
import * as tokenStore from "./tokenStore";

afterEach(() => { localStorage.clear(); sessionStorage.clear(); vi.restoreAllMocks(); });

describe("attemptSilentSso guard", () => {
  it("does nothing for a first-time visitor (no breadcrumb)", async () => {
    const assign = vi.spyOn(window.location, "assign").mockImplementation(() => {});
    const { result } = renderHook(() => useAuth());
    await result.current.attemptSilentSso();
    expect(assign).not.toHaveBeenCalled();
  });
  it("does nothing if already tried this load", async () => {
    markReturningUser(); markSilentTried();
    const assign = vi.spyOn(window.location, "assign").mockImplementation(() => {});
    const { result } = renderHook(() => useAuth());
    await result.current.attemptSilentSso();
    expect(assign).not.toHaveBeenCalled();
  });
  it("does nothing if already authenticated", async () => {
    markReturningUser();
    vi.spyOn(tokenStore, "isAuthenticated").mockReturnValue(true);
    const assign = vi.spyOn(window.location, "assign").mockImplementation(() => {});
    const { result } = renderHook(() => useAuth());
    await result.current.attemptSilentSso();
    expect(assign).not.toHaveBeenCalled();
  });
});
```

Note: `window.location.assign` may need `Object.defineProperty(window, "location", ...)` shimming depending on the vitest/jsdom version — follow whatever pattern the existing client tests use; if `spyOn` on `location.assign` is not permitted, wrap the navigation in a tiny `navigate(url)` helper module and spy on that instead.

- [ ] **Step 3: Run tests**

Run: `cd apps/voice/client && npx vitest run src/auth/useAuth.silent.test.ts`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add apps/voice/client/src/auth/useAuth.ts apps/voice/client/src/auth/useAuth.silent.test.ts
git commit -m "feat(voice-client): silent prompt=none SSO trigger guarded by breadcrumb"
```

---

## Task 4: Wire silent SSO on mount + handle `login_required` in Callback

**Files:**
- Modify: `apps/voice/client/src/App.tsx` (call `attemptSilentSso` on mount; set breadcrumb on interactive auth)
- Modify: `apps/voice/client/src/screens/Callback.tsx` (branch on `error=login_required`; set breadcrumb on success)

**Interfaces:**
- Consumes: `useAuth().attemptSilentSso`, `markReturningUser`, `clearReturningUser` (Task 3).

- [ ] **Step 1: App mount effect**

In `App.tsx`, add an effect (runs once) that kicks the silent attempt. It is internally guarded, so calling it unconditionally is safe:

```typescript
  useEffect(() => {
    void voice; // (keep existing hooks above)
    void auth.attemptSilentSso();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
```

In `handleAuthenticated` (the interactive-return path), set the breadcrumb:
```typescript
  const handleAuthenticated = () => {
    markReturningUser();
    auth.refresh();
    window.history.replaceState({}, "", "/");
    setOnCallbackRoute(false);
  };
```
(Import `markReturningUser` from `./auth/returningStore`.)

- [ ] **Step 2: Callback `login_required` branch**

In `Callback.tsx`'s `run()`, before the "code missing" error, detect the silent-failure error and route to Attract signed-out (clearing the breadcrumb):

```typescript
      const errorParam = params.get("error");
      if (errorParam === "login_required" || errorParam === "interaction_required") {
        // Silent SSO found no live issuer session — expected "session expired".
        clearReturningUser();
        if (!cancelled) { window.history.replaceState({}, "", "/"); onAuthenticated(); }
        return;
      }
```
And in the success path (after `setToken`), set the breadcrumb so future loads try silent SSO:
```typescript
        setToken(accessToken);
        markReturningUser();
```
(Import `clearReturningUser, markReturningUser` from `../auth/returningStore`. Note: `onAuthenticated` here just routes back to `/`; App's `handleAuthenticated` also calls `markReturningUser` — idempotent.)

- [ ] **Step 3: Test the Callback error branch**

```typescript
// apps/voice/client/src/screens/Callback.loginRequired.test.tsx
import { afterEach, describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import Callback from "./Callback";
import { isReturningUser, markReturningUser } from "../auth/returningStore";

afterEach(() => { localStorage.clear(); sessionStorage.clear(); });

describe("Callback login_required", () => {
  it("clears the breadcrumb and routes onward without error UI", async () => {
    markReturningUser();
    window.history.replaceState({}, "", "/callback?error=login_required");
    const onAuthenticated = vi.fn();
    render(<Callback onAuthenticated={onAuthenticated} />);
    await vi.waitFor(() => expect(onAuthenticated).toHaveBeenCalled());
    expect(isReturningUser()).toBe(false);
  });
});
```

- [ ] **Step 4: Run + typecheck**

Run: `cd apps/voice/client && npx vitest run src/screens/Callback.loginRequired.test.tsx && npm run build`
Expected: PASS + clean `tsc`.

- [ ] **Step 5: Commit**

```bash
git add apps/voice/client/src/App.tsx apps/voice/client/src/screens/Callback.tsx apps/voice/client/src/screens/Callback.loginRequired.test.tsx
git commit -m "feat(voice-client): silent SSO on load + login_required handling (single-tap start)"
```

---

## Task 5: Greeting source + render script + make target

**Files:**
- Create: `apps/voice/client/public/greetings/greetings.source.json`
- Create: `apps/voice/scripts/render_greetings.py`
- Modify: `apps/voice/Makefile` (add `greetings` target)
- Generated (committed): `apps/voice/client/public/greetings/greeting-1.mp3` … `greeting-3.mp3`, `greetings.manifest.json`

**Interfaces:**
- Produces: `greetings.manifest.json` = `{ "voiceId": "<id>", "model": "eleven_flash_v2_5", "clips": [{ "text": "...", "file": "greeting-1.mp3" }, ...] }` — consumed by the client (Task 7) and the drift guard (Task 6).

- [ ] **Step 1: Write the greeting source**

```json
// apps/voice/client/public/greetings/greetings.source.json
{
  "greetings": [
    "Hey! I'm KPH — a useful concierge assistant that sounds a lot like Kurt. Let's dig into some of my projects, feel free to interrupt, and just be yourself.",
    "Hey! I'm KPH — a concierge that sounds a lot like Kurt and knows his whole world. Let's dig into some of the projects. Feel free to interrupt me anytime, and just be yourself.",
    "Hey there — I'm KPH, your KlankerMaker concierge. Curious about Kurt, the platform, or DEFCON dot run? Just start talking, and cut me off anytime."
  ]
}
```

- [ ] **Step 2: Write the render script** (mirrors `scripts/audition.py`'s ElevenLabs call)

```python
# apps/voice/scripts/render_greetings.py
"""Render the pre-recorded KPH greetings from the CONFIGURED voice (D-04 slick-start).

Reads voice_id from apps/voice/pipeline.toml so the clips always match the live
TTS voice, then renders each greetings.source.json line to MP3 (iOS-safe) and
writes a manifest the client consumes + the drift-guard test checks. Run via
`make -C apps/voice greetings`. Requires ELEVENLABS_API_KEY in the env.
"""
import json
import os
import sys
import tomllib
from pathlib import Path

import httpx

APP_ROOT = Path(__file__).resolve().parents[1]          # apps/voice
PIPELINE_TOML = APP_ROOT / "pipeline.toml"
GREETINGS_DIR = APP_ROOT / "client" / "public" / "greetings"
SOURCE = GREETINGS_DIR / "greetings.source.json"
MANIFEST = GREETINGS_DIR / "greetings.manifest.json"
API_BASE = "https://api.elevenlabs.io/v1"
MODEL_ID = "eleven_flash_v2_5"
OUTPUT_FORMAT = "mp3_44100_128"
SPEED = 1.1

def voice_id_from_config() -> str:
    data = tomllib.loads(PIPELINE_TOML.read_text())
    vid = str(data.get("tts", {}).get("voice_id", "")).strip()
    if not vid:
        sys.exit("render_greetings: tts.voice_id is empty in pipeline.toml")
    return vid

def main() -> None:
    key = os.environ.get("ELEVENLABS_API_KEY")
    if not key:
        sys.exit("render_greetings: ELEVENLABS_API_KEY not set (run `make -C apps/voice env`)")
    voice_id = voice_id_from_config()
    texts = json.loads(SOURCE.read_text())["greetings"]
    GREETINGS_DIR.mkdir(parents=True, exist_ok=True)

    clips = []
    with httpx.Client(headers={"xi-api-key": key}, timeout=60.0) as client:
        for i, text in enumerate(texts, 1):
            fname = f"greeting-{i}.mp3"
            print(f"rendering greeting {i}/{len(texts)} -> {fname}")
            resp = client.post(
                f"{API_BASE}/text-to-speech/{voice_id}",
                params={"output_format": OUTPUT_FORMAT},
                json={"text": text, "model_id": MODEL_ID, "voice_settings": {"speed": SPEED}},
            )
            resp.raise_for_status()
            (GREETINGS_DIR / fname).write_bytes(resp.content)
            clips.append({"text": text, "file": fname})

    MANIFEST.write_text(json.dumps(
        {"voiceId": voice_id, "model": MODEL_ID, "clips": clips}, indent=2) + "\n")
    print(f"wrote {MANIFEST.relative_to(APP_ROOT)} ({len(clips)} clips, voice {voice_id})")

if __name__ == "__main__":
    main()
```

- [ ] **Step 3: Add the make target**

In `apps/voice/Makefile`, add (match the file's existing style; `.venv` python if that's the pattern):

```makefile
.PHONY: greetings
greetings: ## Render the KPH greeting clips from the configured pipeline.toml voice
	$(PY) scripts/render_greetings.py
```
(Use the same `$(PY)`/venv variable the other targets use, e.g. `audition`.)

- [ ] **Step 4: Render the clips**

Run: `make -C apps/voice env && make -C apps/voice greetings`
Expected: three `greeting-N.mp3` files + `greetings.manifest.json` with `"voiceId": "UgBBYS2sOqTuMpoF3BR0"`.

- [ ] **Step 5: Commit** (binary clips + manifest + script + source)

```bash
git add apps/voice/scripts/render_greetings.py apps/voice/Makefile apps/voice/client/public/greetings/
git commit -m "feat(voice): render KPH greeting clips from configured voice + manifest"
```

---

## Task 6: Voice-drift CI guard

**Files:**
- Create: `apps/voice/tests/test_greeting_voice_drift.py`

**Interfaces:**
- Consumes: `greetings.manifest.json` (Task 5), `pipeline.toml`.

- [ ] **Step 1: Write the failing test**

```python
# apps/voice/tests/test_greeting_voice_drift.py
"""Fails CI if the rendered greetings were made from a different voice than
the one pipeline.toml currently ships — i.e. someone swapped the TTS voice
without re-running `make -C apps/voice greetings`. Closes the stale-voice gap
without needing an ElevenLabs key in CI (pure string comparison)."""
import json
import tomllib
from pathlib import Path

APP_ROOT = Path(__file__).resolve().parents[1]
MANIFEST = APP_ROOT / "client" / "public" / "greetings" / "greetings.manifest.json"
PIPELINE_TOML = APP_ROOT / "pipeline.toml"

def test_greeting_clips_match_configured_voice():
    manifest = json.loads(MANIFEST.read_text())
    configured = str(tomllib.loads(PIPELINE_TOML.read_text())["tts"]["voice_id"]).strip()
    assert manifest["voiceId"] == configured, (
        f"greeting clips were rendered from {manifest['voiceId']!r} but pipeline.toml "
        f"ships {configured!r} — re-run `make -C apps/voice greetings` and commit the clips."
    )

def test_manifest_lists_all_source_greetings():
    manifest = json.loads(MANIFEST.read_text())
    source = json.loads((MANIFEST.parent / "greetings.source.json").read_text())["greetings"]
    assert [c["text"] for c in manifest["clips"]] == source
```

- [ ] **Step 2: Run**

Run: `cd apps/voice && .venv/bin/pytest tests/test_greeting_voice_drift.py -v`
Expected: PASS (clips were just rendered from the current voice in Task 5). To prove the guard bites, temporarily edit `manifest.voiceId`, re-run → FAIL, then revert.

- [ ] **Step 3: Commit**

```bash
git add apps/voice/tests/test_greeting_voice_drift.py
git commit -m "test(voice): CI guard — greeting clips must match configured voice_id"
```

---

## Task 7: Greeting player module

**Files:**
- Create: `apps/voice/client/src/greeting/greetingPlayer.ts`
- Test: `apps/voice/client/src/greeting/greetingPlayer.test.ts`

**Interfaces:**
- Produces: `playRandomGreeting(): { ended: Promise<void>; stop: () => void } | null` — loads the manifest (fetched from `/greetings/greetings.manifest.json`), picks a random clip, plays it on a fresh `Audio` element, resolves `ended` when the clip finishes (or immediately if no clips / playback rejected), and `stop()` halts + releases it. Returns `null` if the manifest has no clips.

- [ ] **Step 1: Write the failing test**

```typescript
// apps/voice/client/src/greeting/greetingPlayer.test.ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { playRandomGreeting } from "./greetingPlayer";

const MANIFEST = { voiceId: "v", model: "eleven_flash_v2_5", clips: [{ text: "hi", file: "greeting-1.mp3" }] };

afterEach(() => vi.restoreAllMocks());

describe("playRandomGreeting", () => {
  it("plays a clip and resolves ended when it finishes", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve(MANIFEST) }));
    const play = vi.fn().mockResolvedValue(undefined);
    vi.spyOn(window, "Audio").mockImplementation(() => {
      const el: Partial<HTMLAudioElement> & { _on: Record<string, () => void> } = {
        _on: {},
        play,
        pause: vi.fn(),
        addEventListener(ev: string, cb: () => void) { this._on[ev] = cb; },
      } as never;
      return el as HTMLAudioElement;
    });
    const handle = await playRandomGreeting();
    expect(handle).not.toBeNull();
    expect(play).toHaveBeenCalled();
  });
  it("returns null when the manifest has no clips", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve({ ...MANIFEST, clips: [] }) }));
    expect(await playRandomGreeting()).toBeNull();
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd apps/voice/client && npx vitest run src/greeting/greetingPlayer.test.ts`
Expected: FAIL (module not found).

- [ ] **Step 3: Implement**

```typescript
// apps/voice/client/src/greeting/greetingPlayer.ts
/**
 * Instant pre-rendered KPH greeting (slick-start Workstream B). Plays a
 * random clip the moment the tap gesture fires (which also unlocks iOS audio
 * playback), masking WebRTC connect latency. The clips are rendered from the
 * configured TTS voice at build time (scripts/render_greetings.py).
 */
interface GreetingManifest {
  voiceId: string;
  model: string;
  clips: { text: string; file: string }[];
}

export interface GreetingHandle {
  /** Resolves when the clip finishes, errors, or can't play — never rejects. */
  ended: Promise<void>;
  /** Halt playback and release the element (idempotent). */
  stop: () => void;
}

let manifestCache: GreetingManifest | null = null;

async function loadManifest(): Promise<GreetingManifest | null> {
  if (manifestCache) return manifestCache;
  try {
    const res = await fetch("/greetings/greetings.manifest.json");
    if (!res.ok) return null;
    manifestCache = (await res.json()) as GreetingManifest;
    return manifestCache;
  } catch {
    return null;
  }
}

export async function playRandomGreeting(): Promise<GreetingHandle | null> {
  const manifest = await loadManifest();
  if (!manifest || manifest.clips.length === 0) return null;
  const clip = manifest.clips[Math.floor(Math.random() * manifest.clips.length)];

  const audio = new Audio(`/greetings/${clip.file}`);
  let done: () => void;
  const ended = new Promise<void>((resolve) => { done = resolve; });
  const finish = () => done();
  audio.addEventListener("ended", finish);
  audio.addEventListener("error", finish);

  // play() may reject if autoplay is blocked; the tap gesture should permit it,
  // but resolve `ended` either way so the handoff never hangs.
  void audio.play().catch(finish);

  return {
    ended,
    stop: () => {
      try { audio.pause(); } catch { /* no-op */ }
      audio.src = "";
      finish();
    },
  };
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd apps/voice/client && npx vitest run src/greeting/greetingPlayer.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/voice/client/src/greeting/greetingPlayer.ts apps/voice/client/src/greeting/greetingPlayer.test.ts
git commit -m "feat(voice-client): pre-rendered greeting player (random clip on tap)"
```

---

## Task 8: Play the greeting on start + hold the Live handoff

**Files:**
- Modify: `apps/voice/client/src/transport/useVoiceSession.ts`

**Interfaces:**
- Consumes: `playRandomGreeting` (Task 7). The greeting plays inside `start()` right after mic grant; the "connected" outcome that drives `App`'s Live screen is deferred until BOTH the clip has ended AND the transport reached connected.

- [ ] **Step 1: Play the clip in `start()`**

In `useVoiceSession.ts`, import and add a ref for the handle:
```typescript
import { playRandomGreeting, type GreetingHandle } from "../greeting/greetingPlayer";
```
```typescript
  const greetingRef = useRef<GreetingHandle | null>(null);
  const greetingEndedRef = useRef<Promise<void>>(Promise.resolve());
```
In `start()`, right after the successful `mic.stream.getTracks().forEach(... stop())` + `dispatch({ type: "MIC_GRANTED" })` and BEFORE `await beginConnect()`:
```typescript
    // Instant greeting: play a random pre-rendered clip on this same gesture
    // (unlocks iOS audio). Runs concurrently with connect; the CONNECTED
    // handoff below waits for it so the greeting never overlaps live STT.
    greetingRef.current?.stop();
    const handle = await playRandomGreeting();
    greetingRef.current = handle;
    greetingEndedRef.current = handle ? handle.ended : Promise.resolve();
    await beginConnect();
```

- [ ] **Step 2: Defer the CONNECTED dispatch until the clip ends**

In `handleSessionEvent`, change the `CONNECTED` branch so the outcome only flips to connected after the greeting clip has finished (so `App` mounts `Live` only once KPH's opener is done and the pipeline is listening):

```typescript
      if (event.type === "CONNECTED") {
        wasConnectedRef.current = true;
        connectedAtRef.current = Date.now();
        setSessionSummary(null);
        retryControllerRef.current?.reportSuccess();
        setRetryStatus(IDLE_RETRY_STATUS);
        // Hold the visible "connected/Live" state until the greeting clip has
        // finished — greeting audio (speaker) and live STT (mic) must not overlap.
        void greetingEndedRef.current.then(() => dispatch(event));
        return;
      }
```

- [ ] **Step 3: Stop the clip on stop()/teardown**

In `stop()` add `greetingRef.current?.stop(); greetingRef.current = null;` alongside the existing cleanup, so a manual stop or a failed connect never leaves a clip playing.

- [ ] **Step 4: Test the deferred handoff**

```typescript
// apps/voice/client/src/transport/useVoiceSession.greeting.test.ts
// Mock ../greeting/greetingPlayer and ../media/getMic and ./voiceSession so
// that: requestMic resolves "granted"; playRandomGreeting returns a handle
// whose `ended` is a promise you control; the fake voiceSession fires a
// CONNECTED event immediately on connect(). Assert: outcome.state does NOT
// become "connected" until the greeting `ended` promise resolves, then does.
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

// (Mocks omitted here for brevity — follow the module-mock pattern already
// used by the client's transport tests; the ASSERTION is the contract:)
//   expect(result.current.outcome.state).not.toBe("connected"); // clip still playing
//   act(() => resolveGreetingEnded());
//   await waitFor(() => expect(result.current.outcome.state).toBe("connected"));
```
Implement the mocks concretely following the existing `useVoiceSession` test setup (mock `../media/getMic` → `{ status: "granted", stream: { getTracks: () => [] } }`, mock `./voiceSession.createVoiceSession` to return a `{ client: {}, connect: () => { onEvent({type:"CONNECTED"}); return Promise.resolve(); }, disconnect: async () => {} }`, mock `../greeting/greetingPlayer.playRandomGreeting` to return `{ ended: controllable, stop: vi.fn() }`).

- [ ] **Step 5: Run + typecheck**

Run: `cd apps/voice/client && npx vitest run src/transport/useVoiceSession.greeting.test.ts && npm run build`
Expected: PASS + clean build.

- [ ] **Step 6: Commit**

```bash
git add apps/voice/client/src/transport/useVoiceSession.ts apps/voice/client/src/transport/useVoiceSession.greeting.test.ts
git commit -m "feat(voice-client): play greeting on tap, hold Live handoff until clip ends"
```

---

## Task 9: Server greet-first config toggle + disable on WebRTC + persona tweak

**Files:**
- Modify: `apps/voice/src/klanker_voice/config.py` (`PersonaConfig.greet_first`)
- Modify: `apps/voice/pipeline.toml` (`[persona] greet_first = false`)
- Modify: `apps/voice/server.py` (guard `register_greet_first`)
- Modify: `apps/voice/prompts/concierge.md` (Opening move)
- Test: `apps/voice/tests/test_config.py` (extend, or create `test_greet_first_config.py`)

**Interfaces:**
- Produces: `cfg.persona.greet_first: bool` (default `True` for backward compatibility; `pipeline.toml` sets it `false`). `server.py` calls `register_greet_first(...)` only when true.

- [ ] **Step 1: Write the failing config test**

```python
# apps/voice/tests/test_greet_first_config.py
from pathlib import Path
from klanker_voice.config import load_config

def test_greet_first_defaults_true_when_absent(tmp_path: Path):
    toml = tmp_path / "p.toml"
    toml.write_text(_MINIMAL)  # a persona table WITHOUT greet_first
    assert load_config(toml).persona.greet_first is True

def test_greet_first_reads_false(tmp_path: Path):
    toml = tmp_path / "p.toml"
    toml.write_text(_MINIMAL.replace('prompt_path = "prompts/concierge.md"',
                                     'prompt_path = "prompts/concierge.md"\ngreet_first = false'))
    assert load_config(toml).persona.greet_first is False
```
Provide `_MINIMAL` = a smallest valid pipeline.toml body (copy the `[stt]/[turn]/[llm]/[tts]/[persona]` tables from an existing config-test fixture; `persona.prompt_path` must point at a real file — reuse the pattern already in `tests/test_config.py`).

- [ ] **Step 2: Implement config**

In `config.py`, add the field to `PersonaConfig`:
```python
@dataclass(frozen=True)
class PersonaConfig:
    prompt_path: Path
    greet_first: bool = True
```
In `load_config`'s persona section, read it:
```python
    persona = PersonaConfig(
        prompt_path=prompt_path,
        greet_first=bool(persona_table.get("greet_first", True)),
    )
```

- [ ] **Step 3: Set the toggle off in `pipeline.toml`**

```toml
[persona]
prompt_path = "prompts/concierge.md"
greet_first = false   # client plays a pre-rendered greeting on tap (slick-start); server must not greet twice
```

- [ ] **Step 4: Guard the server wiring**

In `server.py`, replace the unconditional call:
```python
    if built.config.persona.greet_first:
        register_greet_first(transport, worker, built.context)
```
(Use whatever handle to the loaded `PipelineConfig` is in scope — the `built`/`cfg` the function already holds; confirm the attribute path when editing.)

- [ ] **Step 5: Persona opening-move tweak**

In `prompts/concierge.md`, replace the "## Opening move" section so KPH does NOT re-introduce itself (the client clip is the opener):
```markdown
## Opening move

A short spoken greeting is played to the user the moment they connect (handled
outside your control), so DO NOT open by introducing yourself again. When the
user speaks their first turn, answer it directly and briefly. If they open with
a greeting, greet back in one line and invite their question — never repeat a
full self-introduction.
```

- [ ] **Step 6: Run tests**

Run: `cd apps/voice && .venv/bin/pytest tests/test_greet_first_config.py -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add apps/voice/src/klanker_voice/config.py apps/voice/pipeline.toml apps/voice/server.py apps/voice/prompts/concierge.md apps/voice/tests/test_greet_first_config.py
git commit -m "feat(voice): greet_first config toggle — disable server greeting on WebRTC (client owns opener)"
```

---

## Task 10: Full suite + manual verification

- [ ] **Step 1: Run the whole client + server suites**

Run: `cd apps/voice/client && npm run build && npx vitest run` (expect all green)
Run: `cd apps/voice && .venv/bin/pytest -q` (expect all green)

- [ ] **Step 2: Deploy via the (now-working) CI chain**

Push to `main`; `build-voice.yml` builds the client into the voice image and chains to deploy (the concurrency-deadlock + IAM fixes are already live — see memory `phase5-voice-pipeline-live.md`). Force single-task cutover if needed.

- [ ] **Step 3: Manual verification on a real iPhone (Safari)**

- Returning user (previously signed in): load `voice.klankermaker.ai` → observe one silent bounce during load → **single tap** → hear a random KPH greeting immediately → conversation is live and KPH is NOT greeting twice.
- Signed-out user: load in a fresh private tab → Attract loads instantly → tap → interactive sign-in → back → tap → talking (first-visit two-step, expected).
- Confirm barge-in still works once live; confirm the greeting and live audio don't overlap (handoff waits for clip end).

---

## Self-Review (completed during authoring)

- **Spec coverage:** Workstream A → Tasks 1–4 (+0 spike); Workstream B → Tasks 5–9; testing → each task + Task 10. Every spec section maps to a task.
- **Placeholders:** the two client test files with abbreviated mock bodies (Task 3 `location.assign` shim, Task 8 transport mocks) reference the existing client test patterns rather than inline every mock line — the assertion contract is explicit in both. All product code is complete.
- **Type consistency:** `markReturningUser/clearReturningUser/markSilentTried/wasSilentTried/isReturningUser` (Task 1) used consistently in Tasks 3–4; `playRandomGreeting → GreetingHandle{ended,stop}` (Task 7) used exactly in Task 8; `PersonaConfig.greet_first` (Task 9) matches `pipeline.toml` + `server.py`.

## Sequencing / notes

- **Task 0 gates Workstream A.** If `prompt=none` isn't honored, ship Workstream B alone (Tasks 5–10) — it's independently valuable — and open a follow-up for the issuer config.
- Workstreams A and B are otherwise independent; either order is fine.
- **Voice coupling:** when the retrained Kurt voice is swapped back into `pipeline.toml`, re-run `make -C apps/voice greetings` and commit — the Task 6 guard will fail CI otherwise.
