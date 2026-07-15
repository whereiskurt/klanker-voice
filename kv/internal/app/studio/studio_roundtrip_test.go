package studio

// studio_roundtrip_test.go mirrors cmd/roundtrip_test.go's self-skipping
// dynamodb-local harness, proving two things against a real DynamoDB
// (not the recording fake in dynamo_writer_test.go, which only proves item
// SHAPE):
//
//  1. A code + tier written by the studio writers round-trips through the
//     studio read adapters (ReadCodes/ReadTiers) with matching values.
//  2. TestUpdateAccessCodeTier_PreservesPhoneMapping — the concrete Pitfall-1
//     regression guard: SetPhoneMapping then UpdateAccessCodeTier(code,
//     "no-access") must leave the phone mapping intact and only change
//     tierId. A Put-then-Put test that only checks the final tierId would
//     NOT catch a regression to PutItem-based edits (16-RESEARCH.md Pitfall
//     1 warning signs) — this test explicitly asserts the phone survives.
//
// Requires a local DynamoDB (dynamodb-local) reachable at
// KV_TEST_DYNAMODB_ENDPOINT (default http://localhost:8888) with the
// kmv-auth-electro table already created (pk/sk + gsi1/gsi2/gsi3 indexes) —
// the same table cmd/roundtrip_test.go's tests use. If the endpoint is
// unreachable, every test in this file is skipped (not failed) so `go test
// ./...` stays green in sandboxes without a running container.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-voice/kv/internal/app/electro"
)

const studioRoundTripTable = "kmv-auth-electro"

// studioTestDynamoClient loads a *dynamodb.Client pointed at
// KV_TEST_DYNAMODB_ENDPOINT (default http://localhost:8888) and skips the
// calling test if the endpoint or table is unreachable — mirrors
// cmd/roundtrip_test.go's testDynamoClient exactly (a local copy, not an
// import, per this package's no-cmd-import constraint).
func studioTestDynamoClient(t *testing.T) *dynamodb.Client {
	t.Helper()
	endpoint := os.Getenv("KV_TEST_DYNAMODB_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:8888"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("local", "local", "")),
	)
	if err != nil {
		t.Skipf("skipping round-trip test: could not load aws config: %v", err)
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	if _, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(studioRoundTripTable),
	}); err != nil {
		t.Skipf("skipping round-trip test: dynamodb-local table %q unreachable: %v", studioRoundTripTable, err)
	}
	return client
}

func studioRoundtripSuffix() string {
	return time.Now().UTC().Format("150405.000000000")
}

func deleteStudioTestCode(ctx context.Context, client *dynamodb.Client, code string) {
	_, _ = client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(studioRoundTripTable),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
			"sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
		},
	})
}

func deleteStudioTestTier(ctx context.Context, client *dynamodb.Client, tierID string) {
	_, _ = client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(studioRoundTripTable),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.TierPK(tierID)},
			"sk": &types.AttributeValueMemberS{Value: electro.TierSK()},
		},
	})
}

// TestStudioRoundTrip_WriteThenRead proves a studio-written code + tier is
// read back by studio.ReadCodes/ReadTiers with matching code/tierId/limits
// — a studio-written item round-trips through the studio read adapters, not
// just through a raw GetItem.
func TestStudioRoundTrip_WriteThenRead(t *testing.T) {
	client := studioTestDynamoClient(t)
	ctx := context.Background()

	tierID := "studio-roundtrip-tier-" + studioRoundtripSuffix()
	code := "studio-roundtrip-code-" + studioRoundtripSuffix()

	if err := PutTier(ctx, client, studioRoundTripTable, tierID, "", 3600, 7200, 1); err != nil {
		t.Fatalf("PutTier: %v", err)
	}
	t.Cleanup(func() { deleteStudioTestTier(ctx, client, tierID) })

	if err := PutAccessCode(ctx, client, studioRoundTripTable, code, tierID, "", nil, nil); err != nil {
		t.Fatalf("PutAccessCode: %v", err)
	}
	t.Cleanup(func() { deleteStudioTestCode(ctx, client, code) })

	tiers, err := ReadTiers(ctx, client, studioRoundTripTable)
	if err != nil {
		t.Fatalf("ReadTiers: %v", err)
	}
	foundTier := false
	for _, tr := range tiers {
		if tr.TierID == tierID {
			foundTier = true
			if tr.SessionMaxSeconds != 3600 || tr.PeriodMaxSeconds != 7200 || tr.MaxConcurrent != 1 {
				t.Errorf("tier %q = %+v, want session=3600 period=7200 concurrent=1", tierID, tr)
			}
			break
		}
	}
	if !foundTier {
		t.Fatalf("ReadTiers did not find studio-written tier %q", tierID)
	}

	codes, err := ReadCodes(ctx, client, studioRoundTripTable)
	if err != nil {
		t.Fatalf("ReadCodes: %v", err)
	}
	foundCode := false
	for _, c := range codes {
		if c.Code == code {
			foundCode = true
			if c.TierID != tierID {
				t.Errorf("code %q tierId = %q, want %q", code, c.TierID, tierID)
			}
			break
		}
	}
	if !foundCode {
		t.Fatalf("ReadCodes did not find studio-written code %q", code)
	}
}

// TestPutAccessCode_RejectsExistingCode_DynamoLocal proves the
// attribute_not_exists(pk) create-only guard against a real DynamoDB: a
// second PutAccessCode for the same code must fail, never silently
// overwrite (16-RESEARCH.md Q5).
func TestPutAccessCode_RejectsExistingCode_DynamoLocal(t *testing.T) {
	client := studioTestDynamoClient(t)
	ctx := context.Background()
	code := "studio-roundtrip-dup-" + studioRoundtripSuffix()

	if err := PutAccessCode(ctx, client, studioRoundTripTable, code, "roundtrip-tier", "", nil, nil); err != nil {
		t.Fatalf("first PutAccessCode: %v", err)
	}
	t.Cleanup(func() { deleteStudioTestCode(ctx, client, code) })

	if err := PutAccessCode(ctx, client, studioRoundTripTable, code, "some-other-tier", "", nil, nil); err == nil {
		t.Fatalf("second PutAccessCode for existing code %q succeeded, want a ConditionalCheckFailed error", code)
	}
}

// TestUpdateAccessCodeTier_PreservesPhoneMapping is the concrete Pitfall-1
// regression guard: PutAccessCode -> SetPhoneMapping(+E.164) ->
// UpdateAccessCodeTier(code, "no-access") -> ReadPhoneMappings must return
// the row with Phone STILL SET and TierID == "no-access". Proves studio's
// block/edit path never regresses to a PutItem-based edit that would wipe
// the phone/gsi3 side attributes.
func TestUpdateAccessCodeTier_PreservesPhoneMapping(t *testing.T) {
	client := studioTestDynamoClient(t)
	ctx := context.Background()
	code := "studio-block-preserve-" + studioRoundtripSuffix()

	if err := PutAccessCode(ctx, client, studioRoundTripTable, code, "roundtrip-tier", "", nil, nil); err != nil {
		t.Fatalf("PutAccessCode: %v", err)
	}
	t.Cleanup(func() { deleteStudioTestCode(ctx, client, code) })

	normalized, err := normalizeE164("+1 (416) 555-9999")
	if err != nil {
		t.Fatalf("normalizeE164: %v", err)
	}
	if err := SetPhoneMapping(ctx, client, studioRoundTripTable, code, normalized); err != nil {
		t.Fatalf("SetPhoneMapping: %v", err)
	}

	// The RULE-04 block action: repoint tierId at a zero-limit tier via the
	// surgical UpdateItem — must NOT touch the phone mapping just set above.
	if err := UpdateAccessCodeTier(ctx, client, studioRoundTripTable, code, "no-access"); err != nil {
		t.Fatalf("UpdateAccessCodeTier: %v", err)
	}

	mappings, err := ReadPhoneMappings(ctx, client, studioRoundTripTable)
	if err != nil {
		t.Fatalf("ReadPhoneMappings: %v", err)
	}
	var found *PhoneMappingRecord
	for i := range mappings {
		if mappings[i].Code == code {
			found = &mappings[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("ReadPhoneMappings did not find code %q after UpdateAccessCodeTier — Pitfall 1 regression: the phone mapping was dropped by an edit", code)
	}
	if found.Phone != normalized {
		t.Errorf("Phone = %q, want %q (still present after block/edit) — Pitfall 1 regression", found.Phone, normalized)
	}
	if !found.PhoneEnabled {
		t.Errorf("PhoneEnabled = false, want true — Pitfall 1 regression")
	}
	if found.TierID != "no-access" {
		t.Errorf("TierID = %q, want %q", found.TierID, "no-access")
	}
}

// --------------------------------------------------------------------------
// Plan 03: SOP-03's Apply/reconcile end-to-end proof against a real
// DynamoDB — snapshot -> apply -> re-assemble round-trips to an equivalent
// live config, and a second apply of the same (now-converged) SOP is a
// proven no-op.

// assembleSOPTestView reads the live codes/tiers/phone-mappings from
// dynamodb-local and projects them through AssembleConfig with every
// repo-file-sourced input (Manifest/Unlocks/GateMode/Root/RuleOrder/
// InboundDIDs) left at its zero value — this file's tests only exercise the
// DynamoDB-backed surfaces (rule/tier), so there is nothing repo-file-shaped
// to seed. Both sides of every DiffChangeset call in this file go through
// this same helper, so the comparison is always apples-to-apples.
func assembleSOPTestView(ctx context.Context, client *dynamodb.Client) (ConfigView, error) {
	codes, err := ReadCodes(ctx, client, studioRoundTripTable)
	if err != nil {
		return ConfigView{}, err
	}
	tiers, err := ReadTiers(ctx, client, studioRoundTripTable)
	if err != nil {
		return ConfigView{}, err
	}
	phones, err := ReadPhoneMappings(ctx, client, studioRoundTripTable)
	if err != nil {
		return ConfigView{}, err
	}
	return AssembleConfig(ctx, AssembleInput{
		Table:         studioRoundTripTable,
		Codes:         codes,
		Tiers:         tiers,
		PhoneMappings: phones,
	}), nil
}

// sopTierLimit returns the SessionMaxSeconds of tierID within doc.Tiers, or
// -1 if tierID is absent.
func sopTierLimit(doc SOPDoc, tierID string) int64 {
	for _, t := range doc.Tiers {
		if t.TierID == tierID {
			return t.SessionMaxSeconds
		}
	}
	return -1
}

// TestSOPApply_DynamoLocal proves the full SOP-03 reconcile loop: seed live
// -> AssembleConfig -> ToSOPDoc -> mutate a tier limit in the SOPDoc (the
// mockup's own example changeset: sessionMaxSeconds 1800 -> 1200) ->
// DiffChangeset -> Apply -> re-AssembleConfig -> the re-assembled view now
// equals the SOP (a diff against it is empty).
func TestSOPApply_DynamoLocal(t *testing.T) {
	client := studioTestDynamoClient(t)
	ctx := context.Background()

	tierID := "sop-apply-tier-" + studioRoundtripSuffix()
	code := "sop-apply-code-" + studioRoundtripSuffix()

	if err := PutTier(ctx, client, studioRoundTripTable, tierID, "", 1800, 7200, 1); err != nil {
		t.Fatalf("PutTier: %v", err)
	}
	t.Cleanup(func() { deleteStudioTestTier(ctx, client, tierID) })
	if err := PutAccessCode(ctx, client, studioRoundTripTable, code, tierID, "", nil, nil); err != nil {
		t.Fatalf("PutAccessCode: %v", err)
	}
	t.Cleanup(func() { deleteStudioTestCode(ctx, client, code) })

	liveView, err := assembleSOPTestView(ctx, client)
	if err != nil {
		t.Fatalf("assemble live view: %v", err)
	}

	doc := ToSOPDoc(liveView)
	doc.Name = "sop-apply-dynamolocal-test"
	doc.CreatedAt = "2026-07-15T00:00:00Z"
	for i := range doc.Tiers {
		if doc.Tiers[i].TierID == tierID {
			doc.Tiers[i].SessionMaxSeconds = 1200
		}
	}
	if got := sopTierLimit(doc, tierID); got != 1200 {
		t.Fatalf("test setup: doc's mutated tier limit = %d, want 1200", got)
	}

	changeset := DiffChangeset(doc, liveView)
	foundEdit := false
	for _, e := range changeset {
		if e.Surface == "tier" && e.Kind == "changed" && e.Key == tierID && e.Field == "sessionMaxSeconds" {
			foundEdit = true
			if e.From != int64(1800) || e.To != int64(1200) {
				t.Errorf("tier edit entry = %+v, want From=1800 To=1200", e)
			}
		}
	}
	if !foundEdit {
		t.Fatalf("DiffChangeset did not report the tier's sessionMaxSeconds edit; changeset=%+v", changeset)
	}

	if _, err := Apply(ctx, doc, changeset, ApplyDeps{Writer: client, Table: studioRoundTripTable}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	reassembled, err := assembleSOPTestView(ctx, client)
	if err != nil {
		t.Fatalf("re-assemble view: %v", err)
	}
	if got := sopTierLimit(ToSOPDoc(reassembled), tierID); got != 1200 {
		t.Fatalf("re-assembled tier %q sessionMaxSeconds = %d, want 1200 — Apply did not reconcile live to the SOP", tierID, got)
	}

	if diff := DiffChangeset(doc, reassembled); len(diff) != 0 {
		t.Fatalf("DiffChangeset(doc, reassembled) = %+v, want empty — the re-assembled live view does not yet equal the SOP", diff)
	}
}

// TestSOPApply_Idempotent proves re-applying an already-applied SOP is a
// no-op: after Apply reconciles live to doc, a second DiffChangeset(doc,
// freshly-re-assembled live) is EMPTY — so a second Apply call over that
// (now-empty) changeset issues zero writes by construction (Apply's loop
// has nothing to iterate). T-18-10's idempotent-reapply guarantee.
func TestSOPApply_Idempotent(t *testing.T) {
	client := studioTestDynamoClient(t)
	ctx := context.Background()

	tierID := "sop-idem-tier-" + studioRoundtripSuffix()
	code := "sop-idem-code-" + studioRoundtripSuffix()

	if err := PutTier(ctx, client, studioRoundTripTable, tierID, "", 900, 1800, 1); err != nil {
		t.Fatalf("PutTier: %v", err)
	}
	t.Cleanup(func() { deleteStudioTestTier(ctx, client, tierID) })
	if err := PutAccessCode(ctx, client, studioRoundTripTable, code, tierID, "", nil, nil); err != nil {
		t.Fatalf("PutAccessCode: %v", err)
	}
	t.Cleanup(func() { deleteStudioTestCode(ctx, client, code) })

	liveView, err := assembleSOPTestView(ctx, client)
	if err != nil {
		t.Fatalf("assemble live view: %v", err)
	}
	doc := ToSOPDoc(liveView)
	doc.Name = "sop-idempotent-test"
	doc.CreatedAt = "2026-07-15T00:00:00Z"
	for i := range doc.Tiers {
		if doc.Tiers[i].TierID == tierID {
			doc.Tiers[i].MaxConcurrent = 3
		}
	}

	firstChangeset := DiffChangeset(doc, liveView)
	if len(firstChangeset) == 0 {
		t.Fatalf("test setup: first changeset is empty, want at least the tier's maxConcurrent edit")
	}
	firstResult, err := Apply(ctx, doc, firstChangeset, ApplyDeps{Writer: client, Table: studioRoundTripTable})
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if len(firstResult.Applied) == 0 {
		t.Fatalf("first Apply's ApplyResult.Applied is empty, want at least one entry")
	}

	reassembled, err := assembleSOPTestView(ctx, client)
	if err != nil {
		t.Fatalf("re-assemble view: %v", err)
	}

	secondChangeset := DiffChangeset(doc, reassembled)
	if len(secondChangeset) != 0 {
		t.Fatalf("second DiffChangeset(doc, live) = %+v, want empty — a re-applied SOP must converge to a no-op diff", secondChangeset)
	}

	secondResult, err := Apply(ctx, doc, secondChangeset, ApplyDeps{Writer: client, Table: studioRoundTripTable})
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if len(secondResult.Applied) != 0 || len(secondResult.Skipped) != 0 {
		t.Fatalf("second Apply's ApplyResult = %+v, want no Applied/Skipped entries (empty changeset -> zero writes)", secondResult)
	}
}
