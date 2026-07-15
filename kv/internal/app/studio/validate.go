package studio

// Duplicated write-path input validation. package studio must not import
// package cmd (cmd/studio.go already imports studio — the reverse import
// would cycle; 16-RESEARCH.md Common Pitfall 4). validateCodeCharset and
// normalizeE164 are duplicated VERBATIM from cmd/code.go (they are
// unexported there and cannot be imported), following the same
// documented-duplication convention repofile_adapter.go's
// parseTOMLScalarLine already established for this package. Byte-identity
// with the cmd copies is proven by the parity tests in validate_test.go.

import (
	"fmt"
	"strings"
	"unicode"
)

// maxCodeRunes bounds validateCodeCharset/validateTierID input length
// (16-RESEARCH.md Security Domain V5: cmd/code.go's validateCodeCharset has
// no length bound today — this is a new, stricter guard studio adds before
// any control-character-clean but arbitrarily long string reaches a
// DynamoDB pk/sk/gsi key string).
const maxCodeRunes = 256

// validateCodeCharset duplicates cmd/code.go's validateCodeCharset
// (cmd/code.go:39-50) verbatim, with one addition: a maxCodeRunes length
// bound not present in the cmd original.
func validateCodeCharset(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("code must not be blank")
	}
	if runeCount := len([]rune(trimmed)); runeCount > maxCodeRunes {
		return fmt.Errorf("code %q exceeds max length of %d runes (got %d)", raw, maxCodeRunes, runeCount)
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return fmt.Errorf("code %q contains a control character", raw)
		}
	}
	return nil
}

// validateTierID validates a tier id with validateCodeCharset's charset+
// length rules, wrapping any error with "invalid tier id: " — mirroring how
// cmd/code.go's CreateAccessCode (line 59-61) and cmd/tier.go's DefineTier
// (line 32-34) both wrap validateCodeCharset for a tier id argument.
func validateTierID(tierID string) error {
	if err := validateCodeCharset(tierID); err != nil {
		return fmt.Errorf("invalid tier id: %w", err)
	}
	return nil
}

// normalizeE164 duplicates cmd/code.go's normalizeE164 (cmd/code.go:249-274)
// verbatim — see this file's package doc comment for why it cannot be
// imported instead. Reproduces
// apps/auth/webapp/src/lib/phone-normalization.ts's normalizeE164 canonical
// output rule byte-for-byte (12-RESEARCH.md Pitfall 3): strip everything
// but digits and '+' characters, drop the leading '+' (re-added at the
// end), drop a leading trunk '0' run, and treat a bare 10-digit
// North-American local number as needing a prepended country code '1'.
// Returns an error on blank/no-digit input (the cmd/studio-side divergence
// from the TS helper, which returns "" instead — see cmd/code.go's doc
// comment on normalizeE164 for the full rationale).
func normalizeE164(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("phone number must not be blank")
	}

	var b strings.Builder
	for _, r := range trimmed {
		if r == '+' || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	cleaned := b.String()
	if cleaned == "" {
		return "", fmt.Errorf("phone number %q contains no digits", raw)
	}

	cleaned = strings.TrimPrefix(cleaned, "+")
	cleaned = strings.TrimLeft(cleaned, "0")

	if len(cleaned) == 10 {
		cleaned = "1" + cleaned
	}

	return "+" + cleaned, nil
}

// allowedGateModes is the exact [telephony].gate_mode allowlist from
// apps/voice/src/klanker_voice/telephony/config.py:27's
// ALLOWED_GATE_MODES = frozenset({"dtmf", "passphrase", "either"}). There is
// deliberately no "none"/"off" value here: "no secret required" is
// expressed by writing a separate require_gate=false field, not a fourth
// gate_mode value (16-RESEARCH.md Pitfall 2) — see Plan 02.
var allowedGateModes = map[string]bool{
	"dtmf":       true,
	"passphrase": true,
	"either":     true,
}

// GateModeAllowed reports whether mode is one of the three valid
// [telephony].gate_mode values the voice pipeline's config loader
// (telephony/config.py) accepts. Every studio write path that touches
// gate_mode must call this and reject before writing, so a malformed studio
// write can never produce a telephony.toml the pipeline refuses to boot
// from.
func GateModeAllowed(mode string) bool {
	return allowedGateModes[mode]
}
