import { describe, it, expect } from "vitest";
import { normalizeE164 } from "../phone-normalization";

/**
 * RED (Plan 12-02 Task 1): asserts the canonical E.164 normalization
 * behavior documented in the task's <behavior> block and 12-RESEARCH.md's
 * "E.164 Normalization" section / Example 1. `normalizeE164` does not exist
 * yet at this point, so the import above fails — that import failure IS the
 * RED signal this task's <verify> greps for.
 *
 * This is the SINGLE normalization source used on both write (kv code phone
 * / entity set) and lookup (/tel route + the 12-06 telephony controller) per
 * 12-RESEARCH.md Pitfall 3 — normalization defined once, reused everywhere.
 */
describe("normalizeE164", () => {
  it("normalizes a spaced/parenthesized/dashed North American number to canonical E.164", () => {
    expect(normalizeE164("+1 (416) 555-1234")).toBe("+14165551234");
  });

  it("normalizes a dash-separated number with a leading trunk 1 to canonical E.164", () => {
    expect(normalizeE164("1-416-555-1234")).toBe("+14165551234");
  });

  it("assumes +1 North America for a bare 10-digit local number", () => {
    expect(normalizeE164("416-555-1234")).toBe("+14165551234");
  });

  it("is idempotent on an already-canonical E.164 number", () => {
    expect(normalizeE164("+14165551234")).toBe("+14165551234");
  });

  it("returns '' for empty, null, and undefined input", () => {
    expect(normalizeE164("")).toBe("");
    expect(normalizeE164(null)).toBe("");
    expect(normalizeE164(undefined)).toBe("");
  });
});
