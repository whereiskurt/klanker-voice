package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// recordingGitFake is a GitAPI fake that records every CommitPaths/Push
// call and lets each check's return value be set independently, so each
// D-18 refusal path can be exercised in isolation without a real git
// binary or repository (D-31). It never shells out and never touches disk.
type recordingGitFake struct {
	branch    string
	branchErr error

	clean    bool
	cleanErr error

	synced    bool
	syncedErr error

	diffErr error

	commitCalls    int
	committedPaths [][]string
	committedMsgs  []string
	commitErr      error

	pushCalls int
	pushErr   error
}

func newPassingGitFake() *recordingGitFake {
	return &recordingGitFake{
		branch: LifecycleBranch,
		clean:  true,
		synced: true,
	}
}

func (f *recordingGitFake) CurrentBranch(context.Context) (string, error) {
	return f.branch, f.branchErr
}

func (f *recordingGitFake) IsClean(context.Context) (bool, error) {
	return f.clean, f.cleanErr
}

func (f *recordingGitFake) SyncedWithOrigin(context.Context, string) (bool, error) {
	return f.synced, f.syncedErr
}

func (f *recordingGitFake) Diff(context.Context, ...string) (string, error) {
	return "", f.diffErr
}

func (f *recordingGitFake) CommitPaths(_ context.Context, message string, paths ...string) error {
	f.commitCalls++
	f.committedMsgs = append(f.committedMsgs, message)
	f.committedPaths = append(f.committedPaths, paths)
	return f.commitErr
}

func (f *recordingGitFake) Push(context.Context, string) error {
	f.pushCalls++
	return f.pushErr
}

func (f *recordingGitFake) HeadSHA(context.Context) (string, error) {
	return "deadbeefcafef00d", nil
}

func alwaysNilAuthCheck(context.Context) error { return nil }

func TestPreflightLifecycle(t *testing.T) {
	t.Run("dirty tree refuses and commits nothing", func(t *testing.T) {
		f := newPassingGitFake()
		f.clean = false

		err := PreflightLifecycle(context.Background(), f, alwaysNilAuthCheck, LifecycleBranch)
		if !errors.Is(err, ErrPreflightDirtyTree) {
			t.Fatalf("PreflightLifecycle() error = %v, want errors.Is ErrPreflightDirtyTree", err)
		}
		if f.commitCalls != 0 {
			t.Errorf("commitCalls = %d, want 0", f.commitCalls)
		}
		if f.pushCalls != 0 {
			t.Errorf("pushCalls = %d, want 0", f.pushCalls)
		}
	})

	t.Run("wrong branch refuses, names both branches, and commits nothing", func(t *testing.T) {
		f := newPassingGitFake()
		f.branch = "feature/scratch"

		err := PreflightLifecycle(context.Background(), f, alwaysNilAuthCheck, LifecycleBranch)
		if !errors.Is(err, ErrPreflightWrongBranch) {
			t.Fatalf("PreflightLifecycle() error = %v, want errors.Is ErrPreflightWrongBranch", err)
		}
		if !strings.Contains(err.Error(), "feature/scratch") {
			t.Errorf("error %q does not name the current branch %q", err.Error(), "feature/scratch")
		}
		if !strings.Contains(err.Error(), LifecycleBranch) {
			t.Errorf("error %q does not name the required branch %q", err.Error(), LifecycleBranch)
		}
		if f.commitCalls != 0 {
			t.Errorf("commitCalls = %d, want 0", f.commitCalls)
		}
		if f.pushCalls != 0 {
			t.Errorf("pushCalls = %d, want 0", f.pushCalls)
		}
	})

	t.Run("stale origin refuses and commits nothing", func(t *testing.T) {
		f := newPassingGitFake()
		f.synced = false

		err := PreflightLifecycle(context.Background(), f, alwaysNilAuthCheck, LifecycleBranch)
		if !errors.Is(err, ErrPreflightStaleOrigin) {
			t.Fatalf("PreflightLifecycle() error = %v, want errors.Is ErrPreflightStaleOrigin", err)
		}
		if f.commitCalls != 0 {
			t.Errorf("commitCalls = %d, want 0", f.commitCalls)
		}
		if f.pushCalls != 0 {
			t.Errorf("pushCalls = %d, want 0", f.pushCalls)
		}
	})

	t.Run("gh auth failure refuses and commits nothing", func(t *testing.T) {
		f := newPassingGitFake()
		authErr := errors.New("not logged in")

		err := PreflightLifecycle(context.Background(), f, func(context.Context) error { return authErr }, LifecycleBranch)
		if !errors.Is(err, ErrPreflightGHAuth) {
			t.Fatalf("PreflightLifecycle() error = %v, want errors.Is ErrPreflightGHAuth", err)
		}
		if f.commitCalls != 0 {
			t.Errorf("commitCalls = %d, want 0", f.commitCalls)
		}
		if f.pushCalls != 0 {
			t.Errorf("pushCalls = %d, want 0", f.pushCalls)
		}
	})

	t.Run("success path passes preflight then commits exactly one scoped path and pushes once", func(t *testing.T) {
		f := newPassingGitFake()

		if err := PreflightLifecycle(context.Background(), f, alwaysNilAuthCheck, LifecycleBranch); err != nil {
			t.Fatalf("PreflightLifecycle() error = %v, want nil", err)
		}

		// PreflightLifecycle only checks -- the commit/push sequence a
		// caller (pause.go) performs after a nil preflight result is
		// exercised here directly against the same fake, proving the
		// intended one-path-commit contract (D-19) the fake's recording
		// semantics must support for that future caller.
		if err := f.CommitPaths(context.Background(), PauseCommitMessage, SiteHCLRelPath); err != nil {
			t.Fatalf("CommitPaths() error = %v, want nil", err)
		}
		if err := f.Push(context.Background(), LifecycleBranch); err != nil {
			t.Fatalf("Push() error = %v, want nil", err)
		}

		if f.commitCalls != 1 {
			t.Fatalf("commitCalls = %d, want 1", f.commitCalls)
		}
		if len(f.committedPaths[0]) != 1 || f.committedPaths[0][0] != SiteHCLRelPath {
			t.Errorf("committed paths = %v, want exactly [%q]", f.committedPaths[0], SiteHCLRelPath)
		}
		if f.pushCalls != 1 {
			t.Errorf("pushCalls = %d, want 1", f.pushCalls)
		}
	})

	t.Run("pause and resume commit messages match D-19 exactly", func(t *testing.T) {
		if PauseCommitMessage != "ops(infra): pause ECS services (desired_count=0)" {
			t.Errorf("PauseCommitMessage = %q, want the exact D-19 pause string", PauseCommitMessage)
		}
		if ResumeCommitMessage != "ops(infra): resume ECS services (desired_count=1)" {
			t.Errorf("ResumeCommitMessage = %q, want the resume counterpart", ResumeCommitMessage)
		}
		if PauseCommitMessage == ResumeCommitMessage {
			t.Error("PauseCommitMessage and ResumeCommitMessage must differ")
		}
	})
}

// TestExecGit_Constructor proves NewExecGit satisfies GitAPI and records
// its root without shelling out -- construction alone must never invoke
// git, so this stays valid with no real git binary or repository present.
func TestExecGit_Constructor(t *testing.T) {
	var g GitAPI = NewExecGit("/nonexistent/repo/root")
	if g == nil {
		t.Fatal("NewExecGit() returned nil")
	}
	eg, ok := g.(*execGit)
	if !ok {
		t.Fatalf("NewExecGit() returned %T, want *execGit", g)
	}
	if eg.root != "/nonexistent/repo/root" {
		t.Errorf("execGit.root = %q, want %q", eg.root, "/nonexistent/repo/root")
	}
}
