// Package cmd -- post-apply verification for kv hibernate (2026-09-10 spec
// §8 step 9).
//
// Every check here exists because its failure is SILENT. An orphaned ECS
// service keeps billing with nothing to see; a released or changed NAT
// Elastic IP does not fail at wake, it fails weeks later on the first SMS
// relay or CTF OTP call because the VoIP.ms allowlist went stale. A verify
// that found an orphan and printed a warning would be worse than no verify
// at all, so every one of these is an error.
//
// This verifies terraform's post-apply view, not AWS ground truth: the
// network-unit checks (ALBGone, NATGone, EIPRetained) read the network
// unit's `terragrunt output -json`, the same seam ResolveLiveTargets uses
// (lifecycle_targets.go). If terraform believed it destroyed the ALB while
// AWS still had it, this would not catch it. The mitigation is that the
// orphaned-service check does NOT go through this seam -- it calls
// ECSAPI.DescribeServices against real AWS, because an orphaned service is
// the failure that bills silently and is worth the extra API round-trip to
// confirm against ground truth.
package cmd

import (
	"context"
	"fmt"
	"strings"
)

// NetworkState is the network unit's post-apply resource identity. Each
// field is empty when the resource does not exist -- unmarshalling a JSON
// null output into a string is a no-op that leaves it "", which is exactly
// what a destroyed resource's output looks like after `terragrunt output
// -json` (the output block survives destroy; its value goes null).
type NetworkState struct {
	ALBArn         string
	NATGatewayID   string
	NATEIPPublicIP string
}

// NetworkStateAPI reports the post-apply state of the network unit.
type NetworkStateAPI interface {
	NetworkState(ctx context.Context) (NetworkState, error)
}

// terragruntNetworkState is the production NetworkStateAPI, reading the
// network unit's outputs through the same TerraformOutputReader seam
// ResolveLiveTargets and its narrower resolvers use.
type terragruntNetworkState struct {
	reader TerraformOutputReader
}

// NewNetworkStateAPI builds a NetworkStateAPI backed by r.
func NewNetworkStateAPI(r TerraformOutputReader) NetworkStateAPI {
	return &terragruntNetworkState{reader: r}
}

// NetworkState reads alb_arn, nat_gateway_id, and nat_eip_public_ip from
// the network unit (tfUnitNetwork).
func (n *terragruntNetworkState) NetworkState(ctx context.Context) (NetworkState, error) {
	outputs, err := readUnitOutputs(ctx, n.reader, tfUnitNetwork)
	if err != nil {
		return NetworkState{}, err
	}
	var state NetworkState
	if err := outputValue(outputs, tfUnitNetwork, "alb_arn", &state.ALBArn); err != nil {
		return NetworkState{}, err
	}
	if err := outputValue(outputs, tfUnitNetwork, "nat_gateway_id", &state.NATGatewayID); err != nil {
		return NetworkState{}, err
	}
	if err := outputValue(outputs, tfUnitNetwork, "nat_eip_public_ip", &state.NATEIPPublicIP); err != nil {
		return NetworkState{}, err
	}
	return state, nil
}

// HibernateVerification is what the post-apply checks observed.
type HibernateVerification struct {
	ALBGone           bool
	NATGone           bool
	EIPRetained       bool
	RemainingServices []string
	RetainedIP        string

	// expectedEIP is the EIP address resolved before the applies ran. It
	// is unexported because it exists only so Err() can name both sides
	// of a mismatch -- it is not itself an observation.
	expectedEIP string
}

// VerifyHibernated observes the post-apply state: which ECS services still
// exist (against real AWS, via ecsAPI), and whether the ALB, NAT Gateway,
// and NAT EIP survived the network unit's apply (via net, terraform's
// view). expectedEIP is the EIP address resolved before the applies ran --
// when non-empty, the retained address must match it exactly, because a
// changed EIP breaks the VoIP.ms allowlist exactly as a released one does.
//
// VerifyHibernated returns an error only when it fails to OBSERVE; a bad
// observed state is reported through Err() so the caller can print the
// whole picture before failing.
func VerifyHibernated(
	ctx context.Context,
	ecsAPI ECSAPI,
	net NetworkStateAPI,
	cluster string,
	services []string,
	expectedEIP string,
) (HibernateVerification, error) {
	v := HibernateVerification{expectedEIP: expectedEIP}

	postures, err := ecsAPI.DescribeServices(ctx, cluster, services)
	if err != nil {
		return v, fmt.Errorf("describe services: %w", err)
	}
	for _, p := range postures {
		v.RemainingServices = append(v.RemainingServices, p.Name)
	}

	state, err := net.NetworkState(ctx)
	if err != nil {
		return v, fmt.Errorf("read network state: %w", err)
	}

	v.ALBGone = state.ALBArn == ""
	v.NATGone = state.NATGatewayID == ""
	v.RetainedIP = state.NATEIPPublicIP
	v.EIPRetained = state.NATEIPPublicIP != "" && (expectedEIP == "" || state.NATEIPPublicIP == expectedEIP)

	return v, nil
}

// Err reports the observed state as an error when it is not the clean
// hibernated state. Each problem is named, because each one needs a
// different operator response.
func (v HibernateVerification) Err() error {
	var problems []string

	if len(v.RemainingServices) > 0 {
		problems = append(problems, fmt.Sprintf(
			"ECS services still exist (%s) -- they are ORPHANED: still billing, and still "+
				"holding the ALB listener rules that block the ALB delete. Check that site.hcl has "+
				"ecs_services.enabled = true with an EMPTY services list, not enabled = false "+
				"(which makes terragrunt's exclude skip the destroy entirely)",
			strings.Join(v.RemainingServices, ", ")))
	}
	if !v.ALBGone {
		problems = append(problems, "the ALB still exists -- phase 2 did not complete")
	}
	if !v.NATGone {
		problems = append(problems, "the NAT Gateway still exists -- phase 2 did not complete")
	}
	if !v.EIPRetained {
		switch {
		case v.RetainedIP == "":
			problems = append(problems, fmt.Sprintf(
				"the NAT EIP (%s) is NOT allocated -- it was released with the gateway. The "+
					"VoIP.ms API allowlist is now stale: this will not fail at wake, it will fail "+
					"silently on the first SMS relay or CTF OTP call. Check nat_gateway.retain_eip = true",
				v.expectedEIP))
		default:
			problems = append(problems, fmt.Sprintf(
				"the retained NAT EIP changed (expected %s, got %s) -- the VoIP.ms API allowlist "+
					"is now just as stale as if it had been released: this will not fail at wake, it "+
					"will fail silently on the first SMS relay or CTF OTP call",
				v.expectedEIP, v.RetainedIP))
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("hibernate verification failed:\n  - %s", strings.Join(problems, "\n  - "))
}
