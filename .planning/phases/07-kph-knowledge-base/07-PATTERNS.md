# Phase 7: KPH Knowledge Base - Pattern Map

**Mapped:** 2026-07-05
**Files analyzed:** 6 new-artifact categories (router processor, prompt assembly, knowledge packs/manifest, distillation script, eval scenarios, refresh command)
**Analogs found:** 6 / 6 (one is a third-party base class, no in-repo subclass precedent)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|---|---|---|---|---|
| `apps/voice/src/klanker_voice/knowledge/router.py` (router `FrameProcessor`) | middleware/processor | request-response (frame interception) | `pipecat/processors/text_transformer.py` (`StatelessTextTransformer`, base class pattern) + `apps/voice/src/klanker_voice/pipeline.py` (pipeline assembly site) | role-match (no in-repo `FrameProcessor` subclass exists yet — third-party base is the analog) |
| System-prompt assembly w/ `cache_control` (edits to `pipeline.py` `build_pipeline`/`load_persona`, likely new `knowledge/prompt_assembly.py`) | service/transform | request-response | `apps/voice/src/klanker_voice/pipeline.py` `load_persona()` + `build_pipeline()` (lines 42-63); `factories.py` `_build_llm_anthropic` (settings-object construction) | exact (extends the identical `LLMContext(messages=[{"role": "system", ...}])` seam) |
| Per-topic knowledge packs + manifest (`apps/voice/knowledge/manifest.yaml`, `apps/voice/knowledge/packs/*.md`) | config/data | file-I/O (load-at-startup) | `apps/voice/prompts/concierge.md` (versioned markdown persona, loaded via `cfg.persona.prompt_path`) + `apps/voice/scenarios/*.yaml` (YAML data-file convention) | exact |
| Distillation script (`apps/voice/scripts/refresh_knowledge.py`) | utility/CLI script | batch (offline one-shot) | `apps/voice/scripts/audition.py` (one-shot script under `scripts/`, `.env` loading, artifact/manifest writing) | exact |
| Eval scenarios (`apps/voice/scenarios/kph_knowledge_*.yaml`, `kph_cache_verify.yaml`) | test | request-response (scripted turns + judge) | `apps/voice/scenarios/memory.yaml` (multi-turn scripted scenario using `judge_factory`) | exact |
| Knowledge-refresh command (`kv knowledge refresh` or `make -C apps/voice knowledge`) | route/CLI command | batch | `kv/internal/app/cmd/tier.go` (`NewTierCmd`, cobra subcommand pattern) OR `apps/voice/Makefile` (`env:` target invoking a script) | role-match (either home is a direct structural analog) |

## Pattern Assignments

### `apps/voice/src/klanker_voice/knowledge/router.py` (new `FrameProcessor` subclass)

**Analog:** `apps/voice/.venv/lib/python3.12/site-packages/pipecat/processors/text_transformer.py` (`StatelessTextTransformer`) — this is the only `FrameProcessor` subclass example available; the repo has none of its own yet, so this router is the first.

**Core pattern to copy** (`text_transformer.py` lines 15-51):
```python
class StatelessTextTransformer(FrameProcessor):
    def __init__(self, transform_fn):
        super().__init__()
        self._transform_fn = transform_fn

    async def process_frame(self, frame: Frame, direction: FrameDirection):
        await super().process_frame(frame, direction)
        if isinstance(frame, TextFrame):
            result = self._transform_fn(frame.text)
            ...
            await self.push_frame(TextFrame(text=result))
        else:
            await self.push_frame(frame, direction)
```
**How the router mirrors it:** subclass `FrameProcessor`, override `process_frame`, call `await super().process_frame(frame, direction)` first (mandatory pipecat contract), intercept `TranscriptionFrame` (import from `pipecat.frames.frames`, same module as `TextFrame`), pass everything else through unchanged via `await self.push_frame(frame, direction)`. On a topic match, do the keyword/rule classification (+ tiny-Haiku fallback via `factories.build_llm`-style construction — see `_build_llm_anthropic` in `factories.py` lines 92-98 for the `AnthropicLLMService(api_key=..., settings=Settings(model=...))` shape), push an ack `TextFrame`/`LLMMessagesAppendFrame` or similar to trigger the "OK! Let's dig into it" line, and mutate/replace the selected topic pack (likely by pushing a frame the LLM context aggregator or a new pipeline stage consumes — confirm with `LLMContextAggregatorPair` from `pipeline.py` line 16).

**Insertion point pattern** (`apps/voice/src/klanker_voice/pipeline.py` lines 47-75, `build_pipeline`):
```python
stt = build_stt(cfg)
llm = build_llm(cfg)
tts = build_tts(cfg)
context = LLMContext(messages=[{"role": "system", "content": load_persona(cfg)}])
aggregator_pair = LLMContextAggregatorPair(context, user_params=build_user_aggregator_params(cfg))
user_aggregator, assistant_aggregator = aggregator_pair.user(), aggregator_pair.assistant()
pipeline = Pipeline([
    transport.input(), stt, user_aggregator, llm, tts,
    transport.output(), assistant_aggregator,
])
```
**How the router mirrors it:** insert the new `KnowledgeRouterProcessor` instance in the `Pipeline([...])` list between `stt` and `user_aggregator` (per RESEARCH's stated insertion point "pre-LLM `FrameProcessor`... before `LLMContext`/`LLMContextAggregatorPair`"), constructed and passed in the same explicit-list style as the other stages — no hidden wiring, matching this file's existing "everything is explicit" convention (e.g. the comment on line 3-6 "no custom truncation bookkeeping here").

---

### System-prompt assembly with prompt-caching (extends `pipeline.py` + `factories.py`)

**Analog:** `apps/voice/src/klanker_voice/pipeline.py` `load_persona()` (lines 42-44) and `build_pipeline()`'s `LLMContext` construction (line 58); `factories.py` `_build_llm_anthropic` (lines 92-98) for the `Settings`-object convention.

**Current prompt assembly** (`pipeline.py` lines 42-58):
```python
def load_persona(cfg: PipelineConfig) -> str:
    """Read the versioned persona markdown (PIPE-06) from config."""
    return cfg.persona.prompt_path.read_text(encoding="utf-8")

...
context = LLMContext(messages=[{"role": "system", "content": load_persona(cfg)}])
```
**How the new assembly mirrors it:** keep `load_persona()`'s "read versioned markdown from `cfg`, return text" shape but split it into a stable-prefix builder (router instructions + Kurt-STYLE persona layer, concatenated, wrapped as a `system` block with `"cache_control": {"type": "ephemeral"}` per RESEARCH lines 148-151/314) and a per-turn topic-pack appender (second `system` block, no `cache_control`, swapped by the router). Follow the config-driven-path convention already used for `cfg.persona.prompt_path` (`config.py` `PersonaConfig.prompt_path`, lines 86-87) — add a parallel `knowledge` config table (manifest path, packs dir) validated the same way `_require_table`/`ConfigError` validates `persona` (`config.py` lines 130-134, 224-234). Never put the manifest path or pack paths through `_reject_credential_fields` false positives — confirm names don't match `_CREDENTIAL_FIELD_RE` (lines 36-40).

**Anthropic Settings-object convention to reuse** (`factories.py` lines 92-98):
```python
def _build_llm_anthropic(cfg: PipelineConfig) -> AnthropicLLMService:
    return AnthropicLLMService(
        api_key=_require_env("ANTHROPIC_API_KEY"),
        settings=AnthropicLLMService.Settings(model=cfg.llm.model),
    )
```
**How it mirrors it:** any direct Anthropic Messages API call needed to attach `cache_control` (if pipecat's `AnthropicLLMService` doesn't expose the block-level `cache_control` param through `Settings`) should be built the same way — `_require_env("ANTHROPIC_API_KEY")` for the key, never inlined literals, matching the project's single `_require_env` guard convention.

---

### Per-topic knowledge packs + manifest (`apps/voice/knowledge/`)

**Analog:** `apps/voice/prompts/concierge.md` (versioned persona markdown, loaded via `cfg.persona.prompt_path` in `config.py` lines 224-234) and `apps/voice/scenarios/*.yaml` (checked-in YAML data convention).

**Pattern to copy:** plain versioned markdown files under a checked-in directory (`apps/voice/prompts/` → `apps/voice/knowledge/packs/`), referenced by relative or config-resolved path exactly like `PersonaConfig.prompt_path` resolution (`config.py` lines 225-234):
```python
raw_prompt_path = persona_table.get("prompt_path")
...
prompt_path = Path(str(raw_prompt_path))
if not prompt_path.is_absolute():
    prompt_path = (path.parent / prompt_path).resolve()
if not prompt_path.is_file():
    raise ConfigError(f"persona prompt not found: {prompt_path}")
```
**How the manifest mirrors it:** add a `[knowledge]` table to `pipeline.toml` (manifest path + packs dir), validated with the identical `_require_table` / existence-check / `ConfigError` pattern, producing a new frozen `KnowledgeConfig` dataclass alongside `PersonaConfig` (`config.py` lines 85-96). The manifest file itself should follow the `apps/voice/scenarios/*.yaml` convention (plain YAML, human-edited, PyYAML — already a transitive dep per RESEARCH line 179) rather than inventing a new format — topic entries, priority order (D-05), and per-topic pack file references live there.

---

### Distillation script (`apps/voice/scripts/refresh_knowledge.py`)

**Analog:** `apps/voice/scripts/audition.py` (one-shot generator script under `apps/voice/scripts/`).

**Imports + `.env` pattern** (`audition.py` lines 1-37, 93-103):
```python
APP_ROOT = Path(__file__).resolve().parent.parent
OUT_DIR = APP_ROOT / "artifacts" / "audition"
...
def api_key() -> str:
    load_dotenv(APP_ROOT / ".env", override=True)
    key = os.environ.get("ELEVENLABS_API_KEY", "")
    if not key:
        print("ERROR: ... Run `make -C apps/voice env` ...", file=sys.stderr)
        sys.exit(1)
    return key
```
**Manifest/artifact-writing pattern** (`audition.py` lines 244-259, `main()`):
```python
manifest = {
    "rendered_at": datetime.now(timezone.utc).isoformat(timespec="seconds"),
    ...
}
manifest_path = OUT_DIR / "manifest.json"
manifest_path.write_text(json.dumps(manifest, indent=2) + "\n")
print(f"\nwrote {manifest_path.relative_to(APP_ROOT)}")
```
**How `refresh_knowledge.py` mirrors it:** same `APP_ROOT = Path(__file__).resolve().parent.parent` anchor, same `.env` loading via `load_dotenv(APP_ROOT / ".env", override=True)` and hard `sys.exit(1)` with an actionable `make -C apps/voice env` message if `ANTHROPIC_API_KEY` is missing (matches `_require_env`'s message style in `factories.py` line 61). Unlike `audition.py` (which writes gitignored artifacts), this script writes INTO tracked `apps/voice/knowledge/packs/*.md` and `manifest.yaml` — D-09 requires the output to land as an ordinary git diff for review, not a gitignored artifact directory. Read corpus inputs from the curated manifest only (D-01) — mirror `audition.py`'s explicit, non-magic control flow (`fetch_voices` → `score_voice` → `shortlist` → `render` → `main`) as a linear `survey_repo()` → `distill_topic()` → `style_pass()` → `write_pack()` pipeline (per DESIGN-NOTES' map-reduce shape: per-repo survey → per-topic distill → style pass). Use the same `AnthropicLLMService`/direct Anthropic-client construction convention as `factories.py`/`judge.py` (`_require_env` / `os.environ.get("ANTHROPIC_API_KEY")`, `judge.py` lines 48-56) for the LLM calls — no new vendor.

---

### Eval scenarios (`apps/voice/scenarios/kph_knowledge_*.yaml`, `kph_cache_verify.yaml`)

**Analog:** `apps/voice/scenarios/memory.yaml` (scripted multi-turn scenario using `judge_factory`) and `apps/voice/src/klanker_voice/harness/judge.py` (`judge_factory`).

**Scenario shape to copy** (`memory.yaml` lines 9-44):
```yaml
name: memory
user:
  modality: audio
  speech:
    service: kokoro
    voice: af_heart
judge:
  modality: audio
  transcription:
    service: moonshine
  eval:
    factory: "klanker_voice.harness.judge.judge_factory"
turns:
  - expect:
      - event: tts_response
  - user: "Hi K. My name is Marvin. Please remember my name."
    expect:
      - event: response
        eval: >-
          Answer yes if the reply addresses or mentions the user by the name Marvin...
```
**Judge factory to reuse verbatim** (`judge.py` lines 34-60): `judge_factory(config)` builds an `AnthropicLLMService` from `ANTHROPIC_API_KEY`, honoring an optional `model` override — no code change needed, just reference `"klanker_voice.harness.judge.judge_factory"` in each new scenario's `judge.eval.factory`, exactly as `memory.yaml` does.

**How the new scenarios mirror it:** one YAML per launch topic (`kph_knowledge_km.yaml`, `kph_knowledge_defconrun.yaml`, `kph_knowledge_meshtk.yaml`) plus one unknowns scenario, each with a greeting-observe first turn (matching `memory.yaml`'s "K greets first" comment/turn at lines 22-24), then scripted `user:` turns whose `eval:` prompt checks factual coverage against a short expected-facts list (per RESEARCH line 461) — same lenient "accept close speech-to-text renderings" phrasing style seen in `memory.yaml` lines 28-33 and 42-44. `kph_cache_verify.yaml` follows the same turn-scripting shape but asserts on the harness's usage-reporting path for `cache_read_input_tokens > 0` (RESEARCH line 677) rather than an `eval:` judge prompt — check `harness/report.py` for how `TurnRecord`/usage metrics are captured and extend that assertion surface rather than inventing a new one.

---

### Knowledge-refresh command (`kv knowledge refresh` or `make -C apps/voice knowledge`)

**Analog A (kv CLI cobra subcommand):** `kv/internal/app/cmd/tier.go` (`NewTierCmd`).

**Pattern to copy** (`tier.go` lines 79-136):
```go
func NewTierCmd(cfg *Config) *cobra.Command {
	tierCmd := &cobra.Command{
		Use:   "tier",
		Short: "Manage tiers (session/period/concurrency limits)",
	}
	define := &cobra.Command{
		Use:   "define <tierId>",
		Short: "Define (create or replace) a tier",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error { ... },
	}
	...
	tierCmd.AddCommand(define)
	return tierCmd
}
```
**How `kv knowledge refresh` mirrors it:** if the planner picks the `kv` home, add `NewKnowledgeCmd(cfg *Config) *cobra.Command` in a new `kv/internal/app/cmd/knowledge.go`, following the identical `Use`/`Short`/`RunE` cobra-subcommand shape, registered in `kv/internal/app/cmd/root.go` the same way `tier`/`code` commands are (check `root.go` for the `AddCommand` call site). Since the actual work (LLM digest generation) is Python, `RunE` would `exec.Command` the Python script (`apps/voice/scripts/refresh_knowledge.py`) via `uv run python ...`, not reimplement distillation in Go — `kv` stays a thin dispatcher, matching its existing role as an AWS/DynamoDB-facing operator CLI, not a compute engine.

**Analog B (Make target):** `apps/voice/Makefile` (`env:` target).

**Pattern to copy** (`Makefile` lines 1-6):
```makefile
.PHONY: env

## env: fetch the three provider API keys from SSM and write .env (D-10)
env:
	bash scripts/bootstrap_env.sh
```
**How `make knowledge` mirrors it:** add a `.PHONY: knowledge` target with the same one-line doc-comment convention (`## knowledge: regenerate per-topic packs from the corpus (D-07)`), body `uv run python scripts/refresh_knowledge.py`, matching `env:`'s "just shell out to a script, no inline logic in the Makefile" convention. This is the lower-friction home if `kv` shouldn't grow a Python-shelling subcommand.

## Shared Patterns

### Config validation (`_require_table` / `ConfigError`)
**Source:** `apps/voice/src/klanker_voice/config.py` lines 43-45, 99-134
**Apply to:** any new `[knowledge]` TOML table — validate with the same `ConfigError` subclass and `_require_table`/existence-check helpers used for `persona`, `stt`, `turn`, `llm`, `tts`.

### `_require_env` / actionable missing-key errors
**Source:** `apps/voice/src/klanker_voice/factories.py` lines 57-63; mirrored in `apps/voice/src/klanker_voice/harness/judge.py` lines 48-55 and `apps/voice/scripts/audition.py` lines 93-103
**Apply to:** the router's tiny-Haiku fallback call, the distillation script's Anthropic calls, and the eval judge — all read `ANTHROPIC_API_KEY` the same way, fail loudly with `make -C apps/voice env` guidance, never fall back silently.

### Versioned-markdown-in-repo artifact convention
**Source:** `apps/voice/prompts/concierge.md` + `PersonaConfig.prompt_path` resolution (`config.py` lines 224-234)
**Apply to:** `apps/voice/knowledge/packs/*.md` and any style-layer file — checked-in, human-reviewable-diff, path resolved relative to the TOML file's directory, existence-checked at config load (not lazily at pipeline build time).

### Scenario/judge extension convention
**Source:** `apps/voice/scenarios/memory.yaml` + `apps/voice/src/klanker_voice/harness/judge.py` `judge_factory`
**Apply to:** all new `kph_knowledge_*.yaml` and `kph_cache_verify.yaml` scenarios — reuse `judge_factory` unchanged, only add new YAML files, no harness code changes required for the correctness scenarios.

## No Analog Found

| File | Role | Data Flow | Reason |
|---|---|---|---|
| Router `FrameProcessor` subclass itself | processor | request-response | No in-repo `FrameProcessor` subclass exists yet (only pipecat's own base classes and the project's `UserBotLatencyObserver` subclass in `observers.py`, which is an *Observer*, a different base class with a different lifecycle — `on_push_frame`/event-handler shape, not `process_frame`). Use the third-party `StatelessTextTransformer` as the structural analog and RESEARCH's stated insertion point (`pipeline.py`, between STT and `LLMContextAggregatorPair`) as the wiring analog. |

## Metadata

**Analog search scope:** `apps/voice/src/klanker_voice/` (all non-.venv `.py`), `apps/voice/scenarios/`, `apps/voice/scripts/`, `apps/voice/prompts/`, `kv/internal/app/cmd/`, `apps/voice/Makefile`, plus the installed `pipecat` package under `apps/voice/.venv` for base-class reference.
**Files scanned:** ~14 project source files (factories.py, pipeline.py, config.py, observers.py, judge.py, audition.py, memory.yaml, tier.go, root.go, Makefile) + 1 third-party reference (text_transformer.py).
**Pattern extraction date:** 2026-07-05

## PATTERN MAPPING COMPLETE
