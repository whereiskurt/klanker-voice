package studio

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fixtureSOPDoc returns a fully-populated SOPDoc exercising every section —
// the round-trip/determinism/no-secret-value tests all build on this one
// fixture so a dropped-field regression in any section is caught.
func fixtureSOPDoc() SOPDoc {
	return SOPDoc{
		Name:      "conference-2026",
		CreatedAt: "2026-07-15T10:00:00Z",
		Rules: []SOPRule{
			{
				Code:   "greenhouse",
				Who:    WhoSpec{Type: "known", Numbers: []string{"+15555550100"}},
				TierID: "recruiting-30",
			},
			{
				Code:   "walkin",
				Who:    WhoSpec{Type: "any", Numbers: []string{}},
				TierID: "public-3",
			},
		},
		Tiers: []SOPTier{
			{TierID: "recruiting-30", SessionMaxSeconds: 1800, PeriodMaxSeconds: 3600, MaxConcurrent: 4},
			{TierID: "public-3", SessionMaxSeconds: 180, PeriodMaxSeconds: 600, MaxConcurrent: 4},
		},
		Unlocks: []Unlock{
			{Phrase: "resume", Add: []string{"resume-pack"}},
			{Phrase: "defcon", Add: []string{"defcon-pack", "km-pack"}},
		},
		Knowledge: []SOPPack{
			{
				ID:         "km",
				SpokenName: "the km CLI",
				Pack:       "km.md",
				Sources: []KnowledgeSource{
					{Path: "docs/km/README.md", Kind: "doc", Public: true},
				},
			},
			{
				ID:         "resume-pack",
				SpokenName: "Kurt's resume",
				Pack:       "resume.md",
				Sources:    []KnowledgeSource{},
			},
		},
		Gate: SecretSpec{Mode: "passphrase", Ref: telephonyAccessPinParam},
		Dids: []DIDMeta{
			{Did: "+16135551234", Label: "Ottawa", Region: "ON", DefaultRule: "greenhouse", Greeting: "Hi there"},
			{Did: "+17145550100", Label: "", Region: "", DefaultRule: "", Greeting: ""},
		},
		Order: []string{"greenhouse", "walkin"},
	}
}

func TestSOP_RoundTrip(t *testing.T) {
	doc := fixtureSOPDoc()
	root := t.TempDir()

	if err := WriteSOP(root, doc.Name, doc); err != nil {
		t.Fatalf("WriteSOP: %v", err)
	}

	got, err := ReadSOP(root, doc.Name)
	if err != nil {
		t.Fatalf("ReadSOP: %v", err)
	}

	if !reflect.DeepEqual(got, doc) {
		t.Fatalf("round-trip mismatch:\n  wrote: %#v\n  read:  %#v", doc, got)
	}
}

func TestSOP_RoundTrip_EmptySections(t *testing.T) {
	doc := SOPDoc{
		Name:      "empty-sop",
		CreatedAt: "2026-07-15T00:00:00Z",
		Rules:     []SOPRule{},
		Tiers:     []SOPTier{},
		Unlocks:   []Unlock{},
		Knowledge: []SOPPack{},
		Gate:      SecretSpec{},
		Dids:      []DIDMeta{},
		Order:     []string{},
	}
	root := t.TempDir()

	if err := WriteSOP(root, doc.Name, doc); err != nil {
		t.Fatalf("WriteSOP: %v", err)
	}

	got, err := ReadSOP(root, doc.Name)
	if err != nil {
		t.Fatalf("ReadSOP: %v", err)
	}
	if !reflect.DeepEqual(got, doc) {
		t.Fatalf("empty-section round-trip mismatch:\n  wrote: %#v\n  read:  %#v", doc, got)
	}
}

func TestSOP_Deterministic(t *testing.T) {
	doc := fixtureSOPDoc()
	root1 := t.TempDir()
	root2 := t.TempDir()

	if err := WriteSOP(root1, doc.Name, doc); err != nil {
		t.Fatalf("WriteSOP (1): %v", err)
	}
	if err := WriteSOP(root2, doc.Name, doc); err != nil {
		t.Fatalf("WriteSOP (2): %v", err)
	}

	b1, err := os.ReadFile(filepath.Join(root1, sopsDir, doc.Name+".yaml"))
	if err != nil {
		t.Fatalf("read (1): %v", err)
	}
	b2, err := os.ReadFile(filepath.Join(root2, sopsDir, doc.Name+".yaml"))
	if err != nil {
		t.Fatalf("read (2): %v", err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("WriteSOP is not deterministic:\n--- run 1 ---\n%s\n--- run 2 ---\n%s", b1, b2)
	}

	// Re-writing to the SAME root a second time must also be byte-identical
	// (SOP-01's "re-saving the same live config produces a byte-identical
	// file" truth) — CreatedAt is caller-held-constant, never re-stamped.
	if err := WriteSOP(root1, doc.Name, doc); err != nil {
		t.Fatalf("WriteSOP (re-save): %v", err)
	}
	b3, err := os.ReadFile(filepath.Join(root1, sopsDir, doc.Name+".yaml"))
	if err != nil {
		t.Fatalf("read (re-save): %v", err)
	}
	if string(b1) != string(b3) {
		t.Fatalf("re-saving the same SOPDoc changed the file bytes:\n--- before ---\n%s\n--- after ---\n%s", b1, b3)
	}
}

// TestSOP_NoSecretValues is SOP-04's defense-in-depth byte-scan: every "ref:"
// line a written SOP contains must resolve to an allow-listed secret param
// NAME (allowedSecretParams, secret_reveal.go) — never a value. SOPDoc has
// no value-carrying field to begin with (SecretSpec only has Mode/Ref), so
// this test proves the emitter never introduces one, not just that the type
// system happens to lack one.
func TestSOP_NoSecretValues(t *testing.T) {
	doc := fixtureSOPDoc()
	root := t.TempDir()

	if err := WriteSOP(root, doc.Name, doc); err != nil {
		t.Fatalf("WriteSOP: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(root, sopsDir, doc.Name+".yaml"))
	if err != nil {
		t.Fatalf("read written SOP: %v", err)
	}
	content := string(raw)

	sawRefLine := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "ref:") {
			continue
		}
		sawRefLine = true
		_, val, _ := yamlKeyVal(trimmed)
		refValue := yamlScalar(val)
		if !allowedSecretParams[refValue] {
			t.Fatalf("gate ref line carries a non-allow-listed string %q — a written SOP must only ever carry an allow-listed secret param NAME, never a value", refValue)
		}
	}
	if !sawRefLine {
		t.Fatalf("fixture's gate.ref never appeared in the written SOP — test fixture or emitter is broken")
	}

	// Belt-and-suspenders: no known real SSM secret VALUE shape (this
	// project's telephony gate secrets are short alphanumeric PIN/word
	// strings, never present anywhere in a SOPDoc field) can appear, since
	// SOPDoc simply has no field capable of carrying one.
	forbiddenSubstrings := []string{"WithDecryption", "GetParameterOutput"}
	for _, s := range forbiddenSubstrings {
		if strings.Contains(content, s) {
			t.Fatalf("written SOP unexpectedly contains %q", s)
		}
	}
}

func TestSOP_DropsDerivedFields(t *testing.T) {
	view := ConfigView{
		Meta: Meta{Region: "us-east-1", Profile: "kmv", Table: "kmv-auth-electro", ImportedAtMs: 1234567890, Generator: "kv studio"},
		Rules: []Rule{
			{
				ID:        "greenhouse",
				Who:       WhoSpec{Type: "known", Numbers: []string{"+15555550100"}},
				Secret:    SecretSpec{Mode: "passphrase", Ref: telephonyAccessPinParam},
				Unlocks:   []Unlock{{Phrase: "resume", Add: []string{"resume-pack"}}},
				Grant:     GrantSpec{Minutes: 30, PeriodMin: 60, Concurrency: 4, TierID: "recruiting-30"},
				Knowledge: []string{"resume-pack"},
				Persona:   "concierge",
			},
		},
		Knowledge: []KnowledgePack{
			{ID: "resume-pack", SpokenName: "Kurt's resume", Pack: "resume.md", UsedByRules: 1, TokenEstimate: 512, Talkable: true},
		},
		InboundDIDs: []InboundDID{
			{Did: "+16135551234", Label: "Ottawa", Region: "ON", Routing: "live-pbx-subaccount", DefaultRule: "greenhouse", Greeting: "Hi there"},
		},
		CompilesTo: map[string]string{"rule.who": "DynamoDB"},
	}

	doc := ToSOPDoc(view)

	if len(doc.Dids) != 1 {
		t.Fatalf("expected 1 did row, got %d", len(doc.Dids))
	}
	if doc.Dids[0].Did != "+16135551234" || doc.Dids[0].DefaultRule != "greenhouse" {
		t.Fatalf("did metadata not projected correctly: %#v", doc.Dids[0])
	}

	if len(doc.Knowledge) != 1 {
		t.Fatalf("expected 1 knowledge row, got %d", len(doc.Knowledge))
	}
	if doc.Knowledge[0].ID != "resume-pack" || doc.Knowledge[0].SpokenName != "Kurt's resume" {
		t.Fatalf("knowledge pack not projected correctly: %#v", doc.Knowledge[0])
	}

	if len(doc.Tiers) != 1 || doc.Tiers[0].TierID != "recruiting-30" {
		t.Fatalf("expected exactly one hoisted tier row, got %#v", doc.Tiers)
	}
	if doc.Tiers[0].SessionMaxSeconds != 1800 || doc.Tiers[0].PeriodMaxSeconds != 3600 || doc.Tiers[0].MaxConcurrent != 4 {
		t.Fatalf("tier limits not converted to seconds correctly: %#v", doc.Tiers[0])
	}

	if len(doc.Unlocks) != 1 || doc.Unlocks[0].Phrase != "resume" {
		t.Fatalf("unlocks not hoisted correctly: %#v", doc.Unlocks)
	}
	if doc.Gate.Mode != "passphrase" || doc.Gate.Ref != telephonyAccessPinParam {
		t.Fatalf("gate not hoisted correctly: %#v", doc.Gate)
	}

	if len(doc.Order) != 1 || doc.Order[0] != "greenhouse" {
		t.Fatalf("order not derived from rule sequence correctly: %#v", doc.Order)
	}

	// Serialize and byte-scan for the exact prohibited-field markers
	// (18-01-PLAN.md's P-01-derived-fields gate): the view's Meta timestamp
	// value and the InboundDID's live routing value must never appear
	// anywhere in a written SOP.
	root := t.TempDir()
	if err := WriteSOP(root, "derived-fields-check", doc); err != nil {
		t.Fatalf("WriteSOP: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, sopsDir, "derived-fields-check.yaml"))
	if err != nil {
		t.Fatalf("read written SOP: %v", err)
	}
	content := string(raw)
	if strings.Contains(content, "live-pbx-subaccount") {
		t.Fatalf("written SOP leaked the live inbound-DID routing value:\n%s", content)
	}
	if strings.Contains(content, "1234567890") {
		t.Fatalf("written SOP leaked the ConfigView's ImportedAtMs value:\n%s", content)
	}
}

func TestSOP_DedupesTiersAcrossRules(t *testing.T) {
	view := ConfigView{
		Rules: []Rule{
			{ID: "codeA", Who: WhoSpec{Type: "any", Numbers: []string{}}, Grant: GrantSpec{Minutes: 30, PeriodMin: 60, Concurrency: 4, TierID: "shared-tier"}},
			{ID: "codeB", Who: WhoSpec{Type: "any", Numbers: []string{}}, Grant: GrantSpec{Minutes: 30, PeriodMin: 60, Concurrency: 4, TierID: "shared-tier"}},
		},
	}
	doc := ToSOPDoc(view)
	if len(doc.Tiers) != 1 {
		t.Fatalf("expected exactly 1 deduped tier row for 2 rules sharing a tier, got %d: %#v", len(doc.Tiers), doc.Tiers)
	}
	if len(doc.Rules) != 2 {
		t.Fatalf("expected 2 rule rows, got %d", len(doc.Rules))
	}
}

func TestReadSOP_MissingFileReturnsError(t *testing.T) {
	root := t.TempDir()
	_, err := ReadSOP(root, "does-not-exist")
	if err == nil {
		t.Fatalf("expected an error reading a nonexistent SOP, got nil")
	}
}
