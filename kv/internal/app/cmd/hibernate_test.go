package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSiteHCL builds a throwaway repo root holding a site.hcl with both
// flags, so the flip path can be exercised end to end against real files.
func writeSiteHCL(t *testing.T, paused, hibernated bool) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, filepath.Dir(SiteHCLRelPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	src := "locals {\n  paused = " + boolLit(paused) + "\n  hibernated = " + boolLit(hibernated) + "\n}\n"
	if err := os.WriteFile(filepath.Join(root, SiteHCLRelPath), []byte(src), 0o644); err != nil {
		t.Fatalf("write site.hcl: %v", err)
	}
	return root
}

func boolLit(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// Already hibernated is a clean, idempotent no-op that touches nothing.
func TestRunHibernateFlip_AlreadyHibernatedIsNoOp(t *testing.T) {
	root := writeSiteHCL(t, false, true)
	gh := &recordingGH{runIDSeq: []string{"run-1", "run-2"}}
	var out bytes.Buffer

	deps := HibernateDeps{
		RepoRoot: root,
		Git:      &noopGit{clean: true, branch: "main", synced: true},
		GH:       gh,
		Out:      &out,
		Now:      fixedNow(),
	}
	err := RunHibernateFlip(context.Background(), deps, HibernateOptions{Want: true, Yes: true})
	if err != nil {
		t.Fatalf("RunHibernateFlip error: %v", err)
	}
	if len(gh.calls) != 0 {
		t.Errorf("calls = %v, want none for an idempotent no-op", gh.calls)
	}
	if !strings.Contains(out.String(), "no-op") {
		t.Errorf("output %q does not report a no-op", out.String())
	}
}

// D-12: --dry-run issues no dispatch at all.
func TestRunHibernateFlip_DryRunDispatchesNothing(t *testing.T) {
	root := writeSiteHCL(t, false, false)
	gh := &recordingGH{runIDSeq: []string{"run-1", "run-2"}}
	var out bytes.Buffer

	deps := HibernateDeps{
		RepoRoot: root,
		Git:      &noopGit{clean: true, branch: "main", synced: true},
		GH:       gh,
		Out:      &out,
		Now:      fixedNow(),
	}
	err := RunHibernateFlip(context.Background(), deps, HibernateOptions{Want: true, Yes: true, DryRun: true})
	if err != nil {
		t.Fatalf("RunHibernateFlip error: %v", err)
	}
	if len(gh.calls) != 0 {
		t.Errorf("calls = %v, want none under --dry-run", gh.calls)
	}
	// The flag file must be left exactly as found.
	got, err := ReadHibernatedFlagFile(root)
	if err != nil {
		t.Fatalf("ReadHibernatedFlagFile: %v", err)
	}
	if got != false {
		t.Error("--dry-run modified the hibernated flag")
	}
}

// A dirty tree is refused before anything is written.
func TestRunHibernateFlip_RefusesDirtyTree(t *testing.T) {
	root := writeSiteHCL(t, false, false)
	gh := &recordingGH{}
	deps := HibernateDeps{
		RepoRoot: root,
		Git:      &noopGit{clean: false, branch: "main", synced: true},
		GH:       gh,
		Out:      &bytes.Buffer{},
		Now:      fixedNow(),
	}
	err := RunHibernateFlip(context.Background(), deps, HibernateOptions{Want: true, Yes: true})
	if err == nil {
		t.Fatal("error = nil, want a dirty-tree refusal")
	}
	if len(gh.calls) != 0 {
		t.Errorf("calls = %v, want none after a preflight refusal", gh.calls)
	}
}

// The hibernate phase order must be the removal order, not terragrunt's.
func TestHibernateOptions_UsesHibernatePhasesForWantTrue(t *testing.T) {
	if phasesFor(true)[0].Modules != "ecs-service,cloudfront" {
		t.Error("hibernate must remove services and the CloudFront origin first")
	}
	if phasesFor(false)[0].Modules != "network" {
		t.Error("wake must create the network first")
	}
}

// noopGit is a minimal GitAPI whose preflight answers are configurable.
type noopGit struct {
	clean  bool
	branch string
	synced bool
}

func (g *noopGit) CurrentBranch(ctx context.Context) (string, error) { return g.branch, nil }
func (g *noopGit) IsClean(ctx context.Context) (bool, error)         { return g.clean, nil }
func (g *noopGit) SyncedWithOrigin(ctx context.Context, b string) (bool, error) {
	return g.synced, nil
}
func (g *noopGit) Diff(ctx context.Context, paths ...string) (string, error) { return "diff", nil }
func (g *noopGit) CommitPaths(ctx context.Context, m string, p ...string) error {
	return nil
}
func (g *noopGit) Push(ctx context.Context, branch string) error { return nil }
func (g *noopGit) HeadSHA(ctx context.Context) (string, error)   { return "sha", nil }

var _ = errors.New
var _ = time.Now
