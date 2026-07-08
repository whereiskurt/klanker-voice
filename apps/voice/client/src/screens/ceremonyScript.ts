/** Theatrical boot-ceremony copy (voice-flow-redesign §3.3). Config constant
 * so the personality can be tuned without touching logic. Final line is the
 * "hold" line shown until the real connection lands. */
export const CEREMONY_SCRIPT: { line: string; sub: string }[] = [
  { line: "initializing…", sub: "waking the mic" },
  { line: "paging KPH…", sub: "sending the signal" },
  { line: "let me see if he's out there…", sub: "negotiating the connection" },
  { line: "do do do…", sub: "almost there" },
  { line: "he's warming up…", sub: "waiting for KPH to pick up" },
];

/** Per-line dwell. Total floor ≈ LINE_MS * CEREMONY_SCRIPT.length. */
export const LINE_MS = 850;
