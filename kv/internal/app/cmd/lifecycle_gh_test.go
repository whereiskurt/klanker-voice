package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// recordingRunner is a commandRunner fake that records every argv and
// replays a canned response sequence (one []byte per call, the last
// response repeating if more calls happen than responses provided) -- so
// DispatchWorkflow's exact argv and LatestRunID/WatchRun's multi-call
// polling behavior are provable without ever invoking a real gh binary or
// reaching the network (D-31).
type recordingRunner struct {
	calls     [][]string
	responses [][]byte
	err       error
}

func (r *recordingRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	argv := append([]string{name}, args...)
	r.calls = append(r.calls, argv)
	if r.err != nil {
		return nil, r.err
	}
	idx := len(r.calls) - 1
	if idx < len(r.responses) {
		return r.responses[idx], nil
	}
	if len(r.responses) > 0 {
		return r.responses[len(r.responses)-1], nil
	}
	return nil, nil
}

// newTestExecGH builds an execGH wired to r with an instant no-op sleep, so
// LatestRunID's bounded backoff and WatchRun's poll loop run at test speed.
func newTestExecGH(r *recordingRunner) *execGH {
	return &execGH{
		root:              "/nonexistent",
		run:               r.run,
		sleep:             func(time.Duration) {},
		watchPollInterval: 0,
	}
}

func TestExecGH_DispatchWorkflow_BuildsExpectedArgv(t *testing.T) {
	r := &recordingRunner{}
	g := newTestExecGH(r)

	err := g.DispatchWorkflow(context.Background(), TerragruntApplyWorkflow, LifecycleBranch, map[string]string{"modules": EcsServiceApplyModules})
	if err != nil {
		t.Fatalf("DispatchWorkflow() error = %v, want nil", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("run call count = %d, want 1", len(r.calls))
	}

	argv := r.calls[0]
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, TerragruntApplyWorkflow) {
		t.Errorf("argv %v does not contain workflow file name %q", argv, TerragruntApplyWorkflow)
	}
	if !strings.Contains(joined, "--ref main") {
		t.Errorf("argv %v does not contain '--ref main'", argv)
	}
	if !strings.Contains(joined, "modules=ecs-service") {
		t.Errorf("argv %v does not contain a field flag 'modules=ecs-service'", argv)
	}
}

func TestExecGH_LatestRunID_ReturnsNewestRunAtOrAfterDispatch(t *testing.T) {
	notBefore := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	payload := `[
		{"databaseId": 111, "createdAt": "2026-08-20T11:00:00Z", "status": "completed", "conclusion": "success"},
		{"databaseId": 222, "createdAt": "2026-08-20T12:05:00Z", "status": "in_progress", "conclusion": ""}
	]`
	r := &recordingRunner{responses: [][]byte{[]byte(payload)}}
	g := newTestExecGH(r)

	id, err := g.LatestRunID(context.Background(), TerragruntApplyWorkflow, LifecycleBranch, notBefore)
	if err != nil {
		t.Fatalf("LatestRunID() error = %v, want nil", err)
	}
	if id != "222" {
		t.Errorf("LatestRunID() = %q, want %q (the newer run; the older run predates notBefore)", id, "222")
	}
	if len(r.calls) != 1 {
		t.Errorf("run call count = %d, want 1 (a match on the first poll needs no retry)", len(r.calls))
	}
}

func TestExecGH_LatestRunID_EmptyPayloadReturnsBoundedError(t *testing.T) {
	r := &recordingRunner{responses: [][]byte{[]byte(`[]`)}}
	g := newTestExecGH(r)

	_, err := g.LatestRunID(context.Background(), TerragruntApplyWorkflow, LifecycleBranch, time.Now())
	if !errors.Is(err, ErrLatestRunNotFound) {
		t.Fatalf("LatestRunID() error = %v, want errors.Is ErrLatestRunNotFound", err)
	}
	if len(r.calls) != latestRunIDMaxAttempts {
		t.Errorf("run call count = %d, want %d (bounded attempts, not an infinite loop)", len(r.calls), latestRunIDMaxAttempts)
	}
}

func TestExecGH_WatchRun_FailureConclusionIsError(t *testing.T) {
	responses := [][]byte{
		[]byte(`{"status":"in_progress","conclusion":""}`),
		[]byte(`{"status":"completed","conclusion":"failure"}`),
	}
	r := &recordingRunner{responses: responses}
	g := newTestExecGH(r)

	var buf bytes.Buffer
	err := g.WatchRun(context.Background(), "999", &buf)
	if err == nil {
		t.Fatal("WatchRun() error = nil, want non-nil on a failure conclusion")
	}
	if !strings.Contains(err.Error(), "999") {
		t.Errorf("WatchRun() error = %q, want it to name run id 999", err.Error())
	}
	if !strings.Contains(err.Error(), "failure") {
		t.Errorf("WatchRun() error = %q, want it to name the conclusion 'failure'", err.Error())
	}
}

func TestExecGH_WatchRun_WaitingStateIsNotAnError(t *testing.T) {
	responses := [][]byte{
		[]byte(`{"status":"waiting","conclusion":""}`),
		[]byte(`{"status":"completed","conclusion":"success"}`),
	}
	r := &recordingRunner{responses: responses}
	g := newTestExecGH(r)

	var buf bytes.Buffer
	if err := g.WatchRun(context.Background(), "1000", &buf); err != nil {
		t.Fatalf("WatchRun() error = %v, want nil on an eventual success conclusion", err)
	}
	if !strings.Contains(buf.String(), "awaiting") {
		t.Errorf("WatchRun() output = %q, want a line containing 'awaiting' for the waiting state", buf.String())
	}
}

// TestExecGH_NoTokenOrEnvironmentInArgv is a defense-in-depth companion to
// the file-level grep acceptance criterion: no argv this file assembles,
// across any of its exported entry points, may carry a credential-shaped
// value or process-environment content (T-16-06-05).
func TestExecGH_NoTokenOrEnvironmentInArgv(t *testing.T) {
	r := &recordingRunner{responses: [][]byte{[]byte(`[]`), []byte(`[]`), []byte(`[]`), []byte(`[]`), []byte(`[]`)}}
	g := newTestExecGH(r)

	_ = g.AuthStatus(context.Background())
	_ = g.DispatchWorkflow(context.Background(), TerragruntApplyWorkflow, LifecycleBranch, map[string]string{"modules": EcsServiceApplyModules})
	_, _ = g.LatestRunID(context.Background(), TerragruntApplyWorkflow, LifecycleBranch, time.Now())

	banned := []string{"token", "Bearer", "Authorization", "secret"}
	for _, call := range r.calls {
		joined := strings.ToLower(strings.Join(call, " "))
		for _, word := range banned {
			if strings.Contains(joined, strings.ToLower(word)) {
				t.Errorf("argv %v unexpectedly contains banned substring %q", call, word)
			}
		}
	}
}
