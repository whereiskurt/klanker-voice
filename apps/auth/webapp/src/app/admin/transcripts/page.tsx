import Link from "next/link";

import { listSessions, readSession } from "@/lib/ledger";

/**
 * /admin/transcripts (Plan 15-05, LEDG-03): session-list-by-day report — the
 * day picker + drill-in list that opens onto the threaded chat detail page.
 *
 * listSessions() derives distinct sessions from S3 object keys alone (no
 * body reads). To label each row with its participant (email or caller_id,
 * per CONTEXT's PSTN-identity decision) and a real turn count, this page
 * reads each session's full turn list via readSession() — a handful of
 * small GetObjects per page view, well within the "≤25 users, no scaling
 * concerns" posture this phase is designed around.
 */

function todayUtc(): string {
  return new Date().toISOString().slice(0, 10);
}

export default async function TranscriptsPage({
  searchParams,
}: {
  searchParams: Promise<{ dt?: string }>;
}) {
  const params = await searchParams;
  const day = params.dt || todayUtc();

  const summaries = await listSessions(day);
  const rows = await Promise.all(
    summaries.map(async (summary) => {
      const records = await readSession(summary.sessionId, day);
      const first = records[0];
      const participant = first?.email || first?.caller_id || "anonymous";
      return {
        ...summary,
        participant,
        turnCount: records.length,
      };
    })
  );

  return (
    <div>
      <h1>Transcripts — {day}</h1>
      <form method="get" style={{ margin: "1rem 0" }}>
        <label>
          Day (UTC):{" "}
          <input type="date" name="dt" defaultValue={day} />
        </label>{" "}
        <button type="submit">Go</button>
      </form>
      {rows.length === 0 ? (
        <p>No sessions for {day}.</p>
      ) : (
        <ul style={{ listStyle: "none", padding: 0 }}>
          {rows.map((row) => (
            <li
              key={row.sessionId}
              style={{ padding: "0.5rem 0", borderBottom: "1px solid #333" }}
            >
              <Link href={`/admin/transcripts/${encodeURIComponent(row.sessionId)}?dt=${day}`}>
                {row.participant} — {row.turnCount} turn{row.turnCount === 1 ? "" : "s"} —
                first seen {row.firstSeenHms} UTC
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
