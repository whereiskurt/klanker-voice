"""Structural invariant lint for the Asterisk edge configs (Phase 11, D-01,
D-07, spec §18/§25).

Pure text-assertion tests against the *committed* config files under
`apps/voice/asterisk/` -- no Asterisk process is started. These encode the
security posture that Task 1's configs implement mechanically:

- inbound-only: exactly one dialplan context, no `Dial(` application call
  anywhere (T-11-02-01, §25.A) -- a compromised/misused softphone endpoint
  can never place an outbound/toll call.
- ulaw-only: `pjsip.conf` negotiates exactly one codec (`allow=ulaw`),
  matching the Phase 10 PCMU codec byte-for-byte.
- private/authenticated ARI: `http.conf`/`ari.conf` bind to a
  private/loopback address and declare an authenticated ARI user
  (T-11-02-02, §18/§25.C).

Every negative grep below strips leading-`;`-comment lines FIRST, so a
descriptive comment (e.g. this file's own prose mentioning "Dial" or
"0.0.0.0") can never accidentally self-invalidate a check.

Proof the invariant genuinely bites (documented, not committed as a
mutation): temporarily add a line `same => n,Dial(PJSIP/foo)` to
`extensions.conf` and re-run `test_extensions_conf_has_no_dial_and_one_context`
-- it fails because `dial_calls == 1`. Reverting the edit restores green.
Equivalently, temporarily add `allow=g722` under `pjsip.conf`'s endpoint
template and re-run `test_pjsip_conf_is_ulaw_only` -- it fails because
`allow_lines != ["allow=ulaw"]`.
"""

from __future__ import annotations

import re
from pathlib import Path

APP_ROOT = Path(__file__).resolve().parent.parent
ASTERISK_DIR = APP_ROOT / "asterisk"

HTTP_CONF = ASTERISK_DIR / "http.conf"
ARI_CONF = ASTERISK_DIR / "ari.conf"
PJSIP_CONF = ASTERISK_DIR / "pjsip.conf"
EXTENSIONS_CONF = ASTERISK_DIR / "extensions.conf"
RTP_CONF = ASTERISK_DIR / "rtp.conf"

_PRIVATE_LOOPBACK_RE = re.compile(
    r"^(127\.\d{1,3}\.\d{1,3}\.\d{1,3}|localhost)$"
)

_CONTEXT_RE = re.compile(r"^\[([A-Za-z0-9_-]+)\]\s*$")


def _stripped_lines(path: Path) -> list[str]:
    """Read a config file and strip comment lines (leading `;`) + blank lines.

    Inline trailing comments (` ; ...`) are also stripped so a negative grep
    never matches text that only appears after a `;`.
    """
    lines: list[str] = []
    for raw in path.read_text().splitlines():
        stripped = raw.strip()
        if not stripped or stripped.startswith(";"):
            continue
        # Strip an inline trailing comment (everything from the first
        # unescaped ';' onward), but keep it simple: Asterisk .conf files in
        # this repo never use ';' inside a value, so a naive split is safe.
        code_part = stripped.split(";", 1)[0].strip()
        if code_part:
            lines.append(code_part)
    return lines


class TestExtensionsConfInboundOnly:
    """(a)/(b): exactly one context, zero Dial(), no outbound-sounding context."""

    def test_extensions_conf_has_no_dial_and_one_context(self):
        lines = _stripped_lines(EXTENSIONS_CONF)
        dial_calls = sum(1 for line in lines if "Dial(" in line)
        assert dial_calls == 0, (
            "extensions.conf must NEVER contain a Dial() application call "
            "(§25.A, T-11-02-01) -- found one"
        )
        contexts = [
            m.group(1) for line in lines if (m := _CONTEXT_RE.match(line))
        ]
        assert contexts == ["from-klanker-inbound"], (
            "extensions.conf must declare exactly one context named "
            f"'from-klanker-inbound' -- found {contexts!r}"
        )

    def test_extensions_conf_reaches_stasis(self):
        lines = _stripped_lines(EXTENSIONS_CONF)
        assert any("Stasis(" in line for line in lines), (
            "extensions.conf must hand the call to Stasis(klanker)"
        )

    def test_extensions_conf_no_outbound_context_name(self):
        lines = _stripped_lines(EXTENSIONS_CONF)
        contexts = [
            m.group(1) for line in lines if (m := _CONTEXT_RE.match(line))
        ]
        outbound_markers = ("outbound", "dial", "trunk", "external-call")
        for ctx in contexts:
            assert ctx == "from-klanker-inbound", (
                f"unexpected context {ctx!r} in extensions.conf"
            )
            lowered = ctx.lower()
            assert not any(marker in lowered for marker in outbound_markers), (
                f"context name {ctx!r} looks outbound-sounding"
            )


class TestPjsipConfUlawOnly:
    """(c): allow=ulaw + disallow=all, no other allow= codec line."""

    def test_pjsip_conf_is_ulaw_only(self):
        lines = _stripped_lines(PJSIP_CONF)
        assert "disallow=all" in lines, "pjsip.conf must set disallow=all"
        allow_lines = [line for line in lines if line.startswith("allow=")]
        assert allow_lines == ["allow=ulaw"], (
            "pjsip.conf must allow exactly ulaw and nothing else -- found "
            f"{allow_lines!r}"
        )

    def test_pjsip_conf_endpoint_context_is_inbound_only(self):
        lines = _stripped_lines(PJSIP_CONF)
        assert "context=from-klanker-inbound" in lines, (
            "pjsip.conf's endpoint must point at the single inbound-only "
            "context"
        )


class TestPrivateAuthenticatedAri:
    """(d): http.conf/ari.conf bindaddr is private/loopback; ARI has a user."""

    def test_http_conf_bindaddr_is_private_or_loopback(self):
        lines = _stripped_lines(HTTP_CONF)
        bindaddr_lines = [line for line in lines if line.startswith("bindaddr=")]
        assert len(bindaddr_lines) == 1, "http.conf must set exactly one bindaddr"
        value = bindaddr_lines[0].split("=", 1)[1].strip()
        assert _PRIVATE_LOOPBACK_RE.match(value), (
            f"http.conf bindaddr must be a private/loopback value, got {value!r}"
        )

    def test_ari_conf_declares_authenticated_user(self):
        lines = _stripped_lines(ARI_CONF)
        assert any(line.startswith("[") and line.endswith("]") and line not in ("[general]",) for line in lines), (
            "ari.conf must declare a non-[general] ARI user section"
        )
        assert "type=user" in lines, "ari.conf must declare a type=user"
        password_lines = [line for line in lines if line.startswith("password=")]
        assert password_lines, "ari.conf must set a password= for the ARI user"
        value = password_lines[0].split("=", 1)[1].strip()
        assert value, "ari.conf ARI user password must not be empty"

    def test_ari_conf_allowed_origins_empty(self):
        lines = _stripped_lines(ARI_CONF)
        origins_lines = [line for line in lines if line.startswith("allowed_origins=")]
        assert origins_lines, "ari.conf must set allowed_origins= (even if empty)"
        value = origins_lines[0].split("=", 1)[1].strip()
        assert value == "", "ari.conf allowed_origins must be empty (server-to-server only)"


class TestRtpConfNarrowRange:
    def test_rtp_conf_declares_narrow_range(self):
        lines = _stripped_lines(RTP_CONF)
        start_lines = [line for line in lines if line.startswith("rtpstart=")]
        end_lines = [line for line in lines if line.startswith("rtpend=")]
        assert start_lines and end_lines, "rtp.conf must set rtpstart/rtpend"
        start = int(start_lines[0].split("=", 1)[1])
        end = int(end_lines[0].split("=", 1)[1])
        assert start < end
        assert (end - start) <= 100, "rtp.conf range should stay narrow for the dev harness"
