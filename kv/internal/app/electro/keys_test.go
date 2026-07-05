package electro

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func assertS(t *testing.T, m map[string]types.AttributeValue, key, want string) {
	t.Helper()
	av, ok := m[key]
	if !ok {
		t.Fatalf("missing attribute %q", key)
	}
	s, ok := av.(*types.AttributeValueMemberS)
	if !ok {
		t.Fatalf("attribute %q is not a string member: %T", key, av)
	}
	if s.Value != want {
		t.Errorf("attribute %q = %q, want %q", key, s.Value, want)
	}
}

func assertN(t *testing.T, m map[string]types.AttributeValue, key, want string) {
	t.Helper()
	av, ok := m[key]
	if !ok {
		t.Fatalf("missing attribute %q", key)
	}
	n, ok := av.(*types.AttributeValueMemberN)
	if !ok {
		t.Fatalf("attribute %q is not a number member: %T", key, av)
	}
	if n.Value != want {
		t.Errorf("attribute %q = %q, want %q", key, n.Value, want)
	}
}

// TestKeyCompat_AccessCode asserts the AccessCode key strings built here
// equal the FINAL templates from 03-02-SUMMARY.md / access-code.ts:
//
//	primary pk: "code#${code}"      sk: "code#"
//	gsi1      pk: "accesscodes#"    sk: "code#${code}"
//
// including case normalization: a kv write of "DEMO" must key identically
// to a webapp write of "demo" (Pitfall 1 / T-03-17).
func TestKeyCompat_AccessCode(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantPK     string
		wantGSI1SK string
	}{
		{"lowercase", "demo", "code#demo", "code#demo"},
		{"uppercase normalizes", "DEMO", "code#demo", "code#demo"},
		{"mixed case normalizes", "DeMo", "code#demo", "code#demo"},
		{"leading/trailing whitespace trimmed", "  demo  ", "code#demo", "code#demo"},
		{"kphdemo123", "kphdemo123", "code#kphdemo123", "code#kphdemo123"},
		{"KPHDEMO123 normalizes", "KPHDEMO123", "code#kphdemo123", "code#kphdemo123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AccessCodePK(tc.input); got != tc.wantPK {
				t.Errorf("AccessCodePK(%q) = %q, want %q", tc.input, got, tc.wantPK)
			}
			if got := AccessCodeSK(); got != "code#" {
				t.Errorf("AccessCodeSK() = %q, want %q", got, "code#")
			}
			if got := AccessCodeGSI1PK(); got != "accesscodes#" {
				t.Errorf("AccessCodeGSI1PK() = %q, want %q", got, "accesscodes#")
			}
			if got := AccessCodeGSI1SK(tc.input); got != tc.wantGSI1SK {
				t.Errorf("AccessCodeGSI1SK(%q) = %q, want %q", tc.input, got, tc.wantGSI1SK)
			}
		})
	}
}

// TestKeyCompat_CaseCrossCheck proves a kv write of an uppercase code keys
// IDENTICALLY to a webapp write of the lowercase form — the exact scenario
// that would silently hide a code from login if normalization diverged.
func TestKeyCompat_CaseCrossCheck(t *testing.T) {
	kvWrite := AccessCodePK("DEMO")     // simulates kv normalizing an operator-typed "DEMO"
	webappWrite := AccessCodePK("demo") // simulates the webapp's own normalizeCode("demo")
	if kvWrite != webappWrite {
		t.Fatalf("kv-normalized key %q != webapp-normalized key %q — Pitfall 1 regression", kvWrite, webappWrite)
	}
}

// TestKeyCompat_Tier asserts the Tier key strings equal the FINAL templates
// from 03-02-SUMMARY.md / tier.ts:
//
//	primary pk: "tier#${tierId}"   sk: "tier#"
//	gsi1      pk: "tiers#"         sk: "tier#${tierId}"
func TestKeyCompat_Tier(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantPK     string
		wantGSI1SK string
	}{
		{"lowercase", "demo-tier", "tier#demo-tier", "tier#demo-tier"},
		{"uppercase normalizes", "DEMO-TIER", "tier#demo-tier", "tier#demo-tier"},
		{"no-access", "no-access", "tier#no-access", "tier#no-access"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TierPK(tc.input); got != tc.wantPK {
				t.Errorf("TierPK(%q) = %q, want %q", tc.input, got, tc.wantPK)
			}
			if got := TierSK(); got != "tier#" {
				t.Errorf("TierSK() = %q, want %q", got, "tier#")
			}
			if got := TierGSI1PK(); got != "tiers#" {
				t.Errorf("TierGSI1PK() = %q, want %q", got, "tiers#")
			}
			if got := TierGSI1SK(tc.input); got != tc.wantGSI1SK {
				t.Errorf("TierGSI1SK(%q) = %q, want %q", tc.input, got, tc.wantGSI1SK)
			}
		})
	}
}

// TestAccessCodeItem_Marshal asserts the full item shape (pk/sk/gsi1
// attributes + ElectroDB bookkeeping markers) matches what a live
// `aws dynamodb scan` of an ElectroDB-written kmv-auth-electro AccessCode
// item shows, field name for field name.
func TestAccessCodeItem_Marshal(t *testing.T) {
	maxRedemptions := int64(5)
	item := NewAccessCodeItem("DEMO", "demo-tier", "conference", nil, &maxRedemptions)
	m := item.Marshal()

	assertS(t, m, "pk", "code#demo")
	assertS(t, m, "sk", "code#")
	assertS(t, m, "gsi1pk", "accesscodes#")
	assertS(t, m, "gsi1sk", "code#demo")
	assertS(t, m, EDBEntityAttr, AccessCodeEntityName)
	assertS(t, m, EDBVersionAttr, EDBVersion)
	assertS(t, m, "code", "demo")
	assertS(t, m, "tierId", "demo-tier")
	assertS(t, m, "group", "conference")
	assertN(t, m, "maxRedemptions", "5")
	assertN(t, m, "redemptionCount", "0")

	if _, ok := m["expiresAt"]; ok {
		t.Errorf("expiresAt should be omitted when nil, but was present")
	}
}

// TestTierItem_Marshal asserts the Tier item shape matches the live
// ElectroDB-written Tier item field-for-field.
func TestTierItem_Marshal(t *testing.T) {
	item := NewTierItem("DEMO-TIER", "", 120, 3600, 1)
	m := item.Marshal()

	assertS(t, m, "pk", "tier#demo-tier")
	assertS(t, m, "sk", "tier#")
	assertS(t, m, "gsi1pk", "tiers#")
	assertS(t, m, "gsi1sk", "tier#demo-tier")
	assertS(t, m, EDBEntityAttr, TierEntityName)
	assertS(t, m, EDBVersionAttr, EDBVersion)
	assertS(t, m, "tierId", "demo-tier")
	assertN(t, m, "sessionMaxSeconds", "120")
	assertN(t, m, "periodMaxSeconds", "3600")
	assertN(t, m, "maxConcurrent", "1")

	if _, ok := m["group"]; ok {
		t.Errorf("group should be omitted when empty, but was present")
	}
}
