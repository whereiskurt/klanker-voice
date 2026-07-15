package studio

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// fakeSSMRotateAPI is an in-memory SSMRotateAPI: it never dials AWS, records
// every DescribeParameters/PutParameter call (for the allow-list zero-call
// and KeyId-preservation assertions), and returns a configured
// ParameterMetadata.KeyId / error.
type fakeSSMRotateAPI struct {
	describeKeyID  string // "" means DescribeParameters returns no matching parameter
	describeErr    error
	putErr         error
	describeCalls  int
	putCalls       int
	lastPutInput   *ssm.PutParameterInput
	lastDescribeIn *ssm.DescribeParametersInput
}

func (f *fakeSSMRotateAPI) DescribeParameters(ctx context.Context, params *ssm.DescribeParametersInput, optFns ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error) {
	f.describeCalls++
	f.lastDescribeIn = params
	if f.describeErr != nil {
		return nil, f.describeErr
	}
	if f.describeKeyID == "" {
		return &ssm.DescribeParametersOutput{}, nil
	}
	name := ""
	if len(params.ParameterFilters) > 0 && len(params.ParameterFilters[0].Values) > 0 {
		name = params.ParameterFilters[0].Values[0]
	}
	keyID := f.describeKeyID
	return &ssm.DescribeParametersOutput{
		Parameters: []ssmtypes.ParameterMetadata{
			{Name: &name, KeyId: &keyID},
		},
	}, nil
}

func (f *fakeSSMRotateAPI) PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	f.putCalls++
	f.lastPutInput = params
	if f.putErr != nil {
		return nil, f.putErr
	}
	return &ssm.PutParameterOutput{}, nil
}

// TestRotate_AllowList asserts a non-allowlisted param name is rejected AND
// that the fake records zero PutParameter (and zero DescribeParameters)
// calls (T-17-01).
func TestRotate_AllowList(t *testing.T) {
	fake := &fakeSSMRotateAPI{describeKeyID: "sentinel-key-id"}

	err := RotateSecret(context.Background(), fake, "/kmv/secrets/use1/oidc/cookie_key", "new-value")
	if err == nil {
		t.Fatal("RotateSecret() error = nil, want a rejection for a non-allowlisted name")
	}
	if !errors.Is(err, errSecretNotAllowed) {
		t.Errorf("RotateSecret() error = %v, want errSecretNotAllowed", err)
	}
	if fake.putCalls != 0 {
		t.Errorf("fake.putCalls = %d, want 0 (allow-list must be checked before any AWS call)", fake.putCalls)
	}
	if fake.describeCalls != 0 {
		t.Errorf("fake.describeCalls = %d, want 0 (allow-list must be checked before any AWS call)", fake.describeCalls)
	}
}

// TestRotate_PreservesKeyId asserts RotateSecret reads the current KeyId via
// DescribeParameters (never GetParameter, whose Parameter type has no KeyId
// field) and passes that exact KeyId through on PutParameter, along with
// Type=SecureString and Overwrite=true (T-17-03).
func TestRotate_PreservesKeyId(t *testing.T) {
	fake := &fakeSSMRotateAPI{describeKeyID: "arn:aws:kms:us-east-1:123456789012:key/sentinel-cmk-id"}

	if err := RotateSecret(context.Background(), fake, telephonyAccessPinParam, "brand-new-pin"); err != nil {
		t.Fatalf("RotateSecret() error = %v", err)
	}

	if fake.describeCalls != 1 {
		t.Errorf("fake.describeCalls = %d, want 1", fake.describeCalls)
	}
	if fake.putCalls != 1 {
		t.Fatalf("fake.putCalls = %d, want 1", fake.putCalls)
	}
	got := fake.lastPutInput
	if got.KeyId == nil || *got.KeyId != "arn:aws:kms:us-east-1:123456789012:key/sentinel-cmk-id" {
		t.Errorf("PutParameterInput.KeyId = %v, want the sentinel KeyId recovered from DescribeParameters", got.KeyId)
	}
	if got.Type != ssmtypes.ParameterTypeSecureString {
		t.Errorf("PutParameterInput.Type = %v, want SecureString", got.Type)
	}
	if got.Overwrite == nil || !*got.Overwrite {
		t.Error("PutParameterInput.Overwrite not set true")
	}
}

// TestRotateSecret_AllowedName_IssuesExactlyOneWriteCallWithNewValue
// confirms RotateSecret with an allow-listed name and a new value writes the
// new value exactly once.
func TestRotateSecret_AllowedName_IssuesExactlyOneWriteCallWithNewValue(t *testing.T) {
	fake := &fakeSSMRotateAPI{} // no KeyId on record — the common case (default key)

	if err := RotateSecret(context.Background(), fake, telephonyPassphraseWordsParam, "brand-new-passphrase-words"); err != nil {
		t.Fatalf("RotateSecret() error = %v", err)
	}

	if fake.putCalls != 1 {
		t.Fatalf("fake.putCalls = %d, want exactly 1", fake.putCalls)
	}
	got := fake.lastPutInput
	if got.Name == nil || *got.Name != telephonyPassphraseWordsParam {
		t.Errorf("PutParameterInput.Name = %v, want %q", got.Name, telephonyPassphraseWordsParam)
	}
	if got.Value == nil || *got.Value != "brand-new-passphrase-words" {
		t.Errorf("PutParameterInput.Value = %v, want %q", got.Value, "brand-new-passphrase-words")
	}
	if got.KeyId != nil {
		t.Errorf("PutParameterInput.KeyId = %v, want nil (DescribeParameters returned no KeyId)", got.KeyId)
	}
}

func TestRotateSecret_PutParameterError_YieldsShortNonSensitiveError(t *testing.T) {
	fake := &fakeSSMRotateAPI{putErr: errors.New("AccessDenied: user arn:aws:iam::123456789012:user/ops is not authorized")}

	err := RotateSecret(context.Background(), fake, telephonyAccessPinParam, "x")
	if err == nil {
		t.Fatal("RotateSecret() error = nil, want the classified PutParameter failure")
	}
	if got := err.Error(); strings.Contains(got, "arn:aws:iam") || strings.Contains(got, "123456789012") {
		t.Errorf("RotateSecret() error = %q leaks raw AWS error internals", got)
	}
}
