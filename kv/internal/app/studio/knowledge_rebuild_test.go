package studio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCommandRunner is an in-memory CommandRunner: it never shells a real
// process, records every call it receives (dir/name/args), and returns a
// configured CommandResult. block, when non-nil, is read from before
// returning — used by TestKnowledgeRebuild_SingleFlight to hold a call open
// long enough to prove the single-flight guard rejects a concurrent trigger.
type fakeCommandRunner struct {
	mu     sync.Mutex
	calls  []fakeCall
	result CommandResult
	block  chan struct{}
}

type fakeCall struct {
	Dir  string
	Name string
	Args []string
}

func (f *fakeCommandRunner) Run(_ context.Context, dir, name string, args ...string) CommandResult {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{Dir: dir, Name: name, Args: append([]string(nil), args...)})
	f.mu.Unlock()
	if f.block != nil {
		<-f.block
	}
	return f.result
}

func (f *fakeCommandRunner) callsSnapshot() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeCall(nil), f.calls...)
}

func (f *fakeCommandRunner) countByName(name string) int {
	n := 0
	for _, c := range f.callsSnapshot() {
		if c.Name == name {
			n++
		}
	}
	return n
}

// --------------------------------------------------------------------------
// TestKnowledgeRebuild_NeverCommits (T-17-06)

// TestKnowledgeRebuild_NeverCommits asserts no executed argv contains a
// mutating git subcommand (`git add`/`git commit`) and that the uv
// invocation's working directory and script args are exactly the hardcoded
// constants — regardless of what RebuildReq carries (there is no
// manifest/out-dir field to smuggle one through in the first place, but this
// also proves nothing downstream ever improvises one).
func TestKnowledgeRebuild_NeverCommits(t *testing.T) {
	root := t.TempDir()
	runner := &fakeCommandRunner{result: CommandResult{ExitCode: 0}}
	trig := &KnowledgeRebuildTrigger{Runner: runner, Root: root}

	if _, err := trig.Rebuild(context.Background(), RebuildReq{SkipDistill: true, DryRun: true}); err != nil {
		t.Fatalf("Rebuild() error: %v", err)
	}

	calls := runner.callsSnapshot()
	if len(calls) == 0 {
		t.Fatal("no commands were executed")
	}

	wantAppDir := filepath.Join(root, "apps", "voice")
	sawUv := false
	for _, c := range calls {
		switch c.Name {
		case "uv":
			sawUv = true
			if c.Dir != wantAppDir {
				t.Errorf("uv call Dir = %q, want hardcoded %q", c.Dir, wantAppDir)
			}
			for _, a := range c.Args {
				if a == "--manifest" || a == "--out-dir" {
					t.Errorf("uv argv contains a request-suppliable path flag %q — must never be accepted", a)
				}
			}
		case "git":
			if len(c.Args) == 0 {
				t.Fatal("git call with no args")
			}
			for i, a := range c.Args {
				if a == "add" || a == "commit" {
					t.Errorf("git argv contains a MUTATING subcommand %q at index %d: %v", a, i, c.Args)
				}
			}
			if !(contains(c.Args, "status") || contains(c.Args, "diff")) {
				t.Errorf("git argv is neither status nor diff: %v", c.Args)
			}
		}
	}
	if !sawUv {
		t.Fatal("no uv call recorded")
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// --------------------------------------------------------------------------
// TestKnowledgeRebuild_SingleFlight (T-17-07)

// TestKnowledgeRebuild_SingleFlight asserts a second overlapping Rebuild
// call returns errKnowledgeRebuildAlreadyRunning WITHOUT launching a second
// uv subprocess.
func TestKnowledgeRebuild_SingleFlight(t *testing.T) {
	root := t.TempDir()
	block := make(chan struct{})
	runner := &fakeCommandRunner{result: CommandResult{ExitCode: 0}, block: block}
	trig := &KnowledgeRebuildTrigger{Runner: runner, Root: root}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := trig.Rebuild(context.Background(), RebuildReq{}); err != nil {
			t.Errorf("first Rebuild() error: %v", err)
		}
	}()

	waitUntilRunning(t, trig)

	if _, err := trig.Rebuild(context.Background(), RebuildReq{}); !errors.Is(err, errKnowledgeRebuildAlreadyRunning) {
		t.Fatalf("second concurrent Rebuild() error = %v, want errKnowledgeRebuildAlreadyRunning", err)
	}

	close(block)
	<-done

	if got := runner.countByName("uv"); got != 1 {
		t.Errorf("uv calls = %d, want exactly 1 (single-flight must reject the second trigger, never launch a second subprocess)", got)
	}
}

// waitUntilRunning polls trig.running (unexported, same package) until the
// first Rebuild call has acquired the single-flight lock, or fails the test
// after a generous deadline.
func waitUntilRunning(t *testing.T, trig *KnowledgeRebuildTrigger) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		trig.mu.Lock()
		running := trig.running
		trig.mu.Unlock()
		if running {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the first Rebuild() call to start")
}

// --------------------------------------------------------------------------
// Stderr surfaced verbatim (Pitfall 5) + default-is-full-distill (Pitfall 6)

// TestKnowledgeRebuild_NonZeroExitSurfacesStderrVerbatim asserts a failed
// subprocess's captured stderr is returned exactly, not collapsed into a
// generic "exit status 1"-shaped message.
func TestKnowledgeRebuild_NonZeroExitSurfacesStderrVerbatim(t *testing.T) {
	root := t.TempDir()
	wantStderr := "ConfigError: ANTHROPIC_API_KEY not set. Run make -C apps/voice env"
	runner := &fakeCommandRunner{result: CommandResult{
		ExitCode: 1,
		Stderr:   wantStderr,
		Err:      errors.New("exit status 1"),
	}}
	trig := &KnowledgeRebuildTrigger{Runner: runner, Root: root}

	result, err := trig.Rebuild(context.Background(), RebuildReq{})
	if err != nil {
		t.Fatalf("Rebuild() error: %v, want nil (a failed subprocess is still a successful Rebuild call)", err)
	}
	if result.Success {
		t.Error("result.Success = true, want false for a non-zero exit")
	}
	if result.Stderr != wantStderr {
		t.Errorf("result.Stderr = %q, want the verbatim captured stderr %q", result.Stderr, wantStderr)
	}
	if strings.Contains(result.Summary, wantStderr) {
		t.Error("result.Summary should not itself embed the full stderr text — it's a short pointer, not a duplicate")
	}
}

// TestKnowledgeRebuild_DefaultIsFullDistillPass asserts a zero-value
// RebuildReq invokes the bare `uv run python scripts/refresh_knowledge.py`
// with NEITHER --skip-distill NOR --dry-run — the FULL distill pass,
// matching `make -C apps/voice knowledge`'s own bare invocation
// (17-RESEARCH.md Pitfall 6).
func TestKnowledgeRebuild_DefaultIsFullDistillPass(t *testing.T) {
	root := t.TempDir()
	runner := &fakeCommandRunner{result: CommandResult{ExitCode: 0}}
	trig := &KnowledgeRebuildTrigger{Runner: runner, Root: root}

	if _, err := trig.Rebuild(context.Background(), RebuildReq{}); err != nil {
		t.Fatalf("Rebuild() error: %v", err)
	}

	wantArgs := []string{"run", "python", "scripts/refresh_knowledge.py"}
	found := false
	for _, c := range runner.callsSnapshot() {
		if c.Name != "uv" {
			continue
		}
		found = true
		if len(c.Args) != len(wantArgs) {
			t.Fatalf("uv args = %v, want exactly %v (no --skip-distill/--dry-run on the default request)", c.Args, wantArgs)
		}
		for i, a := range wantArgs {
			if c.Args[i] != a {
				t.Errorf("uv args[%d] = %q, want %q", i, c.Args[i], a)
			}
		}
	}
	if !found {
		t.Fatal("no uv call recorded")
	}
}

// TestKnowledgeRebuild_SkipDistillAndDryRunAreOptIn asserts the two flags
// are forwarded ONLY when the request explicitly sets them.
func TestKnowledgeRebuild_SkipDistillAndDryRunAreOptIn(t *testing.T) {
	root := t.TempDir()
	runner := &fakeCommandRunner{result: CommandResult{ExitCode: 0}}
	trig := &KnowledgeRebuildTrigger{Runner: runner, Root: root}

	if _, err := trig.Rebuild(context.Background(), RebuildReq{SkipDistill: true, DryRun: true}); err != nil {
		t.Fatalf("Rebuild() error: %v", err)
	}

	for _, c := range runner.callsSnapshot() {
		if c.Name != "uv" {
			continue
		}
		if !contains(c.Args, "--skip-distill") {
			t.Errorf("uv args = %v, want --skip-distill present when req.SkipDistill=true", c.Args)
		}
		if !contains(c.Args, "--dry-run") {
			t.Errorf("uv args = %v, want --dry-run present when req.DryRun=true", c.Args)
		}
	}
}

// --------------------------------------------------------------------------
// Changed-file reporting

// TestKnowledgeRebuild_ChangedFilesParsedFromGitStatusPorcelain asserts the
// changed-file list is parsed from the (fake) `git status --porcelain`
// output, and the summary mentions a non-zero count.
func TestKnowledgeRebuild_ChangedFilesParsedFromGitStatusPorcelain(t *testing.T) {
	root := t.TempDir()
	runner := &namedCommandRunner{
		byName: map[string]CommandResult{
			"uv":  {ExitCode: 0},
			"git": {ExitCode: 0, Stdout: " M apps/voice/knowledge/topics/klanker-maker.md\n?? apps/voice/knowledge/topics/meshtk.md\n"},
		},
	}
	trig := &KnowledgeRebuildTrigger{Runner: runner, Root: root}

	result, err := trig.Rebuild(context.Background(), RebuildReq{})
	if err != nil {
		t.Fatalf("Rebuild() error: %v", err)
	}
	wantFiles := []string{"apps/voice/knowledge/topics/klanker-maker.md", "apps/voice/knowledge/topics/meshtk.md"}
	if len(result.ChangedFiles) != len(wantFiles) {
		t.Fatalf("result.ChangedFiles = %v, want %v", result.ChangedFiles, wantFiles)
	}
	for i, want := range wantFiles {
		if result.ChangedFiles[i] != want {
			t.Errorf("result.ChangedFiles[%d] = %q, want %q", i, result.ChangedFiles[i], want)
		}
	}
	if !strings.Contains(result.Summary, "2") {
		t.Errorf("result.Summary = %q, want it to mention the changed-file count", result.Summary)
	}
}

// namedCommandRunner is a fakeCommandRunner variant that returns a
// per-command-name CommandResult (uv vs git need different canned output in
// the changed-files test above).
type namedCommandRunner struct {
	mu     sync.Mutex
	calls  []fakeCall
	byName map[string]CommandResult
}

func (n *namedCommandRunner) Run(_ context.Context, dir, name string, args ...string) CommandResult {
	n.mu.Lock()
	n.calls = append(n.calls, fakeCall{Dir: dir, Name: name, Args: append([]string(nil), args...)})
	n.mu.Unlock()
	return n.byName[name]
}

// --------------------------------------------------------------------------
// No token-floor gate (17-RESEARCH.md Pitfall 1) — the grep-gate itself is
// run as a shell verification step (`grep -Lq '4096' knowledge_rebuild.go`)
// per the plan; this Go test is a redundant, in-suite companion so `go test`
// alone also catches a regression.

func TestKnowledgeRebuild_NoTokenFloorGateReferenced(t *testing.T) {
	b, err := os.ReadFile("knowledge_rebuild.go")
	if err != nil {
		t.Fatalf("read knowledge_rebuild.go: %v", err)
	}
	src := string(b)
	for _, banned := range []string{"cache_floor", "4096"} {
		if strings.Contains(src, banned) {
			t.Errorf("knowledge_rebuild.go references %q — no token-floor write gate belongs here (17-RESEARCH.md Pitfall 1)", banned)
		}
	}
}
