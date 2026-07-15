// Package studio's SOP pre-deploy validation gate (SOP-03/SOP-04): Validate
// runs all 11 checks enumerated in 18-RESEARCH.md's "Enumerated Pre-Deploy
// Validation Checks (Q6)" over an already-parsed SOPDoc, so Deploy (Plan 06)
// can refuse the whole action before touching DynamoDB, git, or the
// knowledge rebuild.
//
// A sops/<name>.yaml is untrusted input the moment it is read back — it may
// be hand-edited. Every check here REUSES an existing validator/allow-list
// verbatim (allowedSecretParams from secret_reveal.go; GateModeAllowed,
// validateCodeCharset, validateTierID, normalizeE164 from validate.go;
// blockTierID from server.go) — this file defines zero new allow-lists
// (18-05-PLAN.md's P-05-reuse-allowlist / P-05-reuse-gate-charset
// prohibitions).
package studio

import (
	"fmt"
	"strings"
)

// ValidationError is one Validate failure: a stable check id (so callers can
// group/count failures by kind) plus a human-readable message. Validate
// returns every failure it finds, not just the first, so the console UI can
// list the complete set in one pass.
type ValidationError struct {
	ID      string
	Message string
}

// Error satisfies the error interface so a ValidationError can be wrapped or
// logged like any other Go error, even though Validate itself returns a
// slice rather than a single error.
func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.ID, e.Message)
}

// Validate runs all 11 pre-deploy checks over doc and returns every failure
// found. An empty/nil return means doc is clean to deploy; ANY non-empty
// return means Deploy must refuse the whole action (enforced in Plan 06) —
// Validate itself never mutates doc or touches DynamoDB/git/SSM.
func Validate(doc SOPDoc) []ValidationError {
	var errs []ValidationError

	errs = append(errs, checkSchema(doc)...)                 // Q6 #1
	errs = append(errs, checkSecretRefs(doc)...)             // Q6 #2
	errs = append(errs, checkGateMode(doc)...)               // Q6 #3
	errs = append(errs, checkGateRequireConsistency(doc)...) // Q6 #4
	errs = append(errs, checkOrphanTiers(doc)...)            // Q6 #5
	errs = append(errs, checkOrphanPacks(doc)...)            // Q6 #6
	errs = append(errs, checkCharset(doc)...)                // Q6 #7
	errs = append(errs, checkPhoneShape(doc)...)             // Q6 #8
	errs = append(errs, checkNoSecretValue(doc)...)          // Q6 #9
	errs = append(errs, checkDuplicateKeys(doc)...)          // Q6 #10
	errs = append(errs, checkReservedTier(doc)...)           // Q6 #11

	return errs
}

// checkSchema is Q6 check #1: reject a structurally empty/incoherent
// SOPDoc. Validate assumes an already-parsed doc — a malformed/truncated
// FILE is refused at the ReadSOP call site (Plan 06) — so "schema" here
// means internal coherence: a non-blank name, and every Order entry
// referencing one of the SOP's own Rules (an order entry pointing at
// nothing is exactly the kind of hand-edit corruption this check exists to
// catch).
func checkSchema(doc SOPDoc) []ValidationError {
	var errs []ValidationError

	if strings.TrimSpace(doc.Name) == "" {
		errs = append(errs, ValidationError{ID: "schema", Message: "sop name must not be blank"})
	}

	ruleCodes := make(map[string]bool, len(doc.Rules))
	for _, r := range doc.Rules {
		ruleCodes[r.Code] = true
	}
	for _, id := range doc.Order {
		if !ruleCodes[id] {
			errs = append(errs, ValidationError{ID: "schema",
				Message: fmt.Sprintf("order references code %q with no matching rule", id)})
		}
	}

	return errs
}

// checkSecretRefs is Q6 check #2: every SecretSpec.Ref in the SOP (v1 has
// exactly one, the hoisted Gate — sop.go's ToSOPDoc doc comment) must be a
// member of allowedSecretParams (secret_reveal.go), reused verbatim. An
// empty Ref (no gate secret configured) is not itself a failure here —
// checkGateRequireConsistency covers that combination.
func checkSecretRefs(doc SOPDoc) []ValidationError {
	var errs []ValidationError
	if doc.Gate.Ref != "" && !allowedSecretParams[doc.Gate.Ref] {
		errs = append(errs, ValidationError{ID: "secret-ref",
			Message: fmt.Sprintf("gate.ref %q is not an allow-listed secret param", doc.Gate.Ref)})
	}
	return errs
}

// checkGateMode is Q6 check #3: a non-empty Gate.Mode must satisfy
// GateModeAllowed (validate.go), reused verbatim — reject an unknown mode
// string. An empty mode (no gate configured) is allowed here; that
// combination with a configured secret Ref is checkGateRequireConsistency's
// job.
func checkGateMode(doc SOPDoc) []ValidationError {
	var errs []ValidationError
	if doc.Gate.Mode != "" && !GateModeAllowed(doc.Gate.Mode) {
		errs = append(errs, ValidationError{ID: "gate-mode",
			Message: fmt.Sprintf("gate.mode %q is not one of the allowed gate modes", doc.Gate.Mode)})
	}
	return errs
}

// checkGateRequireConsistency is Q6 check #4. SOPDoc's hoisted Gate is a
// bare SecretSpec{Mode, Ref} (types.go) — unlike telephony.toml's
// [telephony] block, there is no separate require_gate boolean field on the
// SOP to read. A configured secret Ref is the SOP's only available signal
// that a gate is required, so require_gate/gate_mode consistency is
// enforced as: Ref set => Mode must be non-empty AND GateModeAllowed — a
// hand-edited SOP that names a secret but leaves (or corrupts) the mode is
// rejected rather than silently applied with an unusable gate.
func checkGateRequireConsistency(doc SOPDoc) []ValidationError {
	var errs []ValidationError
	if doc.Gate.Ref != "" && (doc.Gate.Mode == "" || !GateModeAllowed(doc.Gate.Mode)) {
		errs = append(errs, ValidationError{ID: "gate-require-consistency",
			Message: "gate.ref is set but gate.mode is empty or invalid — a gate secret requires a valid gate mode"})
	}
	return errs
}

// checkOrphanTiers is Q6 check #5: every SOPRule.TierID must exist among
// the SOP's own Tiers list — DynamoDB itself enforces no such FK, so a
// hand-edited SOP could otherwise resolve a rule to an unintended/absent
// tier at apply time (T-18-14). blockTierID (server.go) is exempt: it is
// the reserved zero-limit "block a number" tier RULE-04 already guarantees
// exists live (server.go's ensureBlockTier) — a SOP is not required to
// enumerate it in its own Tiers section just to reference it.
func checkOrphanTiers(doc SOPDoc) []ValidationError {
	var errs []ValidationError

	tierIDs := make(map[string]bool, len(doc.Tiers))
	for _, t := range doc.Tiers {
		tierIDs[t.TierID] = true
	}

	for _, r := range doc.Rules {
		if r.TierID == blockTierID {
			continue
		}
		if !tierIDs[r.TierID] {
			errs = append(errs, ValidationError{ID: "orphan-tier",
				Message: fmt.Sprintf("rule %q references tier %q which is not present in the SOP's own tiers list", r.Code, r.TierID)})
		}
	}

	return errs
}

// checkOrphanPacks is Q6 check #6: every knowledge pack id referenced by an
// Unlock.Add entry must exist in the SOP's own Knowledge section.
func checkOrphanPacks(doc SOPDoc) []ValidationError {
	var errs []ValidationError

	packIDs := make(map[string]bool, len(doc.Knowledge))
	for _, p := range doc.Knowledge {
		packIDs[p.ID] = true
	}

	for _, u := range doc.Unlocks {
		for _, id := range u.Add {
			if !packIDs[id] {
				errs = append(errs, ValidationError{ID: "orphan-pack",
					Message: fmt.Sprintf("unlock %q adds pack %q which is not present in the SOP's knowledge list", u.Phrase, id)})
			}
		}
	}

	return errs
}

// checkCharset is Q6 check #7: every code/tier/topic id is re-validated via
// validateCodeCharset/validateTierID (validate.go), reused verbatim — a
// hand-edited SOP bypasses the API's inline per-request validation, so
// Deploy's validate step must re-run it (T-18-15's validate-then-write
// discipline).
func checkCharset(doc SOPDoc) []ValidationError {
	var errs []ValidationError

	for _, r := range doc.Rules {
		if err := validateCodeCharset(r.Code); err != nil {
			errs = append(errs, ValidationError{ID: "charset", Message: fmt.Sprintf("rule code: %v", err)})
		}
	}
	for _, t := range doc.Tiers {
		if err := validateTierID(t.TierID); err != nil {
			errs = append(errs, ValidationError{ID: "charset", Message: err.Error()})
		}
	}
	for _, p := range doc.Knowledge {
		if err := validateCodeCharset(p.ID); err != nil {
			errs = append(errs, ValidationError{ID: "charset", Message: fmt.Sprintf("knowledge pack id: %v", err)})
		}
	}

	return errs
}

// checkPhoneShape is Q6 check #8: every WhoSpec.Numbers entry and
// DIDMeta.Did is re-validated via normalizeE164 (validate.go), reused
// verbatim.
func checkPhoneShape(doc SOPDoc) []ValidationError {
	var errs []ValidationError

	for _, r := range doc.Rules {
		for _, n := range r.Who.Numbers {
			if _, err := normalizeE164(n); err != nil {
				errs = append(errs, ValidationError{ID: "phone-shape",
					Message: fmt.Sprintf("rule %q who.numbers entry %q: %v", r.Code, n, err)})
			}
		}
	}
	for _, d := range doc.Dids {
		if _, err := normalizeE164(d.Did); err != nil {
			errs = append(errs, ValidationError{ID: "phone-shape",
				Message: fmt.Sprintf("did %q: %v", d.Did, err)})
		}
	}

	return errs
}

// suspiciousSecretMarkers are literal substrings that must never appear in
// any string field of a parsed SOPDoc: none of these can legitimately
// appear in a code/tierId/phrase/pack-id/spokenName/label/greeting/mode
// value, so their presence anywhere means a real (or real-looking) secret
// value was smuggled into the SOP.
var suspiciousSecretMarkers = []string{
	"AKIA",       // AWS access key id prefix
	"-----BEGIN", // PEM block
	"sk-",        // common API-secret-key prefix convention
	"sk_live_",
}

// looksLikeSecretValue reports whether s is shaped like a real secret value
// rather than a param NAME/id/label — either an SSM parameter path that is
// NOT itself a member of allowedSecretParams (a bad/unknown path smuggled
// somewhere other than the one field checkSecretRefs already validates), or
// a string containing one of suspiciousSecretMarkers.
func looksLikeSecretValue(s string) bool {
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "/kmv/secrets/") && !allowedSecretParams[s] {
		return true
	}
	for _, marker := range suspiciousSecretMarkers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// checkNoSecretValue is Q6 check #9: a defense-in-depth scan of EVERY
// string field in the parsed doc — not just Gate.Ref, which
// checkSecretRefs already validates — for anything secret-value-shaped.
// This doubles as SOP-04's belt-and-suspenders guard (18-05-PLAN.md's
// P-05-no-secret-value-check): a hand-edited SOP smuggling a real secret
// VALUE into an unrelated field (a rule code, a DID label, a knowledge
// spokenName) is caught here even though none of those fields are secret
// positions by design.
func checkNoSecretValue(doc SOPDoc) []ValidationError {
	var errs []ValidationError

	flag := func(field, val string) {
		if looksLikeSecretValue(val) {
			errs = append(errs, ValidationError{ID: "secret-value",
				Message: fmt.Sprintf("%s carries a secret-value-shaped string %q", field, val)})
		}
	}

	flag("gate.ref", doc.Gate.Ref)
	flag("gate.mode", doc.Gate.Mode)

	for _, r := range doc.Rules {
		flag(fmt.Sprintf("rule %q code", r.Code), r.Code)
		flag(fmt.Sprintf("rule %q tierId", r.Code), r.TierID)
		flag(fmt.Sprintf("rule %q who.type", r.Code), r.Who.Type)
		for _, n := range r.Who.Numbers {
			flag(fmt.Sprintf("rule %q who.numbers", r.Code), n)
		}
	}
	for _, t := range doc.Tiers {
		flag("tier.tierId", t.TierID)
	}
	for _, u := range doc.Unlocks {
		flag("unlock.phrase", u.Phrase)
		for _, id := range u.Add {
			flag("unlock.add", id)
		}
	}
	for _, p := range doc.Knowledge {
		flag(fmt.Sprintf("knowledge %q id", p.ID), p.ID)
		flag(fmt.Sprintf("knowledge %q spokenName", p.ID), p.SpokenName)
		flag(fmt.Sprintf("knowledge %q pack", p.ID), p.Pack)
		for _, s := range p.Sources {
			flag("knowledge.sources.path", s.Path)
			flag("knowledge.sources.kind", s.Kind)
		}
	}
	for _, d := range doc.Dids {
		flag("did", d.Did)
		flag("did.label", d.Label)
		flag("did.region", d.Region)
		flag("did.defaultRule", d.DefaultRule)
		flag("did.greeting", d.Greeting)
	}
	for _, id := range doc.Order {
		flag("order", id)
	}

	return errs
}

// checkDuplicateKeys is Q6 check #10: no two rules share a code, no two
// tiers share a tierId, no two dids rows share a normalized did — a
// duplicate would apply in undefined map-iteration order (T-18-15). A did
// row whose Did fails normalizeE164 is skipped here — checkPhoneShape
// already reports that failure independently.
func checkDuplicateKeys(doc SOPDoc) []ValidationError {
	var errs []ValidationError

	seenCodes := make(map[string]bool, len(doc.Rules))
	for _, r := range doc.Rules {
		if seenCodes[r.Code] {
			errs = append(errs, ValidationError{ID: "duplicate-key", Message: fmt.Sprintf("duplicate rule code %q", r.Code)})
		}
		seenCodes[r.Code] = true
	}

	seenTiers := make(map[string]bool, len(doc.Tiers))
	for _, t := range doc.Tiers {
		if seenTiers[t.TierID] {
			errs = append(errs, ValidationError{ID: "duplicate-key", Message: fmt.Sprintf("duplicate tier id %q", t.TierID)})
		}
		seenTiers[t.TierID] = true
	}

	seenDids := make(map[string]bool, len(doc.Dids))
	for _, d := range doc.Dids {
		norm, err := normalizeE164(d.Did)
		if err != nil {
			continue
		}
		if seenDids[norm] {
			errs = append(errs, ValidationError{ID: "duplicate-key",
				Message: fmt.Sprintf("duplicate did %q (normalized %q)", d.Did, norm)})
		}
		seenDids[norm] = true
	}

	return errs
}

// checkReservedTier is Q6 check #11: a SOP must not redefine blockTierID
// (server.go's "no-access" reserved zero-limit tier) with non-zero limits,
// which would silently defeat RULE-04's block semantics for every
// already-blocked code (T-18-16).
func checkReservedTier(doc SOPDoc) []ValidationError {
	var errs []ValidationError

	for _, t := range doc.Tiers {
		if t.TierID != blockTierID {
			continue
		}
		if t.SessionMaxSeconds != 0 || t.PeriodMaxSeconds != 0 || t.MaxConcurrent != 0 {
			errs = append(errs, ValidationError{ID: "reserved-tier",
				Message: fmt.Sprintf("tier %q (reserved block tier) must have zero limits, got session=%d period=%d concurrent=%d",
					blockTierID, t.SessionMaxSeconds, t.PeriodMaxSeconds, t.MaxConcurrent)})
		}
	}

	return errs
}
