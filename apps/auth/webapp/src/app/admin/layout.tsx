import type { Metadata } from "next";
import { notFound } from "next/navigation";

import { auth } from "@/config/auth";

/**
 * /admin gate (Plan 15-05, T-15-05-01): an ADMIN_EMAILS allowlist check in
 * front of every /admin route. Non-admins (including no session at all) get
 * notFound() — a 404, never a 403 or a redirect-to-login — so the route's
 * existence is never disclosed (approved spec,
 * docs/superpowers/specs/2026-07-06-admin-panel-design.md).
 *
 * There is no top-level src/app/layout.tsx (only the (authlogin) route
 * group defines one), so — like that route group — this layout is its own
 * effective root layout and must include the <html>/<body> shell.
 *
 * Scope is deliberately gate + shell ONLY. Users/codes/kill-switch panels
 * remain Phase 05.1's scope; this phase ships only the transcripts report
 * (LEDG-03) as the first child route.
 */

export const metadata: Metadata = {
  title: "klanker-voice admin",
};

function adminEmails(): string[] {
  return (process.env.ADMIN_EMAILS ?? "")
    .split(",")
    .map((email) => email.trim().toLowerCase())
    .filter(Boolean);
}

export default async function AdminLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const admins = adminEmails();
  const session = await auth();
  const sessionEmail = session?.user?.email?.toLowerCase();

  if (!sessionEmail || !admins.includes(sessionEmail)) {
    notFound();
  }

  return (
    <html lang="en">
      <body style={{ margin: 0, fontFamily: "system-ui, sans-serif" }}>
        <header
          style={{
            padding: "1rem 1.5rem",
            borderBottom: "1px solid #333",
          }}
        >
          <strong>klanker-voice admin</strong>
        </header>
        <main style={{ padding: "1.5rem" }}>{children}</main>
      </body>
    </html>
  );
}
