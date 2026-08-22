#!/usr/bin/env python3
"""Render captured terminal sessions to SVG for the operator manual.

The operator manual shows real `kv` output. Pasting that into a fenced code
block loses the shape of the thing; a PNG screenshot cannot be diffed, goes
stale invisibly, and bloats a public repo. So each capture is stored as a plain
`.session` transcript and rendered here into a self-contained dark terminal
window that renders inline on GitHub *and* on the wiki.

Transcript format — one file per capture, in `docs/assets/terminal/`:

    # title: kv telephony list
    $ kv telephony list
    Inbound DIDs (numbers the public calls):
    DID         DESCRIPTION       ROUTING                     POP
    ...

  - `# title:` (optional, first line) sets the window title bar.
  - `# ---` on its own line draws a horizontal rule (use between two commands
    whose outputs should read as separate blocks).
  - A line starting with `$ ` is a command; everything else is output.
  - `#` comments other than the directives above are dropped.

Anything a reader must not see is redacted **in the transcript**, so the
redaction is reviewable in `git diff` rather than buried in a binary.

Usage:

    python3 scripts/render-terminal-svg.py                 # render all
    python3 scripts/render-terminal-svg.py telephony-list  # render one
"""

import glob
import os
import re
import subprocess
import sys
from xml.sax.saxutils import escape

# --- geometry ---------------------------------------------------------------
# Tuned against the SFMono/Menlo/Consolas stack below: 0.6 em advance is the
# monospace ratio those faces share, so columns line up in every browser that
# resolves any one of them.
FONT_SIZE = 13.0
CHAR_W = FONT_SIZE * 0.6
LINE_H = FONT_SIZE * 1.54
PAD_X = 18.0
PAD_Y = 14.0
TITLEBAR_H = 30.0
MAX_COLS = 132  # wider than this and the SVG stops fitting a README column

FONT_STACK = (
    "ui-monospace,SFMono-Regular,SF Mono,Menlo,Consolas,"
    "Liberation Mono,DejaVu Sans Mono,monospace"
)

# --- palette ----------------------------------------------------------------
# A fixed dark scheme, deliberately: this is a picture of a terminal, and a
# terminal does not follow the reader's GitHub theme.
BG = "#12161c"
CHROME = "#1b2029"
BORDER = "#262d38"
FG = "#c8d1dc"
DIM = "#6b7683"
PROMPT = "#7fd88f"
CMD = "#e8edf3"
HEADER = "#e0a648"
SECTION = "#e0a648"
GOOD = "#7fd88f"
WARN = "#e0704a"
REDACT = "#7a8494"

TITLE_DIRECTIVE = re.compile(r"^#\s*title:\s*(.*)$")
RULE_DIRECTIVE = re.compile(r"^#\s*---\s*$")

# A table header row: two or more ALL-CAPS words separated by 2+ spaces.
HEADER_ROW = re.compile(r"^[A-Z][A-Z0-9 /()-]*?(?:  +[A-Z][A-Z0-9 /()-]*?)+\s*$")
# A section heading the kv printers emit, e.g. "Gate config:".
SECTION_ROW = re.compile(r"^[A-Z][A-Za-z0-9 ,'/()§-]*:\s*$")
REDACTED = re.compile(r"(<redacted[^>]*>|\*{3,}|•{3,})")


def classify(line: str) -> str:
    """Pick a colour role for one output line."""
    if HEADER_ROW.match(line):
        return HEADER
    if SECTION_ROW.match(line):
        return SECTION
    stripped = line.strip()
    if stripped.startswith(("PASS", "STATUS  PASS")) or " PASS" in line[:20]:
        return GOOD
    if stripped.startswith("FAIL") or "error —" in line or "error --" in line:
        return WARN
    if stripped.startswith("(") or stripped.startswith("#"):
        return DIM
    return FG


def parse(path: str):
    """Return (title, rows) where each row is ('cmd'|'out'|'rule', text)."""
    title = os.path.splitext(os.path.basename(path))[0]
    rows = []
    with open(path, encoding="utf-8") as fh:
        for raw in fh.read().split("\n"):
            line = raw.rstrip("\n")
            m = TITLE_DIRECTIVE.match(line)
            if m:
                title = m.group(1).strip()
                continue
            if RULE_DIRECTIVE.match(line):
                rows.append(("rule", ""))
                continue
            if line.startswith("#"):
                continue
            if line.startswith("$ "):
                rows.append(("cmd", line[2:]))
            else:
                rows.append(("out", line))
    # Trim leading/trailing blank output lines.
    while rows and rows[0][0] == "out" and not rows[0][1].strip():
        rows.pop(0)
    while rows and rows[-1][0] == "out" and not rows[-1][1].strip():
        rows.pop()
    return title, rows


def spans(text: str, base_fill: str) -> str:
    """Emit tspans for one line, greying anything that reads as a redaction."""
    parts = REDACTED.split(text)
    if len(parts) == 1:
        return escape(text)
    out = []
    for part in parts:
        if not part:
            continue
        if REDACTED.fullmatch(part):
            out.append(f'<tspan fill="{REDACT}">{escape(part)}</tspan>')
        else:
            out.append(f'<tspan fill="{base_fill}">{escape(part)}</tspan>')
    return "".join(out)


def render(title: str, rows) -> str:
    body = [r for r in rows]
    widest = max([len(title) + 8] + [len(t) for _, t in body] or [0])
    cols = min(max(widest + 2, 40), MAX_COLS)
    width = cols * CHAR_W + PAD_X * 2
    height = TITLEBAR_H + PAD_Y * 2 + len(body) * LINE_H

    out = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width:.0f}" '
        f'height="{height:.0f}" viewBox="0 0 {width:.0f} {height:.0f}" '
        f'font-family="{FONT_STACK}" font-size="{FONT_SIZE}">',
        f'<rect width="{width:.0f}" height="{height:.0f}" rx="8" fill="{BG}" '
        f'stroke="{BORDER}"/>',
        f'<path d="M0 8a8 8 0 0 1 8-8h{width - 16:.0f}a8 8 0 0 1 8 8v'
        f'{TITLEBAR_H - 8:.0f}H0z" fill="{CHROME}"/>',
        f'<line x1="0" y1="{TITLEBAR_H}" x2="{width:.0f}" y2="{TITLEBAR_H}" '
        f'stroke="{BORDER}"/>',
    ]
    for i, colour in enumerate(("#e0704a", "#e0a648", "#7fd88f")):
        out.append(f'<circle cx="{18 + i * 16}" cy="{TITLEBAR_H / 2:.0f}" r="5" '
                   f'fill="{colour}"/>')
    out.append(
        f'<text x="{width / 2:.0f}" y="{TITLEBAR_H / 2 + 4:.0f}" fill="{DIM}" '
        f'font-size="{FONT_SIZE - 1.5}" text-anchor="middle">{escape(title)}</text>'
    )

    y = TITLEBAR_H + PAD_Y + FONT_SIZE
    for kind, text in body:
        if kind == "rule":
            out.append(
                f'<line x1="{PAD_X}" y1="{y - FONT_SIZE / 2:.1f}" '
                f'x2="{width - PAD_X:.0f}" y2="{y - FONT_SIZE / 2:.1f}" '
                f'stroke="{BORDER}"/>'
            )
        elif kind == "cmd":
            out.append(
                f'<text x="{PAD_X}" y="{y:.1f}" xml:space="preserve">'
                f'<tspan fill="{PROMPT}">$ </tspan>'
                f'<tspan fill="{CMD}" font-weight="600">{escape(text)}</tspan></text>'
            )
        elif text.strip():
            fill = classify(text)
            out.append(
                f'<text x="{PAD_X}" y="{y:.1f}" fill="{fill}" '
                f'xml:space="preserve">{spans(text, fill)}</text>'
            )
        y += LINE_H

    out.append("</svg>")
    return "\n".join(out) + "\n"


def repo_root() -> str:
    return subprocess.run(
        ["git", "rev-parse", "--show-toplevel"],
        capture_output=True, text=True, check=True,
    ).stdout.strip()


def main(argv) -> int:
    root = repo_root()
    src_dir = os.path.join(root, "docs", "assets", "terminal")
    wanted = set(argv[1:])
    sessions = sorted(glob.glob(os.path.join(src_dir, "*.session")))
    if not sessions:
        print(f"no .session files in {src_dir}", file=sys.stderr)
        return 1
    count = 0
    for path in sessions:
        stem = os.path.splitext(os.path.basename(path))[0]
        if wanted and stem not in wanted:
            continue
        title, rows = parse(path)
        if not rows:
            print(f"  skip {stem}: empty transcript", file=sys.stderr)
            continue
        dest = os.path.join(src_dir, stem + ".svg")
        with open(dest, "w", encoding="utf-8") as fh:
            fh.write(render(title, rows))
        print(f"  {stem}.svg  ({len(rows)} lines)")
        count += 1
    print(f"rendered {count} capture(s)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
