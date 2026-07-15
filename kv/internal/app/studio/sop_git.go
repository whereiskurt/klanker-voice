// Package studio's scoped local-git commit primitive (SOP-01's "committed
// to git" half, plus the primitive Deploy — Plan 06 — reuses for its
// config-commit step). This file NEVER runs `git push`/`gh pr` and NEVER
// stages via `git add -A`/`git add .`/`git commit -a`: every stage and
// commit uses an EXPLICIT pathspec list, with a post-add staged-set subset
// assertion that refuses (and unstages) on any unexpected path
// (18-RESEARCH.md Pitfall 4 / T-18-11). It reuses knowledge_rebuild.go's
// exported CommandRunner seam — no second subprocess seam is defined here
// (18-RESEARCH.md Pattern: "extend, don't duplicate").
package studio

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// gitCommitScoped stages EXACTLY paths (repo-root-relative) via `git add --
// <paths>`, asserts via `git diff --cached --name-only` that the staged set
// is a SUBSET of paths, refuses and unstages (`git reset`) on any
// mismatch, then commits with `git commit -m msg -- <paths>` and returns the
// new HEAD sha (via `git rev-parse HEAD`). It never invokes `push` and never
// uses `-A`/`.`/`-a` — every call is scoped to the caller-supplied pathspec,
// so an unrelated dirty file elsewhere in the working tree is never swept
// into the commit (18-RESEARCH.md Pitfall 4).
func gitCommitScoped(ctx context.Context, runner CommandRunner, root, msg string, paths []string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("gitCommitScoped: no paths given")
	}

	addArgs := append([]string{"-C", root, "add", "--"}, paths...)
	if res := runner.Run(ctx, root, "git", addArgs...); res.Err != nil {
		return "", fmt.Errorf("git add: %s", res.Stderr)
	}

	staged := runner.Run(ctx, root, "git", "-C", root, "diff", "--cached", "--name-only")
	if staged.Err != nil {
		runner.Run(ctx, root, "git", "-C", root, "reset")
		return "", fmt.Errorf("git diff --cached: %s", staged.Stderr)
	}
	if !stagedSetMatches(staged.Stdout, paths) {
		runner.Run(ctx, root, "git", "-C", root, "reset")
		return "", fmt.Errorf("staged paths do not match the expected SOP pathspec — refusing to commit")
	}

	commitArgs := append([]string{"-C", root, "commit", "-m", msg, "--"}, paths...)
	if res := runner.Run(ctx, root, "git", commitArgs...); res.Err != nil {
		return "", fmt.Errorf("git commit: %s", res.Stderr)
	}

	rev := runner.Run(ctx, root, "git", "-C", root, "rev-parse", "HEAD")
	if rev.Err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %s", rev.Stderr)
	}
	return strings.TrimSpace(rev.Stdout), nil
}

// stagedSetMatches reports whether every path in `git diff --cached
// --name-only`'s stdout is a member of expected — i.e. the staged set is a
// SUBSET of expected (never a superset). Empty stdout (nothing staged — e.g.
// the file's content already matched HEAD) is trivially a subset.
func stagedSetMatches(stdout string, expected []string) bool {
	allowed := make(map[string]bool, len(expected))
	for _, p := range expected {
		allowed[filepath.ToSlash(p)] = true
	}
	for line := range strings.SplitSeq(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !allowed[filepath.ToSlash(line)] {
			return false
		}
	}
	return true
}

// SaveSOP snapshots the current live ConfigView (ToSOPDoc), writes it to
// sops/<name>.yaml (WriteSOP — Plan 01), then commits ONLY that one file
// with a strictly-scoped pathspec — never sweeping in any other
// working-tree change, even a dirty file elsewhere in the repo (SOP-01's
// "committed to git" half). CreatedAt is stamped once, here, at save time
// (sop.go's SOPDoc doc comment). Returns the new commit's sha; never runs a
// remote push — the caller reports "committed — ready to push/PR" (D
// constraint 7).
func SaveSOP(ctx context.Context, root, name string, live ConfigView, runner CommandRunner) (string, error) {
	doc := ToSOPDoc(live)
	doc.Name = name
	doc.CreatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := WriteSOP(root, name, doc); err != nil {
		return "", err
	}

	sopPath := filepath.ToSlash(filepath.Join(sopsDir, name+".yaml"))
	msg := fmt.Sprintf("sop: save %s", name)
	return gitCommitScoped(ctx, runner, root, msg, []string{sopPath})
}
