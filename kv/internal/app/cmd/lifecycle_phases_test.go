package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// recordingGH records every call in order so a test can assert not just
// what happened but the sequence -- which is the whole point of D-07.
type recordingGH struct {
	calls     []string
	watchErr  map[string]error
	runIDSeq  []string
	runIDNext int
}

func (g *recordingGH) AuthStatus(ctx context.Context) error { return nil }

func (g *recordingGH) DispatchWorkflow(ctx context.Context, workflow, ref string, inputs map[string]string) error {
	g.calls = append(g.calls, fmt.Sprintf("dispatch:%s", inputs["modules"]))
	return nil
}

func (g *recordingGH) LatestRunID(ctx context.Context, workflow, ref string, notBefore time.Time) (string, error) {
	id := g.runIDSeq[g.runIDNext]
	g.runIDNext++
	return id, nil
}

func (g *recordingGH) WatchRun(ctx context.Context, runID string, w io.Writer) error {
	g.calls = append(g.calls, "watch:"+runID)
	if err, ok := g.watchErr[runID]; ok {
		return err
	}
	return nil
}

func fixedNow() func() time.Time {
	t := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

// D-07: each phase is dispatched, then watched to completion, before the
// next phase is dispatched at all.
func TestRunApplyPhases_DispatchesStrictlySequentially(t *testing.T) {
	gh := &recordingGH{runIDSeq: []string{"run-1", "run-2"}}

	ids, err := RunApplyPhases(context.Background(), gh, "main", HibernatePhases, fixedNow(), io.Discard)
	if err != nil {
		t.Fatalf("RunApplyPhases error: %v", err)
	}

	want := []string{
		"dispatch:ecs-service,cloudfront",
		"watch:run-1",
		"dispatch:network",
		"watch:run-2",
	}
	if len(gh.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", gh.calls, want)
	}
	for i := range want {
		if gh.calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, gh.calls[i], want[i])
		}
	}
	if len(ids) != 2 {
		t.Errorf("run ids = %v, want 2", ids)
	}
}

// D-07: a failed phase 1 aborts before phase 2 is dispatched. This is the
// test that matters most -- the failure it guards against is a cancelled
// run holding a half-completed destroy.
func TestRunApplyPhases_FailedPhaseAbortsBeforeNextDispatch(t *testing.T) {
	gh := &recordingGH{
		runIDSeq: []string{"run-1", "run-2"},
		watchErr: map[string]error{"run-1": errors.New("run failed")},
	}

	_, err := RunApplyPhases(context.Background(), gh, "main", HibernatePhases, fixedNow(), io.Discard)
	if err == nil {
		t.Fatal("RunApplyPhases error = nil, want a phase-1 failure")
	}
	if !strings.Contains(err.Error(), "ecs-service,cloudfront") {
		t.Errorf("error %q does not name the failing phase's modules", err)
	}

	for _, c := range gh.calls {
		if c == "dispatch:network" {
			t.Fatal("phase 2 was dispatched after phase 1 failed -- D-07 violated")
		}
	}
}

// Wake runs the same units in terragrunt's natural dependency order.
func TestWakePhases_ReverseHibernateOrder(t *testing.T) {
	if len(WakePhases) != 2 {
		t.Fatalf("WakePhases has %d phases, want 2", len(WakePhases))
	}
	if WakePhases[0].Modules != "network" {
		t.Errorf("wake phase 1 modules = %q, want \"network\"", WakePhases[0].Modules)
	}
	if WakePhases[1].Modules != "ecs-service,cloudfront" {
		t.Errorf("wake phase 2 modules = %q, want \"ecs-service,cloudfront\"", WakePhases[1].Modules)
	}
}

// D-08: an empty module list means apply-everything, which would apply the
// email unit and steal the account's active SES receipt rule set.
func TestValidatePhases_RefusesEmptyModuleList(t *testing.T) {
	err := ValidatePhases([]ApplyPhase{{Name: "bad", Modules: ""}})
	if !errors.Is(err, ErrEmptyModuleList) {
		t.Fatalf("error = %v, want ErrEmptyModuleList", err)
	}
}

// D-08: the email unit must never appear in a dispatch.
func TestValidatePhases_RefusesEmailModule(t *testing.T) {
	err := ValidatePhases([]ApplyPhase{{Name: "bad", Modules: "network,email"}})
	if !errors.Is(err, ErrForbiddenModule) {
		t.Fatalf("error = %v, want ErrForbiddenModule", err)
	}
}

// The shipped phase lists must themselves pass validation.
func TestShippedPhaseListsValidate(t *testing.T) {
	if err := ValidatePhases(HibernatePhases); err != nil {
		t.Errorf("HibernatePhases: %v", err)
	}
	if err := ValidatePhases(WakePhases); err != nil {
		t.Errorf("WakePhases: %v", err)
	}
}

// RunApplyPhases validates before dispatching anything at all.
func TestRunApplyPhases_ValidatesBeforeFirstDispatch(t *testing.T) {
	gh := &recordingGH{runIDSeq: []string{"run-1"}}

	_, err := RunApplyPhases(context.Background(), gh, "main",
		[]ApplyPhase{{Name: "bad", Modules: ""}}, fixedNow(), io.Discard)
	if !errors.Is(err, ErrEmptyModuleList) {
		t.Fatalf("error = %v, want ErrEmptyModuleList", err)
	}
	if len(gh.calls) != 0 {
		t.Errorf("calls = %v, want none -- validation must precede dispatch", gh.calls)
	}
}
