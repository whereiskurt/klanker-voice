import Link from "next/link";

import { readSession, type LedgerRecord } from "@/lib/ledger";

/**
 * /admin/transcripts/[sessionId] (Plan 15-05, LEDG-03; redesigned
 * quick-260714-0wr): the threaded conversation view — "read every back and
 * forth like a convo." Turns render in turn_seq order as alternating
 * assistant/user bubbles with role avatars, timestamps, and interruption
 * markers; a carried ?q highlights matches in place.
 *
 * Security (T-15-05-02, stored-XSS-via-speech): `record.text` renders ONLY as
 * plain React text children (and <mark>-wrapped string segments) — never via
 * dangerouslySetInnerHTML — so a prompt-injected utterance stays literal
 * escaped text.
 */

function todayUtc(): string {
  return new Date().toISOString().slice(0, 10);
}

function formatTime(ts: number): string {
  return new Date(ts * 1000).toISOString().slice(11, 19);
}

function duration(from: number, to: number): string {
  const s = Math.max(0, Math.round(to - from));
  const m = Math.floor(s / 60);
  const sec = s % 60;
  return `${m}:${String(sec).padStart(2, "0")}`;
}

/** Case-insensitive highlight → React children with <mark> segments (XSS-safe). */
function highlight(text: string, q: string): React.ReactNode {
  if (!q) return text;
  const lower = text.toLowerCase();
  const needle = q.toLowerCase();
  const out: React.ReactNode[] = [];
  let i = 0;
  let n = 0;
  while (i < text.length) {
    const hit = lower.indexOf(needle, i);
    if (hit === -1) {
      out.push(text.slice(i));
      break;
    }
    if (hit > i) out.push(text.slice(i, hit));
    out.push(<mark key={n++}>{text.slice(hit, hit + q.length)}</mark>);
    i = hit + q.length;
  }
  return out;
}

function isPstn(first: LedgerRecord | undefined): boolean {
  return (
    !!first?.caller_id ||
    first?.channel === "telephony" ||
    first?.channel === "pstn"
  );
}

export default async function TranscriptDetailPage({
  params,
  searchParams,
}: {
  params: Promise<{ sessionId: string }>;
  searchParams: Promise<{ dt?: string; q?: string }>;
}) {
  const { sessionId } = await params;
  const { dt, q: rawQ } = await searchParams;
  const day = dt || todayUtc();
  const q = (rawQ || "").trim();

  const records = await readSession(sessionId, day);
  const first = records[0];
  const last = records[records.length - 1];
  const pstn = isPstn(first);
  const participant = first?.email || first?.caller_id || "anonymous";

  const backHref = `/admin/transcripts?dt=${day}${q ? `&q=${encodeURIComponent(q)}` : ""}`;

  return (
    <div>
      <Link className="tx-back" href={backHref}>
        ‹ back to {day}
      </Link>

      <section className="tx-convo-head">
        <div className="tx-eyebrow">
          {pstn ? "☎ Phone conversation" : "◈ Web conversation"}
        </div>
        <h1 className="tx-h1" style={{ fontSize: "clamp(1.5rem, 4vw, 2rem)" }}>
          {participant}
        </h1>
        <div className="tx-convo-meta">
          <span>
            <b>{records.length}</b> turns
          </span>
          {first && last ? (
            <span>
              <b>{duration(first.ts, last.ts)}</b> long
            </span>
          ) : null}
          {first ? (
            <span>
              {formatTime(first.ts)}
              {last ? `–${formatTime(last.ts)}` : ""} UTC
            </span>
          ) : null}
          {first?.tier_id ? <span>tier <b>{first.tier_id}</b></span> : null}
          {pstn && first?.did ? <span>DID <b>{first.did}</b></span> : null}
          <span>session <b>{sessionId.slice(0, 8)}</b></span>
        </div>
      </section>

      {records.length === 0 ? (
        <div className="tx-empty">
          <div className="tx-empty__mark">◍</div>
          <p>No turns found for this session on {day}.</p>
        </div>
      ) : (
        <div className="tx-thread">
          {records.map((record) => {
            const isUser = record.role === "user";
            return (
              <div
                key={record.turn_seq}
                className={`tx-turn ${isUser ? "tx-turn--user" : "tx-turn--agent"}`}
              >
                <div className={`tx-avatar tx-avatar--${isUser ? "user" : "agent"}`}>
                  {isUser ? (pstn ? "☎" : "◈") : "KV"}
                </div>
                <div className={`tx-bubble tx-bubble--${isUser ? "user" : "agent"}`}>
                  <div className="tx-bubble__head">
                    <span className="tx-bubble__role">
                      {isUser ? "Caller" : "KPH"}
                    </span>
                    <span>{formatTime(record.ts)}</span>
                    {record.interrupted ? (
                      <span className="tx-bubble__int">interrupted</span>
                    ) : null}
                  </div>
                  <div className="tx-bubble__text">
                    {q ? highlight(record.text, q) : record.text}
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
