// Package studio's ephemeral secret reveal: SEC-01. This is the ONLY file in
// package studio that may call SSM GetParameter with WithDecryption=true —
// secret_adapter.go and server.go must never contain a decrypt-capable
// token (TestNoSecretWrites_Phase16Files scans both). allowedSecretParams is
// the single, shared allow-list (also used by secret_rotate.go's
// RotateSecret) that is the entire access-control mechanism for both
// endpoints: a param name outside this 3-name set is rejected BEFORE any AWS
// call, since /kmv/secrets/use1/* also holds the auth app's JWT/OIDC/ALTCHA
// signing secrets, which must stay unreachable from this console
// (17-RESEARCH.md Pitfall 3 / T-17-01).
package studio

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// telephonyEndpointAuthTokenParam is the third §24/§25 gate-secret param
// name (mirrors telephonyAccessPinParam/telephonyPassphraseWordsParam in
// secret_adapter.go/cmd/telephony.go) — the endpoint auth token gating the
// telephony-edge callback, per D-01's accepted answer that reveal scope ==
// rotate scope == the same 3 names.
const telephonyEndpointAuthTokenParam = "/kmv/secrets/use1/telephony/endpoint_auth_token"

// allowedSecretParams is the hardcoded 3-name allow-list shared by
// RevealSecret and RotateSecret (single source of truth — do not duplicate
// this list). It is the entire access-control model for SEC-01/SEC-02: any
// other /kmv/secrets/use1/* param name (e.g. the auth app's jwt/oidc/altcha
// signing secrets, which share this exact prefix) is refused before either
// function ever touches the SSM API.
var allowedSecretParams = map[string]bool{
	telephonyAccessPinParam:         true,
	telephonyPassphraseWordsParam:   true,
	telephonyEndpointAuthTokenParam: true,
}

// SSMRevealAPI is the narrow subset of *ssm.Client RevealSecret needs, so
// tests can inject an in-memory fake instead of a real SSM connection
// (mirrors cmd/telephony.go's ssmGetParameterAPI / dynamo_adapter.go's
// DynamoReadAPI narrow-interface style).
type SSMRevealAPI interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// RevealSecretResult is RevealSecret's outcome: Value is populated only when
// Status is "set" (mirrors cmd/telephony.go's SecretEntry shape). The zero
// value (no Value, empty Status) never round-trips outside this call — see
// RevealSecret's doc comment on ephemerality.
type RevealSecretResult struct {
	Name   string
	Status string
	Value  string
}

// errSecretNotAllowed is returned by RevealSecret/RotateSecret when name is
// not in allowedSecretParams. Checked BEFORE any AWS call — this ordering is
// load-bearing (17-01-PLAN.md Task 1).
var errSecretNotAllowed = errors.New("secret is not allow-listed for reveal/rotate")

// RevealSecret decrypts and returns the current value of an allow-listed
// telephony gate secret, exactly once, as a function return value. It never
// writes the value to a file, a log line, a struct field that outlives this
// call, or any cache (SEC-01 / T-17-02) — the caller (the POST
// /api/secret/reveal handler in server.go) must return it directly in the
// HTTP JSON response body and do nothing else with it.
//
// name is checked against allowedSecretParams before api is ever touched
// (T-17-01) — a rejected name never reaches SSM. Errors from SSM are
// classified exactly like cmd/telephony.go's readTelephonySecrets:
// ParameterNotFound yields a clean Status "not set" (not a Go error); any
// other AWS error yields a short, non-sensitive Status note via
// shortSSMErrorNote (never the raw error, T-17-05).
func RevealSecret(ctx context.Context, api SSMRevealAPI, name string) (RevealSecretResult, error) {
	if !allowedSecretParams[name] {
		return RevealSecretResult{}, fmt.Errorf("%w: %q", errSecretNotAllowed, name)
	}

	out, err := api.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(name),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		if _, ok := errors.AsType[*ssmtypes.ParameterNotFound](err); ok {
			return RevealSecretResult{Name: name, Status: "not set"}, nil
		}
		return RevealSecretResult{Name: name, Status: fmt.Sprintf("error — %s", shortSSMErrorNote(err))}, nil
	}

	value := ""
	if out.Parameter != nil && out.Parameter.Value != nil {
		value = *out.Parameter.Value
	}
	return RevealSecretResult{Name: name, Status: "set", Value: value}, nil
}

// shortSSMErrorNote derives a short, non-sensitive note from an SSM error
// (e.g. AccessDenied) without ever including request internals — package
// studio's own copy of cmd/telephony.go's helper of the same name (package
// studio does not import package cmd, 16-RESEARCH.md Pitfall 4).
func shortSSMErrorNote(err error) string {
	var apiErr interface{ ErrorCode() string }
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return "unavailable"
}

// SecretRevealReq is the POST /api/secret/reveal request body: the operator
// names one allow-listed param to decrypt.
type SecretRevealReq struct {
	Name string `json:"name"`
}

// SecretRevealResp is the POST /api/secret/reveal response body — the
// decrypted value, returned exactly once, plus an explicit ephemeral-not-
// stored contract note for the browser to display alongside it.
type SecretRevealResp struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Value     string `json:"value,omitempty"`
	Ephemeral bool   `json:"ephemeral"`
}
