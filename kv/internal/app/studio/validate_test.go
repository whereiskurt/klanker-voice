package studio

import (
	"strings"
	"testing"
)

// TestValidateCodeCharset mirrors cmd/code_test.go-style table-driven
// coverage of validateCodeCharset: blank/control-char rejection (parity with
// cmd/code.go's validateCodeCharset) plus the new maxCodeRunes length bound
// (16-RESEARCH.md Security Domain V5 gap).
func TestValidateCodeCharset(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "blank", input: "", wantErr: true},
		{name: "whitespace-only", input: "   ", wantErr: true},
		{name: "control character", input: "greenhouse\x00guest", wantErr: true},
		{name: "normal code", input: "greenhouse-guest", wantErr: false},
		{name: "at length bound", input: strings.Repeat("a", maxCodeRunes), wantErr: false},
		{name: "over length bound", input: strings.Repeat("a", maxCodeRunes+1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCodeCharset(tt.input)
			if tt.wantErr && err == nil {
				t.Fatalf("validateCodeCharset(%q) = nil, want an error", tt.input)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateCodeCharset(%q) unexpected error: %v", tt.input, err)
			}
		})
	}
}

// TestValidateTierID asserts validateTierID applies the same charset+length
// rules as validateCodeCharset and wraps errors with "invalid tier id: ",
// mirroring cmd/code.go's CreateAccessCode / cmd/tier.go's DefineTier
// wrapping.
func TestValidateTierID(t *testing.T) {
	if err := validateTierID(""); err == nil {
		t.Fatalf("validateTierID(\"\") = nil, want an error")
	} else if !strings.HasPrefix(err.Error(), "invalid tier id: ") {
		t.Errorf("validateTierID(\"\") error = %q, want prefix %q", err.Error(), "invalid tier id: ")
	}
	if err := validateTierID(strings.Repeat("a", maxCodeRunes+1)); err == nil {
		t.Fatalf("validateTierID(over-long) = nil, want an error")
	}
	if err := validateTierID("kph-tier"); err != nil {
		t.Fatalf("validateTierID(\"kph-tier\") unexpected error: %v", err)
	}
}

// TestNormalizeE164Studio replicates cmd/code_test.go's TestNormalizeE164
// case table against the studio copy — proving byte-identical behavior
// (16-RESEARCH.md Common Pitfall 4 / Don't Hand-Roll: "E.164 phone
// normalization").
func TestNormalizeE164Studio(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "spaced/parenthesized/dashed", input: "+1 (416) 555-1234", want: "+14165551234"},
		{name: "dashed with leading 1", input: "1-416-555-1234", want: "+14165551234"},
		{name: "bare 10-digit local", input: "416-555-1234", want: "+14165551234"},
		{name: "already canonical (idempotent)", input: "+14165551234", want: "+14165551234"},
		{name: "blank", input: "", wantErr: true},
		{name: "whitespace-only", input: "   ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeE164(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeE164(%q) = %q, nil; want an error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeE164(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("normalizeE164(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestNormalizeE164Studio_CmdParity hardcodes the same literal case table
// cmd/code_test.go's TestNormalizeE164_AuthAppParity uses (transcribed from
// the auth-app TS normalizer's own outputs) as the parity oracle, so this
// test fails loudly if the studio copy's behavior for these inputs ever
// diverges from the cmd copy / auth-app TS helper.
func TestNormalizeE164Studio_CmdParity(t *testing.T) {
	authAppCases := map[string]string{
		"+1 (416) 555-1234": "+14165551234",
		"1-416-555-1234":    "+14165551234",
		"416-555-1234":      "+14165551234",
		"+14165551234":      "+14165551234",
	}
	for input, want := range authAppCases {
		got, err := normalizeE164(input)
		if err != nil {
			t.Fatalf("normalizeE164(%q) unexpected error: %v", input, err)
		}
		if got != want {
			t.Errorf("normalization parity broken: studio normalizeE164(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestGateModeAllowed asserts the allowlist accepts exactly
// {dtmf, passphrase, either} — mirroring
// apps/voice/src/klanker_voice/telephony/config.py:27's ALLOWED_GATE_MODES —
// and rejects everything else, including "none" and the empty string
// (16-RESEARCH.md Pitfall 2: "no secret" is require_gate=false, not a
// fourth gate_mode value).
func TestGateModeAllowed(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{"dtmf", true},
		{"passphrase", true},
		{"either", true},
		{"none", false},
		{"off", false},
		{"", false},
		{"DTMF", false}, // case-sensitive, mirrors the Python frozenset check
	}
	for _, tt := range tests {
		if got := GateModeAllowed(tt.mode); got != tt.want {
			t.Errorf("GateModeAllowed(%q) = %v, want %v", tt.mode, got, tt.want)
		}
	}
}
