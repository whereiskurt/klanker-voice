import Link from "next/link";

import { readSession } from "@/lib/ledger";

/**
 * /admin/transcripts/[sessionId] (Plan 15-05, LEDG-03): the threaded
 * conversation detail view — the acceptance bar ("I want to easily see
 * every back and forth like a convo"). Turns render in turn_seq order as
 * alternating user/assistant bubbles.
 *
 * Security (T-15-05-02, stored-XSS-via-speech): `record.text` is rendered
 * ONLY as a plain React text child below — never via dangerouslySetInnerHTML
 * — so a prompt-injected utterance containing HTML/script markup renders as
 * literal escaped text, never as markup.
 */

function todayUtc(): string {
  return new Date().toISOString().slice(0, 10);
}

function formatTime(ts: number): string {
  return new Date(ts * 1000).toISOString().slice(11, 19) + " UTC";
}

export default async function TranscriptDetailPage({
  params,
  searchParams,
}: {
  params: Promise<{ sessionId: string }>;
  searchParams: Promise<{ dt?: string }>;
}) {
  const { sessionId } = await params;
  const { dt } = await searchParams;
  const day = dt || todayUtc();

  const records = await readSession(sessionId, day);

  return (
    <div>
      <p>
        <Link href={`/admin/transcripts?dt=${day}`}>&larr; back to {day}</Link>
      </p>
      <h1>Session {sessionId}</h1>
      {records.length === 0 ? (
        <p>No turns found for this session on {day}.</p>
      ) : (
        <ol style={{ listStyle: "none", padding: 0, margin: 0 }}>
          {records.map((record) => {
            const isUser = record.role === "user";
            return (
              <li
                key={record.turn_seq}
                style={{
                  display: "flex",
                  justifyContent: isUser ? "flex-end" : "flex-start",
                  margin: "0.5rem 0",
                }}
              >
                <div
                  style={{
                    maxWidth: "70%",
                    padding: "0.5rem 0.75rem",
                    borderRadius: "0.75rem",
                    background: isUser ? "#2563eb22" : "#33333322",
                  }}
                >
                  <div style={{ fontSize: "0.75rem", opacity: 0.7 }}>
                    {record.role} &middot; {formatTime(record.ts)}
                    {record.interrupted ? " · interrupted" : ""}
                  </div>
                  <div>{record.text}</div>
                </div>
              </li>
            );
          })}
        </ol>
      )}
    </div>
  );
}
