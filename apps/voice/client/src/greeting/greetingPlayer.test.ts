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
