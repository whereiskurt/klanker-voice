import { afterEach, describe, expect, it, vi } from "vitest";
import { unlockAudioPlayback } from "./greetingPlayer";

const MANIFEST = { voiceId: "v", model: "eleven_flash_v2_5", clips: [{ text: "hi", file: "greeting-1.mp3" }] };

// `vi.resetModules()` + a dynamic re-import per test gives each test a fresh
// copy of `greetingPlayer.ts`'s module-level `manifestCache` -- without this,
// the first test's cached manifest would leak into the second test (Rule 1
// fix: the plan's verbatim test snippet assumed test-file isolation the
// module-level cache doesn't actually provide across cases in one file).
afterEach(() => { vi.restoreAllMocks(); vi.resetModules(); });

describe("playRandomGreeting", () => {
  it("plays a clip and resolves ended when it finishes", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve(MANIFEST) }));
    const play = vi.fn().mockResolvedValue(undefined);
    // A `function` expression (not an arrow function) -- vitest/jsdom invokes
    // this mock via `new Audio(...)`, and arrow functions have no
    // `[[Construct]]` internal slot ("X is not a constructor") (Rule 1 fix:
    // the plan's verbatim snippet used an arrow function).
    vi.spyOn(window, "Audio").mockImplementation(function GreetingAudioMock() {
      const on: Record<string, () => void> = {};
      const el: Partial<HTMLAudioElement> & { _on: Record<string, () => void> } = {
        _on: on,
        play,
        pause: vi.fn(),
        addEventListener(ev: string, cb: () => void) { on[ev] = cb; },
      } as never;
      return el as HTMLAudioElement;
    } as unknown as typeof Audio);
    const { playRandomGreeting } = await import("./greetingPlayer");
    const handle = await playRandomGreeting();
    expect(handle).not.toBeNull();
    expect(play).toHaveBeenCalled();
  });
  it("returns null when the manifest has no clips", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve({ ...MANIFEST, clips: [] }) }));
    const { playRandomGreeting } = await import("./greetingPlayer");
    expect(await playRandomGreeting()).toBeNull();
  });
});

describe("unlockAudioPlayback", () => {
  it("does not throw when called within a gesture", () => {
    expect(() => unlockAudioPlayback()).not.toThrow();
  });
});

/** A mock Audio element as constructed by `mockAudioCtor` below. */
interface MockAudioEl {
  play: () => Promise<void>;
  pause: () => void;
  currentTime: number;
  src: string;
  _on: Record<string, () => void>;
  addEventListener: (ev: string, cb: () => void) => void;
}

/** Builds a mock Audio constructor (same `function`-expression pattern as
 * above -- `new Audio()` needs a `[[Construct]]` slot) that records every
 * constructed element and lets a test fire `ended`/`error` handlers. */
function mockAudioCtor(playImpl: () => Promise<void>) {
  const elements: MockAudioEl[] = [];
  const ctor = function GreetingAudioMock() {
    const on: Record<string, () => void> = {};
    const el: MockAudioEl = {
      _on: on,
      play: vi.fn(playImpl),
      pause: vi.fn(),
      currentTime: 0,
      src: "",
      addEventListener(ev: string, cb: () => void) { on[ev] = cb; },
    };
    elements.push(el);
    return el as unknown as HTMLAudioElement;
  } as unknown as typeof Audio;
  return { ctor, elements };
}

/** Flushes both the microtask queue and one macrotask tick -- enough for the
 * module-scope `void loadManifest()` preload (fetch().then(json).then(...))
 * to settle before a test reads/consumes `manifestCache`. */
async function flushPreload() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

describe("primeGreeting + playRandomGreeting resume (Test A)", () => {
  it("primes the real audio element under the gesture and playRandomGreeting resumes the SAME element", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve(MANIFEST) }));
    const { ctor, elements } = mockAudioCtor(() => Promise.resolve());
    vi.spyOn(window, "Audio").mockImplementation(ctor as unknown as typeof Audio);

    const { primeGreeting, playRandomGreeting } = await import("./greetingPlayer");
    await flushPreload(); // module-scope preload populates manifestCache

    primeGreeting();
    await flushPreload(); // let primeGreeting's play().then(pause) microtasks settle

    expect(elements.length).toBe(1); // one Audio constructed by priming
    const primed = elements[0];
    expect(primed.play).toHaveBeenCalledTimes(1);
    expect(primed.pause).toHaveBeenCalledTimes(1); // armed: played then paused under activation

    const handle = await playRandomGreeting();
    expect(handle).not.toBeNull();
    expect(elements.length).toBe(1); // NO second Audio constructed -- the primed element was resumed
    expect(primed.play).toHaveBeenCalledTimes(2); // resumed via a second play()

    primed._on.ended();
    await expect(handle!.ended).resolves.toBeUndefined();
  });
});

describe("surfaced greeting failures (Test B)", () => {
  it("routes a rejected play() through console.warn + the injectable hook, and ended still resolves (never hangs)", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve(MANIFEST) }));
    const playError = new Error("NotAllowedError");
    const { ctor, elements } = mockAudioCtor(() => Promise.reject(playError));
    vi.spyOn(window, "Audio").mockImplementation(ctor as unknown as typeof Audio);
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});

    const { primeGreeting, playRandomGreeting, setGreetingErrorHandler } = await import("./greetingPlayer");
    await flushPreload();

    const onError = vi.fn();
    setGreetingErrorHandler(onError);

    primeGreeting();
    await flushPreload();
    expect(warnSpy).toHaveBeenCalled();
    expect(onError).toHaveBeenCalledWith(playError);
    expect(elements.length).toBe(1); // primed element still stored even though its arming play() rejected

    warnSpy.mockClear();
    onError.mockClear();

    const handle = await playRandomGreeting();
    expect(handle).not.toBeNull(); // rejection never surfaces as a thrown error
    // ended resolves even though the resumed play() rejected -- never hangs.
    await expect(handle!.ended).resolves.toBeUndefined();
    expect(warnSpy).toHaveBeenCalled();
    expect(onError).toHaveBeenCalledWith(playError);
  });
});

describe("playRandomGreeting fallback when priming did not happen (Test C)", () => {
  it("falls back to deferred load-and-play, returns a handle, and never throws even on a rejected play()", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve(MANIFEST) }));
    const playError = new Error("blocked");
    const { ctor, elements } = mockAudioCtor(() => Promise.reject(playError));
    vi.spyOn(window, "Audio").mockImplementation(ctor as unknown as typeof Audio);
    const warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});

    // No primeGreeting() call at all -- exercises the pure fallback path.
    const { playRandomGreeting } = await import("./greetingPlayer");
    await flushPreload();

    const handle = await playRandomGreeting();
    expect(handle).not.toBeNull();
    expect(elements.length).toBe(1);
    await expect(handle!.ended).resolves.toBeUndefined(); // never hangs on a rejected play()
    expect(warnSpy).toHaveBeenCalled(); // rejection is surfaced, not silently swallowed
  });

  it("returns null when the manifest has no clips and no priming happened", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve({ ...MANIFEST, clips: [] }) }));
    const { playRandomGreeting } = await import("./greetingPlayer");
    await flushPreload();
    expect(await playRandomGreeting()).toBeNull();
  });
});
