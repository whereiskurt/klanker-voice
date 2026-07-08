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
