import { afterEach, describe, expect, it, vi } from "vitest";
import { requestMic } from "./getMic";

function stubGetUserMedia(impl: (constraints?: MediaStreamConstraints) => Promise<MediaStream>): void {
  Object.defineProperty(navigator, "mediaDevices", {
    configurable: true,
    value: { getUserMedia: impl },
  });
}

function clearMediaDevices(): void {
  Object.defineProperty(navigator, "mediaDevices", {
    configurable: true,
    value: undefined,
  });
}

const FAKE_STREAM = { id: "fake-stream" } as unknown as MediaStream;

afterEach(() => {
  clearMediaDevices();
  vi.restoreAllMocks();
});

describe("requestMic", () => {
  it("returns granted with the MediaStream on success", async () => {
    stubGetUserMedia(() => Promise.resolve(FAKE_STREAM));
    const result = await requestMic();
    expect(result.status).toBe("granted");
    expect(result).toEqual({ status: "granted", stream: FAKE_STREAM });
  });

  it("classifies NotAllowedError as denied", async () => {
    stubGetUserMedia(() => Promise.reject(new DOMException("blocked", "NotAllowedError")));
    const result = await requestMic();
    expect(result.status).toBe("denied");
  });

  it("classifies SecurityError as denied", async () => {
    stubGetUserMedia(() => Promise.reject(new DOMException("blocked", "SecurityError")));
    const result = await requestMic();
    expect(result.status).toBe("denied");
  });

  it("classifies NotFoundError as no-device", async () => {
    stubGetUserMedia(() => Promise.reject(new DOMException("no mic", "NotFoundError")));
    const result = await requestMic();
    expect(result.status).toBe("no-device");
  });

  it("classifies OverconstrainedError as no-device", async () => {
    stubGetUserMedia(() => Promise.reject(new DOMException("bad constraints", "OverconstrainedError")));
    const result = await requestMic();
    expect(result.status).toBe("no-device");
  });

  it("classifies an unrecognized error as the denied-style fallback", async () => {
    stubGetUserMedia(() => Promise.reject(new DOMException("weird", "NotReadableError")));
    const result = await requestMic();
    expect(result.status).toBe("denied");
  });

  it("returns unsupported without calling getUserMedia when the API is absent", async () => {
    clearMediaDevices();
    const result = await requestMic();
    expect(result.status).toBe("unsupported");
  });
});
