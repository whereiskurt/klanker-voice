package studio

import "testing"

// validateFixtureSOPDoc returns a fully self-consistent, all-checks-pass
// SOPDoc dedicated to sop_validate_test.go — deliberately separate from
// sop_test.go's fixtureSOPDoc (that fixture exercises round-trip/emitter
// coverage and is NOT orphan-pack-clean: its "defcon"/"km" unlocks
// reference pack ids absent from its own Knowledge list on purpose). Every
// failing-fixture test below starts from a copy of this doc and mutates
// exactly one field to isolate a single check.
func validateFixtureSOPDoc() SOPDoc {
	return SOPDoc{
		Name:      "conference-2026",
		CreatedAt: "2026-07-15T10:00:00Z",
		Rules: []SOPRule{
			{Code: "greenhouse", Who: WhoSpec{Type: "known", Numbers: []string{"+15555550100"}}, TierID: "recruiting-30"},
			{Code: "walkin", Who: WhoSpec{Type: "any", Numbers: []string{}}, TierID: "public-3"},
		},
		Tiers: []SOPTier{
			{TierID: "recruiting-30", SessionMaxSeconds: 1800, PeriodMaxSeconds: 3600, MaxConcurrent: 4},
			{TierID: "public-3", SessionMaxSeconds: 180, PeriodMaxSeconds: 600, MaxConcurrent: 4},
		},
		Unlocks: []Unlock{
			{Phrase: "resume", Add: []string{"resume-pack"}},
			{Phrase: "defcon", Add: []string{"defcon-pack"}},
		},
		Knowledge: []SOPPack{
			{ID: "resume-pack", SpokenName: "Kurt's resume", Pack: "resume.md",
				Sources: []KnowledgeSource{{Path: "docs/resume.md", Kind: "doc", Public: true}}},
			{ID: "defcon-pack", SpokenName: "DEF CON background", Pack: "defcon.md", Sources: []KnowledgeSource{}},
		},
		Gate: SecretSpec{Mode: "passphrase", Ref: telephonyAccessPinParam},
		Dids: []DIDMeta{
			{Did: "+16135551234", Label: "Ottawa", Region: "ON", DefaultRule: "greenhouse", Greeting: "Hi there"},
		},
		Order: []string{"greenhouse", "walkin"},
	}
}

// assertHasErrorID fails the test unless errs contains a ValidationError
// with the given ID.
func assertHasErrorID(t *testing.T, errs []ValidationError, wantID string) {
	t.Helper()
	for _, e := range errs {
		if e.ID == wantID {
			return
		}
	}
	t.Fatalf("expected a %q validation error, got: %#v", wantID, errs)
}

func TestValidate_AllValidPasses(t *testing.T) {
	errs := Validate(validateFixtureSOPDoc())
	if len(errs) != 0 {
		t.Fatalf("expected no validation errors for a fully valid SOPDoc, got: %#v", errs)
	}
}

func TestValidate_RejectsBlankSOPName(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Name = "  "
	assertHasErrorID(t, Validate(doc), "schema")
}

func TestValidate_RejectsOrderReferencingUnknownRule(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Order = append(doc.Order, "ghost-code")
	assertHasErrorID(t, Validate(doc), "schema")
}

func TestValidate_RejectsUnknownSecretRef(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Gate.Ref = "/kmv/secrets/use1/auth/jwt_signing_key"
	assertHasErrorID(t, Validate(doc), "secret-ref")
}

func TestValidate_RejectsBadGateMode(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Gate.Mode = "none"
	assertHasErrorID(t, Validate(doc), "gate-mode")
}

func TestValidate_RejectsGateRefWithoutMode(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Gate.Mode = ""
	assertHasErrorID(t, Validate(doc), "gate-require-consistency")
}

func TestValidate_RejectsOrphanTier(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Tiers = doc.Tiers[:1] // drop public-3; "walkin" rule still references it
	assertHasErrorID(t, Validate(doc), "orphan-tier")
}

// TestValidate_AllowsBlockTierWithoutTiersEntry proves blockTierID is
// exempt from the orphan-tier check: a rule may reference the reserved
// "no-access" tier even when the SOP's own Tiers section doesn't list it
// (server.go's ensureBlockTier already guarantees it exists live).
func TestValidate_AllowsBlockTierWithoutTiersEntry(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Rules[1].TierID = blockTierID
	errs := Validate(doc)
	for _, e := range errs {
		if e.ID == "orphan-tier" {
			t.Fatalf("expected no orphan-tier error for blockTierID, got: %#v", errs)
		}
	}
}

func TestValidate_RejectsOrphanPack(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Unlocks = append(doc.Unlocks, Unlock{Phrase: "ghost-phrase", Add: []string{"ghost-pack"}})
	assertHasErrorID(t, Validate(doc), "orphan-pack")
}

func TestValidate_RejectsBadCharset(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Rules[0].Code = "bad\x00code"
	assertHasErrorID(t, Validate(doc), "charset")
}

func TestValidate_RejectsBadPhoneShape(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Dids[0].Did = ""
	assertHasErrorID(t, Validate(doc), "phone-shape")
}

// TestValidate_RejectsSecretValue is the P-05-no-secret-value-check
// prohibition's fixture: a real-looking secret value in a ref position
// (rather than an allow-listed param name) is rejected. It fires both the
// dedicated secret-value scan (check #9, defense-in-depth) and the
// unknown-secret-ref check (check #2) — belt-and-suspenders by design.
func TestValidate_RejectsSecretValue(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Gate.Ref = "sk_live_abcdef1234567890" //nolint:gosec // gitleaks:allow — intentional fake fixture: verifies Validate() REJECTS a secret-shaped value in a ref position (SOP-04)
	errs := Validate(doc)
	assertHasErrorID(t, errs, "secret-value")
	assertHasErrorID(t, errs, "secret-ref")
}

func TestValidate_RejectsDuplicateCode(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Rules = append(doc.Rules, SOPRule{Code: "greenhouse", Who: WhoSpec{Type: "any", Numbers: []string{}}, TierID: "public-3"})
	assertHasErrorID(t, Validate(doc), "duplicate-key")
}

func TestValidate_RejectsDuplicateTierID(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Tiers = append(doc.Tiers, SOPTier{TierID: "recruiting-30", SessionMaxSeconds: 900, PeriodMaxSeconds: 1800, MaxConcurrent: 2})
	assertHasErrorID(t, Validate(doc), "duplicate-key")
}

func TestValidate_RejectsDuplicateNormalizedDid(t *testing.T) {
	doc := validateFixtureSOPDoc()
	// Same number as Dids[0] after E164 normalization, spelled differently.
	doc.Dids = append(doc.Dids, DIDMeta{Did: "16135551234", Label: "Ottawa (dup)"})
	assertHasErrorID(t, Validate(doc), "duplicate-key")
}

func TestValidate_RejectsReservedTierNonZero(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Tiers = append(doc.Tiers, SOPTier{TierID: blockTierID, SessionMaxSeconds: 60, PeriodMaxSeconds: 0, MaxConcurrent: 0})
	assertHasErrorID(t, Validate(doc), "reserved-tier")
}

// TestValidate_AllowsReservedTierZero proves the reserved-tier guard only
// rejects a NON-zero redefinition — a SOP that explicitly re-declares
// no-access with all-zero limits (the correct shape) passes clean.
func TestValidate_AllowsReservedTierZero(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Tiers = append(doc.Tiers, SOPTier{TierID: blockTierID, SessionMaxSeconds: 0, PeriodMaxSeconds: 0, MaxConcurrent: 0})
	errs := Validate(doc)
	for _, e := range errs {
		if e.ID == "reserved-tier" {
			t.Fatalf("expected no reserved-tier error for an all-zero redefinition, got: %#v", errs)
		}
	}
}

// TestValidate_AccumulatesAllFailures proves Validate returns every
// failure in one pass rather than bailing out on the first — a doc with
// three independent, unrelated violations (bad gate mode, orphan tier,
// orphan pack) must surface all three.
func TestValidate_AccumulatesAllFailures(t *testing.T) {
	doc := validateFixtureSOPDoc()
	doc.Gate.Mode = "none"
	doc.Tiers = doc.Tiers[:1]
	doc.Unlocks = append(doc.Unlocks, Unlock{Phrase: "ghost-phrase", Add: []string{"ghost-pack"}})

	errs := Validate(doc)
	assertHasErrorID(t, errs, "gate-mode")
	assertHasErrorID(t, errs, "orphan-tier")
	assertHasErrorID(t, errs, "orphan-pack")
}
