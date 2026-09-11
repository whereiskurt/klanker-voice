package cmd

import (
	"context"
	"strings"
	"testing"
)

// fakeNetworkState is a canned NetworkStateAPI: it returns a fixed
// NetworkState (or error), never touching a TerraformOutputReader or AWS.
type fakeNetworkState struct {
	state NetworkState
	err   error
}

func (f *fakeNetworkState) NetworkState(ctx context.Context) (NetworkState, error) {
	return f.state, f.err
}

type fakeVerifyECS struct{ postures []ServicePosture }

func (f *fakeVerifyECS) DescribeServices(ctx context.Context, cluster string, services []string) ([]ServicePosture, error) {
	return f.postures, nil
}
func (f *fakeVerifyECS) UpdateDesiredCount(ctx context.Context, cluster, service string, desired int32) error {
	return nil
}
func (f *fakeVerifyECS) ListRunningTasks(ctx context.Context, cluster, service string) ([]string, error) {
	return nil, nil
}
func (f *fakeVerifyECS) GetTaskProtection(ctx context.Context, cluster string, taskARNs []string) (int, error) {
	return 0, nil
}

// The clean hibernated state: ALB and NAT gone, EIP retained at the same
// address it had before the applies ran, no services.
func TestVerifyHibernated_CleanState(t *testing.T) {
	v, err := VerifyHibernated(context.Background(),
		&fakeVerifyECS{postures: nil},
		&fakeNetworkState{state: NetworkState{NATEIPPublicIP: "1.2.3.4"}},
		"cluster", []string{"voice", "auth", "telephony-edge"},
		"1.2.3.4")
	if err != nil {
		t.Fatalf("VerifyHibernated error: %v", err)
	}
	if verr := v.Err(); verr != nil {
		t.Fatalf("Err() = %v, want nil for a clean hibernated state", verr)
	}
}

// The D-03 orphan failure: a service still exists. This must be an error,
// never a warning -- an orphaned service keeps billing silently.
func TestVerifyHibernated_OrphanedServiceIsAnError(t *testing.T) {
	v, err := VerifyHibernated(context.Background(),
		&fakeVerifyECS{postures: []ServicePosture{{Name: "voice", Desired: 1, Running: 1}}},
		&fakeNetworkState{state: NetworkState{NATEIPPublicIP: "1.2.3.4"}},
		"cluster", []string{"voice"}, "1.2.3.4")
	if err != nil {
		t.Fatalf("VerifyHibernated error: %v", err)
	}
	verr := v.Err()
	if verr == nil {
		t.Fatal("Err() = nil, want an error naming the orphaned service")
	}
	if !strings.Contains(verr.Error(), "voice") {
		t.Errorf("error %q does not name the orphaned service", verr)
	}
}

// A released EIP is a failure: the VoIP.ms allowlist is now stale, and
// that fails silently later rather than loudly now.
func TestVerifyHibernated_ReleasedEIPIsAnError(t *testing.T) {
	v, err := VerifyHibernated(context.Background(),
		&fakeVerifyECS{postures: nil},
		&fakeNetworkState{state: NetworkState{}},
		"cluster", []string{"voice"}, "1.2.3.4")
	if err != nil {
		t.Fatalf("VerifyHibernated error: %v", err)
	}
	verr := v.Err()
	if verr == nil {
		t.Fatal("Err() = nil, want an error about the released EIP")
	}
	if !strings.Contains(strings.ToLower(verr.Error()), "eip") {
		t.Errorf("error %q does not mention the EIP", verr)
	}
}

// A surviving ALB means phase 2 did not do its job.
func TestVerifyHibernated_SurvivingALBIsAnError(t *testing.T) {
	v, err := VerifyHibernated(context.Background(),
		&fakeVerifyECS{postures: nil},
		&fakeNetworkState{state: NetworkState{
			ALBArn:         "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/kmv/abc123",
			NATEIPPublicIP: "1.2.3.4",
		}},
		"cluster", []string{"voice"}, "1.2.3.4")
	if err != nil {
		t.Fatalf("VerifyHibernated error: %v", err)
	}
	if v.Err() == nil {
		t.Fatal("Err() = nil, want an error about the surviving ALB")
	}
}

// A changed EIP is its own failure, distinct from a released one: the
// retained address does not match what wake expects to find in the
// VoIP.ms allowlist, and the error must name both addresses.
func TestVerifyHibernated_ChangedEIPIsAnError(t *testing.T) {
	v, err := VerifyHibernated(context.Background(),
		&fakeVerifyECS{postures: nil},
		&fakeNetworkState{state: NetworkState{NATEIPPublicIP: "5.6.7.8"}},
		"cluster", []string{"voice"}, "1.2.3.4")
	if err != nil {
		t.Fatalf("VerifyHibernated error: %v", err)
	}
	verr := v.Err()
	if verr == nil {
		t.Fatal("Err() = nil, want an error about the changed EIP")
	}
	if !strings.Contains(verr.Error(), "1.2.3.4") || !strings.Contains(verr.Error(), "5.6.7.8") {
		t.Errorf("error %q does not name both the expected and actual EIP", verr)
	}
}
