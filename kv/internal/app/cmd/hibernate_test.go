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

// The wake path is the one place a lost or changed NAT EIP is surfaced
// (fix round 1, MEDIUM): it never fails the wake, but it must be visible in
// the report right where the operator is looking. Reuses fakeVerifyECS
// (lifecycle_verify_test.go), fakeTargetHealthAPI (lifecycle_alb_test.go),
// fakeNetworkState (lifecycle_verify_test.go), and fakePageStore
// (lifecycle_page_test.go) so this test drives the full wake branch --
// drain-free, ECS/Net/Pages/Health all wired -- rather than only the
// no-op/dry-run/preflight paths the other three tests reach.
func TestRunHibernateFlip_WakeReportsNATEIPOutcome(t *testing.T) {
	newDeps := func(t *testing.T, preEIP, postEIP string) (HibernateDeps, *bytes.Buffer) {
		t.Helper()
		root := writeSiteHCL(t, false, true) // hibernated=true, so Want:false is a real flip
		gh := &recordingGH{runIDSeq: []string{"run-1", "run-2"}}
		ecs := &fakeVerifyECS{postures: []ServicePosture{
			{Name: "voice", Desired: 1, Running: 1},
			{Name: "auth", Desired: 1, Running: 1},
		}}
		health := newFakeTargetHealthAPI()
		health.sequences["arn-voice"] = [][]TargetState{{{ID: "i-1", State: "healthy"}}}
		health.sequences["arn-auth"] = [][]TargetState{{{ID: "i-2", State: "healthy"}}}
		var out bytes.Buffer
		return HibernateDeps{
			RepoRoot:     root,
			Git:          &noopGit{clean: true, branch: "main", synced: true},
			GH:           gh,
			ECS:          ecs,
			Health:       health,
			Net:          &fakeNetworkState{state: NetworkState{NATEIPPublicIP: postEIP}},
			Pages:        newFakePageStore(),
			Cluster:      "cluster",
			Services:     []string{"voice", "auth"},
			TargetGroups: map[string]string{"voice": "arn-voice", "auth": "arn-auth"},
			AssetBucket:  "bucket",
			NATEIP:       preEIP,
			Out:          &out,
			Now:          fixedNow(),
		}, &out
	}

	t.Run("UnchangedAddressReportsValid", func(t *testing.T) {
		deps, out := newDeps(t, "1.2.3.4", "1.2.3.4")
		if err := RunHibernateFlip(context.Background(), deps, HibernateOptions{Want: false, Yes: true}); err != nil {
			t.Fatalf("RunHibernateFlip error: %v", err)
		}
		if !strings.Contains(out.String(), "NAT EIP 1.2.3.4 is unchanged") {
			t.Errorf("output %q missing the unchanged-EIP line", out.String())
		}
	})

	t.Run("ChangedAddressWarnsLoudly", func(t *testing.T) {
		deps, out := newDeps(t, "1.2.3.4", "5.6.7.8")
		if err := RunHibernateFlip(context.Background(), deps, HibernateOptions{Want: false, Yes: true}); err != nil {
			t.Fatalf("RunHibernateFlip error: %v", err)
		}
		if !strings.Contains(out.String(), "NAT EIP CHANGED: 1.2.3.4 -> 5.6.7.8") {
			t.Errorf("output %q missing the changed-EIP warning", out.String())
		}
		if !strings.Contains(out.String(), "VoIP.ms API allowlist") {
			t.Errorf("output %q missing the VoIP.ms allowlist warning", out.String())
		}
	})
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
