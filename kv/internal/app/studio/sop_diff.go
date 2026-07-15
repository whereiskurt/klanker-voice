// Package studio's changeset differ (SOP-02): DiffChangeset compares a
// SOPDoc against the current live ConfigView, per surface (rule/tier/did/
// unlock/knowledge/gate/order), classifying every difference as added,
// changed, or removed — exactly what the operator reviews on the "Save &
// deploy" tab before pressing Deploy.
//
// Every function in this file is pure and read-only: inputs in,
// []ChangesetEntry out, no DynamoDB/repo-file/git call, no mutation of
// either input (T-18-04). The live side comes from AssembleConfig's
// ConfigView — DiffChangeset converts it via ToSOPDoc (sop.go) so both sides
// of every per-surface differ share the same store-shaped fields; this is
// the same "single source of truth" AssembleConfig already is for every
// other tab (18-RESEARCH.md "Don't Hand-Roll"), never a second read path.
package studio

// ChangesetEntry is one row of a computed changeset: a single surface-level
// difference between a SOPDoc and the current live config. Field/From/To are
// populated only for a field-level "changed" entry (RESEARCH.md Pattern 3) —
// an "added"/"removed" entry carries only Surface/Kind/Key.
type ChangesetEntry struct {
	Surface string `json:"surface"` // "rule" | "tier" | "did" | "unlock" | "knowledge" | "gate" | "order"
	Kind    string `json:"kind"`    // "added" | "changed" | "removed"
	Key     string `json:"key"`     // natural id: code, tierId, did, topic+term (phrase), pack id, "gate", "order"
	Field   string `json:"field,omitempty"`
	From    any    `json:"from,omitempty"`
	To      any    `json:"to,omitempty"`
}

// DiffChangeset compares doc (the named SOP being reviewed/deployed) against
// live (the current, freshly-assembled ConfigView — 18-06 assembles this
// fresh on every request; this function never caches it, T-18-06) and
// returns every per-surface difference in a fixed, deterministic surface
// order: rule, tier, did, unlock, knowledge, gate, order. An unchanged SOP
// (doc == ToSOPDoc(live) in every surface) yields an empty/nil slice — the
// idempotency property Apply (Plan 03) relies on to make a re-deploy a
// no-op.
func DiffChangeset(doc SOPDoc, live ConfigView) []ChangesetEntry {
	liveDoc := ToSOPDoc(live)

	var out []ChangesetEntry
	out = append(out, diffRules(doc.Rules, liveDoc.Rules)...)
	out = append(out, diffTiers(doc.Tiers, liveDoc.Tiers)...)
	out = append(out, diffDids(doc.Dids, liveDoc.Dids)...)
	out = append(out, diffUnlocks(doc.Unlocks, liveDoc.Unlocks)...)
	out = append(out, diffKnowledge(doc.Knowledge, liveDoc.Knowledge)...)
	out = append(out, diffGate(doc.Gate, liveDoc.Gate)...)
	out = append(out, diffOrder(doc.Order, liveDoc.Order)...)
	return out
}

// diffRules diffs SOPDoc.Rules against live, keyed by Code. A live-only
// code is surfaced as "removed" (so the operator SEES it — Apply never
// deletes an AccessCode). A rule whose TierID differs is surfaced as a
// "changed" entry field-by-field; when the SOP moves a code to the
// block/no-access tier (blockTierID, server.go) this is the SAME entry
// shape (Field: "tierId", To: blockTierID) — self-evident to the operator
// reviewing the changeset, never suppressed or silently applied (T-18-05).
func diffRules(sopRules, liveRules []SOPRule) []ChangesetEntry {
	var out []ChangesetEntry

	liveByCode := make(map[string]SOPRule, len(liveRules))
	for _, r := range liveRules {
		liveByCode[r.Code] = r
	}

	seen := make(map[string]bool, len(sopRules))
	for _, r := range sopRules {
		seen[r.Code] = true
		live, ok := liveByCode[r.Code]
		if !ok {
			out = append(out, ChangesetEntry{Surface: "rule", Kind: "added", Key: r.Code})
			continue
		}
		if live.Who.Type != r.Who.Type {
			out = append(out, ChangesetEntry{Surface: "rule", Kind: "changed", Key: r.Code,
				Field: "who.type", From: live.Who.Type, To: r.Who.Type})
		}
		if !equalStringSlices(live.Who.Numbers, r.Who.Numbers) {
			out = append(out, ChangesetEntry{Surface: "rule", Kind: "changed", Key: r.Code,
				Field: "who.numbers", From: live.Who.Numbers, To: r.Who.Numbers})
		}
		if live.TierID != r.TierID {
			out = append(out, ChangesetEntry{Surface: "rule", Kind: "changed", Key: r.Code,
				Field: "tierId", From: live.TierID, To: r.TierID})
		}
	}

	for _, r := range liveRules {
		if !seen[r.Code] {
			out = append(out, ChangesetEntry{Surface: "rule", Kind: "removed", Key: r.Code})
		}
	}
	return out
}

// diffTiers diffs SOPDoc.Tiers against live, keyed by TierID, field-level
// for the three DynamoDB Tier limits. A live-only tier is surfaced as
// "removed" so the operator SEES it, but Apply (Plan 03) will NEVER delete
// it — apply is additive+update-only (18-RESEARCH.md's locked decision).
func diffTiers(sopTiers, liveTiers []SOPTier) []ChangesetEntry {
	var out []ChangesetEntry

	liveByID := make(map[string]SOPTier, len(liveTiers))
	for _, t := range liveTiers {
		liveByID[t.TierID] = t
	}

	seen := make(map[string]bool, len(sopTiers))
	for _, t := range sopTiers {
		seen[t.TierID] = true
		live, ok := liveByID[t.TierID]
		if !ok {
			out = append(out, ChangesetEntry{Surface: "tier", Kind: "added", Key: t.TierID})
			continue
		}
		if live.SessionMaxSeconds != t.SessionMaxSeconds {
			out = append(out, ChangesetEntry{Surface: "tier", Kind: "changed", Key: t.TierID,
				Field: "sessionMaxSeconds", From: live.SessionMaxSeconds, To: t.SessionMaxSeconds})
		}
		if live.PeriodMaxSeconds != t.PeriodMaxSeconds {
			out = append(out, ChangesetEntry{Surface: "tier", Kind: "changed", Key: t.TierID,
				Field: "periodMaxSeconds", From: live.PeriodMaxSeconds, To: t.PeriodMaxSeconds})
		}
		if live.MaxConcurrent != t.MaxConcurrent {
			out = append(out, ChangesetEntry{Surface: "tier", Kind: "changed", Key: t.TierID,
				Field: "maxConcurrent", From: live.MaxConcurrent, To: t.MaxConcurrent})
		}
	}

	for _, t := range liveTiers {
		if !seen[t.TierID] {
			out = append(out, ChangesetEntry{Surface: "tier", Kind: "removed", Key: t.TierID})
		}
	}
	return out
}

// diffDids diffs SOPDoc.Dids against live, keyed by Did, field-level over
// the four studio-owned dids.yaml metadata fields (Label/Region/
// DefaultRule/Greeting) — never the live-only Routing field (that lives on
// InboundDID, not DIDMeta; ToSOPDoc already drops it, see sop.go).
func diffDids(sopDids, liveDids []DIDMeta) []ChangesetEntry {
	var out []ChangesetEntry

	liveByDid := make(map[string]DIDMeta, len(liveDids))
	for _, d := range liveDids {
		liveByDid[d.Did] = d
	}

	seen := make(map[string]bool, len(sopDids))
	for _, d := range sopDids {
		seen[d.Did] = true
		live, ok := liveByDid[d.Did]
		if !ok {
			out = append(out, ChangesetEntry{Surface: "did", Kind: "added", Key: d.Did})
			continue
		}
		if live.Label != d.Label {
			out = append(out, ChangesetEntry{Surface: "did", Kind: "changed", Key: d.Did,
				Field: "label", From: live.Label, To: d.Label})
		}
		if live.Region != d.Region {
			out = append(out, ChangesetEntry{Surface: "did", Kind: "changed", Key: d.Did,
				Field: "region", From: live.Region, To: d.Region})
		}
		if live.DefaultRule != d.DefaultRule {
			out = append(out, ChangesetEntry{Surface: "did", Kind: "changed", Key: d.Did,
				Field: "defaultRule", From: live.DefaultRule, To: d.DefaultRule})
		}
		if live.Greeting != d.Greeting {
			out = append(out, ChangesetEntry{Surface: "did", Kind: "changed", Key: d.Did,
				Field: "greeting", From: live.Greeting, To: d.Greeting})
		}
	}

	for _, d := range liveDids {
		if !seen[d.Did] {
			out = append(out, ChangesetEntry{Surface: "did", Kind: "removed", Key: d.Did})
		}
	}
	return out
}

// diffUnlocks diffs SOPDoc.Unlocks against live, keyed by Phrase (the
// topic-map keyword — v1's "topic+term" natural id, PLAN.md's wording), one
// changed entry when the phrase's Add (knowledge pack ids it unlocks)
// differs.
func diffUnlocks(sopUnlocks, liveUnlocks []Unlock) []ChangesetEntry {
	var out []ChangesetEntry

	liveByPhrase := make(map[string]Unlock, len(liveUnlocks))
	for _, u := range liveUnlocks {
		liveByPhrase[u.Phrase] = u
	}

	seen := make(map[string]bool, len(sopUnlocks))
	for _, u := range sopUnlocks {
		seen[u.Phrase] = true
		live, ok := liveByPhrase[u.Phrase]
		if !ok {
			out = append(out, ChangesetEntry{Surface: "unlock", Kind: "added", Key: u.Phrase})
			continue
		}
		if !equalStringSlices(live.Add, u.Add) {
			out = append(out, ChangesetEntry{Surface: "unlock", Kind: "changed", Key: u.Phrase,
				Field: "add", From: live.Add, To: u.Add})
		}
	}

	for _, u := range liveUnlocks {
		if !seen[u.Phrase] {
			out = append(out, ChangesetEntry{Surface: "unlock", Kind: "removed", Key: u.Phrase})
		}
	}
	return out
}

// diffKnowledge diffs SOPDoc.Knowledge against live, keyed by pack ID,
// field-level over spokenName/pack/sources — never the read-time-computed
// UsedByRules/TokenEstimate/Talkable fields (ToSOPDoc already drops those,
// see sop.go's SOPPack doc comment; they are never operator-authored, so
// they can never appear in a diff).
func diffKnowledge(sopPacks, livePacks []SOPPack) []ChangesetEntry {
	var out []ChangesetEntry

	liveByID := make(map[string]SOPPack, len(livePacks))
	for _, p := range livePacks {
		liveByID[p.ID] = p
	}

	seen := make(map[string]bool, len(sopPacks))
	for _, p := range sopPacks {
		seen[p.ID] = true
		live, ok := liveByID[p.ID]
		if !ok {
			out = append(out, ChangesetEntry{Surface: "knowledge", Kind: "added", Key: p.ID})
			continue
		}
		if live.SpokenName != p.SpokenName {
			out = append(out, ChangesetEntry{Surface: "knowledge", Kind: "changed", Key: p.ID,
				Field: "spokenName", From: live.SpokenName, To: p.SpokenName})
		}
		if live.Pack != p.Pack {
			out = append(out, ChangesetEntry{Surface: "knowledge", Kind: "changed", Key: p.ID,
				Field: "pack", From: live.Pack, To: p.Pack})
		}
		if !equalKnowledgeSources(live.Sources, p.Sources) {
			out = append(out, ChangesetEntry{Surface: "knowledge", Kind: "changed", Key: p.ID,
				Field: "sources", From: live.Sources, To: p.Sources})
		}
	}

	for _, p := range livePacks {
		if !seen[p.ID] {
			out = append(out, ChangesetEntry{Surface: "knowledge", Kind: "removed", Key: p.ID})
		}
	}
	return out
}

// diffGate diffs SOPDoc.Gate against live's hoisted gate config (v1 has
// exactly one gate config for every rule — sop.go's ToSOPDoc doc comment).
// Gate is a single global entity, so both entries — when present — share
// the fixed key "gate".
func diffGate(sopGate, liveGate SecretSpec) []ChangesetEntry {
	var out []ChangesetEntry

	if liveGate.Mode != sopGate.Mode {
		out = append(out, ChangesetEntry{Surface: "gate", Kind: "changed", Key: "gate",
			Field: "mode", From: liveGate.Mode, To: sopGate.Mode})
	}
	if liveGate.Ref != sopGate.Ref {
		out = append(out, ChangesetEntry{Surface: "gate", Kind: "changed", Key: "gate",
			Field: "ref", From: liveGate.Ref, To: sopGate.Ref})
	}
	return out
}

// diffOrder diffs SOPDoc.Order against live's rule-authoring display order
// (RULE-03, presentation only). Order is a single ordered list, not a
// per-item keyed collection, so a difference anywhere in the sequence
// (reordering, or a different membership) yields exactly one "changed"
// entry carrying both full lists — matching the "ordered code list" natural
// id PLAN.md calls out.
func diffOrder(sopOrder, liveOrder []string) []ChangesetEntry {
	if equalStringSlices(sopOrder, liveOrder) {
		return nil
	}
	return []ChangesetEntry{{Surface: "order", Kind: "changed", Key: "order", From: liveOrder, To: sopOrder}}
}

// equalStringSlices reports whether a and b hold the same strings in the
// same order (length-0 slices, nil or not, are equal).
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// equalKnowledgeSources reports whether a and b hold the same
// KnowledgeSource rows in the same order (every field is comparable, so a
// direct struct comparison is exact).
func equalKnowledgeSources(a, b []KnowledgeSource) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
