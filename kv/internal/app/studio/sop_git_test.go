package studio

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --------------------------------------------------------------------------
// TestSaveSOP_ScopedCommit (SOP-01 / T-18-11)

// TestSaveSOP_ScopedCommit runs SaveSOP against a REAL temporary git repo
// (execCommandRunner{} — the real os/exec CommandRunner, no fake) that has
// an UNRELATED dirty file already present in the working tree, and asserts
// the resulting commit contains ONLY sops/<name>.yaml — the unrelated file
// is never staged, never committed, and is still dirty afterward
// (18-RESEARCH.md Pitfall 4; must-have "even when unrelated dirty files
// exist in the working tree").
func TestSaveSOP_ScopedCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}

	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "test")

	// Seed a tracked, committed file, then dirty it — the "unrelated
	// in-progress edit the operator has open in their editor" scenario
	// 18-RESEARCH.md's Pitfall 4 describes.
	unrelatedPath := filepath.Join(root, "unrelated.txt")
	if err := os.WriteFile(unrelatedPath, []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write unrelated seed file: %v", err)
	}
	runGit(t, root, "add", "unrelated.txt")
	runGit(t, root, "commit", "-q", "-m", "seed")

	if err := os.WriteFile(unrelatedPath, []byte("unrelated in-progress edit\n"), 0o644); err != nil {
		t.Fatalf("dirty unrelated file: %v", err)
	}

	live := ConfigView{}
	runner := execCommandRunner{}

	sha, err := SaveSOP(context.Background(), root, "conference-2026", live, runner)
	if err != nil {
		t.Fatalf("SaveSOP() error = %v", err)
	}
	if strings.TrimSpace(sha) == "" {
		t.Fatal("SaveSOP() returned an empty sha")
	}

	// The SOP file itself must exist on disk.
	sopFile := filepath.Join(root, "apps", "voice", "configs", "studio", "sops", "conference-2026.yaml")
	if _, err := os.Stat(sopFile); err != nil {
		t.Fatalf("expected SOP file at %s: %v", sopFile, err)
	}

	// The commit must touch EXACTLY sops/conference-2026.yaml — nothing
	// else, and definitely not unrelated.txt.
	statOut := runGitOutput(t, root, "show", "--stat", "--format=", sha)
	if !strings.Contains(statOut, "apps/voice/configs/studio/sops/conference-2026.yaml") {
		t.Errorf("commit stat = %q, want it to contain apps/voice/configs/studio/sops/conference-2026.yaml", statOut)
	}
	if strings.Contains(statOut, "unrelated.txt") {
		t.Errorf("commit stat = %q, must NOT contain unrelated.txt", statOut)
	}

	nameOnly := runGitOutput(t, root, "show", "--name-only", "--format=", sha)
	files := []string{}
	for _, line := range strings.Split(strings.TrimSpace(nameOnly), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	if len(files) != 1 || files[0] != "apps/voice/configs/studio/sops/conference-2026.yaml" {
		t.Errorf("commit touched files %v, want exactly [apps/voice/configs/studio/sops/conference-2026.yaml]", files)
	}

	// unrelated.txt must still be dirty (modified, uncommitted) — never
	// swept into the SaveSOP commit.
	statusOut := runGitOutput(t, root, "status", "--porcelain")
	if !strings.Contains(statusOut, "unrelated.txt") {
		t.Errorf("git status --porcelain = %q, want unrelated.txt still dirty (not committed)", statusOut)
	}

	// No push subcommand was ever invoked — SaveSOP/gitCommitScoped is a
	// local-only commit (18-RESEARCH.md D constraint 7 / T-18-12).
	logOut := runGitOutput(t, root, "log", "--oneline", "-n", "5")
	if strings.Contains(logOut, "push") {
		t.Errorf("git log unexpectedly mentions push: %q", logOut)
	}
}

// --------------------------------------------------------------------------
// TestGitCommitScoped_RefusesUnexpectedStage (T-18-11)

// TestGitCommitScoped_RefusesUnexpectedStage uses a fakeCommandRunner whose
// `git diff --cached --name-only` response reports an extra, unexpected
// path — proving gitCommitScoped's staged-subset assertion refuses to
// commit and issues a `git reset` instead.
func TestGitCommitScoped_RefusesUnexpectedStage(t *testing.T) {
	runner := &nameOnlyFakeRunner{extraStagedPath: "unrelated/sneaky.yaml"}

	sha, err := gitCommitScoped(context.Background(), runner, "/repo", "sop: save x", []string{"sops/x.yaml"})
	if err == nil {
		t.Fatalf("gitCommitScoped() error = nil, sha = %q, want a refusal error", sha)
	}
	if sha != "" {
		t.Errorf("gitCommitScoped() sha = %q, want empty on refusal", sha)
	}

	calls := runner.callsSnapshot()
	if len(calls) == 0 {
		t.Fatal("expected at least one git invocation")
	}

	// A `git reset` must have run (unstage-on-refusal), and `git commit`
	// must NEVER have run — the mismatch must be caught before commit.
	sawReset := false
	for _, c := range calls {
		if c.Name != "git" {
			continue
		}
		for _, a := range c.Args {
			if a == "commit" {
				t.Fatalf("gitCommitScoped invoked `git commit` despite an unexpected staged path: %+v", calls)
			}
			if a == "reset" {
				sawReset = true
			}
		}
	}
	if !sawReset {
		t.Errorf("expected a `git reset` call to unstage on refusal, calls = %+v", calls)
	}
}

// nameOnlyFakeRunner is a CommandRunner fake that returns a real result for
// `git add`, injects an unexpected path into `git diff --cached
// --name-only`'s stdout, and records every call — used by
// TestGitCommitScoped_RefusesUnexpectedStage to prove the subset assertion
// fires without needing a real git repo.
type nameOnlyFakeRunner struct {
	extraStagedPath string
	calls           []fakeCall
}

func (n *nameOnlyFakeRunner) Run(_ context.Context, dir, name string, args ...string) CommandResult {
	n.calls = append(n.calls, fakeCall{Dir: dir, Name: name, Args: append([]string(nil), args...)})
	for _, a := range args {
		if a == "diff" {
			return CommandResult{Stdout: n.extraStagedPath + "\n"}
		}
	}
	return CommandResult{}
}

func (n *nameOnlyFakeRunner) callsSnapshot() []fakeCall {
	return append([]fakeCall(nil), n.calls...)
}

// --------------------------------------------------------------------------
// TestStagedSetMatches — pure unit coverage of the subset-assertion helper.

func TestStagedSetMatches(t *testing.T) {
	tests := []struct {
		name     string
		stdout   string
		expected []string
		want     bool
	}{
		{"empty stdout is a subset", "", []string{"sops/x.yaml"}, true},
		{"exact match", "sops/x.yaml\n", []string{"sops/x.yaml"}, true},
		{"subset of a larger expected list", "sops/x.yaml\n", []string{"sops/x.yaml", "sops/y.yaml"}, true},
		{"unexpected extra path refused", "sops/x.yaml\nunrelated.txt\n", []string{"sops/x.yaml"}, false},
		{"blank lines ignored", "sops/x.yaml\n\n", []string{"sops/x.yaml"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stagedSetMatches(tt.stdout, tt.expected)
			if got != tt.want {
				t.Errorf("stagedSetMatches(%q, %v) = %v, want %v", tt.stdout, tt.expected, got, tt.want)
			}
		})
	}
}
