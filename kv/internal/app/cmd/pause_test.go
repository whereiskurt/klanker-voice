package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --------------------------------------------------------------------------
// Shared fixtures + fakes for RunLifecycleFlip (pause+resume) tests. These
// deliberately track step ORDER, not just call counts (the plan's "assert
// call ordering, not just call counts" instruction) via a shared *stepLog
// every fake below marks into.

// stepLog records the order distinct orchestration steps occur in, across
// several independent fakes (GitAPI, GHAPI, ECSAPI, TargetHealthAPI) that
// RunLifecycleFlip calls through.
type stepLog struct {
	steps []string
}

func (s *stepLog) mark(step string) { s.steps = append(s.steps, step) }

func (s *stepLog) indexOf(step string) int {
	for i, v := range s.steps {
		if v == step {
			return i
		}
	}
	return -1
}

// siteHCLFixture copies the 16-05 unpaused.hcl/paused.hcl testdata fixture
// into a fresh t.TempDir() at SiteHCLRelPath, returning the temp repo root
// and the fixture's original bytes so a test can assert byte-identity after
// a refused or declined flip.
func siteHCLFixture(t *testing.T, initiallyPaused bool) (repoRoot string, original []byte) {
	t.Helper()
	name := "unpaused.hcl"
	if initiallyPaused {
		name = "paused.hcl"
	}
	src, err := os.ReadFile(filepath.Join("testdata", "site-hcl", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, SiteHCLRelPath)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dest, src, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir, src
}

// flowGitFake is a GitAPI fake purpose-built for RunLifecycleFlip's tests:
// beyond recordingGitFake-style call counts, it marks Diff/CommitPaths/Push
// into a shared stepLog so ordering ACROSS the whole flow (not just one
// fake's own call count) is directly assertable.
type flowGitFake struct {
	branch string
	clean  bool
	synced bool

	branchErr, cleanErr, syncedErr, diffErr, commitErr, pushErr error

	diffCalls      int
	commitCalls    int
	committedMsgs  []string
	committedPaths [][]string
	pushCalls      int

	log *stepLog
}

func newFlowGitFake(log *stepLog) *flowGitFake {
	return &flowGitFake{branch: LifecycleBranch, clean: true, synced: true, log: log}
}

func (f *flowGitFake) CurrentBranch(context.Context) (string, error) { return f.branch, f.branchErr }
func (f *flowGitFake) IsClean(context.Context) (bool, error)         { return f.clean, f.cleanErr }
func (f *flowGitFake) SyncedWithOrigin(context.Context, string) (bool, error) {
	return f.synced, f.syncedErr
}

func (f *flowGitFake) Diff(context.Context, ...string) (string, error) {
	f.diffCalls++
	f.log.mark("diff")
	return "--- a/site.hcl\n+++ b/site.hcl\n-  paused = false\n+  paused = true\n", f.diffErr
}

func (f *flowGitFake) CommitPaths(_ context.Context, message string, paths ...string) error {
	f.commitCalls++
	f.committedMsgs = append(f.committedMsgs, message)
	f.committedPaths = append(f.committedPaths, paths)
	f.log.mark("commit")
	return f.commitErr
}

func (f *flowGitFake) Push(context.Context, string) error {
	f.pushCalls++
	f.log.mark("push")
	return f.pushErr
}

func (f *flowGitFake) HeadSHA(context.Context) (string, error) { return "deadbeefcafef00d", nil }

// flowGHFake is a GHAPI fake marking dispatch/latestrun/watch into the
// shared stepLog.
type flowGHFake struct {
	authErr      error
	dispatchErr  error
	latestRunErr error
	watchErr     error
	runID        string

	dispatchCalls  int
	dispatchInputs []map[string]string
	latestRunCalls int
	watchCalls     int

	log *stepLog
}

func newFlowGHFake(log *stepLog) *flowGHFake {
	return &flowGHFake{runID: "1001", log: log}
}

func (f *flowGHFake) AuthStatus(context.Context) error { return f.authErr }

func (f *flowGHFake) DispatchWorkflow(_ context.Context, _ string, _ string, inputs map[string]string) error {
	f.dispatchCalls++
	f.dispatchInputs = append(f.dispatchInputs, inputs)
	f.log.mark("dispatch")
	return f.dispatchErr
}

func (f *flowGHFake) LatestRunID(context.Context, string, string, time.Time) (string, error) {
	f.latestRunCalls++
	f.log.mark("latestrun")
	return f.runID, f.latestRunErr
}

func (f *flowGHFake) WatchRun(_ context.Context, runID string, w io.Writer) error {
	f.watchCalls++
	f.log.mark("watch")
	if f.watchErr != nil {
		return f.watchErr
	}
	fmt.Fprintf(w, "run %s: completed\n", runID)
	return nil
}

// orderedECS wraps fakeDrainECS (lifecycle_ecs_test.go) with a one-shot mark
// into the shared stepLog on its first DescribeServices call, so DrainToZero
// / WaitForServicesRunning's position in the whole flow is assertable
// alongside the git/gh steps above.
type orderedECS struct {
	*fakeDrainECS
	log    *stepLog
	label  string
	marked bool
}

func (f *orderedECS) DescribeServices(ctx context.Context, cluster string, services []string) ([]ServicePosture, error) {
	if !f.marked {
		f.marked = true
		f.log.mark(f.label)
	}
	return f.fakeDrainECS.DescribeServices(ctx, cluster, services)
}

// orderedHealth wraps fakeTargetHealthAPI (lifecycle_alb_test.go) with the
// same one-shot marking behavior as orderedECS.
type orderedHealth struct {
	*fakeTargetHealthAPI
	log    *stepLog
	marked bool
}

func (f *orderedHealth) DescribeTargetHealth(ctx context.Context, arn string) ([]TargetState, error) {
	if !f.marked {
		f.marked = true
		f.log.mark("health")
	}
	return f.fakeTargetHealthAPI.DescribeTargetHealth(ctx, arn)
}

// --------------------------------------------------------------------------
// Task 1: TestRunLifecycleFlip (pause path), TestPauseCmd.

func TestRunLifecycleFlip(t *testing.T) {
	t.Run("AlreadyPausedIsANoOp", func(t *testing.T) {
		root, original := siteHCLFixture(t, true)
		log := &stepLog{}
		git := newFlowGitFake(log)
		gh := newFlowGHFake(log)
		ecs := &orderedECS{fakeDrainECS: newFakeDrainECS(map[string]*serviceProfile{}), log: log, label: "drain"}
		var out bytes.Buffer
		deps := LifecycleDeps{
			RepoRoot: root, Git: git, GH: gh, ECS: ecs,
			Cluster: "cluster", Services: []string{"voice", "auth", "telephony-edge"},
			Now: time.Now, In: strings.NewReader(""), Out: &out,
		}
		opts := LifecycleFlipOptions{Want: true, RequiredBranch: LifecycleBranch}

		if err := RunLifecycleFlip(context.Background(), deps, opts); err != nil {
			t.Fatalf("RunLifecycleFlip() error = %v, want nil", err)
		}
		if git.commitCalls != 0 || git.pushCalls != 0 || gh.dispatchCalls != 0 {
			t.Errorf("recorded commit=%d push=%d dispatch=%d, want all 0 for an already-in-state flip", git.commitCalls, git.pushCalls, gh.dispatchCalls)
		}
		got, err := os.ReadFile(filepath.Join(root, SiteHCLRelPath))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, original) {
			t.Error("flag file changed on an already-in-state flip")
		}
		if !strings.Contains(out.String(), "true") {
			t.Errorf("status output = %q, want it to report paused=true", out.String())
		}
	})

	t.Run("PreflightRefusalLeavesFileUnchanged", func(t *testing.T) {
		root, original := siteHCLFixture(t, false)
		log := &stepLog{}
		git := newFlowGitFake(log)
		git.clean = false // dirty tree -> ErrPreflightDirtyTree
		gh := newFlowGHFake(log)
		ecs := &orderedECS{fakeDrainECS: newFakeDrainECS(nil), log: log, label: "drain"}
		var out bytes.Buffer
		deps := LifecycleDeps{
			RepoRoot: root, Git: git, GH: gh, ECS: ecs,
			Cluster: "c", Services: []string{"voice"},
			Now: time.Now, In: strings.NewReader(""), Out: &out,
		}
		opts := LifecycleFlipOptions{Want: true, RequiredBranch: LifecycleBranch}

		err := RunLifecycleFlip(context.Background(), deps, opts)
		if !errors.Is(err, ErrPreflightDirtyTree) {
			t.Fatalf("RunLifecycleFlip() error = %v, want errors.Is ErrPreflightDirtyTree", err)
		}
		got, rerr := os.ReadFile(filepath.Join(root, SiteHCLRelPath))
		if rerr != nil {
			t.Fatal(rerr)
		}
		if !bytes.Equal(got, original) {
			t.Error("flag file changed despite a preflight refusal")
		}
		if git.commitCalls != 0 || git.pushCalls != 0 || gh.dispatchCalls != 0 {
			t.Error("commit/push/dispatch recorded despite a preflight refusal")
		}
	})

	t.Run("DeclinedConfirmationRestoresFile", func(t *testing.T) {
		root, original := siteHCLFixture(t, false)
		log := &stepLog{}
		git := newFlowGitFake(log)
		gh := newFlowGHFake(log)
		ecs := &orderedECS{fakeDrainECS: newFakeDrainECS(nil), log: log, label: "drain"}
		var out bytes.Buffer
		deps := LifecycleDeps{
			RepoRoot: root, Git: git, GH: gh, ECS: ecs,
			Cluster: "c", Services: []string{"voice"},
			Now: time.Now, In: strings.NewReader("no\n"), Out: &out,
		}
		opts := LifecycleFlipOptions{Want: true, RequiredBranch: LifecycleBranch}

		err := RunLifecycleFlip(context.Background(), deps, opts)
		if !errors.Is(err, ErrLifecycleCancelled) {
			t.Fatalf("RunLifecycleFlip() error = %v, want errors.Is ErrLifecycleCancelled", err)
		}
		got, rerr := os.ReadFile(filepath.Join(root, SiteHCLRelPath))
		if rerr != nil {
			t.Fatal(rerr)
		}
		if !bytes.Equal(got, original) {
			t.Error("flag file not restored to its original bytes after a declined confirmation")
		}
		if git.commitCalls != 0 {
			t.Errorf("commitCalls = %d, want 0 after a declined confirmation", git.commitCalls)
		}
	})

	t.Run("NonInteractiveStdinWithoutYesRefuses", func(t *testing.T) {
		root, original := siteHCLFixture(t, false)
		log := &stepLog{}
		git := newFlowGitFake(log)
		gh := newFlowGHFake(log)
		ecs := &orderedECS{fakeDrainECS: newFakeDrainECS(nil), log: log, label: "drain"}
		var out bytes.Buffer
		deps := LifecycleDeps{
			RepoRoot: root, Git: git, GH: gh, ECS: ecs,
			Cluster: "c", Services: []string{"voice"},
			Now: time.Now, In: strings.NewReader(""), Out: &out, // EOF immediately: non-interactive
		}
		opts := LifecycleFlipOptions{Want: true, RequiredBranch: LifecycleBranch}

		err := RunLifecycleFlip(context.Background(), deps, opts)
		if err == nil {
			t.Fatal("RunLifecycleFlip() error = nil, want non-nil for a non-interactive stdin without --yes")
		}
		if git.commitCalls != 0 {
			t.Errorf("commitCalls = %d, want 0", git.commitCalls)
		}
		got, rerr := os.ReadFile(filepath.Join(root, SiteHCLRelPath))
		if rerr != nil {
			t.Fatal(rerr)
		}
		if !bytes.Equal(got, original) {
			t.Error("flag file changed despite a non-interactive-stdin refusal")
		}
	})

	t.Run("HappyPathRecordsStepsInOrder", func(t *testing.T) {
		root, _ := siteHCLFixture(t, false)
		log := &stepLog{}
		git := newFlowGitFake(log)
		gh := newFlowGHFake(log)
		profiles := map[string]*serviceProfile{
			"voice":          {naturalZeroAtCall: 1},
			"auth":           {naturalZeroAtCall: 1},
			"telephony-edge": {naturalZeroAtCall: 1},
		}
		ecs := &orderedECS{fakeDrainECS: newFakeDrainECS(profiles), log: log, label: "drain"}
		var out bytes.Buffer
		deps := LifecycleDeps{
			RepoRoot: root, Git: git, GH: gh, ECS: ecs,
			Cluster: "cluster", Services: []string{"voice", "auth", "telephony-edge"},
			Now: time.Now, In: strings.NewReader("y\n"), Out: &out,
		}
		opts := LifecycleFlipOptions{
			Want: true, RequiredBranch: LifecycleBranch,
			Drain: DrainOptions{PollInterval: time.Millisecond, Timeout: time.Second, Correct: true},
		}

		if err := RunLifecycleFlip(context.Background(), deps, opts); err != nil {
			t.Fatalf("RunLifecycleFlip() error = %v, want nil", err)
		}

		if git.diffCalls != 1 {
			t.Errorf("diffCalls = %d, want 1", git.diffCalls)
		}
		if git.commitCalls != 1 {
			t.Fatalf("commitCalls = %d, want 1", git.commitCalls)
		}
		if git.committedMsgs[0] != PauseCommitMessage {
			t.Errorf("commit message = %q, want %q", git.committedMsgs[0], PauseCommitMessage)
		}
		if len(git.committedPaths[0]) != 1 || git.committedPaths[0][0] != SiteHCLRelPath {
			t.Errorf("committed paths = %v, want exactly [%q]", git.committedPaths[0], SiteHCLRelPath)
		}
		if git.pushCalls != 1 {
			t.Errorf("pushCalls = %d, want 1", git.pushCalls)
		}
		if gh.dispatchCalls != 1 {
			t.Fatalf("dispatchCalls = %d, want 1", gh.dispatchCalls)
		}
		if got := gh.dispatchInputs[0]["modules"]; got != EcsServiceApplyModules {
			t.Errorf("dispatch modules input = %q, want %q", got, EcsServiceApplyModules)
		}
		if gh.latestRunCalls != 1 {
			t.Errorf("latestRunCalls = %d, want 1", gh.latestRunCalls)
		}
		if gh.watchCalls != 1 {
			t.Errorf("watchCalls = %d, want 1", gh.watchCalls)
		}

		want := []string{"diff", "commit", "push", "dispatch", "latestrun", "watch", "drain"}
		if strings.Join(log.steps, ",") != strings.Join(want, ",") {
			t.Errorf("step order = %v, want %v", log.steps, want)
		}

		got, rerr := os.ReadFile(filepath.Join(root, SiteHCLRelPath))
		if rerr != nil {
			t.Fatal(rerr)
		}
		paused, perr := ReadPausedFlag(got)
		if perr != nil {
			t.Fatal(perr)
		}
		if !paused {
			t.Error("flag file was not rewritten to paused = true")
		}
	})

	t.Run("FailedCIRunAbortsBeforeDrain", func(t *testing.T) {
		root, _ := siteHCLFixture(t, false)
		log := &stepLog{}
		git := newFlowGitFake(log)
		gh := newFlowGHFake(log)
		gh.watchErr = fmt.Errorf("run 1001 finished with conclusion %q", "failure")
		ecs := &orderedECS{fakeDrainECS: newFakeDrainECS(nil), log: log, label: "drain"}
		var out bytes.Buffer
		deps := LifecycleDeps{
			RepoRoot: root, Git: git, GH: gh, ECS: ecs,
			Cluster: "c", Services: []string{"voice"},
			Now: time.Now, In: strings.NewReader("y\n"), Out: &out,
		}
		opts := LifecycleFlipOptions{Want: true, RequiredBranch: LifecycleBranch}

		err := RunLifecycleFlip(context.Background(), deps, opts)
		if err == nil {
			t.Fatal("RunLifecycleFlip() error = nil, want non-nil on a failed CI run")
		}
		if !strings.Contains(err.Error(), "1001") {
			t.Errorf("error %q does not name the run", err.Error())
		}
		if ecs.describeCalls != 0 {
			t.Errorf("describeCalls = %d, want 0 -- drain must never run after a failed CI run", ecs.describeCalls)
		}
	})

	t.Run("DrainErrorReturnsNonZeroNamingService", func(t *testing.T) {
		root, _ := siteHCLFixture(t, false)
		log := &stepLog{}
		git := newFlowGitFake(log)
		gh := newFlowGHFake(log)
		profiles := map[string]*serviceProfile{
			"voice": {running: 1, desired: 1, ignoreUpdate: true},
		}
		ecs := &orderedECS{fakeDrainECS: newFakeDrainECS(profiles), log: log, label: "drain"}
		var out bytes.Buffer
		deps := LifecycleDeps{
			RepoRoot: root, Git: git, GH: gh, ECS: ecs,
			Cluster: "c", Services: []string{"voice"},
			Now: time.Now, In: strings.NewReader("y\n"), Out: &out,
		}
		opts := LifecycleFlipOptions{
			Want: true, RequiredBranch: LifecycleBranch,
			Drain: DrainOptions{Timeout: 5 * time.Millisecond, PollInterval: time.Millisecond, GraceAfterCorrection: time.Millisecond, Correct: true},
		}

		err := RunLifecycleFlip(context.Background(), deps, opts)
		if err == nil {
			t.Fatal("RunLifecycleFlip() error = nil, want non-nil when a service never reaches zero")
		}
		if !strings.Contains(err.Error(), "voice") {
			t.Errorf("error %q does not name the stuck service", err.Error())
		}
	})
}

func TestPauseCmd(t *testing.T) {
	t.Run("HasYesReasonFlagsAndStatusSubcommand", func(t *testing.T) {
		cmd := NewPauseCmd(&Config{})
		if cmd.Flags().Lookup("yes") == nil {
			t.Error("missing --yes flag")
		}
		if cmd.Flags().Lookup("reason") == nil {
			t.Error("missing --reason flag")
		}
		found := false
		for _, sub := range cmd.Commands() {
			if sub.Name() == "status" {
				found = true
			}
		}
		if !found {
			t.Error("missing status subcommand")
		}
	})
}

// --------------------------------------------------------------------------
// Task 2: TestRunLifecycleFlipResume, TestPrintPauseReport, TestResumeCmd.

func TestRunLifecycleFlipResume(t *testing.T) {
	t.Run("ResumeCallsWaitForServicesRunningThenWaitForTargetsHealthy", func(t *testing.T) {
		root, _ := siteHCLFixture(t, true) // currently paused, resuming to false
		log := &stepLog{}
		git := newFlowGitFake(log)
		gh := newFlowGHFake(log)
		profiles := map[string]*serviceProfile{
			"voice": {desired: 1, rampToRunningAtCall: 1},
			"auth":  {desired: 1, rampToRunningAtCall: 1},
		}
		ecs := &orderedECS{fakeDrainECS: newFakeDrainECS(profiles), log: log, label: "wait-running"}
		health := &orderedHealth{fakeTargetHealthAPI: newFakeTargetHealthAPI(), log: log}
		health.sequences["arn-voice"] = [][]TargetState{{{ID: "i-1", State: "healthy"}}}
		health.sequences["arn-auth"] = [][]TargetState{{{ID: "i-2", State: "healthy"}}}
		var out bytes.Buffer
		deps := LifecycleDeps{
			RepoRoot: root, Git: git, GH: gh, ECS: ecs, Health: health,
			Cluster: "cluster", Services: []string{"voice", "auth"},
			TargetGroups: map[string]string{"voice": "arn-voice", "auth": "arn-auth"},
			Now:          time.Now, In: strings.NewReader("y\n"), Out: &out,
		}
		opts := LifecycleFlipOptions{
			Want: false, RequiredBranch: LifecycleBranch,
			Drain: DrainOptions{PollInterval: time.Millisecond, Timeout: time.Second},
		}

		if err := RunLifecycleFlip(context.Background(), deps, opts); err != nil {
			t.Fatalf("RunLifecycleFlip() error = %v, want nil", err)
		}

		waitIdx := log.indexOf("wait-running")
		healthIdx := log.indexOf("health")
		if waitIdx == -1 || healthIdx == -1 || waitIdx > healthIdx {
			t.Errorf("step order = %v, want wait-running before health", log.steps)
		}
		if len(git.committedMsgs) == 0 || git.committedMsgs[0] != ResumeCommitMessage {
			t.Errorf("commit message = %v, want first entry %q", git.committedMsgs, ResumeCommitMessage)
		}
		if !strings.Contains(out.String(), "kv resume: complete.") {
			t.Errorf("output does not contain the resume success line: %q", out.String())
		}
	})

	t.Run("HealthWaitErrorMakesResumeNonZeroNoSuccessLine", func(t *testing.T) {
		root, _ := siteHCLFixture(t, true)
		log := &stepLog{}
		git := newFlowGitFake(log)
		gh := newFlowGHFake(log)
		profiles := map[string]*serviceProfile{
			"voice": {desired: 1, rampToRunningAtCall: 1},
			"auth":  {desired: 1, rampToRunningAtCall: 1},
		}
		ecs := &orderedECS{fakeDrainECS: newFakeDrainECS(profiles), log: log, label: "wait-running"}
		health := &orderedHealth{fakeTargetHealthAPI: newFakeTargetHealthAPI(), log: log}
		health.sequences["arn-voice"] = [][]TargetState{{{ID: "i-1", State: "unhealthy"}}}
		health.sequences["arn-auth"] = [][]TargetState{{{ID: "i-2", State: "healthy"}}}
		var out bytes.Buffer
		deps := LifecycleDeps{
			RepoRoot: root, Git: git, GH: gh, ECS: ecs, Health: health,
			Cluster: "cluster", Services: []string{"voice", "auth"},
			TargetGroups: map[string]string{"voice": "arn-voice", "auth": "arn-auth"},
			Now:          time.Now, In: strings.NewReader("y\n"), Out: &out,
		}
		opts := LifecycleFlipOptions{
			Want: false, RequiredBranch: LifecycleBranch,
			Drain: DrainOptions{PollInterval: time.Millisecond, Timeout: 5 * time.Millisecond},
		}

		err := RunLifecycleFlip(context.Background(), deps, opts)
		if err == nil {
			t.Fatal("RunLifecycleFlip() error = nil, want non-nil when target-group health never converges")
		}
		if strings.Contains(out.String(), "kv resume: complete.") {
			t.Errorf("output contains the success line despite the health wait failing: %q", out.String())
		}
	})
}

func TestPrintPauseReport(t *testing.T) {
	t.Run("ContainsCostPostureElevenLabsKillswitch503FastBusy", func(t *testing.T) {
		var buf bytes.Buffer
		PrintPauseReport(&buf, PauseReport{Paused: true, RunID: "42", Elapsed: time.Minute})
		out := buf.String()
		for _, want := range []string{"ElevenLabs", "99", "kill-switch", "503", "fast busy"} {
			if !strings.Contains(out, want) {
				t.Errorf("report does not contain %q:\n%s", want, out)
			}
		}
	})

	t.Run("StatesMidPauseDeployLeavesStackPaused", func(t *testing.T) {
		var buf bytes.Buffer
		PrintPauseReport(&buf, PauseReport{Paused: true})
		if !strings.Contains(buf.String(), "leaves the stack paused") {
			t.Errorf("report does not state the config-is-the-guard property:\n%s", buf.String())
		}
	})

	t.Run("StatesPausedBehaviourFacts", func(t *testing.T) {
		var buf bytes.Buffer
		PrintPauseReport(&buf, PauseReport{Paused: true})
		out := buf.String()
		for _, want := range []string{"still loads", "mic tap", "auth.klankermaker.ai", "provisioned"} {
			if !strings.Contains(out, want) {
				t.Errorf("report does not contain %q:\n%s", want, out)
			}
		}
	})

	t.Run("StatesKillswitchNotTouched", func(t *testing.T) {
		var buf bytes.Buffer
		PrintPauseReport(&buf, PauseReport{Paused: true})
		if !strings.Contains(buf.String(), "kill-switch was not touched") {
			t.Errorf("report does not state kill-switch orthogonality:\n%s", buf.String())
		}
	})

	t.Run("NamesCorrectedServicesWhenPresent", func(t *testing.T) {
		var buf bytes.Buffer
		PrintPauseReport(&buf, PauseReport{Paused: true, Corrected: []string{"voice"}})
		out := buf.String()
		if !strings.Contains(out, "voice") || !strings.Contains(out, "Application Auto Scaling") {
			t.Errorf("report does not name the corrected service and the correction: %s", out)
		}
	})
}

func TestResumeCmd(t *testing.T) {
	t.Run("HasYesFlag", func(t *testing.T) {
		cmd := NewResumeCmd(&Config{})
		if cmd.Flags().Lookup("yes") == nil {
			t.Error("missing --yes flag")
		}
	})
}
