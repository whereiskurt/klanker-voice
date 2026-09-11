package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
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

// C1: an already-committed flag is NOT a no-op. The flag is committed at
// step 4, before any apply runs, so finding it already true proves only
// that some earlier invocation got that far -- not that the applies, the
// page swap or the verification ever happened. The runbook's documented
// recovery from a half-applied hibernate is "re-run kv hibernate", so a
// re-run must actually re-run the remaining steps; exiting 0 here would
// report success for having done nothing.
func TestRunHibernateFlip_AlreadyHibernatedRerunsRemainingSteps(t *testing.T) {
	h := newHibernateHarness(t, true) // hibernated=true already
	err := RunHibernateFlip(context.Background(), h.deps, HibernateOptions{Want: true, Yes: true})
	if err != nil {
		t.Fatalf("RunHibernateFlip error: %v", err)
	}

	// The remaining steps DID run, in the R15 order.
	if got := h.log.events; !equalEvents(got, wantHibernateOrder) {
		t.Errorf("events = %v,\nwant %v", got, wantHibernateOrder)
	}
	// ...and nothing was committed or pushed, because there was nothing to
	// flip -- the flag was already in the wanted state.
	if h.git.commits != 0 || h.git.pushes != 0 {
		t.Errorf("commits=%d pushes=%d, want 0/0 for an already-flipped flag", h.git.commits, h.git.pushes)
	}
	if !strings.Contains(h.out.String(), "re-running the remaining steps") {
		t.Errorf("output %q does not say the remaining steps are being re-run", h.out.String())
	}
}

// wantHibernateOrder is the spec §8 + R15 sequence: drain, phase 1, the
// page swap, phase 2, then verification. The swap sits BETWEEN the phases
// because phase 1 drops CloudFront's /api/* behaviour -- after it, the real
// SPA is a mic button against a stack that cannot answer.
var wantHibernateOrder = []string{
	"ecs.describe",                    // drain
	"dispatch:ecs-service,cloudfront", // phase 1
	"watch:run-1",
	"page.copy:index.html->index.spa.html", // page swap, between the phases
	"page.put:index.html",
	"dispatch:network", // phase 2
	"watch:run-2",
	"ecs.describe", // verification
	"net.state",
}

// The happy path, start to finish, asserting the ORDER of the steps rather
// than only that each happened.
func TestRunHibernateFlip_HibernateStepOrder(t *testing.T) {
	h := newHibernateHarness(t, false)
	if err := RunHibernateFlip(context.Background(), h.deps, HibernateOptions{Want: true, Yes: true}); err != nil {
		t.Fatalf("RunHibernateFlip error: %v", err)
	}
	if got := h.log.events; !equalEvents(got, wantHibernateOrder) {
		t.Fatalf("events = %v,\nwant %v", got, wantHibernateOrder)
	}
	if h.git.commits != 1 || h.git.pushes != 1 {
		t.Errorf("commits=%d pushes=%d, want 1/1", h.git.commits, h.git.pushes)
	}
	if !strings.Contains(h.out.String(), "kv hibernate: complete.") {
		t.Errorf("output %q missing the success line", h.out.String())
	}
	if h.pages.objects[SPABackupKey] != "REAL-SPA" {
		t.Errorf("%s = %q, want the saved real SPA", SPABackupKey, h.pages.objects[SPABackupKey])
	}
}

// A non-nil HibernateVerification.Err() aborts before the success line: an
// ACTIVE service still in the cluster after both phases is an orphan --
// still billing, still holding the listener rules -- and must never be
// reported as a completed hibernation.
func TestRunHibernateFlip_VerificationFailureAbortsBeforeSuccessLine(t *testing.T) {
	h := newHibernateHarness(t, false)
	h.ecs.orphanAfterApply = true

	err := RunHibernateFlip(context.Background(), h.deps, HibernateOptions{Want: true, Yes: true})
	if err == nil {
		t.Fatal("error = nil, want a verification failure")
	}
	if !strings.Contains(err.Error(), "ORPHANED") {
		t.Errorf("error = %v, want it to name the orphaned service", err)
	}
	if strings.Contains(h.out.String(), "kv hibernate: complete.") {
		t.Errorf("output %q printed the success line despite a failed verification", h.out.String())
	}
}

// --------------------------------------------------------------------------
// A harness that records every side effect into ONE ordered log, so the
// ordering between components (ECS vs CI vs S3 vs terraform) is assertable
// -- each component's own call list can only prove ordering within itself.

type hibLog struct{ events []string }

func (o *hibLog) add(e string) { o.events = append(o.events, e) }

func equalEvents(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// hibECS reports every service already at zero (so DrainToZero returns
// on its first poll) and, when orphanAfterApply is set, reports an ACTIVE
// leftover service to the post-apply verification.
type hibECS struct {
	log              *hibLog
	calls            int
	orphanAfterApply bool
}

func (e *hibECS) DescribeServices(ctx context.Context, cluster string, services []string) ([]ServicePosture, error) {
	e.log.add("ecs.describe")
	e.calls++
	if e.calls > 1 && e.orphanAfterApply {
		return []ServicePosture{{Name: "voice", Status: "ACTIVE"}}, nil
	}
	if e.calls > 1 {
		return nil, nil // verification: the services are gone
	}
	return []ServicePosture{
		{Name: "voice", Desired: 0, Running: 0},
		{Name: "auth", Desired: 0, Running: 0},
	}, nil
}
func (e *hibECS) UpdateDesiredCount(ctx context.Context, cluster, service string, desired int32) error {
	return nil
}
func (e *hibECS) ListRunningTasks(ctx context.Context, cluster, service string) ([]string, error) {
	return nil, nil
}
func (e *hibECS) GetTaskProtection(ctx context.Context, cluster string, taskARNs []string) (int, error) {
	return 0, nil
}

type hibGH struct {
	log      *hibLog
	runIDSeq []string
	next     int
}

func (g *hibGH) AuthStatus(ctx context.Context) error { return nil }
func (g *hibGH) DispatchWorkflow(ctx context.Context, workflow, ref string, inputs map[string]string) error {
	g.log.add("dispatch:" + inputs["modules"])
	return nil
}
func (g *hibGH) LatestRunID(ctx context.Context, workflow, ref string, notBefore time.Time) (string, error) {
	id := g.runIDSeq[g.next]
	g.next++
	return id, nil
}
func (g *hibGH) WatchRun(ctx context.Context, runID string, w io.Writer) error {
	g.log.add("watch:" + runID)
	return nil
}

type hibPages struct {
	log     *hibLog
	objects map[string]string
}

func (p *hibPages) CopyObject(ctx context.Context, bucket, srcKey, dstKey string) error {
	p.log.add("page.copy:" + srcKey + "->" + dstKey)
	v, ok := p.objects[srcKey]
	if !ok {
		return errors.New("no such key: " + srcKey)
	}
	p.objects[dstKey] = v
	return nil
}
func (p *hibPages) PutObject(ctx context.Context, bucket, key string, body []byte, cacheControl, contentType string) error {
	p.log.add("page.put:" + key)
	p.objects[key] = string(body)
	return nil
}
func (p *hibPages) DeleteObject(ctx context.Context, bucket, key string) error {
	p.log.add("page.delete:" + key)
	delete(p.objects, key)
	return nil
}
func (p *hibPages) HeadObject(ctx context.Context, bucket, key string) (bool, error) {
	_, ok := p.objects[key]
	return ok, nil
}

type hibNet struct {
	log   *hibLog
	state NetworkState
}

func (n *hibNet) NetworkState(ctx context.Context) (NetworkState, error) {
	n.log.add("net.state")
	return n.state, nil
}

// countingGit is noopGit plus commit/push counters, so a test can assert
// that an already-flipped flag commits nothing.
type countingGit struct {
	noopGit
	commits int
	pushes  int
}

func (g *countingGit) CommitPaths(ctx context.Context, m string, p ...string) error {
	g.commits++
	return nil
}
func (g *countingGit) Push(ctx context.Context, branch string) error {
	g.pushes++
	return nil
}

type hibernateHarness struct {
	deps  HibernateDeps
	log   *hibLog
	ecs   *hibECS
	pages *hibPages
	git   *countingGit
	out   *bytes.Buffer
}

// newHibernateHarness builds a repo root with both site.hcl and the
// committed maintenance page, and wires every seam to the shared log.
func newHibernateHarness(t *testing.T, hibernated bool) *hibernateHarness {
	t.Helper()
	root := writeSiteHCL(t, false, hibernated)
	pagePath := filepath.Join(root, MaintenancePageRelPath)
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatalf("mkdir maintenance page dir: %v", err)
	}
	if err := os.WriteFile(pagePath, []byte("MAINT"), 0o644); err != nil {
		t.Fatalf("write maintenance page: %v", err)
	}

	log := &hibLog{}
	ecs := &hibECS{log: log}
	pages := &hibPages{log: log, objects: map[string]string{IndexKey: "REAL-SPA"}}
	git := &countingGit{noopGit: noopGit{clean: true, branch: "main", synced: true}}
	var out bytes.Buffer

	return &hibernateHarness{
		log: log, ecs: ecs, pages: pages, git: git, out: &out,
		deps: HibernateDeps{
			RepoRoot:    root,
			Git:         git,
			GH:          &hibGH{log: log, runIDSeq: []string{"run-1", "run-2"}},
			ECS:         ecs,
			Net:         &hibNet{log: log, state: NetworkState{NATEIPPublicIP: "1.2.3.4"}},
			Pages:       pages,
			Cluster:     "cluster",
			Services:    []string{"voice", "auth"},
			AssetBucket: "bucket",
			NATEIP:      "1.2.3.4",
			Out:         &out,
			Now:         fixedNow(),
		},
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
			RepoRoot: root,
			Git:      &noopGit{clean: true, branch: "main", synced: true},
			GH:       gh,
			ECS:      ecs,
			Health:   health,
			Net:      &fakeNetworkState{state: NetworkState{NATEIPPublicIP: postEIP}},
			Pages:    newFakePageStore(),
			// C2: going into a wake these are the HIBERNATED posture --
			// the ecs-service unit's `services` and `target_groups`
			// outputs are comprehensions over resources that do not exist
			// yet, so both are empty. The health gate must run against
			// what ResolvePosture reads back AFTER the applies.
			Cluster:      "",
			Services:     nil,
			TargetGroups: nil,
			ResolvePosture: func(ctx context.Context) (string, []string, map[string]string, error) {
				return "cluster", []string{"voice", "auth"},
					map[string]string{"voice": "arn-voice", "auth": "arn-auth"}, nil
			},
			AssetBucket: "bucket",
			NATEIP:      preEIP,
			Out:         &out,
			Now:         fixedNow(),
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

// C2: an apply that reports success but recreates no services is not a
// successful wake. Re-resolving is what makes this observable at all --
// gating on the pre-apply (hibernated, empty) posture would pass vacuously.
func TestRunHibernateFlip_WakeFailsWhenNoServicesComeBack(t *testing.T) {
	root := writeSiteHCL(t, false, true)
	var out bytes.Buffer
	deps := HibernateDeps{
		RepoRoot: root,
		Git:      &noopGit{clean: true, branch: "main", synced: true},
		GH:       &recordingGH{runIDSeq: []string{"run-1", "run-2"}},
		ECS:      &fakeVerifyECS{},
		Health:   newFakeTargetHealthAPI(),
		Net:      &fakeNetworkState{},
		Pages:    newFakePageStore(),
		ResolvePosture: func(ctx context.Context) (string, []string, map[string]string, error) {
			return "cluster", nil, map[string]string{}, nil
		},
		AssetBucket: "bucket",
		Out:         &out,
		Now:         fixedNow(),
	}
	err := RunHibernateFlip(context.Background(), deps, HibernateOptions{Want: false, Yes: true})
	if err == nil {
		t.Fatal("error = nil, want a failure when the apply recreated no services")
	}
	if !strings.Contains(err.Error(), "did not actually recreate them") {
		t.Errorf("error = %v, want it to name the empty service list", err)
	}
	if strings.Contains(out.String(), "kv wake: complete.") {
		t.Errorf("output %q printed the success line with no services running", out.String())
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
