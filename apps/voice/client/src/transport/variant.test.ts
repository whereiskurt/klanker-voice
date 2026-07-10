import { describe, expect, it } from "vitest";
import { DEFAULT_VARIANT, variantFromPath } from "./variant";
import { buildConnectParams } from "./voiceSession";

describe("variantFromPath", () => {
  it("maps a known first segment to its variant", () => {
    expect(variantFromPath("/voice2")).toBe("voice2");
    expect(variantFromPath("/voice2/")).toBe("voice2");
    expect(variantFromPath("/VOICE2")).toBe("voice2");
    expect(variantFromPath("/voice1")).toBe("voice1");
  });

  it("defaults the root and unknown paths to voice1", () => {
    expect(variantFromPath("/")).toBe(DEFAULT_VARIANT);
    expect(variantFromPath("")).toBe(DEFAULT_VARIANT);
    expect(variantFromPath("/anything-else")).toBe(DEFAULT_VARIANT);
    expect(variantFromPath("/auth/callback")).toBe(DEFAULT_VARIANT);
  });
});

describe("buildConnectParams variant wiring", () => {
  it("keeps the bare endpoint for the default variant (voice1 unchanged)", () => {
    expect(buildConnectParams(null, "voice1").endpoint).toBe("/api/offer");
  });

  it("appends ?variant= for a non-default variant", () => {
    expect(buildConnectParams(null, "voice2").endpoint).toBe("/api/offer?variant=voice2");
  });

  it("still attaches the bearer header", () => {
    const params = buildConnectParams("jwt-abc", "voice2");
    expect((params.headers as Headers).get("Authorization")).toBe("Bearer jwt-abc");
  });
});
