// Package studio is the read-only unified-config core for `kv studio` — the
// operator console's local server (docs/superpowers/specs/2026-07-15-kv-studio-operator-console-design.md).
//
// It assembles a single ConfigView from three live sources:
//   - DynamoDB, via kv/internal/app/electro key templates (codes, tiers, byPhone)
//   - repo config files (knowledge/manifest.yaml, knowledge/router/topic-map.yaml,
//     configs/telephony.toml's [telephony] block)
//   - SSM secret NAMES only (never values — no GetParameter/decrypt call lives
//     in this package)
//
// This phase (15-01) is deliberately server-less and cobra-less: no HTTP
// listener, no command wiring. Phase 15-02 wraps this package's AssembleConfig
// behind a local REST endpoint; Phase 16's write-path rule/DID editors and
// Phase 18's SOP snapshot both reuse this same read core so every consumer of
// the config sees byte-identical assembly logic.
package studio
