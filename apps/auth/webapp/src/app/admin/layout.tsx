import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { auth } from "@/config/auth";
import { fontSans, fontMono, fontMuseo } from "@/config/fonts";

/**
 * /admin gate (Plan 15-05, T-15-05-01): an ADMIN_EMAILS allowlist check in
 * front of every /admin route. Non-admins (including no session at all) get
 * notFound() — a 404, never a 403 or a redirect-to-login — so the route's
 * existence is never disclosed (approved spec,
 * docs/superpowers/specs/2026-07-06-admin-panel-design.md).
 *
 * There is no top-level src/app/layout.tsx (only the (authlogin) route
 * group defines one), so — like that route group — this layout is its own
 * effective root layout and must include the <html>/<body> shell. It ships a
 * scoped "operator console" design system (dark, editorial-terminal) inline
 * so the admin routes render polished without depending on the Tailwind /
 * globals.css pipeline the (authlogin) group uses.
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

// Scoped design system — dark operator console. Brand violet (#686EA0) lifted
// for dark-bg contrast; MuseoModerno for display, Fira Code for technical
// metadata (ids, timestamps, hashes), Inter for prose.
const CONSOLE_CSS = `
:root {
  --bg: #0b0d12;
  --bg-2: #10131b;
  --surface: #151925;
  --surface-2: #1b2030;
  --line: #262c3d;
  --line-soft: #1e2331;
  --text: #e7e9f2;
  --text-dim: #949cb3;
  --text-faint: #6b7488;
  --brand: #8b91d6;
  --brand-dim: #686ea0;
  --user: #d8a657;
  --user-soft: #d8a65722;
  --agent: #7cc4b0;
  --agent-soft: #7cc4b01e;
  --pstn: #d97a8f;
  --web: #7c9ed9;
  --radius: 14px;
}
* { box-sizing: border-box; }
body {
  margin: 0;
  background:
    radial-gradient(1200px 600px at 85% -10%, #1a1f34 0%, transparent 55%),
    radial-gradient(900px 500px at -5% 5%, #171b2b 0%, transparent 50%),
    var(--bg);
  color: var(--text);
  font-family: var(--font-sans), system-ui, sans-serif;
  -webkit-font-smoothing: antialiased;
  line-height: 1.5;
  min-height: 100vh;
}
a { color: inherit; text-decoration: none; }
.mono { font-family: var(--font-mono), ui-monospace, monospace; }

/* ---- shell ---- */
.tx-shell { max-width: 940px; margin: 0 auto; padding: 0 1.25rem 4rem; }
.tx-topbar {
  position: sticky; top: 0; z-index: 10;
  display: flex; align-items: center; gap: 0.7rem;
  padding: 0.9rem 1.25rem;
  background: color-mix(in srgb, var(--bg) 82%, transparent);
  backdrop-filter: blur(10px);
  border-bottom: 1px solid var(--line-soft);
}
.tx-topbar__mark {
  width: 26px; height: 26px; border-radius: 7px;
  display: grid; place-items: center;
  background: linear-gradient(150deg, var(--brand) 0%, var(--brand-dim) 100%);
  color: #0b0d12; font-weight: 800; font-size: 0.85rem;
  font-family: var(--font-museo), var(--font-sans), sans-serif;
}
.tx-topbar__title {
  font-family: var(--font-museo), var(--font-sans), sans-serif;
  font-weight: 700; letter-spacing: 0.02em; font-size: 0.98rem;
}
.tx-topbar__crumb { color: var(--text-dim); font-size: 0.82rem; margin-left: 0.1rem; }
.tx-topbar__spacer { flex: 1; }
.tx-topbar__who { color: var(--text-dim); font-size: 0.78rem; }
.tx-topbar__who b { color: var(--text); font-weight: 600; }

/* ---- generic ---- */
.tx-eyebrow {
  font-family: var(--font-mono), monospace;
  text-transform: uppercase; letter-spacing: 0.22em;
  font-size: 0.66rem; color: var(--brand);
}
.tx-h1 {
  font-family: var(--font-museo), var(--font-sans), sans-serif;
  font-weight: 700; font-size: clamp(1.9rem, 5vw, 2.7rem);
  letter-spacing: -0.01em; margin: 0.35rem 0 0;
}
.tx-muted { color: var(--text-dim); }
.tx-faint { color: var(--text-faint); }
mark { background: var(--brand); color: #0b0d12; border-radius: 3px; padding: 0 2px; }

/* ---- hero / controls ---- */
.tx-hero { padding: 2.4rem 0 1.4rem; }
.tx-controls {
  display: flex; flex-wrap: wrap; align-items: stretch; gap: 0.6rem;
  margin-top: 1.4rem;
}
.tx-daynav { display: inline-flex; align-items: stretch; gap: 0.35rem; }
.tx-btn {
  display: inline-flex; align-items: center; justify-content: center;
  min-width: 2.3rem; padding: 0 0.85rem; height: 2.5rem;
  background: var(--surface); border: 1px solid var(--line);
  border-radius: 10px; color: var(--text); font-size: 0.85rem;
  font-family: var(--font-sans), sans-serif; cursor: pointer;
  transition: border-color .15s, background .15s, transform .05s;
}
.tx-btn:hover { border-color: var(--brand-dim); background: var(--surface-2); }
.tx-btn:active { transform: translateY(1px); }
.tx-btn--brand { background: var(--brand-dim); border-color: var(--brand); color: #0b0d12; font-weight: 600; }
.tx-btn--brand:hover { background: var(--brand); }
.tx-date {
  height: 2.5rem; padding: 0 0.7rem; background: var(--surface);
  border: 1px solid var(--line); border-radius: 10px; color: var(--text);
  font-family: var(--font-mono), monospace; font-size: 0.85rem;
  color-scheme: dark;
}
.tx-search { position: relative; flex: 1 1 260px; display: flex; }
.tx-search input {
  width: 100%; height: 2.5rem; padding: 0 0.9rem 0 2.2rem;
  background: var(--surface); border: 1px solid var(--line);
  border-radius: 10px; color: var(--text); font-size: 0.9rem;
  font-family: var(--font-sans), sans-serif;
}
.tx-search input::placeholder { color: var(--text-faint); }
.tx-search input:focus { outline: none; border-color: var(--brand); }
.tx-search__icon { position: absolute; left: 0.75rem; top: 50%; transform: translateY(-50%); color: var(--text-faint); font-size: 0.9rem; }
.tx-search__clear { align-self: center; margin-left: 0.5rem; color: var(--text-dim); font-size: 0.8rem; }

/* ---- summary strip ---- */
.tx-summary {
  display: flex; flex-wrap: wrap; gap: 1.4rem; align-items: baseline;
  margin: 1.6rem 0 0.4rem; padding-bottom: 1rem;
  border-bottom: 1px solid var(--line-soft);
  font-size: 0.82rem; color: var(--text-dim);
}
.tx-summary b { color: var(--text); font-weight: 600; }
.tx-summary .mono { color: var(--text); }

/* ---- session cards ---- */
.tx-list { display: flex; flex-direction: column; gap: 0.7rem; margin-top: 1.2rem; }
.tx-card {
  display: block; padding: 1rem 1.1rem;
  background: var(--surface); border: 1px solid var(--line-soft);
  border-radius: var(--radius);
  transition: border-color .15s, background .15s, transform .06s;
}
.tx-card:hover { border-color: var(--brand-dim); background: var(--surface-2); transform: translateY(-1px); }
.tx-card__top { display: flex; align-items: center; gap: 0.6rem; }
.tx-card__who { font-weight: 600; font-size: 0.98rem; word-break: break-all; }
.tx-card__spacer { flex: 1; }
.tx-card__time { font-family: var(--font-mono), monospace; font-size: 0.76rem; color: var(--text-dim); white-space: nowrap; }
.tx-card__meta {
  margin-top: 0.45rem; display: flex; flex-wrap: wrap; gap: 0.5rem 1rem;
  font-family: var(--font-mono), monospace; font-size: 0.73rem; color: var(--text-faint);
}
.tx-card__snippet {
  margin-top: 0.7rem; padding: 0.55rem 0.75rem;
  background: var(--bg-2); border-left: 2px solid var(--brand-dim);
  border-radius: 0 8px 8px 0; font-size: 0.85rem; color: var(--text-dim);
}
.tx-card__snippet .role { font-family: var(--font-mono), monospace; font-size: 0.68rem; text-transform: uppercase; letter-spacing: 0.1em; color: var(--text-faint); margin-right: 0.4rem; }

/* ---- channel badge ---- */
.tx-badge {
  display: inline-flex; align-items: center; gap: 0.32rem;
  padding: 0.16rem 0.5rem; border-radius: 999px;
  font-family: var(--font-mono), monospace; font-size: 0.66rem;
  font-weight: 600; letter-spacing: 0.08em; text-transform: uppercase;
  border: 1px solid transparent; white-space: nowrap;
}
.tx-badge--web { color: var(--web); background: #7c9ed915; border-color: #7c9ed933; }
.tx-badge--pstn { color: var(--pstn); background: #d97a8f15; border-color: #d97a8f33; }

/* ---- empty state ---- */
.tx-empty {
  margin-top: 2rem; padding: 2.4rem; text-align: center;
  border: 1px dashed var(--line); border-radius: var(--radius);
  color: var(--text-dim);
}
.tx-empty__mark { font-size: 1.8rem; opacity: 0.5; }

/* ---- conversation ---- */
.tx-back { display: inline-flex; align-items: center; gap: 0.4rem; margin: 1.8rem 0 0; color: var(--text-dim); font-size: 0.85rem; }
.tx-back:hover { color: var(--text); }
.tx-convo-head { margin: 1.2rem 0 0.2rem; }
.tx-convo-meta {
  display: flex; flex-wrap: wrap; gap: 0.5rem 1rem; margin-top: 0.9rem;
  padding: 0.9rem 1rem; background: var(--surface); border: 1px solid var(--line-soft);
  border-radius: 12px; font-family: var(--font-mono), monospace;
  font-size: 0.74rem; color: var(--text-dim);
}
.tx-convo-meta b { color: var(--text); font-weight: 600; }
.tx-thread { display: flex; flex-direction: column; gap: 1rem; margin: 2rem 0; }
.tx-turn { display: flex; gap: 0.75rem; max-width: 82%; }
.tx-turn--user { align-self: flex-end; flex-direction: row-reverse; }
.tx-avatar {
  flex: none; width: 30px; height: 30px; border-radius: 9px;
  display: grid; place-items: center; font-family: var(--font-mono), monospace;
  font-size: 0.66rem; font-weight: 700; margin-top: 0.15rem;
}
.tx-avatar--agent { background: var(--agent-soft); color: var(--agent); border: 1px solid #7cc4b033; }
.tx-avatar--user { background: var(--user-soft); color: var(--user); border: 1px solid #d8a65733; }
.tx-bubble { padding: 0.6rem 0.85rem; border-radius: 13px; border: 1px solid var(--line-soft); }
.tx-bubble--agent { background: var(--agent-soft); border-color: #7cc4b026; border-top-left-radius: 4px; }
.tx-bubble--user { background: var(--user-soft); border-color: #d8a65726; border-top-right-radius: 4px; }
.tx-bubble__head {
  display: flex; gap: 0.55rem; align-items: baseline; margin-bottom: 0.2rem;
  font-family: var(--font-mono), monospace; font-size: 0.66rem;
  text-transform: uppercase; letter-spacing: 0.1em; color: var(--text-faint);
}
.tx-bubble__role { color: var(--text-dim); font-weight: 600; }
.tx-bubble__int { color: var(--pstn); }
.tx-bubble__text { font-size: 0.94rem; color: var(--text); white-space: pre-wrap; word-break: break-word; }

@media (max-width: 560px) {
  .tx-turn { max-width: 100%; }
  .tx-card__time { font-size: 0.7rem; }
}
`;

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
    <html
      lang="en"
      className={`${fontSans.variable} ${fontMono.variable} ${fontMuseo.variable}`}
    >
      <body>
        <style dangerouslySetInnerHTML={{ __html: CONSOLE_CSS }} />
        <header className="tx-topbar">
          <Link href="/admin/transcripts" className="tx-topbar__mark">
            kv
          </Link>
          <span className="tx-topbar__title">klanker-voice</span>
          <span className="tx-topbar__crumb">/ admin / transcripts</span>
          <span className="tx-topbar__spacer" />
          <span className="tx-topbar__who">
            signed in as <b>{sessionEmail}</b>
          </span>
        </header>
        <main className="tx-shell">{children}</main>
      </body>
    </html>
  );
}
