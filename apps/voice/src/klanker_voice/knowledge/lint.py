"""Advisory do-not-say lint (Amendment 3-E, D-02, T-07-01).

Demoted from a build-blocking gate (the original D-01/D-02 "foundational
control, refuse-on-finding" framing) to a thin, flag-only advisory check: the
knowledge corpus is public-by-design (Amendment 3-E rationale -- Kurt
controls what goes into the recorded transcripts and the curated manifest),
so the risk here is narrower than a secrets scanner -- it's "public-in-a-repo
but shouldn't be volunteered aloud on a public mic" (e.g. a real AWS account
ID that happens to appear in docs/tests).

``advisory_lint`` NEVER raises and NEVER blocks anything -- it only returns
findings for the offline refresh workflow's git-diff human review (D-09) to
look at. Patterns are described generically here (never a literal
secret-shaped example baked into source, per the plan's own guidance).
"""

from __future__ import annotations

import re
from dataclasses import dataclass

#: (pattern name, compiled regex) -- generic shape detectors, not literal
#: secrets. Order is check order; a line can produce multiple findings.
_PATTERNS: tuple[tuple[str, re.Pattern[str]], ...] = (
    ("aws_account_id", re.compile(r"\b\d{12}\b")),
    ("role_arn", re.compile(r"\barn:aws:iam::\d{12}:(?:role|user)/[\w+=,.@-]+", re.IGNORECASE)),
    ("aws_access_key_id", re.compile(r"\bAKIA[0-9A-Z]{16}\b")),
    (
        "key_block",
        re.compile(r"-----BEGIN [A-Z0-9 ]*(?:PRIVATE KEY|CERTIFICATE)-----"),
    ),
    ("internal_hostname", re.compile(r"\.(?:internal|local)\b", re.IGNORECASE)),
    ("cloud_map_reference", re.compile(r"\bcloud\s?map\b", re.IGNORECASE)),
)


@dataclass(frozen=True)
class Finding:
    """One advisory hit -- a name + line + the matched excerpt, for review."""

    pattern: str
    line: int
    excerpt: str


def advisory_lint(text: str) -> list[Finding]:
    """Scan ``text`` for do-not-say shapes. Flags only -- never raises, never
    blocks (Amendment 3-E). Any unexpected input (empty string, binary-ish
    text, huge input) degrades to an empty or partial finding list, never an
    exception -- callers (the refresh workflow) must be able to run this
    unconditionally over arbitrary corpus content.
    """
    findings: list[Finding] = []
    try:
        lines = text.splitlines()
        for line_no, line in enumerate(lines, start=1):
            for name, pattern in _PATTERNS:
                try:
                    match = pattern.search(line)
                except Exception:  # pragma: no cover -- defensive, regex is static
                    continue
                if match:
                    findings.append(Finding(pattern=name, line=line_no, excerpt=match.group(0)))
    except Exception:
        # Advisory-only: never let a lint bug block the offline refresh
        # workflow it's meant to help, not gate (Amendment 3-E).
        return findings
    return findings
