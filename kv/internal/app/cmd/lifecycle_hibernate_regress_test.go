package cmd

import (
	"strings"
	"testing"
)

// Regression tests for two defects that survived every review and only
// surfaced during the first real hibernation, 2026-09-11.

// Terraform OMITS a null-valued output from `output -json` entirely rather
// than emitting it as null. So once the ALB and NAT Gateway are destroyed the
// network unit simply stops reporting alb_arn and nat_gateway_id. Treating
// that absence as an error made VerifyHibernated fail on every SUCCESSFUL
// hibernation -- the run that proved it had already destroyed the right nine
// and then four resources before the verification errored.
//
// The genuinely bad case is still caught one level up: an unreadable unit
// fails in readUnitOutputs, before this is ever reached.
func TestAbsentAsEmpty_MissingKeyIsNotAnError(t *testing.T) {
	var got string
	if err := absentAsEmpty(map[string]tfOutputEnvelope{}, tfUnitNetwork, "alb_arn", &got); err != nil {
		t.Fatalf("absentAsEmpty on a missing key returned an error: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want the empty string for an absent output", got)
	}
}

// The phase lists must name the CloudFront DISTRIBUTION unit, not the
// cf-assets bucket. terragrunt-apply.yml resolves a BARE name to
// region/us-east-1/<name>, and `region/us-east-1/cloudfront` is the assets
// unit; the distribution carrying alb_origin_enabled is global/cloudfront.
//
// Naming it wrong fails SILENTLY: the apply succeeds, reports "0 changed"
// against the wrong unit, and the distribution keeps an ALB origin pointing
// at an ALB the next phase destroys. On wake it is worse -- the origin is
// never restored, so the mic stays broken after everything else returns.
func TestPhaseListsNameTheCloudFrontDistributionUnit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		phases []ApplyPhase
	}{
		{"hibernate", HibernatePhases},
		{"wake", WakePhases},
	} {
		var found bool
		for _, p := range tc.phases {
			for _, m := range strings.Split(p.Modules, ",") {
				switch strings.TrimSpace(m) {
				case "cloudfront":
					t.Errorf("%s: bare \"cloudfront\" resolves to the cf-assets unit; use \"global/cloudfront\"", tc.name)
				case "global/cloudfront":
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s phases never name global/cloudfront", tc.name)
		}
	}
}
