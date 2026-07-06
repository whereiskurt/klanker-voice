import { act, createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it } from "vitest";
import { RTVIEvent, type PipecatClient } from "@pipecat-ai/client-js";
import {
  INITIAL_LATENCY_METRICS,
  formatP50Seconds,
  formatStageMs,
  reduceLatencyMessage,
  useHudOpen,
  useLatencyMetrics,
  type LatencyMetrics,
} from "./useLatencyMetrics";

// Same tiny render-harness pattern as `useCountdown.test.ts` -- no
// react-hooks-testing-library in this project's devDependencies (Rule 3
// excludes package installs from auto-fix).
function renderHook<T>(useHook: () => T) {
  const container = document.createElement("div");
  let latest!: T;
  let root!: Root;

  function Harness() {
    latest = useHook();
    return null;
  }

  act(() => {
    root = createRoot(container);
    root.render(createElement(Harness));
  });

  return {
    get value(): T {
      return latest;
    },
    rerender() {
      act(() => {
        root.render(createElement(Harness));
      });
    },
    unmount() {
      act(() => root.unmount());
    },
  };
}

const SAMPLE_KMV_LATENCY = {
  type: "kmv-latency",
  data: {
    stt_ms: 142,
    llm_ttft_ms: 318,
    tts_first_audio_ms: 76,
    voice_to_voice_ms: 1402,
    v2v_p50_ms: 1400,
  },
};

describe("reduceLatencyMessage", () => {
  it("reduces a kmv-latency server message into the latest per-stage values", () => {
    const next = reduceLatencyMessage(INITIAL_LATENCY_METRICS, SAMPLE_KMV_LATENCY);
    expect(next).toEqual({
      sttMs: 142,
      llmTtftMs: 318,
      ttsFirstAudioMs: 76,
      voiceToVoiceMs: 1402,
      v2vP50Ms: 1400,
    });
  });

  it("ignores a non-kmv-latency message, returning current unchanged", () => {
    const next = reduceLatencyMessage(INITIAL_LATENCY_METRICS, { type: "something-else", data: {} });
    expect(next).toBe(INITIAL_LATENCY_METRICS);
  });

  it("ignores malformed/non-object messages without throwing", () => {
    expect(() => reduceLatencyMessage(INITIAL_LATENCY_METRICS, null)).not.toThrow();
    expect(() => reduceLatencyMessage(INITIAL_LATENCY_METRICS, "oops")).not.toThrow();
    expect(reduceLatencyMessage(INITIAL_LATENCY_METRICS, undefined)).toBe(INITIAL_LATENCY_METRICS);
  });

  it("carries a null (never-observed) stage through as null, not 0", () => {
    const message = {
      type: "kmv-latency",
      data: {
        stt_ms: null,
        llm_ttft_ms: 318,
        tts_first_audio_ms: null,
        voice_to_voice_ms: 900,
        v2v_p50_ms: null,
      },
    };
    const next = reduceLatencyMessage(INITIAL_LATENCY_METRICS, message);
    expect(next.sttMs).toBeNull();
    expect(next.ttsFirstAudioMs).toBeNull();
    expect(next.v2vP50Ms).toBeNull();
    expect(next.llmTtftMs).toBe(318);
  });
});

describe("formatStageMs / formatP50Seconds", () => {
  it("renders a dash placeholder for a null stage, never 0 or a crash", () => {
    expect(formatStageMs(null)).toBe("—");
    expect(formatP50Seconds(null)).toBe("—");
  });

  it("renders real values in the UI-SPEC HUD units", () => {
    expect(formatStageMs(142)).toBe("142 ms");
    expect(formatP50Seconds(1400)).toBe("1.40 s");
  });
});

describe("useLatencyMetrics", () => {
  function createFakeClient() {
    const handlers = new Map<string, (data: unknown) => void>();
    const client = {
      on: (event: string, handler: (data: unknown) => void) => {
        handlers.set(event, handler);
      },
      off: (event: string) => {
        handlers.delete(event);
      },
    } as unknown as PipecatClient;
    return {
      client,
      emitServerMessage(data: unknown) {
        handlers.get(RTVIEvent.ServerMessage)?.(data);
      },
    };
  }

  it("starts all-null (dash) before any message arrives", () => {
    const { client } = createFakeClient();
    const hook = renderHook(() => useLatencyMetrics(client));
    const metrics: LatencyMetrics = hook.value;
    expect(metrics.sttMs).toBeNull();
    expect(metrics.v2vP50Ms).toBeNull();
    hook.unmount();
  });

  it("updates from a live kmv-latency serverMessage event", () => {
    const { client, emitServerMessage } = createFakeClient();
    const hook = renderHook(() => useLatencyMetrics(client));

    act(() => {
      emitServerMessage(SAMPLE_KMV_LATENCY);
    });

    expect(hook.value.sttMs).toBe(142);
    expect(hook.value.v2vP50Ms).toBe(1400);
    hook.unmount();
  });

  it("reports all-null when there is no client yet (not connected)", () => {
    const hook = renderHook(() => useLatencyMetrics(null));
    expect(hook.value).toEqual(INITIAL_LATENCY_METRICS);
    hook.unmount();
  });
});

describe("useHudOpen", () => {
  afterEach(() => {
    // Nothing to restore globally -- each test's harness unmounts its own
    // keydown listener via the hook's cleanup.
  });

  it("defaults closed (CLNT-06: pristine for audiences)", () => {
    const hook = renderHook(() => useHudOpen());
    expect(hook.value[0]).toBe(false);
    hook.unmount();
  });

  it("toggles open on an 'H' keydown, and closed again on a second press", () => {
    const hook = renderHook(() => useHudOpen());

    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "h" }));
    });
    expect(hook.value[0]).toBe(true);

    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "h" }));
    });
    expect(hook.value[0]).toBe(false);
    hook.unmount();
  });

  it("is case-insensitive ('H' shift-key variant)", () => {
    const hook = renderHook(() => useHudOpen());
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "H" }));
    });
    expect(hook.value[0]).toBe(true);
    hook.unmount();
  });

  it("ignores other keys", () => {
    const hook = renderHook(() => useHudOpen());
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "j" }));
    });
    expect(hook.value[0]).toBe(false);
    hook.unmount();
  });

  it("toggles via the returned toggle() function (the affordance's onClick)", () => {
    const hook = renderHook(() => useHudOpen());
    act(() => {
      hook.value[1]();
    });
    expect(hook.value[0]).toBe(true);
    hook.unmount();
  });
});
