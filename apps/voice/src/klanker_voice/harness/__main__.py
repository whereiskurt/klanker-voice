"""Harness CLI: re-render and compare latency artifacts from JSON alone.

Usage::

    uv run python -m klanker_voice.harness report artifacts/harness/a.json [...]
    uv run python -m klanker_voice.harness compare a.json b.json [...]

``report`` re-renders the per-stage p50/p95 table for past runs; ``compare``
renders the side-by-side per-stage diff table — the D-11 diffable A/B
instrument plan 01-04 uses for TUNING.md verdict tables.

Exit codes (D-13): threshold verdicts NEVER produce a nonzero exit — both
subcommands exit 0 regardless of check/warn marks. Nonzero (1) is reserved
for genuine I/O or schema errors (missing file, invalid JSON, wrong
schema_version), which are surfaced, never masked.
"""

from __future__ import annotations

import json
from pathlib import Path

import typer
from rich.console import Console

from klanker_voice.harness.report import Report, build_comparison_table

app = typer.Typer(
    name="harness",
    help="Latency harness artifact tools (report / compare).",
    no_args_is_help=True,
)

_console = Console()
_err_console = Console(stderr=True)


@app.callback()
def _callback() -> None:
    """Anchor so `report` and `compare` stay explicit subcommands."""


def _load(path: Path) -> Report:
    """Load one artifact; genuine I/O/schema errors exit 1 with the reason."""
    try:
        return Report.load(path)
    except FileNotFoundError:
        _err_console.print(f"[red]error:[/red] artifact not found: {path}")
        raise typer.Exit(code=1)
    except json.JSONDecodeError as e:
        _err_console.print(f"[red]error:[/red] invalid JSON in {path}: {e}")
        raise typer.Exit(code=1)
    except ValueError as e:
        _err_console.print(f"[red]error:[/red] bad harness artifact {path}: {e}")
        raise typer.Exit(code=1)


@app.command("report")
def report(
    artifacts: list[Path] = typer.Argument(..., help="One or more harness JSON artifacts."),
) -> None:
    """Re-render the per-stage p50/p95 table for past runs."""
    for path in artifacts:
        loaded = _load(path)
        _console.print(f"[dim]{path}[/dim]")
        loaded.render(console=_console)
    # D-13: verdicts are informational — always exit 0 past this point.


@app.command("compare")
def compare(
    artifacts: list[Path] = typer.Argument(..., help="Two or more harness JSON artifacts."),
) -> None:
    """Side-by-side per-stage p50/p95 diff table across artifacts."""
    if len(artifacts) < 2:
        _err_console.print("[red]error:[/red] compare needs at least two artifacts")
        raise typer.Exit(code=1)
    labeled = []
    for path in artifacts:
        loaded = _load(path)
        arm = loaded.config.get("arm", "?")
        labeled.append((f"{arm}\n{path.stem}", loaded))
    _console.print(build_comparison_table(labeled))
    # D-13: threshold warnings never flip the exit code.


if __name__ == "__main__":
    app()
