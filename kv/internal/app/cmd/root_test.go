package cmd

import "testing"

// TestResolveAWSProfile table-tests the do-what-I-mean AWS profile
// precedence: AWS_PROFILE always wins when set (even alongside static
// creds); explicit static/CI creds (AWS_ACCESS_KEY_ID) suppress the operator
// default so CI runners are never hijacked; otherwise the operator SSO
// default (klanker-application) applies.
func TestResolveAWSProfile(t *testing.T) {
	tests := []struct {
		name          string
		awsProfileEnv string
		awsAccessKey  string
		wantProfile   string
	}{
		{
			name:          "AWS_PROFILE set, no static creds -> profile wins",
			awsProfileEnv: "prod",
			awsAccessKey:  "",
			wantProfile:   "prod",
		},
		{
			name:          "AWS_PROFILE set, static creds also set -> profile still wins",
			awsProfileEnv: "prod",
			awsAccessKey:  "AKIA...",
			wantProfile:   "prod",
		},
		{
			name:          "AWS_PROFILE unset, static creds set -> no profile (CI/env creds win)",
			awsProfileEnv: "",
			awsAccessKey:  "AKIA...",
			wantProfile:   "",
		},
		{
			name:          "neither set -> operator SSO default",
			awsProfileEnv: "",
			awsAccessKey:  "",
			wantProfile:   "klanker-application",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAWSProfile(tt.awsProfileEnv, tt.awsAccessKey)
			if got != tt.wantProfile {
				t.Errorf("resolveAWSProfile(%q, %q) = %q, want %q", tt.awsProfileEnv, tt.awsAccessKey, got, tt.wantProfile)
			}
		})
	}
}

// TestResolveAWSRegion asserts AWS_REGION passthrough when set, and the
// us-east-1 default when unset.
func TestResolveAWSRegion(t *testing.T) {
	tests := []struct {
		name         string
		awsRegionEnv string
		wantRegion   string
	}{
		{
			name:         "region set -> passthrough",
			awsRegionEnv: "eu-west-1",
			wantRegion:   "eu-west-1",
		},
		{
			name:         "region unset -> us-east-1 default",
			awsRegionEnv: "",
			wantRegion:   "us-east-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAWSRegion(tt.awsRegionEnv)
			if got != tt.wantRegion {
				t.Errorf("resolveAWSRegion(%q) = %q, want %q", tt.awsRegionEnv, got, tt.wantRegion)
			}
		})
	}
}
