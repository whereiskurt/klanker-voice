// Package studio's SSM SecureString rotate: SEC-02. This is the ONLY file
// in package studio that may call SSM PutParameter — secret_adapter.go and
// server.go must never contain a write token
// (TestNoSecretWrites_Phase16Files scans both). RotateSecret shares
// allowedSecretParams with secret_reveal.go's RevealSecret — a single
// source of truth for the entire access-control model of both endpoints
// (17-RESEARCH.md Pitfall 3 / T-17-01).
package studio

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// SSMRotateAPI is the narrow subset of *ssm.Client RotateSecret needs, so
// tests can inject an in-memory fake instead of a real SSM connection.
// DescribeParameters recovers the current KMS KeyId (17-RESEARCH.md
// Pitfall 2 — the GetParameter response's Parameter type has no KeyId
// field; KeyId lives only on ParameterMetadata, returned by
// DescribeParameters); PutParameter performs the write.
type SSMRotateAPI interface {
	DescribeParameters(ctx context.Context, params *ssm.DescribeParametersInput, optFns ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error)
	PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
}

// RotateSecret overwrites an allow-listed telephony gate secret in SSM as a
// SecureString. name is checked against the shared allowedSecretParams
// allow-list BEFORE any AWS call (T-17-01) — a rejected name never reaches
// SSM. On an allowed name, RotateSecret first calls DescribeParameters
// (ParameterFilters Key="Name" Values=[name]) to read the parameter's
// current ParameterMetadata.KeyId, then issues PutParameter with
// Type=SecureString, Overwrite=true, and that recovered KeyId passed
// through explicitly — omitting KeyId on an Overwrite silently re-encrypts
// with the AWS-managed default key, dropping a customer-managed KMS key
// (17-RESEARCH.md Pitfall 2 / T-17-03). If DescribeParameters returns no
// KeyId (the common case — these 3 params were hand-provisioned with no
// --key-id, i.e. already the default), KeyId is simply omitted on
// PutParameter too. newValue is never logged or persisted anywhere by this
// function — it is handed to SSM and then discarded. AWS errors are
// classified to a short, non-sensitive note (mirrors
// cmd/telephony.go's shortSSMErrorNote), never the raw error (T-17-05).
func RotateSecret(ctx context.Context, api SSMRotateAPI, name, newValue string) error {
	if !allowedSecretParams[name] {
		return fmt.Errorf("%w: %q", errSecretNotAllowed, name)
	}

	keyID, err := currentKeyID(ctx, api, name)
	if err != nil {
		return fmt.Errorf("rotate %s: read current KeyId: %s", name, shortSSMErrorNote(err))
	}

	input := &ssm.PutParameterInput{
		Name:      aws.String(name),
		Value:     aws.String(newValue),
		Type:      ssmtypes.ParameterTypeSecureString,
		Overwrite: aws.Bool(true),
	}
	if keyID != "" {
		input.KeyId = aws.String(keyID)
	}

	if _, err := api.PutParameter(ctx, input); err != nil {
		return fmt.Errorf("rotate %s: %s", name, shortSSMErrorNote(err))
	}
	return nil
}

// currentKeyID reads name's current ParameterMetadata.KeyId via
// DescribeParameters. A ParameterNotFound-shaped "no matching parameter"
// (an empty Parameters slice) is not an error here — it degrades to an
// empty KeyId, meaning PutParameter's own Overwrite will simply create/set
// the value with the default key, same as if the param never had a custom
// one.
func currentKeyID(ctx context.Context, api SSMRotateAPI, name string) (string, error) {
	out, err := api.DescribeParameters(ctx, &ssm.DescribeParametersInput{
		ParameterFilters: []ssmtypes.ParameterStringFilter{
			{Key: aws.String("Name"), Option: aws.String("Equals"), Values: []string{name}},
		},
	})
	if err != nil {
		return "", err
	}
	for _, meta := range out.Parameters {
		if meta.Name != nil && *meta.Name == name && meta.KeyId != nil {
			return *meta.KeyId, nil
		}
	}
	return "", nil
}

// SecretRotateReq is the POST /api/secret/rotate request body: the operator
// names one allow-listed param and supplies its new value.
type SecretRotateReq struct {
	Name     string `json:"name"`
	NewValue string `json:"newValue"`
}

// SecretRotateResp is the POST /api/secret/rotate response body — a
// no-value acknowledgment (the new value is never echoed back).
type SecretRotateResp struct {
	Name    string `json:"name"`
	Rotated bool   `json:"rotated"`
}
