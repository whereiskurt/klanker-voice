import Link from "next/link";

import { listSessions, readSession, type LedgerRecord } from "@/lib/ledger";

/**
 * /admin/transcripts (Plan 15-05, LEDG-03; redesigned quick-260714-0wr): the
 * operator conversation index — a day view of every session with a
 * cross-turn search box, web/PSTN channel badges, and a match snippet.
 *
 * listSessions() derives distinct sessions from S3 object keys alone; this
 * page then reads each session's turns (readSession) to label the row with
 * its participant, channel, turn count, time span, and — when a search query
 * is present — the first matching turn as a highlighted snippet. A handful of
 * small GetObjects per view, well within this phase's "≤25 users" posture.
 */

function todayUtc(): string {
  return new Date().toISOString().slice(0, 10);
}

/** Shift a YYYY-MM-DD day by ±1 (UTC), returning YYYY-MM-DD. */
function shiftDay(day: string, delta: number): string {
  const [y, m, d] = day.split("-").map(Number);
  const dt = new Date(Date.UTC(y, m - 1, d + delta));
  return dt.toISOString().slice(0, 10);
}

function hms(seconds: number): string {
  return new Date(seconds * 1000).toISOString().slice(11, 19);
}

/** mm:ss (or h:mm:ss) span between two epoch seconds. */
function duration(from: number, to: number): string {
  const s = Math.max(0, Math.round(to - from));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const pad = (n: number) => String(n).padStart(2, "0");
  return h > 0 ? `${h}:${pad(m)}:${pad(sec)}` : `${m}:${pad(sec)}`;
}

interface Channel {
  kind: "web" | "pstn";
  label: string;
  glyph: string;
}

function channelOf(first: LedgerRecord | undefined): Channel {
  const isPstn =
    !!first?.caller_id ||
    first?.channel === "telephony" ||
    first?.channel === "pstn";
  return isPstn
    ? { kind: "pstn", label: "PSTN", glyph: "☎" }
    : { kind: "web", label: "Web", glyph: "◈" };
}

/**
 * Splits `text` around case-insensitive matches of `q` and returns React
 * children with matches wrapped in <mark>. Each segment is a plain string
 * child (never dangerouslySetInnerHTML) — a prompt-injected utterance stays
 * literal escaped text (stored-XSS guard, T-15-05-02).
 */
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

/** A ±60-char window around the first match, for a compact card snippet. */
function snippetAround(text: string, q: string): string {
  const idx = text.toLowerCase().indexOf(q.toLowerCase());
  if (idx === -1) return text.slice(0, 140);
  const start = Math.max(0, idx - 60);
  const end = Math.min(text.length, idx + q.length + 60);
  return (start > 0 ? "…" : "") + text.slice(start, end) + (end < text.length ? "…" : "");
}

export default async function TranscriptsPage({
  searchParams,
}: {
  searchParams: Promise<{ dt?: string; q?: string }>;
}) {
  const params = await searchParams;
  const day = params.dt || todayUtc();
  const q = (params.q || "").trim();

  const summaries = await listSessions(day);
  const enriched = await Promise.all(
    summaries.map(async (summary) => {
      const records = await readSession(summary.sessionId, day);
      const first = records[0];
      const last = records[records.length - 1];
      const channel = channelOf(first);
      const participant =
        first?.email || first?.caller_id || "anonymous";
      // Cross-turn search: match participant OR any turn's text.
      const match = q
        ? (participant.toLowerCase().includes(q.toLowerCase())
            ? { record: first, kind: "who" as const }
            : records.find((r) => r.text.toLowerCase().includes(q.toLowerCase()))
              ? {
                  record: records.find((r) =>
                    r.text.toLowerCase().includes(q.toLowerCase())
                  )!,
                  kind: "turn" as const,
                }
              : null)
        : null;
      return {
        summary,
        participant,
        channel,
        tier: first?.tier_id ?? null,
        turnCount: records.length,
        firstTs: first?.ts,
        lastTs: last?.ts,
        match,
      };
    })
  );

  const rows = q ? enriched.filter((r) => r.match) : enriched;
  const totalTurns = rows.reduce((n, r) => n + r.turnCount, 0);
  const pstnCount = rows.filter((r) => r.channel.kind === "pstn").length;

  const qs = (extra: Record<string, string>) => {
    const sp = new URLSearchParams({ dt: day, ...(q ? { q } : {}), ...extra });
    return `?${sp.toString()}`;
  };

  return (
    <div>
      <section className="tx-hero">
        <div className="tx-eyebrow">Conversation ledger</div>
        <h1 className="tx-h1">Transcripts</h1>

        <form method="get" className="tx-controls">
          <div className="tx-daynav">
            <Link
              className="tx-btn"
              href={`?dt=${shiftDay(day, -1)}${q ? `&q=${encodeURIComponent(q)}` : ""}`}
              aria-label="Previous day"
            >
              ‹
            </Link>
            <input
              className="tx-date"
              type="date"
              name="dt"
              defaultValue={day}
              aria-label="Day (UTC)"
            />
            <Link
              className="tx-btn"
              href={`?dt=${shiftDay(day, 1)}${q ? `&q=${encodeURIComponent(q)}` : ""}`}
              aria-label="Next day"
            >
              ›
            </Link>
          </div>
          <div className="tx-search">
            <span className="tx-search__icon">⌕</span>
            <input
              type="search"
              name="q"
              defaultValue={q}
              placeholder="Search participants and every spoken turn…"
              autoComplete="off"
            />
          </div>
          <button className="tx-btn tx-btn--brand" type="submit">
            Go
          </button>
          {q ? (
            <Link className="tx-btn" href={`?dt=${day}`}>
              Clear
            </Link>
          ) : null}
        </form>

        <div className="tx-summary">
          <span>
            <b>{rows.length}</b> session{rows.length === 1 ? "" : "s"}
            {q ? <span className="tx-faint"> matching “{q}”</span> : null}
          </span>
          <span>
            <b>{totalTurns}</b> turn{totalTurns === 1 ? "" : "s"}
          </span>
          <span>
            <b>{pstnCount}</b> phone · <b>{rows.length - pstnCount}</b> web
          </span>
          <span className="mono tx-faint">{day} UTC</span>
        </div>
      </section>

      {rows.length === 0 ? (
        <div className="tx-empty">
          <div className="tx-empty__mark">◍</div>
          <p>
            {q
              ? `No sessions on ${day} match “${q}”.`
              : `No conversations recorded on ${day}.`}
          </p>
        </div>
      ) : (
        <div className="tx-list">
          {rows.map((r) => (
            <Link
              key={r.summary.sessionId}
              className="tx-card"
              href={`/admin/transcripts/${encodeURIComponent(r.summary.sessionId)}${qs({})}`}
            >
              <div className="tx-card__top">
                <span className={`tx-badge tx-badge--${r.channel.kind}`}>
                  {r.channel.glyph} {r.channel.label}
                </span>
                <span className="tx-card__who">
                  {q ? highlight(r.participant, q) : r.participant}
                </span>
                <span className="tx-card__spacer" />
                <span className="tx-card__time">
                  {r.firstTs != null ? hms(r.firstTs) : r.summary.firstSeenHms}
                  {r.firstTs != null && r.lastTs != null
                    ? ` · ${duration(r.firstTs, r.lastTs)}`
                    : ""}
                </span>
              </div>
              <div className="tx-card__meta">
                <span>
                  {r.turnCount} turn{r.turnCount === 1 ? "" : "s"}
                </span>
                {r.tier ? <span>tier {r.tier}</span> : null}
                <span>{r.summary.sessionId.slice(0, 8)}</span>
              </div>
              {r.match?.kind === "turn" ? (
                <div className="tx-card__snippet">
                  <span className="role">{r.match.record.role}</span>
                  {highlight(snippetAround(r.match.record.text, q), q)}
                </div>
              ) : null}
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
