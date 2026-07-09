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

/** The real greeting Audio element, armed (played-then-paused) inside the tap
 * gesture by `primeGreeting()`. Consumed exactly once by `playRandomGreeting()`
 * on Live mount, which RESUMES it instead of constructing a second element --
 * the resume is what makes the later, out-of-gesture `.play()` call permitted
 * by WebKit/Safari's autoplay policy. */
let primedGreeting: { audio: HTMLAudioElement } | null = null;

/** Injectable failure hook (in addition to the mandatory `console.warn`) so a
 * caller (e.g. telemetry) can observe a blocked/failed greeting play without
 * greetingPlayer.ts depending on any particular reporting mechanism. */
let onGreetingError: ((err: unknown) => void) | null = null;

export function setGreetingErrorHandler(fn: ((err: unknown) => void) | null): void {
  onGreetingError = fn;
}

/** The single place every greeting play/prime failure must flow through --
 * replaces every previous silent swallow. Never throws. */
function reportGreetingFailure(err: unknown): void {
  console.warn("[greetingPlayer] greeting playback failed", err);
  if (onGreetingError) {
    try {
      onGreetingError(err);
    } catch {
      /* a broken error handler must not break greeting playback */
    }
  }
}

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

// Early preload (voice-flow-redesign UX hardening): kick off the manifest
// fetch as soon as this module is imported, well before the tap gesture, so
// `primeGreeting()` (which must stay synchronous/gesture-safe and therefore
// reads `manifestCache` directly, never awaiting) usually finds it already
// populated. `loadManifest()` already try/catches a missing/failed fetch and
// resolves `null`, so this is safe to fire-and-forget under SSR/test too.
void loadManifest();

/**
 * iOS audio unlock (voice-flow-redesign Task 9). Play + immediately pause a
 * muted, silent audio element inside the start gesture so a LATER
 * `playRandomGreeting()` (fired on Live mount, after the ceremony) is
 * permitted by Safari's autoplay policy. No-op-safe: swallows a blocked play.
 */
export function unlockAudioPlayback(): void {
  try {
    const el = new Audio();
    el.muted = true;
    // A 1-sample silent wav data URI — enough to satisfy the gesture unlock.
    el.src =
      "data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEAgD4AAIA+AAABAAgAZGF0YQAAAAA=";
    void el.play().then(() => el.pause()).catch(() => { /* blocked: greeting will retry on mount */ });
  } catch { /* no Audio ctor (SSR/test): no-op */ }
}

/**
 * Arms the REAL greeting Audio element under user activation (voice-flow-redesign
 * UX hardening): must be called synchronously inside the tap gesture, right
 * alongside `unlockAudioPlayback()`. Reads `manifestCache` DIRECTLY (no await --
 * a gesture handler can't wait on a fetch) so it only does anything useful once
 * the early module-scope preload above has resolved; if it hasn't, or there is
 * no `Audio` constructor (SSR/test), this is a safe no-op and `playRandomGreeting()`
 * falls back to its own deferred load-and-play path on Live mount.
 *
 * Plays the element and immediately pauses it in the `.then()` -- this "arms"
 * it under the current user activation so a LATER, out-of-gesture `.play()`
 * call (the resume in `playRandomGreeting()`) is permitted by WebKit/Safari's
 * autoplay policy. Never throws out of the gesture handler.
 */
export function primeGreeting(): void {
  try {
    if (!manifestCache || manifestCache.clips.length === 0) return;
    if (typeof Audio === "undefined") return;
    const clip = manifestCache.clips[Math.floor(Math.random() * manifestCache.clips.length)];
    const audio = new Audio(`/greetings/${clip.file}`);
    // Store the primed element regardless of whether the arming play()
    // itself resolves -- a rejected arm doesn't preclude a later resume
    // attempt (and the failure is still surfaced below either way).
    primedGreeting = { audio };
    void audio
      .play()
      .then(() => {
        audio.pause();
        audio.currentTime = 0;
      })
      .catch((err: unknown) => reportGreetingFailure(err));
  } catch (err) {
    reportGreetingFailure(err);
  }
}

export async function playRandomGreeting(): Promise<GreetingHandle | null> {
  if (primedGreeting) {
    const { audio } = primedGreeting;
    primedGreeting = null; // consume: never resume the same element twice

    let done: () => void;
    const ended = new Promise<void>((resolve) => { done = resolve; });
    const finish = () => done();
    audio.addEventListener("ended", finish);
    audio.addEventListener("error", finish);

    audio.currentTime = 0;
    void audio.play().catch((err: unknown) => {
      reportGreetingFailure(err);
      finish();
    });

    return {
      ended,
      stop: () => {
        try { audio.pause(); } catch { /* no-op */ }
        audio.src = "";
        finish();
      },
    };
  }

  // Priming didn't happen (manifest not cached in time yet, or no Audio ctor
  // in SSR/test) -- best-effort deferred fallback, unchanged in spirit from
  // the original implementation.
  const manifest = await loadManifest();
  if (!manifest || manifest.clips.length === 0) return null;
  const clip = manifest.clips[Math.floor(Math.random() * manifest.clips.length)];

  const audio = new Audio(`/greetings/${clip.file}`);
  let done: () => void;
  const ended = new Promise<void>((resolve) => { done = resolve; });
  const finish = () => done();
  audio.addEventListener("ended", finish);
  audio.addEventListener("error", finish);

  // play() may reject if autoplay is blocked; resolve `ended` either way so
  // the handoff never hangs, and surface the failure instead of swallowing it.
  void audio.play().catch((err: unknown) => {
    reportGreetingFailure(err);
    finish();
  });

  return {
    ended,
    stop: () => {
      try { audio.pause(); } catch { /* no-op */ }
      audio.src = "";
      finish();
    },
  };
}
