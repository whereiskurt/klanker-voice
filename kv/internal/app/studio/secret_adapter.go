// Package studio's secret adapter: name-only SSM param references for the
// §24 telephony gate secrets. This file MUST NEVER read or decrypt a secret
// VALUE from SSM (T-15-02, spec §7 "Secrets never enter a SOP or git" /
// "Reveal is deliberate" — reveal is a later phase's job, not this one's).
// A grep gate in CI/plan verification asserts this file makes no SSM
// decrypt-capable API call — do not add one.
package studio

// telephonyAccessPinParam / telephonyPassphraseWordsParam are the two §24
// gate secret parameter NAMES (mirrors cmd/telephony.go's
// telephonyAccessPinParam / telephonyPassphraseWordsParam — same SSM
// SecureString params, referenced here by name only).
const (
	telephonyAccessPinParam       = "/kmv/secrets/use1/telephony/access_pin"
	telephonyPassphraseWordsParam = "/kmv/secrets/use1/telephony/passphrase_words"
)

// ReadSecretRefs returns the known gate-secret param names as SecretRefs
// carrying the current gate mode. It makes NO network call and reads NO
// value — it is a pure function over a string.
//
// Includes telephonyEndpointAuthTokenParam (declared in secret_reveal.go,
// this same package) as a third ref — per 17-RESEARCH.md D-01, SEC-01's
// reveal scope matches SEC-02's rotate scope exactly: the same 3 telephony
// gate secrets.
func ReadSecretRefs(gateMode string) []SecretRef {
	return []SecretRef{
		{Name: telephonyAccessPinParam, Store: "ssm", Mode: gateMode},
		{Name: telephonyPassphraseWordsParam, Store: "ssm", Mode: gateMode},
		{Name: telephonyEndpointAuthTokenParam, Store: "ssm", Mode: gateMode},
	}
}
