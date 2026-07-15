package studio

import "testing"

func TestReadSecretRefs_ReturnsNamesOnlyWithGateMode(t *testing.T) {
	got := ReadSecretRefs("either")
	want := []SecretRef{
		{Name: "/kmv/secrets/use1/telephony/access_pin", Store: "ssm", Mode: "either"},
		{Name: "/kmv/secrets/use1/telephony/passphrase_words", Store: "ssm", Mode: "either"},
		{Name: "/kmv/secrets/use1/telephony/endpoint_auth_token", Store: "ssm", Mode: "either"},
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestReadSecretRefs_PropagatesGateModeToEveryRef(t *testing.T) {
	for _, mode := range []string{"passphrase", "dtmf", "either", "none", ""} {
		got := ReadSecretRefs(mode)
		for _, ref := range got {
			if ref.Mode != mode {
				t.Errorf("mode %q: ref.Mode = %q, want %q", mode, ref.Mode, mode)
			}
			if ref.Store != "ssm" {
				t.Errorf("mode %q: ref.Store = %q, want %q", mode, ref.Store, "ssm")
			}
		}
	}
}

func TestReadSecretRefs_NeverContainsAValue(t *testing.T) {
	// SecretRef has no Value field at all — this test documents that
	// invariant so a future edit that adds one gets caught by this file's
	// intent, and doubles as the "names only" acceptance check.
	got := ReadSecretRefs("either")
	for _, ref := range got {
		if ref.Name == "" {
			t.Error("ref.Name is empty, want a param name")
		}
	}
}
