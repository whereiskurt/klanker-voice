"""Named pipeline variants -> config-file resolution (full-duplex, 2026-07-10).

One deployed server, many front-door pages. The browser posts its SDP offer to
``/api/offer?variant=<name>``; the server maps that name, through this module,
to the pipeline config the session runs. A variant is nothing more than "which
``pipeline.toml`` (and therefore which STT arm / persona / knowledge / duplex
profile) this session uses" — the auth, quota, transport, and teardown seams
are identical across variants (quota is a *global* budget guardrail and stays
sourced from the default config, never per-variant).

Security (T-1-04 sibling): the variant name is attacker-controlled (it rides in
a query string on a public endpoint). It is therefore used ONLY as a key into a
fixed in-code allowlist — it never becomes a filesystem path. An unknown or
malformed name silently resolves to the default variant, so there is no
path-traversal or arbitrary-config-load surface. Every resolved path is
anchored under ``APP_ROOT``.

Adding a variant is one line in ``_VARIANT_CONFIGS`` plus (if it needs its own
pipeline) a checked-in ``configs/<name>.toml``.
"""

from __future__ import annotations

from pathlib import Path

from klanker_voice.config import APP_ROOT

#: The live, shipped experience — today's half-duplex cascade. ``None`` means
#: "the default pipeline.toml", i.e. exactly ``load_config()`` with no override,
#: so voice1 is provably byte-for-byte the current behavior.
DEFAULT_VARIANT = "voice1"

#: variant name -> config path relative to APP_ROOT (apps/voice/), or ``None``
#: for the default pipeline.toml. This is the ONLY place a variant name is
#: trusted; anything not a key here resolves to DEFAULT_VARIANT.
_VARIANT_CONFIGS: dict[str, str | None] = {
    "voice1": None,
    "voice2": "configs/voice2.toml",
}


def known_variants() -> tuple[str, ...]:
    """The allowlisted variant names, for logging / tests / a future menu."""
    return tuple(_VARIANT_CONFIGS)


def is_known_variant(name: str | None) -> bool:
    """True iff ``name`` is an allowlisted variant (exact match)."""
    return name in _VARIANT_CONFIGS


def normalize_variant(name: str | None) -> str:
    """Map an arbitrary, possibly-hostile request value to a safe variant name.

    Unknown/None/empty -> :data:`DEFAULT_VARIANT`. The return value is always a
    key of ``_VARIANT_CONFIGS``, so callers can index it without re-checking.
    """
    if isinstance(name, str):
        name = name.strip()
    return name if name in _VARIANT_CONFIGS else DEFAULT_VARIANT


def variant_config_path(name: str | None) -> Path | None:
    """Resolve a variant name to its absolute config path, or ``None``.

    ``None`` means "use the default pipeline.toml" (the loaders' own default
    resolution). Any non-None path is resolved under ``APP_ROOT`` and returned
    absolute. The name is normalized first, so this never touches an
    un-allowlisted string.
    """
    rel = _VARIANT_CONFIGS[normalize_variant(name)]
    if rel is None:
        return None
    return (APP_ROOT / rel).resolve()
