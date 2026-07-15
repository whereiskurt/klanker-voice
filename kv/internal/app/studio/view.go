package studio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AssembleInput carries every adapter's already-fetched output — DynamoDB
// codes/tiers/phone mappings, the repo-file manifest/topic-map/gate mode —
// so AssembleConfig is a pure, deterministic, table-testable function with
// no AWS or filesystem access of its own, with ONE narrow, deliberate
// exception: Root below, used solely to compute each KnowledgePack's
// KNOW-01 token-size estimate directly from its already-on-disk pack file
// (packTokenEstimate) — re-plumbing a pre-read map of pack bytes through
// this struct for a single cheap len/4 estimate would be more machinery,
// not less. Every other field here is still adapter-pre-fetched.
type AssembleInput struct {
	Region  string
	Profile string
	Table   string

	Codes         []CodeRecord
	Tiers         []TierRecord
	PhoneMappings []PhoneMappingRecord

	Manifest []KnowledgePack
	Unlocks  []Unlock
	GateMode string

	// Root is RepoFiles.Root (the klanker-voice repo root) — used ONLY to
	// locate each KnowledgePack.Pack's on-disk markdown file under
	// knowledgePacksDir for TokenEstimate (KNOW-01). Empty in a table test
	// that doesn't care about token estimates — TokenEstimate then degrades
	// to 0 for every pack (packTokenEstimate's nil-root guard), never an
	// error.
	Root string

	// RuleOrder is rule-order.yaml's operator-facing display order (RULE-03,
	// presentation only — see studio_files.go's WriteRuleOrder doc comment).
	// An empty/nil slice falls back to the existing DynamoDB read order.
	RuleOrder []string

	// InboundDIDs is the Plan-04 merged (live VoIP.ms + dids.yaml metadata)
	// inbound-DID list (DID-01/02), already assembled by the caller (see
	// server.go's mergedInboundDIDs) — AssembleConfig only carries it
	// through into the ConfigView, keeping this function's own I/O-free
	// contract intact.
	InboundDIDs []InboundDID

	// DynamoErr, when non-nil, short-circuits assembly: AssembleConfig
	// returns Meta + a structured ErrorBanner and empty (non-nil) Rules/
	// DIDs/Knowledge/Secrets — never a partial or blank view (spec §8).
	DynamoErr error
}

// AssembleConfig projects the three stores' adapter outputs into a single
// ConfigView per spec §5's Rule field->store mapping:
//
//   - who.numbers = the code's phone attribute; who.type = "known" when a
//     phone is set, else "any" (block is not populated in v1 — no blocklist
//     source exists yet).
//   - grant joins AccessCode.tierId -> the matching Tier row (minutes =
//     sessionMaxSeconds/60, periodMin = periodMaxSeconds/60, concurrency =
//     maxConcurrent).
//   - secret.mode = the telephony.toml gate_mode; secret.ref = the access-pin
//     SSM param name (a reference only — see secret_adapter.go).
//   - unlocks = every topic-map keyword phrase (same set attached to every
//     rule in v1 — the router is global, not per-code).
//   - knowledge = every manifest pack id (v1: "all rules can reach all
//     packs", spec §5) — same reasoning as UsedByRules below.
//   - persona = "concierge" (single persona in v1, spec §5).
//
// DIDs are one row per phone mapping. Knowledge packs' UsedByRules is the
// count of rules (v1: every rule reaches every pack, spec §5).
func AssembleConfig(ctx context.Context, in AssembleInput) ConfigView {
	meta := Meta{
		Region:       in.Region,
		Profile:      in.Profile,
		Table:        in.Table,
		ImportedAtMs: time.Now().UnixMilli(),
		Generator:    "kv studio",
	}

	if in.DynamoErr != nil {
		return ConfigView{
			Meta:        meta,
			Rules:       []Rule{},
			DIDs:        []DID{},
			Knowledge:   []KnowledgePack{},
			Secrets:     []SecretRef{},
			InboundDIDs: []InboundDID{},
			CompilesTo:  compilesToMap(),
			Error: &ErrorBanner{
				Store:   "dynamodb",
				Region:  in.Region,
				Profile: in.Profile,
				Message: fmt.Sprintf("can't reach DynamoDB in %s with profile %s", in.Region, in.Profile),
			},
		}
	}

	tiersByID := make(map[string]TierRecord, len(in.Tiers))
	for _, t := range in.Tiers {
		tiersByID[t.TierID] = t
	}

	knowledgeIDs := make([]string, 0, len(in.Manifest))
	for _, k := range in.Manifest {
		knowledgeIDs = append(knowledgeIDs, k.ID)
	}

	rules := make([]Rule, 0, len(in.Codes))
	for _, c := range in.Codes {
		whoType := "any"
		numbers := []string{}
		if c.Phone != "" {
			whoType = "known"
			numbers = []string{c.Phone}
		}

		tier := tiersByID[c.TierID]
		grant := GrantSpec{
			Minutes:     tier.SessionMaxSeconds / 60,
			PeriodMin:   tier.PeriodMaxSeconds / 60,
			Concurrency: tier.MaxConcurrent,
			TierID:      c.TierID,
		}

		rules = append(rules, Rule{
			ID:  c.Code,
			Who: WhoSpec{Type: whoType, Numbers: numbers},
			Secret: SecretSpec{
				Mode: in.GateMode,
				Ref:  telephonyAccessPinParam,
			},
			Unlocks:   in.Unlocks,
			Grant:     grant,
			Knowledge: knowledgeIDs,
			Persona:   "concierge",
		})
	}

	dids := make([]DID, 0, len(in.PhoneMappings))
	for _, p := range in.PhoneMappings {
		dids = append(dids, DID{
			Phone:   p.Phone,
			Code:    p.Code,
			TierID:  p.TierID,
			Enabled: p.PhoneEnabled,
		})
	}

	knowledge := make([]KnowledgePack, len(in.Manifest))
	copy(knowledge, in.Manifest)
	for i := range knowledge {
		knowledge[i].UsedByRules = len(rules)
		knowledge[i].TokenEstimate = packTokenEstimate(in.Root, knowledge[i].Pack)
	}

	inboundDIDs := in.InboundDIDs
	if inboundDIDs == nil {
		inboundDIDs = []InboundDID{}
	}

	return ConfigView{
		Meta:        meta,
		Rules:       applyRuleOrder(rules, in.RuleOrder),
		DIDs:        dids,
		Knowledge:   knowledge,
		Secrets:     ReadSecretRefs(in.GateMode),
		InboundDIDs: inboundDIDs,
		CompilesTo:  compilesToMap(),
	}
}

// knowledgePacksDir is the repo-root-relative directory KnowledgePack.Pack
// filenames resolve against for KNOW-01's token-size estimate —
// apps/voice/src/klanker_voice/config.py's load_knowledge_config defaults
// [knowledge].packs_dir to "knowledge/topics" resolved relative to
// apps/voice, i.e. apps/voice/knowledge/topics — matching
// repofile_adapter.go's manifestPath/topicMapPath repo-root-relative
// convention.
const knowledgePacksDir = "apps/voice/knowledge/topics"

// packTokenEstimate returns a cheap len(bytes)/4 token-size estimate for a
// KnowledgePack's on-disk markdown file (root/knowledgePacksDir/pack) — NOT
// an Anthropic token-count API call (17-RESEARCH.md Pattern 1 / Assumption
// A3: unnecessary cost+latency for a console listing). Returns 0 — never an
// error, never a panic — when root or pack is empty, or the file is
// missing/unreadable (a not-yet-built pack): a read-only view must never
// fail on that.
func packTokenEstimate(root, pack string) int {
	if root == "" || pack == "" {
		return 0
	}
	b, err := os.ReadFile(filepath.Join(root, knowledgePacksDir, pack))
	if err != nil {
		return 0
	}
	return len(b) / 4
}

// applyRuleOrder returns rules reordered per order — a stable partition, not
// a sort: every code id listed in order appears first, in that exact
// sequence; every rule NOT listed in order is appended afterward, in its
// original (DynamoDB read) relative order. An empty/nil order is a no-op
// (RULE-03: the display falls back to DynamoDB read order). This ordering
// is presentation-only — see studio_files.go's ReadRuleOrder/WriteRuleOrder
// doc comments for why no runtime resolver ever consults it.
func applyRuleOrder(rules []Rule, order []string) []Rule {
	if len(order) == 0 {
		return rules
	}
	byID := make(map[string]Rule, len(rules))
	for _, r := range rules {
		byID[r.ID] = r
	}
	out := make([]Rule, 0, len(rules))
	used := make(map[string]bool, len(rules))
	for _, id := range order {
		if r, ok := byID[id]; ok && !used[id] {
			out = append(out, r)
			used[id] = true
		}
	}
	for _, r := range rules {
		if !used[r.ID] {
			out = append(out, r)
		}
	}
	return out
}

// compilesToMap is RULE-05's per-field backing-store metadata: the ACTUAL
// store every Rule/DID field round-trips to, named in the terms the
// console's "compiles to" panel shows an operator — DynamoDB
// (AccessCode/Tier), TOML (telephony.toml), YAML (topic-map.yaml /
// manifest.yaml / the two studio-owned files), or SSM (name-only secret
// references — this phase never reads/writes a secret VALUE, see
// secret_adapter.go). Static and table-driven per 16-RESEARCH.md's
// field->store enumeration, so RULE-05's panel is truthful, not a guess.
func compilesToMap() map[string]string {
	return map[string]string{
		"rule.who":                "DynamoDB (AccessCode.phone / gsi3pk — sparse byPhone index)",
		"rule.grant":              "DynamoDB (Tier item, joined by AccessCode.tierId)",
		"rule.secret.mode":        "TOML (apps/voice/configs/telephony.toml [telephony].gate_mode)",
		"rule.secret.requireGate": "TOML (apps/voice/configs/telephony.toml [telephony].require_gate)",
		"rule.secret.ref":         "SSM (parameter name only — value never read or written here)",
		"rule.unlocks":            "YAML (apps/voice/knowledge/router/topic-map.yaml keywords)",
		"rule.knowledge":          "YAML (apps/voice/knowledge/manifest.yaml — read-only display in v1)",
		"rule.order":              "YAML (apps/voice/configs/studio/rule-order.yaml — presentation only, not consulted by any runtime resolver)",
		"did.defaultRule":         "YAML (apps/voice/configs/studio/dids.yaml)",
		"did.greeting":            "YAML (apps/voice/configs/studio/dids.yaml)",
		"did.routing":             "VoIP.ms (live account state; not persisted locally)",
	}
}

// MergeInboundDIDs merges the live VoIP.ms inbound-DID list with the
// studio-owned dids.yaml metadata, keyed by Did: a live entry's Label/Region
// fall back to the metadata's when VoIP.ms doesn't supply one, and
// DefaultRule/Greeting always come from metadata (VoIP.ms has no concept of
// either). Metadata rows with no matching live entry are still included
// (e.g. VoIP.ms creds are absent this session, or the row was authored
// ahead of the DID actually being routed) so an operator never loses
// visibility into metadata they already wrote. Pure and I/O-free so it is
// directly unit-testable.
func MergeInboundDIDs(live []InboundDID, meta []DIDMeta) []InboundDID {
	metaByDID := make(map[string]DIDMeta, len(meta))
	for _, m := range meta {
		metaByDID[m.Did] = m
	}

	merged := make([]InboundDID, 0, len(live)+len(meta))
	seen := make(map[string]bool, len(live))
	for _, d := range live {
		if m, ok := metaByDID[d.Did]; ok {
			d.DefaultRule = m.DefaultRule
			d.Greeting = m.Greeting
			if d.Label == "" {
				d.Label = m.Label
			}
			if d.Region == "" {
				d.Region = m.Region
			}
		}
		merged = append(merged, d)
		seen[d.Did] = true
	}
	for _, m := range meta {
		if seen[m.Did] {
			continue
		}
		merged = append(merged, InboundDID{
			Did:         m.Did,
			Label:       m.Label,
			Region:      m.Region,
			DefaultRule: m.DefaultRule,
			Greeting:    m.Greeting,
		})
	}
	return merged
}
