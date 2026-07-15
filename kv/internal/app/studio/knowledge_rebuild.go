// Package studio's knowledge rebuild trigger: KNOW-03. This file shells the
// EXACT same subprocess `kv knowledge refresh` (cmd/knowledge.go) already
// shells — `uv run python scripts/refresh_knowledge.py`, working dir
// <repo>/apps/voice — with the manifest/out-dir paths hardcoded exactly as
// that cobra command's own flags never expose them (T-17-06). It is a thin
// trigger + reporter only: the distill/chunk/lint pipeline lives entirely in
// refresh_knowledge.py, unmodified. After the subprocess exits, this file
// runs ONLY read-only `git status --porcelain` / `git diff --stat` scoped to
// apps/voice/knowledge — it NEVER runs `git add`/`git commit` (D-09's
// human-review gate stays; see TestKnowledgeRebuild_NeverCommits). This file
// deliberately builds no minimum-pack-size write gate of any kind: the
// runtime prompt-caching threshold documented at config.py's D-13 is
// unrelated to what refresh_knowledge.py writes to disk
// (17-RESEARCH.md Pitfall 1) and has no business appearing here.
package studio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// rebuildTimeout bounds the subprocess's DECOUPLED context (17-RESEARCH.md
// Pattern 3): a browser navigating away mid-request must not SIGKILL a
// multi-minute LLM distillation pass mid-write, so the subprocess runs under
// context.Background() with this generous bound, never r.Context() directly.
const rebuildTimeout = 10 * time.Minute

// gitStatusTimeout bounds the read-only git status/diff calls that run
// after the subprocess exits — generous but far shorter than the
// distillation pass itself, and also decoupled from the inbound request's
// context for the same reason as rebuildTimeout.
const gitStatusTimeout = 30 * time.Second

// CommandResult is one subprocess invocation's captured outcome —
// CommandRunner's return shape, used for both the `uv` refresh invocation
// and the read-only `git` calls.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	// Err is non-nil for any exec.Cmd.Run() failure (a non-zero exit OR a
	// launch failure, e.g. the binary not found). ExitCode is derived from
	// Err via *exec.ExitError when possible; -1 for a launch failure.
	Err error
}

// CommandRunner is the narrow subprocess-execution seam KnowledgeRebuildTrigger
// depends on, so tests can inject a fake instead of shelling a real,
// multi-minute LLM call (17-RESEARCH.md Wave 0 Gaps). execCommandRunner is
// the only production implementation.
type CommandRunner interface {
	Run(ctx context.Context, dir, name string, args ...string) CommandResult
}

// execCommandRunner is CommandRunner's real os/exec implementation —
// mirrors cmd/knowledge.go's exec.CommandContext usage exactly, capturing
// stdout/stderr into buffers instead of streaming to a cobra command's
// OutOrStdout/ErrOrStderr (there is no terminal on the other end of an HTTP
// handler).
type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, dir, name string, args ...string) CommandResult {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode, Err: err}
}

// errKnowledgeRebuildAlreadyRunning is returned by Rebuild when a prior call
// hasn't finished yet — the in-process single-flight guard (17-RESEARCH.md
// Pitfall 4 / T-17-07): a local single-operator console still runs in a
// browser tab, and a double-click (or two tabs) must not launch two
// concurrent refresh_knowledge.py subprocesses writing into the same
// apps/voice/knowledge/ tree.
var errKnowledgeRebuildAlreadyRunning = errors.New("knowledge rebuild already running")

// KnowledgeRebuildTrigger triggers `kv knowledge refresh`'s exact subprocess
// invocation and reports what changed, guarded by an in-process
// single-flight lock. Root is the klanker-voice repo root (RepoFiles.Root);
// Runner defaults to execCommandRunner when nil.
type KnowledgeRebuildTrigger struct {
	Runner CommandRunner
	Root   string

	mu      sync.Mutex
	running bool
}

// NewKnowledgeRebuildTrigger returns a KnowledgeRebuildTrigger wired to the
// real os/exec CommandRunner, rooted at root.
func NewKnowledgeRebuildTrigger(root string) *KnowledgeRebuildTrigger {
	return &KnowledgeRebuildTrigger{Runner: execCommandRunner{}, Root: root}
}

func (t *KnowledgeRebuildTrigger) runner() CommandRunner {
	if t.Runner != nil {
		return t.Runner
	}
	return execCommandRunner{}
}

// Rebuild shells `uv run python scripts/refresh_knowledge.py` from
// <Root>/apps/voice (cmd/knowledge.go's exact invocation shape), with the
// script path and working directory hardcoded — req can never supply a
// manifest or out-dir path, because RebuildReq has no such fields
// (T-17-06). The default (req's zero value) is the FULL distill pass: only
// --skip-distill/--dry-run are forwarded, and only when req explicitly asks
// for them (17-RESEARCH.md Pitfall 6 — a bare invocation must never
// silently degrade to an index-only rebuild). The subprocess runs under a
// context decoupled from ctx (Pattern 3). After it exits, Rebuild runs ONLY
// read-only `git status --porcelain` / `git diff --stat` scoped to
// apps/voice/knowledge and returns their output — it never runs `git
// add`/`git commit` (D-09 stays a human step). A non-zero exit's stderr is
// surfaced verbatim in the result (Pitfall 5) — never collapsed into a
// generic error string. The only Go error this returns is
// errKnowledgeRebuildAlreadyRunning (the single-flight rejection); a failed
// subprocess run is still a successful Rebuild call, reported via
// RebuildResult.Success=false.
func (t *KnowledgeRebuildTrigger) Rebuild(_ context.Context, req RebuildReq) (RebuildResult, error) {
	t.mu.Lock()
	if t.running {
		t.mu.Unlock()
		return RebuildResult{}, errKnowledgeRebuildAlreadyRunning
	}
	t.running = true
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.running = false
		t.mu.Unlock()
	}()

	appDir := filepath.Join(t.Root, "apps", "voice")
	args := []string{"run", "python", "scripts/refresh_knowledge.py"}
	if req.SkipDistill {
		args = append(args, "--skip-distill")
	}
	if req.DryRun {
		args = append(args, "--dry-run")
	}

	runCtx, cancel := context.WithTimeout(context.Background(), rebuildTimeout)
	defer cancel()
	res := t.runner().Run(runCtx, appDir, "uv", args...)

	result := RebuildResult{
		Success:  res.Err == nil,
		ExitCode: res.ExitCode,
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
	}

	gitCtx, gitCancel := context.WithTimeout(context.Background(), gitStatusTimeout)
	defer gitCancel()
	result.ChangedFiles = t.gitChangedFiles(gitCtx)
	result.DiffStat = t.gitDiffStat(gitCtx)
	result.Summary = rebuildSummary(result)

	return result, nil
}

// knowledgeSubtree is the apps/voice/knowledge subtree the read-only git
// commands scope to — never the whole repo, and never git add/commit.
const knowledgeSubtree = "apps/voice/knowledge"

// gitChangedFiles runs `git -C <root> status --porcelain -- <subtree>` and
// parses the changed-file paths out of porcelain output. A failure (e.g.
// git not on PATH, or Root isn't a git worktree) degrades to an empty slice
// — this is a best-effort report, never a reason to fail the whole Rebuild
// call (the subprocess already ran; the operator can always run `git
// status` themselves).
func (t *KnowledgeRebuildTrigger) gitChangedFiles(ctx context.Context) []string {
	res := t.runner().Run(ctx, t.Root, "git", "-C", t.Root, "status", "--porcelain", "--", knowledgeSubtree)
	if res.Err != nil {
		return []string{}
	}
	files := []string{}
	for line := range strings.SplitSeq(res.Stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" || len(line) <= 3 {
			continue
		}
		files = append(files, strings.TrimSpace(line[3:]))
	}
	return files
}

// gitDiffStat runs `git -C <root> diff --stat -- <subtree>` for a
// human-readable summary of what changed — the read-only diff surfacing an
// operator reviews before ever running `git add`/`git commit` themselves
// (D-09).
func (t *KnowledgeRebuildTrigger) gitDiffStat(ctx context.Context) string {
	res := t.runner().Run(ctx, t.Root, "git", "-C", t.Root, "diff", "--stat", "--", knowledgeSubtree)
	if res.Err != nil {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// rebuildSummary is the short, human-readable line the console shows
// alongside the full report — "rebuild ran → N packs changed → review the
// diff" per 17-RESEARCH.md's locked decision, never an auto-commit.
func rebuildSummary(r RebuildResult) string {
	if !r.Success {
		return fmt.Sprintf("rebuild failed (exit %d) — review stderr below", r.ExitCode)
	}
	n := len(r.ChangedFiles)
	if n == 0 {
		return "rebuild ran → no files changed"
	}
	return fmt.Sprintf("rebuild ran → %d file(s) changed → review the diff before committing", n)
}
