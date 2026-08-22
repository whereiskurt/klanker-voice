package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// GitAPI is the narrow seam every git operation kv pause / kv resume needs
// runs through (D-30). Every method takes a context so the exec-backed
// implementation can be cancelled; PreflightLifecycle and the future
// commit-and-push orchestration in pause.go/resume.go depend only on this
// interface, never on os/exec directly, so the four D-18 refusal paths are
// provable with a recording fake and no real git binary or repository
// (D-31).
type GitAPI interface {
	// CurrentBranch reports the checked-out branch name.
	CurrentBranch(ctx context.Context) (string, error)
	// IsClean reports true only when the working tree has no pending
	// changes (tracked or untracked).
	IsClean(ctx context.Context) (bool, error)
	// SyncedWithOrigin reports true only when the local branch's commit
	// object is identical to origin's -- false covers behind, ahead, and
	// diverged alike, since kv must never push a pause on top of unpushed
	// local work or a stale view of origin.
	SyncedWithOrigin(ctx context.Context, branch string) (bool, error)
	// Diff returns the unstaged+staged diff scoped to paths, for the
	// show-the-diff-and-confirm step (spec 5.3 step 3).
	Diff(ctx context.Context, paths ...string) (string, error)
	// CommitPaths stages and commits only paths -- never `git add -A` or
	// `git add .` -- so an unrelated stray file in the working tree can
	// never ride along on a pause/resume commit (T-16-06-01).
	CommitPaths(ctx context.Context, message string, paths ...string) error
	// Push pushes the current HEAD to origin/branch.
	Push(ctx context.Context, branch string) error
	// HeadSHA reports the current commit object at HEAD.
	HeadSHA(ctx context.Context) (string, error)
}

// LifecycleBranch is the only branch kv pause / kv resume ever commit to,
// and the branch PreflightLifecycle's wrong-branch check requires (D-18,
// D-19).
const LifecycleBranch = "main"

// PauseCommitMessage / ResumeCommitMessage are the exact D-19 commit
// messages, scoped to exactly SiteHCLRelPath (see lifecycle_pauseflag.go).
// Both services normally run at desired_count = 1 (see
// infra/terraform/live/site/services/{voice,auth,telephony-edge}/
// service.hcl), so ResumeCommitMessage's count is the literal baseline
// desired_count the paused flag flips back to, phrased identically to the
// pause message apart from the verb and the count.
const (
	PauseCommitMessage  = "ops(infra): pause ECS services (desired_count=0)"
	ResumeCommitMessage = "ops(infra): resume ECS services (desired_count=1)"
)

// The four D-18 preflight refusals, each a distinct sentinel so a caller
// (and every test) can assert exactly which check failed via errors.Is,
// never a single vague error. PreflightLifecycle wraps each with actionable
// context -- what was found, what was required, and the one command that
// fixes it -- at the raise site below.
var (
	ErrPreflightDirtyTree   = errors.New("preflight refused: working tree is not clean")
	ErrPreflightWrongBranch = errors.New("preflight refused: not on the required branch")
	ErrPreflightStaleOrigin = errors.New("preflight refused: local branch is out of sync with origin")
	ErrPreflightGHAuth      = errors.New("preflight refused: gh is not authenticated")
)

// PreflightLifecycle runs the four D-18 checks in a fixed order -- clean
// tree, branch, origin sync, gh auth -- returning on the first failure so a
// caller (pause.go/resume.go) never proceeds to commit or push onto a
// surprise. authCheck is taken as a function parameter, not a GHAPI value,
// so this file carries no dependency on lifecycle_gh.go (Task 2) -- a
// caller typically passes an execGH's AuthStatus method.
func PreflightLifecycle(ctx context.Context, g GitAPI, authCheck func(context.Context) error, requiredBranch string) error {
	clean, err := g.IsClean(ctx)
	if err != nil {
		return fmt.Errorf("preflight: check working tree: %w", err)
	}
	if !clean {
		return fmt.Errorf("%w: run `git status` and commit or stash the pending changes before retrying", ErrPreflightDirtyTree)
	}

	branch, err := g.CurrentBranch(ctx)
	if err != nil {
		return fmt.Errorf("preflight: resolve current branch: %w", err)
	}
	if branch != requiredBranch {
		return fmt.Errorf("%w: on %q, need %q; run `git checkout %s`", ErrPreflightWrongBranch, branch, requiredBranch, requiredBranch)
	}

	synced, err := g.SyncedWithOrigin(ctx, requiredBranch)
	if err != nil {
		return fmt.Errorf("preflight: check sync with origin: %w", err)
	}
	if !synced {
		return fmt.Errorf("%w: local %q does not match origin/%s; run `git fetch origin && git status` to see whether you are ahead, behind, or diverged", ErrPreflightStaleOrigin, requiredBranch, requiredBranch)
	}

	if authCheck != nil {
		if err := authCheck(ctx); err != nil {
			return fmt.Errorf("%w: %v; run `gh auth login`", ErrPreflightGHAuth, err)
		}
	}

	return nil
}

// execGit is the production GitAPI, shelling out to the git binary with Dir
// set to root, following knowledge.go's exec.CommandContext convention.
type execGit struct {
	root string
}

// NewExecGit builds a GitAPI rooted at root (see repoRoot() in
// knowledge.go for the git-rev-parse pattern callers typically use to
// resolve it).
func NewExecGit(root string) GitAPI {
	return &execGit{root: root}
}

// run executes `git <args...>` with Dir set to g.root, capturing stdout and
// stderr separately. On failure the returned error names the args and
// includes trimmed stderr, but never the process environment or any
// credential material -- git itself holds no secret this project cares
// about, but the convention is kept identical to lifecycle_gh.go's so
// neither file's error paths ever drift into leaking one.
func (g *execGit) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (g *execGit) CurrentBranch(ctx context.Context) (string, error) {
	out, err := g.run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (g *execGit) IsClean(ctx context.Context) (bool, error) {
	out, err := g.run(ctx, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// SyncedWithOrigin fetches the remote ref for branch and compares the local
// and remote commit objects directly, reporting false when the local
// branch is behind, ahead, or diverged -- the operator must not push a
// pause on top of unpushed local work, nor commit against a stale view of
// origin (T-16-06-02).
func (g *execGit) SyncedWithOrigin(ctx context.Context, branch string) (bool, error) {
	if _, err := g.run(ctx, "fetch", "origin", branch); err != nil {
		return false, err
	}
	local, err := g.run(ctx, "rev-parse", branch)
	if err != nil {
		return false, err
	}
	remote, err := g.run(ctx, "rev-parse", "origin/"+branch)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(local) == strings.TrimSpace(remote), nil
}

func (g *execGit) Diff(ctx context.Context, paths ...string) (string, error) {
	args := append([]string{"diff", "--"}, paths...)
	return g.run(ctx, args...)
}

// CommitPaths stages only paths (`git add -- <paths>`) and scopes the
// commit to only those paths (`git commit -- <paths>`) -- both steps name
// paths explicitly so a stray unrelated change elsewhere in the working
// tree can never be swept into a pause/resume commit, even if it were
// already staged (T-16-06-01).
func (g *execGit) CommitPaths(ctx context.Context, message string, paths ...string) error {
	addArgs := append([]string{"add", "--"}, paths...)
	if _, err := g.run(ctx, addArgs...); err != nil {
		return err
	}
	commitArgs := append([]string{"commit", "-m", message, "--"}, paths...)
	if _, err := g.run(ctx, commitArgs...); err != nil {
		return err
	}
	return nil
}

func (g *execGit) Push(ctx context.Context, branch string) error {
	_, err := g.run(ctx, "push", "origin", branch)
	return err
}

func (g *execGit) HeadSHA(ctx context.Context) (string, error) {
	out, err := g.run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
