#!/usr/bin/env python3
"""Render Asterisk config templates with real secrets from the environment.

Phase 11 quick-task 260712-ckd (§19-C one-command harness). Renders ONLY into
the gitignored output dir (`--out`, normally `.rendered/`), NEVER back into
the tracked template files under `apps/voice/asterisk/` (D-09: the tracked
`.conf` files stay placeholder-only forever, so `test_asterisk_configs.py`'s
lint keeps passing and no real secret is ever committed).

Dependency-free stdlib script -- runnable by a plain `python3` inside the
`python:3.12-slim` render sidecar (no uv/venv needed, no pip install). Reads
each of the two templates that carry a `${VAR}` placeholder (`ari.conf`,
`pjsip.conf`) and substitutes values from `os.environ` via
`string.Template(...).safe_substitute(...)` -- chosen over sed/shell so a
password containing shell-special characters (spaces, `$`, `&`, `|`, quotes,
etc.) renders correctly with zero escaping surface. The other three configs
(`http.conf`, `extensions.conf`, `rtp.conf`) carry no placeholder and are not
rendered here -- Asterisk bind-mounts them directly from the tracked files.

This script contains NO secret and NO hardcoded password -- it only moves
values already present in the ambient environment into the output dir.

Usage:
    python3 render_configs.py --templates <dir> --out <dir>
"""

from __future__ import annotations

import argparse
import os
import string
import sys
from pathlib import Path

#: Exactly the two tracked templates that carry a `${VAR}` placeholder.
RENDERED_FILENAMES = ("ari.conf", "pjsip.conf")


def render_configs(templates_dir: Path, out_dir: Path) -> list[Path]:
    """Render each file in RENDERED_FILENAMES from templates_dir into out_dir.

    Substitutes `${VAR}` placeholders from `os.environ` via
    `string.Template.safe_substitute` (unset vars are left as literal `${VAR}`
    text rather than raising -- the caller's own verification step is
    responsible for confirming secrets were actually supplied). Returns the
    list of rendered output paths in RENDERED_FILENAMES order.
    """
    out_dir.mkdir(parents=True, exist_ok=True)
    rendered: list[Path] = []
    for name in RENDERED_FILENAMES:
        src = templates_dir / name
        text = src.read_text()
        rendered_text = string.Template(text).safe_substitute(os.environ)
        dest = out_dir / name
        dest.write_text(rendered_text)
        rendered.append(dest)
        print(f"rendered {src} -> {dest}")
    return rendered


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--templates",
        required=True,
        type=Path,
        help="Directory containing the tracked ari.conf/pjsip.conf templates.",
    )
    parser.add_argument(
        "--out",
        required=True,
        type=Path,
        help="Directory to write the rendered, secret-bearing copies into "
        "(must be gitignored -- see apps/voice/asterisk/.gitignore).",
    )
    args = parser.parse_args(argv)
    render_configs(args.templates, args.out)
    return 0


if __name__ == "__main__":
    sys.exit(main())
