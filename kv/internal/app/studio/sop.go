// Package studio's SOP snapshot primitive (SOP-01/SOP-04): a dedicated,
// store-shaped SOPDoc type plus a hand-rolled, fixed-order YAML reader and
// writer for apps/voice/configs/studio/sops/<name>.yaml.
//
// This file intentionally does NOT add a YAML/TOML parser module dependency
// (18-RESEARCH.md's Standard Stack, mirroring repofile_adapter.go's package
// doc comment): WriteSOP emits fixed-order text via strings.Builder/
// fmt.Fprintf exactly like studio_files.go's formatDIDMetaBlock, and ReadSOP
// reuses repofile_adapter.go's existing scanYAMLLines/yamlKeyVal/yamlScalar
// line-scanner primitives — no new parser machinery.
package studio

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sopsDir is the SOP snapshot directory, relative to the klanker-voice repo
// root — mirrors studioDIDsPath/ruleOrderPath's repo-root-relative
// convention (studio_files.go).
const sopsDir = "apps/voice/configs/studio/sops"

// SOPDoc is the portable, versioned snapshot of a unified klanker-voice
// configuration (SOP-01), serialized to sops/<name>.yaml and git-committed.
// Every section is store-shaped — mirroring the underlying DynamoDB/repo-
// file stores, not the per-rule ConfigView projection: Rules carry only
// code/who/tierId (the per-rule Unlocks/Knowledge/Persona duplication
// ConfigView.Rule carries is NEVER copied here), and Tiers/Unlocks/
// Knowledge/Gate are hoisted to their own top-level sections exactly once
// (18-RESEARCH.md Pattern 1 — v1's "router is global, not per-code" quirk
// means every rule's Unlocks/Knowledge slice is identical; hoisting once
// keeps the file small and diff-friendly).
//
// SOPDoc deliberately has NO value-carrying secret field (SOP-04): Gate's
// embedded SecretSpec.Ref is a name only, inherited from types.go's existing
// SecretSpec/SecretRef shapes, which never had a value field to begin with.
type SOPDoc struct {
	// Name is the SOP's identifier (also its filename stem). Set by the
	// caller at Save time — ToSOPDoc never populates it.
	Name string

	// CreatedAt is an RFC3339 timestamp set ONCE, by the caller, at Save
	// time — never computed inside ToSOPDoc/WriteSOP, so re-projecting and
	// re-writing the same live config is fully deterministic (re-saving
	// produces a byte-identical file when CreatedAt is held constant).
	CreatedAt string

	Rules     []SOPRule
	Tiers     []SOPTier
	Unlocks   []Unlock
	Knowledge []SOPPack
	Gate      SecretSpec
	Dids      []DIDMeta
	Order     []string
}

// SOPRule is one Rules row: the code, its WHO match, and a TierID reference
// into Tiers — never an embedded grant/unlocks/knowledge/persona (those are
// hoisted to their own top-level sections; a SOP has ONE knowledge/unlocks
// list, not one per rule).
type SOPRule struct {
	Code   string
	Who    WhoSpec
	TierID string
}

// SOPTier is one Tiers row: a tier id and its three DynamoDB Tier-item
// limits (dynamo_adapter.go's TierRecord shape), in seconds — the store's
// own unit, not ConfigView.Grant's derived minutes.
type SOPTier struct {
	TierID            string
	SessionMaxSeconds int64
	PeriodMaxSeconds  int64
	MaxConcurrent     int64
}

// SOPPack is one Knowledge row: a manifest.yaml topic's store-shaped fields
// only (id/spokenName/pack/sources) — never the read-time-computed
// UsedByRules/TokenEstimate/Talkable fields KnowledgePack carries, which are
// derived at AssembleConfig time from the current rule count and an on-disk
// file read, not from the manifest store itself.
type SOPPack struct {
	ID         string
	SpokenName string
	Pack       string
	Sources    []KnowledgeSource
}

// ToSOPDoc projects a live ConfigView into a store-shaped SOPDoc. Name and
// CreatedAt are left zero — the caller sets both once, at Save time (see
// SOPDoc's doc comment) — so ToSOPDoc itself is a pure, deterministic
// function of view alone: the same ConfigView always yields the same
// Rules/Tiers/Unlocks/Knowledge/Gate/Dids/Order.
//
// Dropped on purpose, and never copied into the returned SOPDoc: the view's
// per-field backing-store metadata map, its read-time import timestamp, and
// each merged inbound-DID row's live telephony-carrier state — none of those
// are store-shaped, all three are computed fresh on every read, and copying
// any of them would churn a SOP's diff on every save for no
// operator-meaningful reason.
func ToSOPDoc(view ConfigView) SOPDoc {
	doc := SOPDoc{
		Rules: make([]SOPRule, 0, len(view.Rules)),
		Order: make([]string, 0, len(view.Rules)),
	}

	seenTiers := make(map[string]bool, len(view.Rules))
	for _, r := range view.Rules {
		doc.Rules = append(doc.Rules, SOPRule{
			Code:   r.ID,
			Who:    r.Who,
			TierID: r.Grant.TierID,
		})
		doc.Order = append(doc.Order, r.ID)

		if !seenTiers[r.Grant.TierID] {
			seenTiers[r.Grant.TierID] = true
			doc.Tiers = append(doc.Tiers, SOPTier{
				TierID:            r.Grant.TierID,
				SessionMaxSeconds: r.Grant.Minutes * 60,
				PeriodMaxSeconds:  r.Grant.PeriodMin * 60,
				MaxConcurrent:     r.Grant.Concurrency,
			})
		}
	}

	// v1 has exactly one gate config and one unlocks list for every rule
	// (view.go's AssembleConfig doc comment: "the router is global, not
	// per-code") — hoist from the first rule rather than duplicating per
	// row. With no rules yet, both stay at their zero value (nothing
	// operator-set to snapshot).
	if len(view.Rules) > 0 {
		doc.Unlocks = view.Rules[0].Unlocks
		doc.Gate = view.Rules[0].Secret
	}

	doc.Knowledge = make([]SOPPack, 0, len(view.Knowledge))
	for _, k := range view.Knowledge {
		doc.Knowledge = append(doc.Knowledge, SOPPack{
			ID:         k.ID,
			SpokenName: k.SpokenName,
			Pack:       k.Pack,
			Sources:    k.Sources,
		})
	}

	// view's merged per-DID rows carry a live telephony-carrier field this
	// snapshot must never persist (see this function's doc comment) —
	// project only the studio-owned metadata fields across.
	doc.Dids = make([]DIDMeta, 0, len(view.InboundDIDs))
	for _, d := range view.InboundDIDs {
		doc.Dids = append(doc.Dids, DIDMeta{
			Did:         d.Did,
			Label:       d.Label,
			Region:      d.Region,
			DefaultRule: d.DefaultRule,
			Greeting:    d.Greeting,
		})
	}

	return doc
}

// --------------------------------------------------------------------------
// WriteSOP: hand-rolled deterministic YAML emitter.

// WriteSOP renders doc as fixed-order YAML text and writes it to
// root/apps/voice/configs/studio/sops/<name>.yaml (0o644), overwriting any
// existing file. Every section is emitted in the same declared order every
// call, every list is emitted in its slice order (never sorted/reordered),
// and every scalar is %q-quoted — so two calls with an identical doc produce
// byte-identical output (SOP-01's "deterministic re-emit" truth).
func WriteSOP(root, name string, doc SOPDoc) error {
	var b strings.Builder

	fmt.Fprintf(&b, "name: %q\n", doc.Name)
	fmt.Fprintf(&b, "createdAt: %q\n", doc.CreatedAt)

	writeSOPRules(&b, doc.Rules)
	writeSOPTiers(&b, doc.Tiers)
	writeSOPUnlocks(&b, doc.Unlocks)
	writeSOPKnowledge(&b, doc.Knowledge)
	writeSOPGate(&b, doc.Gate)
	writeSOPDids(&b, doc.Dids)
	writeSOPOrder(&b, doc.Order)

	path := filepath.Join(root, sopsDir, name+".yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return &RepoFileError{Path: path, Err: err}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return &RepoFileError{Path: path, Err: err}
	}
	return nil
}

func writeSOPRules(b *strings.Builder, rules []SOPRule) {
	if len(rules) == 0 {
		b.WriteString("rules: []\n")
		return
	}
	b.WriteString("rules:\n")
	for _, r := range rules {
		fmt.Fprintf(b, "  - code: %q\n", r.Code)
		fmt.Fprintf(b, "    tierId: %q\n", r.TierID)
		b.WriteString("    who:\n")
		fmt.Fprintf(b, "      type: %q\n", r.Who.Type)
		if len(r.Who.Numbers) == 0 {
			b.WriteString("      numbers: []\n")
		} else {
			b.WriteString("      numbers:\n")
			for _, n := range r.Who.Numbers {
				fmt.Fprintf(b, "        - %q\n", n)
			}
		}
	}
}

func writeSOPTiers(b *strings.Builder, tiers []SOPTier) {
	if len(tiers) == 0 {
		b.WriteString("tiers: []\n")
		return
	}
	b.WriteString("tiers:\n")
	for _, t := range tiers {
		fmt.Fprintf(b, "  - tierId: %q\n", t.TierID)
		fmt.Fprintf(b, "    sessionMaxSeconds: %d\n", t.SessionMaxSeconds)
		fmt.Fprintf(b, "    periodMaxSeconds: %d\n", t.PeriodMaxSeconds)
		fmt.Fprintf(b, "    maxConcurrent: %d\n", t.MaxConcurrent)
	}
}

func writeSOPUnlocks(b *strings.Builder, unlocks []Unlock) {
	if len(unlocks) == 0 {
		b.WriteString("unlocks: []\n")
		return
	}
	b.WriteString("unlocks:\n")
	for _, u := range unlocks {
		fmt.Fprintf(b, "  - phrase: %q\n", u.Phrase)
		if len(u.Add) == 0 {
			b.WriteString("    add: []\n")
		} else {
			b.WriteString("    add:\n")
			for _, id := range u.Add {
				fmt.Fprintf(b, "      - %q\n", id)
			}
		}
	}
}

func writeSOPKnowledge(b *strings.Builder, packs []SOPPack) {
	if len(packs) == 0 {
		b.WriteString("knowledge: []\n")
		return
	}
	b.WriteString("knowledge:\n")
	for _, k := range packs {
		fmt.Fprintf(b, "  - id: %q\n", k.ID)
		fmt.Fprintf(b, "    spokenName: %q\n", k.SpokenName)
		fmt.Fprintf(b, "    pack: %q\n", k.Pack)
		if len(k.Sources) == 0 {
			b.WriteString("    sources: []\n")
		} else {
			b.WriteString("    sources:\n")
			for _, s := range k.Sources {
				fmt.Fprintf(b, "      - path: %q\n", s.Path)
				fmt.Fprintf(b, "        kind: %q\n", s.Kind)
				fmt.Fprintf(b, "        public: %t\n", s.Public)
			}
		}
	}
}

func writeSOPGate(b *strings.Builder, gate SecretSpec) {
	b.WriteString("gate:\n")
	fmt.Fprintf(b, "  mode: %q\n", gate.Mode)
	fmt.Fprintf(b, "  ref: %q\n", gate.Ref)
}

func writeSOPDids(b *strings.Builder, dids []DIDMeta) {
	if len(dids) == 0 {
		b.WriteString("dids: []\n")
		return
	}
	b.WriteString("dids:\n")
	for _, d := range dids {
		fmt.Fprintf(b, "  - did: %q\n", d.Did)
		if d.Label != "" {
			fmt.Fprintf(b, "    label: %q\n", d.Label)
		}
		if d.Region != "" {
			fmt.Fprintf(b, "    region: %q\n", d.Region)
		}
		if d.DefaultRule != "" {
			fmt.Fprintf(b, "    defaultRule: %q\n", d.DefaultRule)
		}
		if d.Greeting != "" {
			fmt.Fprintf(b, "    greeting: %q\n", d.Greeting)
		}
	}
}

func writeSOPOrder(b *strings.Builder, order []string) {
	if len(order) == 0 {
		b.WriteString("order: []\n")
		return
	}
	b.WriteString("order:\n")
	for _, id := range order {
		fmt.Fprintf(b, "  - %q\n", id)
	}
}

// --------------------------------------------------------------------------
// ReadSOP: reuses repofile_adapter.go's line-scanner primitives — no new
// parser. The SOP's shape (no comments inside sections, no block scalars) is
// simpler than manifest.yaml/topic-map.yaml, so this is a smaller instance
// of the same traversal those readers already use.

// ReadSOP parses root/apps/voice/configs/studio/sops/<name>.yaml into a
// SOPDoc. A missing file returns a *RepoFileError (unlike the studio-owned
// dids.yaml/rule-order.yaml readers, a SOP name the caller asked for by name
// is expected to exist — there is no "seeded empty" convention for
// individual SOPs).
func ReadSOP(root, name string) (SOPDoc, error) {
	path := filepath.Join(root, sopsDir, name+".yaml")
	f, err := os.Open(path)
	if err != nil {
		return SOPDoc{}, &RepoFileError{Path: path, Err: err}
	}
	defer f.Close()

	lines, err := scanYAMLLines(f)
	if err != nil {
		return SOPDoc{}, &RepoFileError{Path: path, Err: err}
	}

	doc := SOPDoc{}
	if v, ok := findTopLevelScalar(lines, "name"); ok {
		doc.Name = v
	}
	if v, ok := findTopLevelScalar(lines, "createdAt"); ok {
		doc.CreatedAt = v
	}
	doc.Rules = parseSOPRules(lines)
	doc.Tiers = parseSOPTiers(lines)
	doc.Unlocks = parseSOPUnlocks(lines)
	doc.Knowledge = parseSOPKnowledge(lines)
	doc.Gate = parseSOPGate(lines)
	doc.Dids = parseSOPDids(lines)
	doc.Order = parseSOPOrder(lines)

	return doc, nil
}

// findTopLevelScalar returns the value of a top-level "<key>: <value>" line
// (yamlScalar-unquoted), or ("", false) if key is absent, or key is present
// only in block form ("<key>:" with no inline value — a list/object
// section, not a scalar).
func findTopLevelScalar(lines []yamlLine, key string) (string, bool) {
	prefix := key + ":"
	for _, l := range lines {
		if l.indent != 0 || !strings.HasPrefix(l.text, prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(l.text, prefix))
		if rest == "" {
			return "", false
		}
		_, val, hasVal := yamlKeyVal(l.text)
		if !hasVal {
			return "", false
		}
		return yamlScalar(val), true
	}
	return "", false
}

func parseSOPRules(lines []yamlLine) []SOPRule {
	rules := []SOPRule{}
	idx, isFlowEmpty, found := findTopLevelListKey(lines, "rules")
	if !found || isFlowEmpty {
		return rules
	}
	i := idx + 1
	for i < len(lines) && lines[i].indent > 0 {
		l := lines[i]
		if l.indent == 2 && strings.HasPrefix(l.text, "- code:") {
			r := SOPRule{Code: yamlScalar(strings.TrimPrefix(l.text, "- code:"))}
			i++
			for i < len(lines) && lines[i].indent >= 4 {
				fl := lines[i]
				if fl.indent != 4 {
					i++
					continue
				}
				key, val, hasVal := yamlKeyVal(fl.text)
				switch key {
				case "tierId":
					r.TierID = yamlScalar(val)
					i++
				case "who":
					_ = hasVal
					i++
					who, ni := parseSOPWho(lines, i)
					r.Who = who
					i = ni
				default:
					i++
					i = skipYAMLBlock(lines, i, fl.indent)
				}
			}
			rules = append(rules, r)
		} else {
			i++
		}
	}
	return rules
}

// parseSOPWho parses a rules[] item's `who:` block (fields at indent 6,
// `numbers:` items at indent 8), returning the parsed WhoSpec and the index
// just past the block.
func parseSOPWho(lines []yamlLine, i int) (WhoSpec, int) {
	who := WhoSpec{Numbers: []string{}}
	for i < len(lines) && lines[i].indent >= 6 {
		l := lines[i]
		if l.indent != 6 {
			i++
			continue
		}
		key, val, _ := yamlKeyVal(l.text)
		switch key {
		case "type":
			who.Type = yamlScalar(val)
			i++
		case "numbers":
			i++
			nums, ni := parseSOPScalarList(lines, i, 8)
			who.Numbers = nums
			i = ni
		default:
			i++
			i = skipYAMLBlock(lines, i, l.indent)
		}
	}
	return who, i
}

// parseSOPScalarList parses a `- "value"` list at the given item indent,
// returning the parsed values and the index just past the list.
func parseSOPScalarList(lines []yamlLine, i, itemIndent int) ([]string, int) {
	out := []string{}
	for i < len(lines) && lines[i].indent >= itemIndent {
		l := lines[i]
		if l.indent == itemIndent && strings.HasPrefix(l.text, "-") {
			out = append(out, yamlScalar(strings.TrimPrefix(l.text, "-")))
		}
		i++
	}
	return out, i
}

func parseSOPTiers(lines []yamlLine) []SOPTier {
	tiers := []SOPTier{}
	idx, isFlowEmpty, found := findTopLevelListKey(lines, "tiers")
	if !found || isFlowEmpty {
		return tiers
	}
	i := idx + 1
	for i < len(lines) && lines[i].indent > 0 {
		l := lines[i]
		if l.indent == 2 && strings.HasPrefix(l.text, "- tierId:") {
			t := SOPTier{TierID: yamlScalar(strings.TrimPrefix(l.text, "- tierId:"))}
			i++
			for i < len(lines) && lines[i].indent >= 4 {
				fl := lines[i]
				if fl.indent != 4 {
					i++
					continue
				}
				key, val, _ := yamlKeyVal(fl.text)
				switch key {
				case "sessionMaxSeconds":
					t.SessionMaxSeconds = parseSOPInt64(val)
					i++
				case "periodMaxSeconds":
					t.PeriodMaxSeconds = parseSOPInt64(val)
					i++
				case "maxConcurrent":
					t.MaxConcurrent = parseSOPInt64(val)
					i++
				default:
					i++
					i = skipYAMLBlock(lines, i, fl.indent)
				}
			}
			tiers = append(tiers, t)
		} else {
			i++
		}
	}
	return tiers
}

func parseSOPInt64(val string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func parseSOPUnlocks(lines []yamlLine) []Unlock {
	unlocks := []Unlock{}
	idx, isFlowEmpty, found := findTopLevelListKey(lines, "unlocks")
	if !found || isFlowEmpty {
		return unlocks
	}
	i := idx + 1
	for i < len(lines) && lines[i].indent > 0 {
		l := lines[i]
		if l.indent == 2 && strings.HasPrefix(l.text, "- phrase:") {
			u := Unlock{Phrase: yamlScalar(strings.TrimPrefix(l.text, "- phrase:")), Add: []string{}}
			i++
			for i < len(lines) && lines[i].indent >= 4 {
				fl := lines[i]
				if fl.indent != 4 {
					i++
					continue
				}
				key, _, _ := yamlKeyVal(fl.text)
				if key == "add" {
					i++
					add, ni := parseSOPScalarList(lines, i, 6)
					u.Add = add
					i = ni
				} else {
					i++
					i = skipYAMLBlock(lines, i, fl.indent)
				}
			}
			unlocks = append(unlocks, u)
		} else {
			i++
		}
	}
	return unlocks
}

func parseSOPKnowledge(lines []yamlLine) []SOPPack {
	packs := []SOPPack{}
	idx, isFlowEmpty, found := findTopLevelListKey(lines, "knowledge")
	if !found || isFlowEmpty {
		return packs
	}
	i := idx + 1
	for i < len(lines) && lines[i].indent > 0 {
		l := lines[i]
		if l.indent == 2 && strings.HasPrefix(l.text, "- id:") {
			p := SOPPack{ID: yamlScalar(strings.TrimPrefix(l.text, "- id:")), Sources: []KnowledgeSource{}}
			i++
			for i < len(lines) && lines[i].indent >= 4 {
				fl := lines[i]
				if fl.indent != 4 {
					i++
					continue
				}
				key, val, _ := yamlKeyVal(fl.text)
				switch key {
				case "spokenName":
					p.SpokenName = yamlScalar(val)
					i++
				case "pack":
					p.Pack = yamlScalar(val)
					i++
				case "sources":
					i++
					srcs, ni := parseSOPKnowledgeSources(lines, i)
					p.Sources = srcs
					i = ni
				default:
					i++
					i = skipYAMLBlock(lines, i, fl.indent)
				}
			}
			packs = append(packs, p)
		} else {
			i++
		}
	}
	return packs
}

// parseSOPKnowledgeSources parses a knowledge[] item's `sources:` list
// (items at indent 6, fields at indent 8) — mirrors
// repofile_adapter.go's parseYAMLSources.
func parseSOPKnowledgeSources(lines []yamlLine, i int) ([]KnowledgeSource, int) {
	out := []KnowledgeSource{}
	for i < len(lines) && lines[i].indent >= 6 {
		l := lines[i]
		if l.indent == 6 && strings.HasPrefix(l.text, "- path:") {
			src := KnowledgeSource{Path: yamlScalar(strings.TrimPrefix(l.text, "- path:"))}
			i++
			for i < len(lines) && lines[i].indent == 8 {
				key, val, _ := yamlKeyVal(lines[i].text)
				switch key {
				case "kind":
					src.Kind = yamlScalar(val)
					i++
				case "public":
					src.Public = yamlScalar(val) == "true"
					i++
				default:
					i++
					i = skipYAMLBlock(lines, i, 8)
				}
			}
			out = append(out, src)
		} else {
			i++
		}
	}
	return out, i
}

func parseSOPGate(lines []yamlLine) SecretSpec {
	gate := SecretSpec{}
	i := findTopLevelKey(lines, "gate")
	if i < 0 {
		return gate
	}
	i++
	for i < len(lines) && lines[i].indent > 0 {
		l := lines[i]
		if l.indent != 2 {
			i++
			continue
		}
		key, val, _ := yamlKeyVal(l.text)
		switch key {
		case "mode":
			gate.Mode = yamlScalar(val)
			i++
		case "ref":
			gate.Ref = yamlScalar(val)
			i++
		default:
			i++
			i = skipYAMLBlock(lines, i, l.indent)
		}
	}
	return gate
}

func parseSOPDids(lines []yamlLine) []DIDMeta {
	dids := []DIDMeta{}
	idx, isFlowEmpty, found := findTopLevelListKey(lines, "dids")
	if !found || isFlowEmpty {
		return dids
	}
	i := idx + 1
	for i < len(lines) && lines[i].indent > 0 {
		l := lines[i]
		if l.indent == 2 && strings.HasPrefix(l.text, "- did:") {
			d := DIDMeta{Did: yamlScalar(strings.TrimPrefix(l.text, "- did:"))}
			i++
			for i < len(lines) && lines[i].indent >= 4 {
				fl := lines[i]
				if fl.indent != 4 {
					i++
					continue
				}
				key, val, _ := yamlKeyVal(fl.text)
				switch key {
				case "label":
					d.Label = yamlScalar(val)
					i++
				case "region":
					d.Region = yamlScalar(val)
					i++
				case "defaultRule":
					d.DefaultRule = yamlScalar(val)
					i++
				case "greeting":
					d.Greeting = yamlScalar(val)
					i++
				default:
					i++
					i = skipYAMLBlock(lines, i, fl.indent)
				}
			}
			dids = append(dids, d)
		} else {
			i++
		}
	}
	return dids
}

func parseSOPOrder(lines []yamlLine) []string {
	order := []string{}
	idx, isFlowEmpty, found := findTopLevelListKey(lines, "order")
	if !found || isFlowEmpty {
		return order
	}
	i := idx + 1
	for i < len(lines) && lines[i].indent > 0 {
		l := lines[i]
		if l.indent == 2 && strings.HasPrefix(l.text, "-") {
			order = append(order, yamlScalar(strings.TrimPrefix(l.text, "-")))
		}
		i++
	}
	return order
}
