package studio

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// --------------------------------------------------------------------------
// Shared fakes for this file.

// deployFakeRunner is an in-memory CommandRunner: it never shells a real
// process, records every call it receives (dir/name/args, in call order —
// the ordering property TestDeploy_Order needs), and returns a real-looking
// sha for `git rev-parse HEAD`. Sharing ONE instance between DeployDeps.Runner
// and a KnowledgeRebuildTrigger.Runner lets a test observe the interleaved
// call order between the git commit step and the uv rebuild subprocess.
type deployFakeRunner struct {
	mu    sync.Mutex
	calls []fakeCall
}

func (r *deployFakeRunner) Run(_ context.Context, dir, name string, args ...string) CommandResult {
	r.mu.Lock()
	r.calls = append(r.calls, fakeCall{Dir: dir, Name: name, Args: append([]string(nil), args...)})
	r.mu.Unlock()
	if name == "git" && len(args) > 0 && args[len(args)-1] == "HEAD" {
		return CommandResult{Stdout: "deadbeef1234\n"}
	}
	return CommandResult{}
}

func (r *deployFakeRunner) callsSnapshot() []fakeCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]fakeCall(nil), r.calls...)
}

// failIfInvokedRunner implements CommandRunner and fails the test the moment
// ANY command is run — used by TestDeploy_RefusesOnInvalid /
// TestDeploy_ReportsPerSurface to prove git/the knowledge rebuild are never
// reached when they must not be.
type failIfInvokedRunner struct {
	t     *testing.T
	mu    sync.Mutex
	calls []fakeCall
}

func (f *failIfInvokedRunner) Run(_ context.Context, dir, name string, args ...string) CommandResult {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{Dir: dir, Name: name, Args: append([]string(nil), args...)})
	f.mu.Unlock()
	f.t.Fatalf("CommandRunner invoked (%s %v) when it must not have been", name, args)
	return CommandResult{}
}

// failNthPutWriter implements DynamoWriteAPI and fails ONLY the failAt-th
// (1-indexed) PutItem call, succeeding on every other Put/Update/Delete —
// used to simulate a genuine mid-deploy AWS write failure on a LATER
// changeset entry, after an earlier entry has already succeeded
// (TestDeploy_PartialApplyFailure_NoPartialConfigCommit / T-18-19 / T-19-07).
// fakeDynamoWriteAPI's putErr (used everywhere else in this package) fails
// EVERY PutItem call uniformly, which cannot express "the first entry
// succeeded, the second failed" — this fake fills that gap.
type failNthPutWriter struct {
	putCount int
	failAt   int
	err      error
}

func (f *failNthPutWriter) PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.putCount++
	if f.putCount == f.failAt {
		return nil, f.err
	}
	return &dynamodb.PutItemOutput{}, nil
}

func (f *failNthPutWriter) UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *failNthPutWriter) DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}

// fakeFailWriter implements DynamoWriteAPI and fails the test if any of its
// methods are ever invoked — TestDeploy_RefusesOnInvalid's store-side guard.
type fakeFailWriter struct{ t *testing.T }

func (f *fakeFailWriter) PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.t.Fatal("PutItem invoked on an invalid SOP — Deploy must refuse before any store call")
	return nil, nil
}

func (f *fakeFailWriter) UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.t.Fatal("UpdateItem invoked on an invalid SOP — Deploy must refuse before any store call")
	return nil, nil
}

func (f *fakeFailWriter) DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	f.t.Fatal("DeleteItem invoked on an invalid SOP — Deploy must refuse before any store call")
	return nil, nil
}

// --------------------------------------------------------------------------
// deploySOPFixtureRepo seeds a temp repo root with the four Plan-04 repo
// files (writableRepo's golden fixtures, reused verbatim from server_test.go
// — same package) a test's WriteSOP call can then add a sops/<name>.yaml
// alongside.
func deploySOPFixtureRepo(t *testing.T) RepoFiles {
	t.Helper()
	return writableRepo(t)
}

// --------------------------------------------------------------------------
// TestDeploy_Order (P-06-commit-before-rebuild / T-18-17)

// TestDeploy_Order proves the config commit's `git commit` call happens
// BEFORE the knowledge rebuild's `uv` subprocess call — a whole new
// knowledge pack (Kind "added") both drives the commit pathspec AND
// triggers the conditional rebuild, so a single Deploy call exercises both
// steps and their relative order.
func TestDeploy_Order(t *testing.T) {
	repo := deploySOPFixtureRepo(t)
	runner := &deployFakeRunner{}
	trig := &KnowledgeRebuildTrigger{Runner: runner, Root: repo.Root}

	doc := SOPDoc{
		Name: "conference-2026",
		Knowledge: []SOPPack{
			{ID: "km", SpokenName: "klanker maker", Pack: "km.md",
				Sources: []KnowledgeSource{{Path: "docs/km.md", Kind: "docs", Public: true}}},
		},
	}
	if err := WriteSOP(repo.Root, "conference-2026", doc); err != nil {
		t.Fatalf("WriteSOP() error: %v", err)
	}

	live := ConfigView{} // empty live -> the km pack is "added"

	deps := DeployDeps{
		Root:             repo.Root,
		Writer:           &fakeDynamoWriteAPI{},
		Table:            "kmv-auth-electro",
		Repo:             repo,
		Runner:           runner,
		KnowledgeRebuild: trig,
	}

	result := Deploy(context.Background(), "conference-2026", live, deps)
	if result.Error != "" {
		t.Fatalf("Deploy() result.Error = %q, want empty (failedSurface=%q)", result.Error, result.FailedSurface)
	}
	if len(result.ValidationErrors) != 0 {
		t.Fatalf("Deploy() result.ValidationErrors = %+v, want none", result.ValidationErrors)
	}
	if result.CommitSha == "" {
		t.Fatal("result.CommitSha is empty, want a commit sha (the knowledge surface changed)")
	}
	if !result.RefreshTriggered {
		t.Fatal("result.RefreshTriggered = false, want true (a knowledge pack was added)")
	}

	calls := runner.callsSnapshot()
	commitIdx, uvIdx := -1, -1
	for i, c := range calls {
		if c.Name == "git" && contains(c.Args, "commit") && commitIdx == -1 {
			commitIdx = i
		}
		if c.Name == "uv" && uvIdx == -1 {
			uvIdx = i
		}
	}
	if commitIdx == -1 {
		t.Fatalf("no `git commit` call recorded: %+v", calls)
	}
	if uvIdx == -1 {
		t.Fatalf("no `uv` (knowledge rebuild) call recorded: %+v", calls)
	}
	if commitIdx >= uvIdx {
		t.Errorf("git commit call at index %d, uv call at index %d — commit must precede the rebuild", commitIdx, uvIdx)
	}
}

// --------------------------------------------------------------------------
// TestDeploy_RefusesOnInvalid (P-06-validate-first / T-18-18)

// TestDeploy_RefusesOnInvalid gives Deploy a SOP that fails Validate (a rule
// referencing a tier absent from the SOP's own Tiers list — the orphan-tier
// check) alongside fakes that fail the test the instant they're invoked,
// proving the whole action refuses before touching DynamoDB, git, or the
// knowledge rebuild.
func TestDeploy_RefusesOnInvalid(t *testing.T) {
	repo := deploySOPFixtureRepo(t)
	failRunner := &failIfInvokedRunner{t: t}
	trig := &KnowledgeRebuildTrigger{Runner: failRunner, Root: repo.Root}

	doc := SOPDoc{
		Name: "broken-sop",
		Rules: []SOPRule{
			{Code: "orphan-rule", Who: WhoSpec{Type: "any"}, TierID: "missing-tier"},
		},
		// Tiers deliberately does NOT include "missing-tier" — orphan-tier
		// validation failure (sop_validate.go's checkOrphanTiers).
	}
	if err := WriteSOP(repo.Root, "broken-sop", doc); err != nil {
		t.Fatalf("WriteSOP() error: %v", err)
	}

	deps := DeployDeps{
		Root:             repo.Root,
		Writer:           &fakeFailWriter{t: t},
		Table:            "kmv-auth-electro",
		Repo:             repo,
		Runner:           failRunner,
		KnowledgeRebuild: trig,
	}

	result := Deploy(context.Background(), "broken-sop", ConfigView{}, deps)

	if len(result.ValidationErrors) == 0 {
		t.Fatal("result.ValidationErrors is empty, want the orphan-tier failure")
	}
	found := false
	for _, e := range result.ValidationErrors {
		if e.ID == "orphan-tier" {
			found = true
		}
	}
	if !found {
		t.Errorf("result.ValidationErrors = %+v, want an \"orphan-tier\" entry", result.ValidationErrors)
	}

	if result.CommitSha != "" {
		t.Errorf("result.CommitSha = %q, want empty — no commit must be attempted", result.CommitSha)
	}
	if result.RefreshTriggered {
		t.Error("result.RefreshTriggered = true, want false — no rebuild must be attempted")
	}
	if len(result.Applied) != 0 || len(result.Skipped) != 0 {
		t.Errorf("result.Applied/Skipped = %+v/%+v, want both empty — Apply must never run", result.Applied, result.Skipped)
	}
	if len(failRunner.calls) != 0 {
		t.Errorf("failIfInvokedRunner recorded %d call(s), want 0: %+v", len(failRunner.calls), failRunner.calls)
	}
}

// --------------------------------------------------------------------------
// TestDeploy_NeverCommitsKnowledgeSubtree (P-06-never-commits-generated-packs
// / T-18-17, extends TestKnowledgeRebuild_NeverCommits)

// TestDeploy_NeverCommitsKnowledgeSubtree runs the same "added knowledge
// pack" scenario as TestDeploy_Order and asserts every git add/commit call's
// argv only ever names the exact expected pathspec — never a bare
// "apps/voice/knowledge" directory, and never anything under
// apps/voice/knowledge/topics or /chunks (the rebuild's generated-output
// subtree).
func TestDeploy_NeverCommitsKnowledgeSubtree(t *testing.T) {
	repo := deploySOPFixtureRepo(t)
	runner := &deployFakeRunner{}
	trig := &KnowledgeRebuildTrigger{Runner: runner, Root: repo.Root}

	doc := SOPDoc{
		Name: "conference-2026",
		Knowledge: []SOPPack{
			{ID: "km", SpokenName: "klanker maker", Pack: "km.md",
				Sources: []KnowledgeSource{{Path: "docs/km.md", Kind: "docs", Public: true}}},
		},
	}
	if err := WriteSOP(repo.Root, "conference-2026", doc); err != nil {
		t.Fatalf("WriteSOP() error: %v", err)
	}

	deps := DeployDeps{
		Root:             repo.Root,
		Writer:           &fakeDynamoWriteAPI{},
		Table:            "kmv-auth-electro",
		Repo:             repo,
		Runner:           runner,
		KnowledgeRebuild: trig,
	}

	result := Deploy(context.Background(), "conference-2026", ConfigView{}, deps)
	if result.Error != "" {
		t.Fatalf("Deploy() result.Error = %q, want empty (failedSurface=%q)", result.Error, result.FailedSurface)
	}

	wantPaths := map[string]bool{
		"apps/voice/configs/studio/sops/conference-2026.yaml": true,
		manifestPath: true,
	}

	for _, c := range runner.callsSnapshot() {
		if c.Name != "git" {
			continue
		}
		mutating := contains(c.Args, "add") || contains(c.Args, "commit")
		if !mutating {
			continue
		}
		for _, a := range c.Args {
			switch {
			case a == "add", a == "commit", a == "-m", a == "--", a == "-C", a == repo.Root, strings.HasPrefix(a, "sop: deploy"):
				continue
			case a == "apps/voice/knowledge":
				t.Errorf("git %v staged/committed the bare knowledge directory — must be an exact file path", c.Args)
			case strings.HasPrefix(a, "apps/voice/knowledge/topics"), strings.HasPrefix(a, "apps/voice/knowledge/chunks"):
				t.Errorf("git %v touched the generated-pack subtree %q — D-09 gate violated", c.Args, a)
			case !wantPaths[a]:
				t.Errorf("git %v staged/committed unexpected path %q, want one of %v", c.Args, a, wantPaths)
			}
		}
	}
}

// --------------------------------------------------------------------------
// TestDeploy_ReportsPerSurface (T-18-19)

// TestDeploy_ReportsPerSurface exercises a changeset spanning multiple
// surfaces in one Deploy call — a new tier, a new rule, a new DID's
// metadata, and a live-only rule the SOP has dropped — asserting Applied/
// Skipped/CommitSha/RefreshTriggered all report accurately per surface.
func TestDeploy_ReportsPerSurface(t *testing.T) {
	repo := deploySOPFixtureRepo(t)
	runner := &deployFakeRunner{}
	// A knowledge change never occurs in this scenario — a fail-if-invoked
	// runner on the rebuild trigger proves RefreshTriggered stays false.
	failRunner := &failIfInvokedRunner{t: t}
	trig := &KnowledgeRebuildTrigger{Runner: failRunner, Root: repo.Root}

	doc := SOPDoc{
		Name: "conference-2026",
		Rules: []SOPRule{
			{Code: "new-guest", TierID: "kph-tier", Who: WhoSpec{Type: "any"}},
		},
		Tiers: []SOPTier{
			{TierID: "kph-tier", SessionMaxSeconds: 600, PeriodMaxSeconds: 3600, MaxConcurrent: 4},
		},
		Dids: []DIDMeta{
			{Did: "+16135550100", DefaultRule: "new-guest", Greeting: "hi there"},
		},
		// Order mirrors the SOP's own Rules (ToSOPDoc's normal shape) — it
		// still differs from live's order (["old-guest"]) since rule
		// membership itself differs, which is what drives the "order"
		// surface's own changed entry below.
		Order: []string{"new-guest"},
	}
	if err := WriteSOP(repo.Root, "conference-2026", doc); err != nil {
		t.Fatalf("WriteSOP() error: %v", err)
	}

	live := ConfigView{
		Rules: []Rule{
			{ID: "old-guest", Who: WhoSpec{Type: "any"}, Grant: GrantSpec{TierID: "old-tier", Minutes: 5, PeriodMin: 60, Concurrency: 1}},
		},
	}

	deps := DeployDeps{
		Root:             repo.Root,
		Writer:           &fakeDynamoWriteAPI{},
		Table:            "kmv-auth-electro",
		Repo:             repo,
		Runner:           runner,
		KnowledgeRebuild: trig,
	}

	result := Deploy(context.Background(), "conference-2026", live, deps)
	if result.Error != "" {
		t.Fatalf("Deploy() result.Error = %q, want empty (failedSurface=%q)", result.Error, result.FailedSurface)
	}
	if len(result.ValidationErrors) != 0 {
		t.Fatalf("result.ValidationErrors = %+v, want none", result.ValidationErrors)
	}

	// Applied: rule "added", tier "added", did "added", order "changed" — 4
	// entries (rule membership differing from live inherently produces an
	// "order" changed entry too — sop_diff.go's diffOrder compares the whole
	// list; Apply reports it Applied even though the write itself happens
	// via deployOrderSurface, not Apply, sop_apply.go's Scope note).
	if len(result.Applied) != 4 {
		t.Errorf("result.Applied has %d entries, want 4: %+v", len(result.Applied), result.Applied)
	}
	// Skipped: the live-only "old-guest" rule and its live-only "old-tier"
	// tier — neither is ever deleted (additive + update-only).
	if len(result.Skipped) != 2 {
		t.Errorf("result.Skipped has %d entries, want 2: %+v", len(result.Skipped), result.Skipped)
	}

	if result.CommitSha == "" {
		t.Error("result.CommitSha is empty, want a commit sha (the did/order surfaces changed repo files)")
	}
	wantCommitted := map[string]bool{
		"apps/voice/configs/studio/sops/conference-2026.yaml": true,
		studioDIDsPath: true,
		ruleOrderPath:  true,
	}
	if len(result.CommittedPaths) != len(wantCommitted) {
		t.Errorf("result.CommittedPaths = %v, want exactly %v", result.CommittedPaths, wantCommitted)
	}
	for _, p := range result.CommittedPaths {
		if !wantCommitted[p] {
			t.Errorf("result.CommittedPaths contains unexpected path %q", p)
		}
	}

	if result.RefreshTriggered {
		t.Error("result.RefreshTriggered = true, want false — no knowledge surface changed")
	}
	if result.RefreshResult != nil {
		t.Errorf("result.RefreshResult = %+v, want nil", result.RefreshResult)
	}
	if len(failRunner.calls) != 0 {
		t.Errorf("the rebuild's fail-if-invoked runner recorded %d call(s), want 0", len(failRunner.calls))
	}
}

// --------------------------------------------------------------------------
// TestDeploy_PartialApplyFailure_NoPartialConfigCommit (T-18-19 / T-19-07)

// TestDeploy_PartialApplyFailure_NoPartialConfigCommit gives Deploy a
// changeset spanning two DynamoDB surfaces — a "rule" entry (applied first,
// per sop_diff.go's fixed surface order: rule, tier, did, unlock, knowledge,
// gate, order) and a "tier" entry — with failNthPutWriter configured to let
// the FIRST PutItem call (the rule) succeed and fail the SECOND (the tier).
// This proves three things every other Deploy test in this file cannot,
// since they only ever simulate an all-succeed or an all-refuse-before-Apply
// scenario:
//   - the report names EXACTLY which surface failed (FailedSurface="tier"),
//   - the surface that already succeeded is recorded in Applied (the "rule"
//     entry), so an operator sees what already landed, and
//   - NO config-commit is attempted at all after an Apply failure — Deploy
//     returns immediately on the Apply error, before ever reaching the git
//     commit step, so a failed surface never leaves a half-committed repo
//     file (the "resumable changeset" must_have: re-running Deploy after
//     fixing the tier failure is always safe, since Apply is idempotent by
//     changeset).
func TestDeploy_PartialApplyFailure_NoPartialConfigCommit(t *testing.T) {
	repo := deploySOPFixtureRepo(t)
	// The knowledge rebuild must never be reached either — Deploy returns on
	// the Apply error long before Step 5's conditional trigger.
	failRunner := &failIfInvokedRunner{t: t}
	trig := &KnowledgeRebuildTrigger{Runner: failRunner, Root: repo.Root}

	doc := SOPDoc{
		Name: "conference-2026",
		Rules: []SOPRule{
			{Code: "new-guest", TierID: "new-tier", Who: WhoSpec{Type: "any"}},
		},
		Tiers: []SOPTier{
			{TierID: "new-tier", SessionMaxSeconds: 600, PeriodMaxSeconds: 3600, MaxConcurrent: 4},
		},
	}
	if err := WriteSOP(repo.Root, "conference-2026", doc); err != nil {
		t.Fatalf("WriteSOP() error: %v", err)
	}

	writer := &failNthPutWriter{failAt: 2, err: errors.New("operation error DynamoDB: PutItem, https response error StatusCode: 0, RequestError: send request failed")}
	// A real git runner (never actually invoked on this path — asserted
	// below) so a bug that DID reach the commit step would be caught by a
	// git failure rather than silently no-opping against a fake.
	gitRunner := &deployFakeRunner{}

	deps := DeployDeps{
		Root:             repo.Root,
		Writer:           writer,
		Table:            "kmv-auth-electro",
		Repo:             repo,
		Runner:           gitRunner,
		KnowledgeRebuild: trig,
	}

	result := Deploy(context.Background(), "conference-2026", ConfigView{}, deps)

	if len(result.ValidationErrors) != 0 {
		t.Fatalf("result.ValidationErrors = %+v, want none (the SOP is valid)", result.ValidationErrors)
	}
	if result.Error == "" {
		t.Fatal("result.Error is empty, want the simulated PutItem failure surfaced")
	}
	if result.FailedSurface != "tier" {
		t.Errorf("result.FailedSurface = %q, want \"tier\" (the second PutItem call, per the rule/tier/did/... surface order)", result.FailedSurface)
	}

	if len(result.Applied) != 1 || result.Applied[0].Surface != "rule" {
		t.Errorf("result.Applied = %+v, want exactly one entry naming the already-succeeded \"rule\" surface", result.Applied)
	}

	// No config-commit was attempted: CommitSha/CommittedPaths stay empty,
	// and `git commit`/`git add` never ran.
	if result.CommitSha != "" {
		t.Errorf("result.CommitSha = %q, want empty — a mid-Apply failure must leave no partial config commit", result.CommitSha)
	}
	if len(result.CommittedPaths) != 0 {
		t.Errorf("result.CommittedPaths = %v, want empty", result.CommittedPaths)
	}
	for _, c := range gitRunner.callsSnapshot() {
		if c.Name == "git" {
			t.Errorf("git was invoked (%v) after a mid-Apply failure — Deploy must return before the config-commit step", c.Args)
		}
	}

	if result.RefreshTriggered {
		t.Error("result.RefreshTriggered = true, want false — the knowledge rebuild step must never be reached")
	}
	if len(failRunner.calls) != 0 {
		t.Errorf("the rebuild's fail-if-invoked runner recorded %d call(s), want 0", len(failRunner.calls))
	}

	// The changeset itself is still echoed back in full — an operator can
	// re-run Deploy against the SAME (still-valid, still-resumable) SOP once
	// the underlying AWS failure clears, and Apply's idempotency (T-18-10)
	// makes that safe: the already-applied "rule" entry diffs to nothing on
	// a re-run, and only "tier" is retried.
	if len(result.Changeset) != 2 {
		t.Errorf("result.Changeset has %d entries, want 2 (rule + tier)", len(result.Changeset))
	}
}

// --------------------------------------------------------------------------
// TestDeploy_NoPushOrKnowledgeGlobReferenced — redundant, in-suite companion
// to the shell grep gate this plan's PLAN.md verification step runs, so `go
// test` alone also catches a regression (mirrors
// TestKnowledgeRebuild_NoTokenFloorGateReferenced's convention).

func TestDeploy_NoPushOrKnowledgeGlobReferenced(t *testing.T) {
	b, err := os.ReadFile("sop_deploy.go")
	if err != nil {
		t.Fatalf("read sop_deploy.go: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, banned := range []string{`"push"`, "git push", "gh pr", "knowledge/*"} {
			if strings.Contains(line, banned) {
				t.Errorf("sop_deploy.go references %q in a non-comment line: %q", banned, line)
			}
		}
	}
}
