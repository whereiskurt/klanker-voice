package studio

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// --------------------------------------------------------------------------
// TestSOPApply_ChangedTierUsesUpdate (P-03-tier-update-not-put)

// TestSOPApply_ChangedTierUsesUpdate proves a "tier"/"changed" entry drives
// UpdateTierLimits (an UpdateItem call), never PutTier's guarded PutItem.
func TestSOPApply_ChangedTierUsesUpdate(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	doc := SOPDoc{
		Tiers: []SOPTier{
			{TierID: "recruiting-30", SessionMaxSeconds: 1200, PeriodMaxSeconds: 7200, MaxConcurrent: 1},
		},
	}
	changeset := []ChangesetEntry{
		{Surface: "tier", Kind: "changed", Key: "recruiting-30", Field: "sessionMaxSeconds", From: int64(1800), To: int64(1200)},
	}

	result, err := Apply(context.Background(), doc, changeset, ApplyDeps{Writer: fake, Table: "kmv-auth-electro"})
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}

	if len(fake.putCalls) != 0 {
		t.Fatalf("PutItem called %d times, want 0 — a changed tier must never drive PutTier", len(fake.putCalls))
	}
	if len(fake.updateCalls) != 1 {
		t.Fatalf("UpdateItem called %d times, want 1", len(fake.updateCalls))
	}
	call := fake.updateCalls[0]
	wantExpr := "SET sessionMaxSeconds = :s, periodMaxSeconds = :p, maxConcurrent = :c"
	if call.UpdateExpression == nil || *call.UpdateExpression != wantExpr {
		t.Errorf("UpdateExpression = %v, want %q", call.UpdateExpression, wantExpr)
	}
	sAV, ok := call.ExpressionAttributeValues[":s"].(*types.AttributeValueMemberN)
	if !ok || sAV.Value != "1200" {
		t.Errorf(":s = %v, want the SOP's target value %q (not the changed field's raw To value alone)", call.ExpressionAttributeValues[":s"], "1200")
	}

	if len(result.Applied) != 1 || result.Applied[0].Key != "recruiting-30" {
		t.Errorf("ApplyResult.Applied = %+v, want the tier entry marked applied", result.Applied)
	}
	if len(result.Skipped) != 0 {
		t.Errorf("ApplyResult.Skipped = %+v, want none", result.Skipped)
	}
}

// --------------------------------------------------------------------------
// TestSOPApply_AddedVsChangedRouting

// TestSOPApply_AddedVsChangedRouting proves "added" entries drive the
// create-only writer (PutTier/PutAccessCode) and "changed" entries drive the
// in-place update writer (UpdateTierLimits/UpdateAccessCodeTier) — across
// both the tier and rule surfaces in the same Apply call.
func TestSOPApply_AddedVsChangedRouting(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	doc := SOPDoc{
		Rules: []SOPRule{
			{Code: "new-guest", TierID: "kph-tier", Who: WhoSpec{Type: "any"}},
			{Code: "existing-guest", TierID: "no-access", Who: WhoSpec{Type: "any"}},
		},
		Tiers: []SOPTier{
			{TierID: "new-tier", SessionMaxSeconds: 600, PeriodMaxSeconds: 1200, MaxConcurrent: 1},
			{TierID: "kph-tier", SessionMaxSeconds: 3600, PeriodMaxSeconds: 7200, MaxConcurrent: 2},
		},
	}
	changeset := []ChangesetEntry{
		{Surface: "tier", Kind: "added", Key: "new-tier"},
		{Surface: "rule", Kind: "added", Key: "new-guest"},
		{Surface: "rule", Kind: "changed", Key: "existing-guest", Field: "tierId", From: "kph-tier", To: "no-access"},
	}

	result, err := Apply(context.Background(), doc, changeset, ApplyDeps{Writer: fake, Table: "kmv-auth-electro"})
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}

	if len(fake.putCalls) != 2 {
		t.Fatalf("PutItem called %d times, want 2 (new-tier PutTier + new-guest PutAccessCode)", len(fake.putCalls))
	}
	if len(fake.updateCalls) != 1 {
		t.Fatalf("UpdateItem called %d times, want 1 (existing-guest's tierId edit)", len(fake.updateCalls))
	}
	call := fake.updateCalls[0]
	if call.UpdateExpression == nil || *call.UpdateExpression != "SET tierId = :t" {
		t.Errorf("UpdateExpression = %v, want the UpdateAccessCodeTier shape", call.UpdateExpression)
	}
	tAV, ok := call.ExpressionAttributeValues[":t"].(*types.AttributeValueMemberS)
	if !ok || tAV.Value != "no-access" {
		t.Errorf(":t = %v, want %q", call.ExpressionAttributeValues[":t"], "no-access")
	}

	if len(result.Applied) != 3 {
		t.Errorf("ApplyResult.Applied has %d entries, want 3", len(result.Applied))
	}
}

// --------------------------------------------------------------------------
// TestSOPApply_NeverDeletes (P-03-no-auto-delete / T-18-07)

// TestSOPApply_NeverDeletes proves a "removed" entry (a rule/tier/did
// present live but absent from the SOP) is skipped — Apply's fake writer's
// DeleteItem is never invoked, matching the grep gate over sop_apply.go's
// source (this plan's verification step) that already proves the literal
// function names DeleteAccessCode/RemovePhoneMapping don't appear.
func TestSOPApply_NeverDeletes(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	doc := SOPDoc{} // the SOP doesn't even carry the removed keys — they're live-only
	changeset := []ChangesetEntry{
		{Surface: "rule", Kind: "removed", Key: "live-only-code"},
		{Surface: "tier", Kind: "removed", Key: "live-only-tier"},
		{Surface: "did", Kind: "removed", Key: "+14165551234"},
	}

	result, err := Apply(context.Background(), doc, changeset, ApplyDeps{Writer: fake, Table: "kmv-auth-electro"})
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}

	if len(fake.deleteCalls) != 0 {
		t.Fatalf("DeleteItem called %d times, want 0 — Apply must never delete a live-only record", len(fake.deleteCalls))
	}
	if len(fake.putCalls) != 0 || len(fake.updateCalls) != 0 {
		t.Fatalf("PutItem/UpdateItem called (%d/%d), want 0 — a removed entry must not be looked up in the SOP's own lists (it isn't there) nor trigger any write", len(fake.putCalls), len(fake.updateCalls))
	}
	if len(result.Skipped) != 3 {
		t.Fatalf("ApplyResult.Skipped has %d entries, want 3", len(result.Skipped))
	}
	if len(result.Applied) != 0 {
		t.Fatalf("ApplyResult.Applied has %d entries, want 0", len(result.Applied))
	}
}

// --------------------------------------------------------------------------
// TestSOPApply_KnowledgeAddedOnly (P-03-knowledge-added-only / T-18-09)

// TestSOPApply_KnowledgeAddedOnly proves WriteManifestSource fires ONLY for
// a source present in the SOP's target Sources but absent from the
// changeset entry's live (From) Sources — never for a source already
// present live, even though it's part of the SAME "changed"/"sources"
// entry.
func TestSOPApply_KnowledgeAddedOnly(t *testing.T) {
	dir, _ := writeGoldenManifestRepo(t)

	existing := KnowledgeSource{Path: "/Users/khundeck/working/meshtk/README.md", Kind: "docs", Public: true}
	added := KnowledgeSource{Path: "apps/voice/knowledge/corpus/meshtk-extra.md", Kind: "code", Public: true}

	doc := SOPDoc{
		Knowledge: []SOPPack{
			{ID: "meshtk", SpokenName: "mesh T K, the meshtastic toolkit", Pack: "meshtk.md", Sources: []KnowledgeSource{existing, added}},
		},
	}
	changeset := []ChangesetEntry{
		{Surface: "knowledge", Kind: "changed", Key: "meshtk", Field: "sources",
			From: []KnowledgeSource{existing}, To: []KnowledgeSource{existing, added}},
	}

	result, err := Apply(context.Background(), doc, changeset, ApplyDeps{Repo: RepoFiles{Root: dir}})
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}
	if len(result.Applied) != 1 {
		t.Fatalf("ApplyResult.Applied has %d entries, want 1", len(result.Applied))
	}

	got := readManifestFile(t, dir)

	if n := strings.Count(got, "- path: "+added.Path); n != 1 {
		t.Errorf("added source %q appears %d times in manifest.yaml, want exactly 1", added.Path, n)
	}
	if n := strings.Count(got, "- path: "+existing.Path); n != 1 {
		t.Errorf("existing source %q appears %d times in manifest.yaml, want exactly 1 (must not be re-added)", existing.Path, n)
	}

	// Re-applying the SAME changeset entry a second time (simulating a
	// caller that failed to recompute the changeset) must still not
	// duplicate the already-added source, since it is now also present in
	// entry.To but Apply only writes what's absent from entry.From — this
	// guards the idempotency property at the unit level, complementing
	// Task 3's dynamodb-local DiffChangeset-driven idempotency proof.
	secondFrom := []KnowledgeSource{existing, added}
	changeset[0].From = secondFrom
	if _, err := Apply(context.Background(), doc, changeset, ApplyDeps{Repo: RepoFiles{Root: dir}}); err != nil {
		t.Fatalf("second Apply() error: %v", err)
	}
	got2 := readManifestFile(t, dir)
	if n := strings.Count(got2, "- path: "+added.Path); n != 1 {
		t.Errorf("after a second apply with an up-to-date From, added source %q appears %d times, want exactly 1 (no duplicate)", added.Path, n)
	}
}

// --------------------------------------------------------------------------
// TestSOPApply_UnknownSurfaceErrors — a defensive routing test: an
// unrecognized Surface value is an error, not a silent no-op, so a future
// diff surface added to sop_diff.go without a matching Apply case is loud,
// not silently ignored.

func TestSOPApply_UnknownSurfaceErrors(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	changeset := []ChangesetEntry{{Surface: "bogus", Kind: "added", Key: "x"}}
	if _, err := Apply(context.Background(), SOPDoc{}, changeset, ApplyDeps{Writer: fake, Table: "kmv-auth-electro"}); err == nil {
		t.Fatalf("Apply() with an unknown surface = nil, want an error")
	}
}

// --------------------------------------------------------------------------
// TestSOPApply_GateAndOrderAreNoOps — documents this plan's deliberate scope
// boundary (see sop_apply.go's package doc comment "Scope note"): gate/order
// changeset entries are accepted without error but drive no write.

func TestSOPApply_GateAndOrderAreNoOps(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	changeset := []ChangesetEntry{
		{Surface: "gate", Kind: "changed", Key: "gate", Field: "mode", From: "dtmf", To: "passphrase"},
		{Surface: "order", Kind: "changed", Key: "order", From: []string{"a"}, To: []string{"b", "a"}},
	}
	result, err := Apply(context.Background(), SOPDoc{}, changeset, ApplyDeps{Writer: fake, Table: "kmv-auth-electro"})
	if err != nil {
		t.Fatalf("Apply() error: %v", err)
	}
	if len(fake.putCalls) != 0 || len(fake.updateCalls) != 0 || len(fake.deleteCalls) != 0 {
		t.Fatalf("gate/order entries triggered a DynamoDB write — want none")
	}
	if len(result.Applied) != 2 {
		t.Fatalf("ApplyResult.Applied has %d entries, want 2 (gate/order are reported applied even though no write fires — no-op is a defined outcome, not a failure)", len(result.Applied))
	}
}
