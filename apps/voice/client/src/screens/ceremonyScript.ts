/** Theatrical boot-ceremony copy (voice-flow-redesign §3.3). Config constant
 * so the personality can be tuned without touching logic. Final line is the
 * "hold" line shown until the real connection lands. */
export const CEREMONY_SCRIPT: { line: string; sub: string }[] = [
  { line: "Initializing…", sub: "Waking-up the mic" },
  { line: "FAXing …", sub: "Sending /the/ signal" },
  { line: "Meshtasing …", sub: "Negotiating the connection" },
  { line: "[snap][crackle][pop]…", sub: "Almost there" },
  { line: "COMING IN HOT! …", sub: "Waiting for KPH to pick up" },
];

/** Per-line dwell. Total floor ≈ LINE_MS * CEREMONY_SCRIPT.length. */
export const LINE_MS = 850;
