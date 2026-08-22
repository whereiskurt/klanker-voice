package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeTargetHealthAPI is a scripted TargetHealthAPI: sequences maps a
// target-group ARN to a per-call sequence of observed states (the last
// entry repeats once the sequence is exhausted), so a "stuck unhealthy",
// "initial-then-healthy", or "empty-then-registers" scenario is exact and
// call-count driven -- never a wall-clock race (D-31: no test constructs a
// real ELBv2 client or reaches AWS).
type fakeTargetHealthAPI struct {
	sequences     map[string][][]TargetState
	callCounts    map[string]int
	errsRemaining map[string]int
	permanentErr  map[string]bool
}

func newFakeTargetHealthAPI() *fakeTargetHealthAPI {
	return &fakeTargetHealthAPI{
		sequences:     map[string][][]TargetState{},
		callCounts:    map[string]int{},
		errsRemaining: map[string]int{},
		permanentErr:  map[string]bool{},
	}
}

func (f *fakeTargetHealthAPI) DescribeTargetHealth(_ context.Context, targetGroupARN string) ([]TargetState, error) {
	f.callCounts[targetGroupARN]++
	if f.permanentErr[targetGroupARN] {
		return nil, errors.New("simulated permanent describe-target-health failure")
	}
	if n := f.errsRemaining[targetGroupARN]; n > 0 {
		f.errsRemaining[targetGroupARN] = n - 1
		return nil, errors.New("simulated transient describe-target-health failure")
	}
	seq := f.sequences[targetGroupARN]
	if len(seq) == 0 {
		return nil, nil
	}
	idx := f.callCounts[targetGroupARN] - 1
	if idx >= len(seq) {
		idx = len(seq) - 1
	}
	return seq[idx], nil
}

func TestWaitForTargetsHealthy(t *testing.T) {
	ctx := context.Background()

	t.Run("AllHealthyFirstPollReturnsNilImmediately", func(t *testing.T) {
		f := newFakeTargetHealthAPI()
		f.sequences["arn-voice"] = [][]TargetState{{{ID: "i-1", State: "healthy"}}}
		f.sequences["arn-auth"] = [][]TargetState{{{ID: "i-2", State: "healthy"}}}

		var buf bytes.Buffer
		err := WaitForTargetsHealthy(ctx, f, map[string]string{"voice": "arn-voice", "auth": "arn-auth"}, &buf, time.Second, time.Millisecond)
		if err != nil {
			t.Fatalf("WaitForTargetsHealthy() error = %v, want nil", err)
		}
		if f.callCounts["arn-voice"] != 1 || f.callCounts["arn-auth"] != 1 {
			t.Errorf("callCounts = %+v, want exactly 1 poll per group", f.callCounts)
		}
	})

	t.Run("InitialThenHealthyAcrossPolls", func(t *testing.T) {
		f := newFakeTargetHealthAPI()
		f.sequences["arn-voice"] = [][]TargetState{
			{{ID: "i-1", State: "initial"}},
			{{ID: "i-1", State: "healthy"}},
		}

		var buf bytes.Buffer
		err := WaitForTargetsHealthy(ctx, f, map[string]string{"voice": "arn-voice"}, &buf, time.Second, time.Millisecond)
		if err != nil {
			t.Fatalf("WaitForTargetsHealthy() error = %v, want nil", err)
		}
		out := buf.String()
		if !strings.Contains(out, "i-1=initial") {
			t.Errorf("writer output %q missing the initial-state progress line", out)
		}
		if !strings.Contains(out, "i-1=healthy") {
			t.Errorf("writer output %q missing the healthy-state progress line", out)
		}
	})

	t.Run("StuckUnhealthyThroughTimeoutReturnsError", func(t *testing.T) {
		f := newFakeTargetHealthAPI()
		f.sequences["arn-voice"] = [][]TargetState{{{ID: "i-1", State: "unhealthy"}}}

		var buf bytes.Buffer
		err := WaitForTargetsHealthy(ctx, f, map[string]string{"voice": "arn-voice"}, &buf, 10*time.Millisecond, 3*time.Millisecond)
		if err == nil {
			t.Fatal("WaitForTargetsHealthy() error = nil, want non-nil for a target stuck unhealthy through the timeout")
		}
		if !strings.Contains(err.Error(), "voice") || !strings.Contains(err.Error(), "unhealthy") {
			t.Errorf("error = %q, want it to name the group %q and state 'unhealthy'", err.Error(), "voice")
		}
	})

	t.Run("EmptyTargetGroupThenRegistersHealthy", func(t *testing.T) {
		f := newFakeTargetHealthAPI()
		f.sequences["arn-voice"] = [][]TargetState{
			{},
			{},
			{{ID: "i-1", State: "healthy"}},
		}

		var buf bytes.Buffer
		err := WaitForTargetsHealthy(ctx, f, map[string]string{"voice": "arn-voice"}, &buf, time.Second, time.Millisecond)
		if err != nil {
			t.Fatalf("WaitForTargetsHealthy() error = %v, want nil once targets register and go healthy", err)
		}
		if !strings.Contains(buf.String(), "awaiting registration") {
			t.Errorf("writer output %q does not report the empty group as awaiting registration (never silently treated as healthy)", buf.String())
		}
	})

	t.Run("TransientDescribeErrorRetried", func(t *testing.T) {
		f := newFakeTargetHealthAPI()
		f.errsRemaining["arn-voice"] = 2
		f.sequences["arn-voice"] = [][]TargetState{{{ID: "i-1", State: "healthy"}}}

		var buf bytes.Buffer
		err := WaitForTargetsHealthy(ctx, f, map[string]string{"voice": "arn-voice"}, &buf, time.Second, time.Millisecond)
		if err != nil {
			t.Fatalf("WaitForTargetsHealthy() error = %v, want nil once the transient errors resolve", err)
		}
	})

	t.Run("PersistentDescribeErrorReturnsError", func(t *testing.T) {
		f := newFakeTargetHealthAPI()
		f.permanentErr["arn-voice"] = true

		var buf bytes.Buffer
		err := WaitForTargetsHealthy(ctx, f, map[string]string{"voice": "arn-voice"}, &buf, time.Second, time.Millisecond)
		if err == nil {
			t.Fatal("WaitForTargetsHealthy() error = nil, want non-nil on a persistent describe-target-health error")
		}
	})
}

func TestVoiceAndAuthTargetGroups(t *testing.T) {
	t.Run("SelectsVoiceAndAuthTolerantOfMissingTelephonyEdge", func(t *testing.T) {
		all := map[string]string{
			"voice-use1-lb-0": "arn-voice",
			"auth-use1-lb-0":  "arn-auth",
		}
		got, err := VoiceAndAuthTargetGroups(all)
		if err != nil {
			t.Fatalf("VoiceAndAuthTargetGroups() error = %v, want nil", err)
		}
		if len(got) != 2 || got["voice-use1-lb-0"] != "arn-voice" || got["auth-use1-lb-0"] != "arn-auth" {
			t.Errorf("VoiceAndAuthTargetGroups() = %v, want both voice and auth entries unchanged", got)
		}
	})

	t.Run("SimplifiedExactKeysAlsoMatch", func(t *testing.T) {
		all := map[string]string{"voice": "arn-voice", "auth": "arn-auth"}
		got, err := VoiceAndAuthTargetGroups(all)
		if err != nil {
			t.Fatalf("VoiceAndAuthTargetGroups() error = %v, want nil", err)
		}
		if len(got) != 2 {
			t.Errorf("VoiceAndAuthTargetGroups() = %v, want 2 entries", got)
		}
	})

	t.Run("ReturnsErrorWhenNeitherPresent", func(t *testing.T) {
		all := map[string]string{"other-service-lb-0": "arn-other"}
		_, err := VoiceAndAuthTargetGroups(all)
		if err == nil {
			t.Fatal("VoiceAndAuthTargetGroups() error = nil, want non-nil when neither voice nor auth is present")
		}
	})
}
