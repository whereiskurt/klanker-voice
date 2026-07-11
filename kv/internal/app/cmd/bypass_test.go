package cmd

import (
	"regexp"
	"strings"
	"testing"
)

// TestGenerateBypassToken_CharsetAndLength asserts every minted bypass token is
// exactly bypassTokenLength base62 chars (a-zA-Z0-9) — the charset that keeps
// the /use1/join/<token> path segment URL-safe with no escaping.
func TestGenerateBypassToken_CharsetAndLength(t *testing.T) {
	base62 := regexp.MustCompile(`^[a-zA-Z0-9]+$`)
	for i := 0; i < 500; i++ {
		tok, err := generateBypassToken()
		if err != nil {
			t.Fatalf("generateBypassToken() error: %v", err)
		}
		if len(tok) != bypassTokenLength {
			t.Fatalf("token %q length = %d, want %d", tok, len(tok), bypassTokenLength)
		}
		if !base62.MatchString(tok) {
			t.Fatalf("token %q contains non-base62 characters", tok)
		}
	}
}

// TestGenerateBypassToken_Unique asserts minted tokens don't collide across a
// large batch (a smoke test for the crypto/rand source + rejection sampling).
func TestGenerateBypassToken_Unique(t *testing.T) {
	seen := make(map[string]struct{}, 2000)
	for i := 0; i < 2000; i++ {
		tok, err := generateBypassToken()
		if err != nil {
			t.Fatalf("generateBypassToken() error: %v", err)
		}
		if _, dup := seen[tok]; dup {
			t.Fatalf("duplicate token %q at iteration %d", tok, i)
		}
		seen[tok] = struct{}{}
	}
}

// TestBypassJoinURL asserts the shareable URL points at the AUTH app's /join
// route under the region basePath (matching the App Router route built in
// apps/auth/webapp/src/app/join/[token]/route.ts), defaulting to prod.
func TestBypassJoinURL(t *testing.T) {
	t.Setenv("KV_AUTH_ORIGIN", "")
	t.Setenv("REGION_SHORT", "")
	got := bypassJoinURL("abc123")
	want := "https://auth.klankermaker.ai/use1/join/abc123"
	if got != want {
		t.Errorf("bypassJoinURL() = %q, want %q", got, want)
	}
	if !strings.Contains(got, "/join/") {
		t.Errorf("URL %q missing /join/ segment", got)
	}

	// Overridable for non-prod deployments.
	t.Setenv("KV_AUTH_ORIGIN", "https://auth.example.test")
	t.Setenv("REGION_SHORT", "cac1")
	if got, want := bypassJoinURL("tok"), "https://auth.example.test/cac1/join/tok"; got != want {
		t.Errorf("bypassJoinURL() override = %q, want %q", got, want)
	}
}
