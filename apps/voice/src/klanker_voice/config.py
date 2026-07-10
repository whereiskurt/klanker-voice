"""pipeline.toml -> frozen dataclasses with validation.

The TOML file is the single stage-selection surface (D-09, PIPE-04): providers,
models, endpointing knobs, persona path, voice settings. It never carries
credentials — API keys come exclusively from environment variables, and any
field whose name looks like credential material is rejected loudly (ASVS V5,
T-1-04).

``load_config()`` honors the optional ``KLANKER_PIPELINE_CONFIG`` env var so
later A/B plans can point at ``configs/arm-*.toml`` without touching code.
"""

from __future__ import annotations

import os
import re
import tomllib
from dataclasses import dataclass
from pathlib import Path

#: Env var that overrides the default config path (A/B arm selection).
CONFIG_PATH_ENV_VAR = "KLANKER_PIPELINE_CONFIG"

#: apps/voice/ — the app root this module lives under (src/klanker_voice/..).
APP_ROOT = Path(__file__).resolve().parents[2]

DEFAULT_CONFIG_PATH = APP_ROOT / "pipeline.toml"

ALLOWED_STT_PROVIDERS = frozenset({"deepgram-nova3", "deepgram-flux"})
ALLOWED_LLM_PROVIDERS = frozenset({"anthropic"})
ALLOWED_TTS_PROVIDERS = frozenset({"elevenlabs"})
ALLOWED_TURN_STRATEGIES = frozenset({"vad_timeout", "smart_turn_v3"})

#: Field names that suggest credential material. A secret pasted into the TOML
#: must fail loudly at load time instead of silently loading (T-1-04).
_CREDENTIAL_FIELD_RE = re.compile(
    r"(?:^|_)(?:api_?key|key|keys|secret|secrets|token|tokens|password|passwd|"
    r"credential|credentials|bearer|auth)(?:_|$)|apikey",
    re.IGNORECASE,
)


class ConfigError(ValueError):
    """Raised when pipeline.toml is invalid."""


@dataclass(frozen=True)
class FluxConfig:
    """Deepgram Flux knobs — only consumed when stt.provider is deepgram-flux."""

    eot_threshold: float = 0.7
    eager_eot_threshold: float = 0.0  # 0.0 = disabled


@dataclass(frozen=True)
class SttConfig:
    provider: str
    model: str
    flux: FluxConfig


@dataclass(frozen=True)
class TurnConfig:
    """Local turn-detection knobs — ignored when stt.provider is deepgram-flux."""

    strategy: str
    vad_stop_secs: float
    user_speech_timeout: float


@dataclass(frozen=True)
class LlmConfig:
    provider: str
    model: str


@dataclass(frozen=True)
class TtsConfig:
    provider: str
    model: str
    voice_id: str
    speed: float
    # 07.1 tunable voice character; defaults keep direct constructors (tests,
    # other callers) working and match load_config()'s parse-time fallbacks.
    stability: float = 0.4
    similarity_boost: float = 0.85
    style: float = 0.1


@dataclass(frozen=True)
class PersonaConfig:
    prompt_path: Path  # resolved absolute path; existence-checked at load
    greet_first: bool = True  # server plays register_greet_first when true (default: back-compat)


@dataclass(frozen=True)
class PipelineConfig:
    stt: SttConfig
    turn: TurnConfig
    llm: LlmConfig
    tts: TtsConfig
    persona: PersonaConfig
    # Display-only per-variant label (subtle live-UI tag, e.g. "KPH(v1)").
    # Trailing default keeps every existing positional/keyword constructor and
    # fixture working. A plain top-level TOML scalar -- read directly from the
    # document, not a [table].
    label: str = "KPH"


@dataclass(frozen=True)
class KnowledgeConfig:
    """The ``[knowledge]`` table (Phase 7, D-01/D-13): where the router's
    curated N-topic manifest, keyword topic-map, per-topic packs, and Kurt
    STYLE layer live, plus the Haiku prompt-caching floor.

    Loaded independently via :func:`load_knowledge_config` -- mirrors the
    :class:`QuotaConfig` / :func:`load_quota_config` precedent, not a
    ``PipelineConfig`` field, so the 168+-test suite's fixtures that omit
    ``[knowledge]`` (many built before this phase) are unaffected.
    """

    manifest_path: Path  # resolved absolute path; existence-checked at load
    topic_map_path: Path  # resolved absolute path; existence-checked at load
    packs_dir: Path  # resolved absolute dir; existence-checked at load
    style_path: Path  # resolved absolute path; existence-checked at load
    cache_floor: int = 4096  # D-13: Haiku 4.5's minimum cacheable prefix
    # --- 07-02: local BM25/FTS5 retrieval (Amendment 3-A/B/C, PIPE-07) ---
    index_dir: Path = Path("knowledge/index")  # resolved absolute dir; existence-checked when retrieval_enabled
    retrieval_enabled: bool = True  # off -> router never queries; behavior == Plan 01 (07-01)
    retrieval_top_k: int = 4  # top-k chunks injected into system[1] per deep turn
    retrieval_budget: int = 1500  # approx-token cap on injected chunk text (D-13 cache_floor
    # rename precedent: NOT named retrieval_max_tokens -- _CREDENTIAL_FIELD_RE rejects any
    # field ending in _token(s) as credential-looking material)


#: Default backchannel lexicon (full-duplex, 2026-07-10). A user utterance
#: spoken *while the concierge is talking* that consists ONLY of these short
#: words (and is no longer than ``max_backchannel_words``) is treated as a
#: listening cue — "keep going" — not a barge-in, so it never truncates the
#: bot's turn. Kept deliberately small and unambiguous: every entry must be a
#: word a listener says to *encourage* the speaker, never one that starts a
#: real interjection ("wait", "no", "stop" are NOT here — those must barge in).
DEFAULT_BACKCHANNEL_WORDS: tuple[str, ...] = (
    "yeah",
    "yep",
    "yes",
    "uh-huh",
    "uhhuh",
    "mhm",
    "mm-hm",
    "mmhm",
    "mm",
    "okay",
    "ok",
    "right",
    "sure",
    "gotcha",
    "got",
    "it",
    "cool",
    "nice",
    "totally",
    "exactly",
    "true",
    "hmm",
    "oh",
    "wow",
    "makes",
    "sense",
)

#: Default bot backchannel phrases (full-duplex emitter). When the emitter is
#: on, the concierge drops one of these — short, sent straight to TTS, never
#: added to the LLM context — when the visitor pauses mid-thought, so it sounds
#: like it's actively listening. Rotated round-robin (deterministic) so it
#: doesn't repeat the same token back to back.
DEFAULT_EMITTER_PHRASES: tuple[str, ...] = ("mm-hm.", "right.", "yeah.", "gotcha.")


@dataclass(frozen=True)
class DuplexConfig:
    """The optional ``[duplex]`` table (full-duplex concept, 2026-07-10).

    Loaded independently via :func:`load_duplex_config`, mirroring
    :class:`QuotaConfig` / :class:`KnowledgeConfig`. Unlike those two,
    ``[duplex]`` is **optional**: a config file without the table yields the
    default (``enabled=False``), so the live ``voice1`` pipeline and every
    existing fixture behave exactly as before. Only the ``voice2`` variant
    (``configs/voice2.toml``) turns it on.

    Attributes:
        enabled: Master switch. False -> no ``DuplexController`` is inserted
            and the pipeline is the shipped half-duplex cascade.
        backchannel_emitter: When True the concierge emits its own short
            "mm-hm" listening cues while the visitor talks (higher "we're both
            live" feel, higher talk-over risk — the 07-08 spec's deferred
            non-goal, opted into for voice2).
        max_backchannel_words: An utterance longer than this (in words) is
            never treated as a backchannel, however it's worded.
        interruption_hold_ms: How long the controller holds a barge-in
            interruption while it waits for the first transcript to decide
            backchannel-vs-real. Genuine interruptions are delayed by at most
            this (or the time-to-first-partial, whichever is shorter). The one
            knob that trades barge-in latency for backchannel accuracy — tune
            live.
        emitter_min_gap_seconds: Rate-limit: minimum spacing between emitted
            bot backchannels, so it can't machine-gun "mm-hm".
        backchannel_words: The listening-cue lexicon (see
            ``DEFAULT_BACKCHANNEL_WORDS``).
        emitter_phrases: The bot's own backchannel phrases (see
            ``DEFAULT_EMITTER_PHRASES``).
    """

    enabled: bool = False
    backchannel_emitter: bool = False
    max_backchannel_words: int = 3
    interruption_hold_ms: int = 250
    emitter_min_gap_seconds: float = 4.0
    backchannel_words: tuple[str, ...] = DEFAULT_BACKCHANNEL_WORDS
    emitter_phrases: tuple[str, ...] = DEFAULT_EMITTER_PHRASES


#: Default D-04 wind-down copy (QUOT-03, 04-05). Lives here — not in a code
#: comment — so `load_quota_config` can fall back to a real, spoken-ready
#: string when `pipeline.toml` doesn't override it; the checked-in TOML sets
#: its own copy so wording iterates without a code change.
DEFAULT_WARNING_COPY = (
    "Just a heads up: we're coming up on the time limit for this chat, so let's "
    "start wrapping up."
)
DEFAULT_GOODBYE_COPY = "That's my cue to go — thanks for chatting, take care!"


@dataclass(frozen=True)
class QuotaConfig:
    """Race-safe quota enforcement + wind-down/teardown knobs (QUOT-01/02/03/05,
    D-01..D-03, D-04..D-07, D-09, D-14).

    Loaded independently of :class:`PipelineConfig` via :func:`load_quota_config`
    (not a ``PipelineConfig`` field) so existing pipeline-only fixtures/tests
    that omit ``[quota]`` are unaffected — only quota.py/session.py callers
    need this.
    """

    heartbeat_renew_interval: float  # seconds between ticks (D-01/D-02)
    heartbeat_ttl: float  # seconds; a crashed task's lease self-expires after this
    sub_floor_seconds: float  # D-03: block session start below this much remaining daily time
    per_task_max_sessions: int  # D-14: per-task soft cap -> retryable at-capacity reject
    auto_trip_ceiling_seconds: float  # D-09: site-wide daily seconds ceiling
    auto_trip_ceiling_dollars: float  # D-09: site-wide daily est.-cost ceiling
    est_cost_per_second: float  # coarse blended cost estimate (CONTEXT.md: precision deferred)
    # --- 04-05: spoken wind-down + layered idle teardown (QUOT-03/QUOT-05) ---
    winddown_warning_seconds: float = 30.0  # D-04: lead time before session_max for the warning
    goodbye_grace_seconds: float = 5.0  # D-05: cap on letting the goodbye TTS finish before hard-close
    user_silence_timeout: float = 50.0  # D-06 layer 2: no user speech for this long -> teardown
    reconnect_grace_seconds: float = 12.0  # D-07: transport-disconnect grace before teardown
    warning_copy: str = DEFAULT_WARNING_COPY  # D-04: injected as a high-priority LLM instruction
    goodbye_copy: str = DEFAULT_GOODBYE_COPY  # D-04/D-05: spoken straight to TTS, bypassing the LLM


def _reject_credential_fields(data: object, path: str = "") -> None:
    """Recursively reject any field whose name suggests credential material."""
    if isinstance(data, dict):
        for key, value in data.items():
            key_path = f"{path}.{key}" if path else key
            if _CREDENTIAL_FIELD_RE.search(str(key)):
                raise ConfigError(
                    f"pipeline.toml field '{key_path}' looks like credential material. "
                    "Secrets never live in TOML (D-09) — API keys come from .env "
                    "via `make -C apps/voice env`."
                )
            _reject_credential_fields(value, key_path)
    elif isinstance(data, list):
        for i, item in enumerate(data):
            _reject_credential_fields(item, f"{path}[{i}]")


def _require_range(name: str, value: float, lo: float, hi: float) -> float:
    value = float(value)
    if not (lo <= value <= hi):
        raise ConfigError(f"{name} must be within {lo}-{hi}, got {value}")
    return value


def _require_positive_under(name: str, value: float, ceiling: float) -> float:
    value = float(value)
    if not (0 < value < ceiling):
        raise ConfigError(f"{name} must be positive and under {ceiling}s, got {value}")
    return value


def _require_table(data: dict, name: str) -> dict:
    table = data.get(name)
    if not isinstance(table, dict):
        raise ConfigError(f"pipeline.toml is missing the [{name}] table")
    return table


def _resolve_config_path(path: Path | str | None) -> Path:
    """Resolution order: explicit ``path`` arg -> ``KLANKER_PIPELINE_CONFIG``
    env var -> ``apps/voice/pipeline.toml``."""
    if path is None:
        env_path = os.environ.get(CONFIG_PATH_ENV_VAR)
        path = Path(env_path) if env_path else DEFAULT_CONFIG_PATH
    return Path(path).expanduser().resolve()


def _load_toml_data(path: Path) -> dict:
    """Parse ``path`` as TOML and reject any credential-looking field (T-1-04).

    Shared by :func:`load_config` and :func:`load_quota_config` so both stay
    subject to the same "no secrets in TOML" gate on the same file.
    """
    if not path.is_file():
        raise ConfigError(f"pipeline config not found: {path}")

    with path.open("rb") as f:
        try:
            data = tomllib.load(f)
        except tomllib.TOMLDecodeError as exc:
            raise ConfigError(f"invalid TOML in {path}: {exc}") from exc

    _reject_credential_fields(data)
    return data


def load_config(path: Path | str | None = None) -> PipelineConfig:
    """Parse and validate a pipeline TOML file into a ``PipelineConfig``.

    Resolution order for the config path:
    1. explicit ``path`` argument,
    2. ``KLANKER_PIPELINE_CONFIG`` env var,
    3. ``apps/voice/pipeline.toml``.
    """
    path = _resolve_config_path(path)
    data = _load_toml_data(path)

    stt_table = _require_table(data, "stt")
    turn_table = _require_table(data, "turn")
    llm_table = _require_table(data, "llm")
    tts_table = _require_table(data, "tts")
    persona_table = _require_table(data, "persona")

    # --- stt ---
    stt_provider = str(stt_table.get("provider", ""))
    if stt_provider not in ALLOWED_STT_PROVIDERS:
        raise ConfigError(
            f"unknown stt.provider {stt_provider!r}; allowed: {sorted(ALLOWED_STT_PROVIDERS)}"
        )
    flux_table = stt_table.get("flux", {})
    flux = FluxConfig(
        eot_threshold=_require_range(
            "stt.flux.eot_threshold", flux_table.get("eot_threshold", 0.7), 0.5, 0.9
        ),
        eager_eot_threshold=_require_range(
            "stt.flux.eager_eot_threshold",
            flux_table.get("eager_eot_threshold", 0.0),
            0.0,
            0.9,
        ),
    )
    stt = SttConfig(provider=stt_provider, model=str(stt_table.get("model", "")), flux=flux)

    # --- turn ---
    turn_strategy = str(turn_table.get("strategy", ""))
    if turn_strategy not in ALLOWED_TURN_STRATEGIES:
        raise ConfigError(
            f"unknown turn.strategy {turn_strategy!r}; allowed: {sorted(ALLOWED_TURN_STRATEGIES)}"
        )
    turn = TurnConfig(
        strategy=turn_strategy,
        vad_stop_secs=_require_positive_under(
            "turn.vad_stop_secs", turn_table.get("vad_stop_secs", 0.2), 5.0
        ),
        user_speech_timeout=_require_positive_under(
            "turn.user_speech_timeout", turn_table.get("user_speech_timeout", 0.6), 5.0
        ),
    )

    # --- llm ---
    llm_provider = str(llm_table.get("provider", ""))
    if llm_provider not in ALLOWED_LLM_PROVIDERS:
        raise ConfigError(
            f"unknown llm.provider {llm_provider!r}; allowed: {sorted(ALLOWED_LLM_PROVIDERS)}"
        )
    llm = LlmConfig(provider=llm_provider, model=str(llm_table.get("model", "")))

    # --- tts ---
    tts_provider = str(tts_table.get("provider", ""))
    if tts_provider not in ALLOWED_TTS_PROVIDERS:
        raise ConfigError(
            f"unknown tts.provider {tts_provider!r}; allowed: {sorted(ALLOWED_TTS_PROVIDERS)}"
        )
    tts = TtsConfig(
        provider=tts_provider,
        model=str(tts_table.get("model", "")),
        voice_id=str(tts_table.get("voice_id", "")),
        speed=_require_range("tts.speed", tts_table.get("speed", 1.0), 0.7, 1.2),
        # 07.1 tunable voice character (ElevenLabs voice_settings, 0.0-1.0).
        stability=_require_range("tts.stability", tts_table.get("stability", 0.4), 0.0, 1.0),
        similarity_boost=_require_range(
            "tts.similarity_boost", tts_table.get("similarity_boost", 0.85), 0.0, 1.0
        ),
        style=_require_range("tts.style", tts_table.get("style", 0.1), 0.0, 1.0),
    )

    # --- persona ---
    raw_prompt_path = persona_table.get("prompt_path")
    if not raw_prompt_path:
        raise ConfigError("persona.prompt_path is required")
    prompt_path = Path(str(raw_prompt_path))
    if not prompt_path.is_absolute():
        # Relative to the config file's directory (apps/voice for the checked-in file).
        prompt_path = (path.parent / prompt_path).resolve()
    if not prompt_path.is_file():
        raise ConfigError(f"persona prompt not found: {prompt_path}")
    persona = PersonaConfig(
        prompt_path=prompt_path,
        greet_first=bool(persona_table.get("greet_first", True)),
    )

    # --- label (display-only, plain top-level scalar) ---
    label = str(data.get("label", "KPH"))

    return PipelineConfig(stt=stt, turn=turn, llm=llm, tts=tts, persona=persona, label=label)


def _resolve_relative_to(base_dir: Path, raw: str) -> Path:
    """Resolve ``raw`` relative to ``base_dir`` unless already absolute.

    Mirrors ``PersonaConfig.prompt_path`` resolution (config.py convention).
    """
    p = Path(raw)
    if not p.is_absolute():
        p = (base_dir / p).resolve()
    return p


def load_knowledge_config(path: Path | str | None = None) -> KnowledgeConfig:
    """Parse and validate the ``[knowledge]`` table into a ``KnowledgeConfig``.

    Same file/path resolution as :func:`load_config`. ``[knowledge]`` is
    required (like ``[quota]``, unlike ``PipelineConfig``'s other tables) --
    no knowledge-consuming caller wants a silent default for the manifest/
    topic-map/pack paths.
    """
    path = _resolve_config_path(path)
    data = _load_toml_data(path)
    knowledge_table = _require_table(data, "knowledge")

    manifest_path = _resolve_relative_to(
        path.parent, str(knowledge_table.get("manifest", "knowledge/manifest.yaml"))
    )
    if not manifest_path.is_file():
        raise ConfigError(f"knowledge manifest not found: {manifest_path}")

    topic_map_path = _resolve_relative_to(
        path.parent, str(knowledge_table.get("topic_map", "knowledge/router/topic-map.yaml"))
    )
    if not topic_map_path.is_file():
        raise ConfigError(f"knowledge topic_map not found: {topic_map_path}")

    packs_dir = _resolve_relative_to(
        path.parent, str(knowledge_table.get("packs_dir", "knowledge/topics"))
    )
    if not packs_dir.is_dir():
        raise ConfigError(f"knowledge packs_dir not found: {packs_dir}")

    style_path = _resolve_relative_to(
        path.parent, str(knowledge_table.get("style_path", "knowledge/style/kurt-voice.md"))
    )
    if not style_path.is_file():
        raise ConfigError(f"knowledge style_path not found: {style_path}")

    cache_floor = int(
        _require_positive_under(
            "knowledge.cache_floor", knowledge_table.get("cache_floor", 4096), 200000.0
        )
    )

    # --- 07-02: local BM25/FTS5 retrieval (Amendment 3-A/B/C, PIPE-07) ---
    retrieval_enabled = bool(knowledge_table.get("retrieval_enabled", True))
    index_dir = _resolve_relative_to(
        path.parent, str(knowledge_table.get("index_dir", "knowledge/index"))
    )
    if retrieval_enabled and not index_dir.is_dir():
        raise ConfigError(f"knowledge index_dir not found: {index_dir}")
    retrieval_top_k = int(
        _require_positive_under(
            "knowledge.retrieval_top_k", knowledge_table.get("retrieval_top_k", 4), 50.0
        )
    )
    retrieval_budget = int(
        _require_positive_under(
            "knowledge.retrieval_budget", knowledge_table.get("retrieval_budget", 1500), 200000.0
        )
    )

    return KnowledgeConfig(
        manifest_path=manifest_path,
        topic_map_path=topic_map_path,
        packs_dir=packs_dir,
        style_path=style_path,
        cache_floor=cache_floor,
        index_dir=index_dir,
        retrieval_enabled=retrieval_enabled,
        retrieval_top_k=retrieval_top_k,
        retrieval_budget=retrieval_budget,
    )


def _require_str_tuple(name: str, raw: object, default: tuple[str, ...]) -> tuple[str, ...]:
    """Parse an optional TOML array-of-strings into a lowercased tuple.

    Absent -> ``default``. A present-but-not-a-list value, or any non-string /
    empty entry, is a loud :class:`ConfigError` (a typo'd ``[duplex]`` list
    must fail at load, not silently drop entries).
    """
    if raw is None:
        return default
    if not isinstance(raw, list):
        raise ConfigError(f"{name} must be a TOML array of strings")
    out: list[str] = []
    for i, item in enumerate(raw):
        if not isinstance(item, str) or not item.strip():
            raise ConfigError(f"{name}[{i}] must be a non-empty string")
        out.append(item.strip().lower())
    if not out:
        raise ConfigError(f"{name} must not be empty when present")
    return tuple(out)


def load_duplex_config(path: Path | str | None = None) -> DuplexConfig:
    """Parse the OPTIONAL ``[duplex]`` table into a :class:`DuplexConfig`.

    Same file/path resolution as :func:`load_config`. Unlike ``[quota]`` /
    ``[knowledge]``, ``[duplex]`` is optional: a config file without it returns
    ``DuplexConfig()`` (disabled), so the shipped ``voice1`` pipeline and every
    fixture that predates full-duplex are unaffected. Only ``voice2`` sets it.
    """
    path = _resolve_config_path(path)
    data = _load_toml_data(path)
    table = data.get("duplex")
    if table is None:
        return DuplexConfig()
    if not isinstance(table, dict):
        raise ConfigError("pipeline.toml [duplex] must be a table")

    return DuplexConfig(
        enabled=bool(table.get("enabled", False)),
        backchannel_emitter=bool(table.get("backchannel_emitter", False)),
        max_backchannel_words=int(
            _require_positive_under(
                "duplex.max_backchannel_words", table.get("max_backchannel_words", 3), 20.0
            )
        ),
        interruption_hold_ms=int(
            _require_range(
                "duplex.interruption_hold_ms", table.get("interruption_hold_ms", 250), 0.0, 2000.0
            )
        ),
        emitter_min_gap_seconds=_require_range(
            "duplex.emitter_min_gap_seconds", table.get("emitter_min_gap_seconds", 4.0), 0.0, 60.0
        ),
        backchannel_words=_require_str_tuple(
            "duplex.backchannel_words", table.get("backchannel_words"), DEFAULT_BACKCHANNEL_WORDS
        ),
        emitter_phrases=_require_str_tuple(
            "duplex.emitter_phrases", table.get("emitter_phrases"), DEFAULT_EMITTER_PHRASES
        ),
    )


def load_quota_config(path: Path | str | None = None) -> QuotaConfig:
    """Parse and validate the ``[quota]`` table into a ``QuotaConfig``.

    Same file/path resolution as :func:`load_config`; ``[quota]`` is required
    (unlike ``PipelineConfig``'s other tables, no quota-consuming caller ever
    wants silent defaults for budget-guardrail numbers).
    """
    path = _resolve_config_path(path)
    data = _load_toml_data(path)
    quota_table = _require_table(data, "quota")

    heartbeat_renew_interval = _require_positive_under(
        "quota.heartbeat_renew_interval", quota_table.get("heartbeat_renew_interval", 15), 300.0
    )
    heartbeat_ttl = _require_positive_under(
        "quota.heartbeat_ttl", quota_table.get("heartbeat_ttl", 45), 600.0
    )
    if heartbeat_ttl <= heartbeat_renew_interval:
        raise ConfigError(
            "quota.heartbeat_ttl must exceed quota.heartbeat_renew_interval "
            f"(got ttl={heartbeat_ttl}, renew_interval={heartbeat_renew_interval})"
        )
    sub_floor_seconds = _require_range(
        "quota.sub_floor_seconds", quota_table.get("sub_floor_seconds", 30), 0.0, 600.0
    )
    per_task_max_sessions = int(
        _require_positive_under(
            "quota.per_task_max_sessions", quota_table.get("per_task_max_sessions", 5), 100.0
        )
    )
    auto_trip_ceiling_seconds = _require_positive_under(
        "quota.auto_trip_ceiling_seconds",
        quota_table.get("auto_trip_ceiling_seconds", 7200),
        86400.0 * 7,
    )
    auto_trip_ceiling_dollars = _require_positive_under(
        "quota.auto_trip_ceiling_dollars", quota_table.get("auto_trip_ceiling_dollars", 40), 10000.0
    )
    est_cost_per_second = _require_positive_under(
        "quota.est_cost_per_second", quota_table.get("est_cost_per_second", 0.005), 10.0
    )

    # --- 04-05: spoken wind-down + layered idle teardown (QUOT-03/QUOT-05) ---
    winddown_warning_seconds = _require_positive_under(
        "quota.winddown_warning_seconds", quota_table.get("winddown_warning_seconds", 30), 600.0
    )
    goodbye_grace_seconds = _require_positive_under(
        "quota.goodbye_grace_seconds", quota_table.get("goodbye_grace_seconds", 5), 60.0
    )
    user_silence_timeout = _require_range(
        "quota.user_silence_timeout", quota_table.get("user_silence_timeout", 50), 10.0, 300.0
    )
    reconnect_grace_seconds = _require_range(
        "quota.reconnect_grace_seconds", quota_table.get("reconnect_grace_seconds", 12), 1.0, 120.0
    )
    warning_copy = str(quota_table.get("warning_copy", DEFAULT_WARNING_COPY))
    goodbye_copy = str(quota_table.get("goodbye_copy", DEFAULT_GOODBYE_COPY))
    if not warning_copy.strip():
        raise ConfigError("quota.warning_copy must not be empty")
    if not goodbye_copy.strip():
        raise ConfigError("quota.goodbye_copy must not be empty")

    return QuotaConfig(
        heartbeat_renew_interval=heartbeat_renew_interval,
        heartbeat_ttl=heartbeat_ttl,
        sub_floor_seconds=sub_floor_seconds,
        per_task_max_sessions=per_task_max_sessions,
        auto_trip_ceiling_seconds=auto_trip_ceiling_seconds,
        auto_trip_ceiling_dollars=auto_trip_ceiling_dollars,
        est_cost_per_second=est_cost_per_second,
        winddown_warning_seconds=winddown_warning_seconds,
        goodbye_grace_seconds=goodbye_grace_seconds,
        user_silence_timeout=user_silence_timeout,
        reconnect_grace_seconds=reconnect_grace_seconds,
        warning_copy=warning_copy,
        goodbye_copy=goodbye_copy,
    )
