package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// numAV builds a DynamoDB number AttributeValue from an int64 — restore_test
// fixtures use epoch seconds/milliseconds throughout.
func numAV(n int64) types.AttributeValue {
	return &types.AttributeValueMemberN{Value: strconv.FormatInt(n, 10)}
}

var restoreTestNow = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

func TestIsEphemeralItem(t *testing.T) {
	t.Run("HeartbeatLeaseIsConcurrencyLease", func(t *testing.T) {
		item := map[string]types.AttributeValue{
			"pk": strAV("session#user-123"),
			"sk": strAV("heartbeat#sess-abc"),
		}
		ephemeral, reason := IsEphemeralItem("voice-usage", item, restoreTestNow)
		if !ephemeral {
			t.Fatal("IsEphemeralItem(heartbeat lease) = false, want true")
		}
		if reason != EphemeralReasonConcurrencyLease {
			t.Errorf("reason = %q, want %q", reason, EphemeralReasonConcurrencyLease)
		}
	})

	t.Run("VoiceUsageNonEphemeralItemsSurvive", func(t *testing.T) {
		daily := map[string]types.AttributeValue{
			"pk": strAV("user#user-123"),
			"sk": strAV("day#2026-08-20"),
		}
		rollup := map[string]types.AttributeValue{
			"pk": strAV("rollup#"),
			"sk": strAV("day#2026-08-20"),
		}
		control := map[string]types.AttributeValue{
			"pk": strAV("control#"),
			"sk": strAV("killswitch#"),
		}
		for name, item := range map[string]map[string]types.AttributeValue{
			"UsageDaily":   daily,
			"UsageRollup":  rollup,
			"UsageControl": control,
		} {
			if ephemeral, reason := IsEphemeralItem("voice-usage", item, restoreTestNow); ephemeral {
				t.Errorf("IsEphemeralItem(%s) = true (reason %q), want false", name, reason)
			}
		}
	})

	t.Run("ElectroOIDCSessionInteractionAndLoginIntentAreEphemeral", func(t *testing.T) {
		session := map[string]types.AttributeValue{
			"pk": strAV(oidcModelPKSession),
			"sk": strAV("$oidcmodel_1#id_abc123"),
		}
		interaction := map[string]types.AttributeValue{
			"pk": strAV(oidcModelPKInteraction),
			"sk": strAV("$oidcmodel_1#id_xyz789"),
		}
		loginIntent := map[string]types.AttributeValue{
			"pk":        strAV("loginintent#foo@bar.com"),
			"sk":        strAV("loginintent#"),
			"expiresAt": numAV(restoreTestNow.Add(10 * time.Minute).UnixMilli()),
			"ttl":       numAV(restoreTestNow.Add(10 * time.Minute).Unix()),
		}
		for name, item := range map[string]map[string]types.AttributeValue{
			"OIDCSession":     session,
			"OIDCInteraction": interaction,
			"LoginIntent":     loginIntent,
		} {
			ephemeral, _ := IsEphemeralItem("auth-electro", item, restoreTestNow)
			if !ephemeral {
				t.Errorf("IsEphemeralItem(%s) = false, want true", name)
			}
		}

		accessCode := map[string]types.AttributeValue{
			"pk":        strAV("code#greenhouse1234"),
			"sk":        strAV("code#"),
			"gsi1pk":    strAV("accesscodes#"),
			"gsi1sk":    strAV("code#greenhouse1234"),
			"expiresAt": numAV(restoreTestNow.Add(-24 * time.Hour).UnixMilli()), // intentionally expired, must still survive
		}
		tier := map[string]types.AttributeValue{
			"pk": strAV("tier#standard"),
			"sk": strAV("tier#"),
		}
		authProfile := map[string]types.AttributeValue{
			"pk": strAV("authprofile#user-123"),
			"sk": strAV("authprofile#"),
		}
		codeRedemption := map[string]types.AttributeValue{
			"pk": strAV("coderedemption#greenhouse1234#user-123"),
			"sk": strAV("coderedemption#"),
		}
		for name, item := range map[string]map[string]types.AttributeValue{
			"AccessCode":     accessCode,
			"Tier":           tier,
			"AuthProfile":    authProfile,
			"CodeRedemption": codeRedemption,
		} {
			if ephemeral, reason := IsEphemeralItem("auth-electro", item, restoreTestNow); ephemeral {
				t.Errorf("IsEphemeralItem(%s) = true (reason %q), want false", name, reason)
			}
		}
	})

	t.Run("AuthjsSessionAndVerificationTokenAreEphemeral", func(t *testing.T) {
		session := map[string]types.AttributeValue{
			"pk": strAV("USER#user-123"),
			"sk": strAV("SESSION#tok-abc"),
		}
		vt := map[string]types.AttributeValue{
			"pk": strAV("VT#user@example.com"),
			"sk": strAV("VT#token-xyz"),
		}
		for name, item := range map[string]map[string]types.AttributeValue{
			"Session":           session,
			"VerificationToken": vt,
		} {
			ephemeral, _ := IsEphemeralItem("auth-authjs", item, restoreTestNow)
			if !ephemeral {
				t.Errorf("IsEphemeralItem(%s) = false, want true", name)
			}
		}

		user := map[string]types.AttributeValue{
			"pk": strAV("USER#user-123"),
			"sk": strAV("USER#user-123"),
		}
		account := map[string]types.AttributeValue{
			"pk": strAV("USER#user-123"),
			"sk": strAV("ACCOUNT#nodemailer#user@example.com"),
		}
		for name, item := range map[string]map[string]types.AttributeValue{
			"User":    user,
			"Account": account,
		} {
			if ephemeral, reason := IsEphemeralItem("auth-authjs", item, restoreTestNow); ephemeral {
				t.Errorf("IsEphemeralItem(%s) = true (reason %q), want false", name, reason)
			}
		}
	})

	t.Run("ExpiredTTLIsEphemeralRegardlessOfKeyShape_FutureTTLIsNot", func(t *testing.T) {
		pastVoiceUsage := map[string]types.AttributeValue{
			"pk":        strAV("user#some-other-shape"), // deliberately not a heartbeat key shape
			"sk":        strAV("day#2026-08-19"),
			"expiresAt": numAV(restoreTestNow.Add(-1 * time.Hour).Unix()),
		}
		ephemeral, reason := IsEphemeralItem("voice-usage", pastVoiceUsage, restoreTestNow)
		if !ephemeral {
			t.Fatal("IsEphemeralItem(past expiresAt, voice-usage) = false, want true")
		}
		if reason != EphemeralReasonExpiredTTL {
			t.Errorf("reason = %q, want %q", reason, EphemeralReasonExpiredTTL)
		}

		futureVoiceUsage := map[string]types.AttributeValue{
			"pk":        strAV("user#some-other-shape"),
			"sk":        strAV("day#2026-08-19"),
			"expiresAt": numAV(restoreTestNow.Add(1 * time.Hour).Unix()),
		}
		if ephemeral, reason := IsEphemeralItem("voice-usage", futureVoiceUsage, restoreTestNow); ephemeral {
			t.Errorf("IsEphemeralItem(future expiresAt, voice-usage) = true (reason %q), want false", reason)
		}
	})

	t.Run("ClassificationIsConsultedRegardlessOfAnyExternalSkipDecision", func(t *testing.T) {
		// IsEphemeralItem itself takes no "skip" option — classification is
		// pure and total, independent of what a caller later decides to do
		// with the result. Simulate both a "skip" and a "keep" loop the way
		// RestoreTable (Task 2) will, and confirm the classifier is called
		// (and counted) identically in both, while only the "skip" loop
		// actually drops the item.
		items := []map[string]types.AttributeValue{
			{"pk": strAV("session#user-1"), "sk": strAV("heartbeat#sess-1")}, // ephemeral
			{"pk": strAV("user#user-1"), "sk": strAV("day#2026-08-20")},      // not ephemeral
		}

		countAndFilter := func(skip bool) (kept int, counts map[EphemeralReason]int64) {
			counts = map[EphemeralReason]int64{}
			for _, item := range items {
				ephemeral, reason := IsEphemeralItem("voice-usage", item, restoreTestNow)
				if ephemeral {
					counts[reason]++
					if skip {
						continue
					}
				}
				kept++
			}
			return kept, counts
		}

		keptSkipped, countsSkipped := countAndFilter(true)
		keptKept, countsKept := countAndFilter(false)

		if keptSkipped != 1 {
			t.Errorf("kept with skip=true = %d, want 1 (ephemeral item dropped)", keptSkipped)
		}
		if keptKept != 2 {
			t.Errorf("kept with skip=false = %d, want 2 (nothing dropped)", keptKept)
		}
		if countsSkipped[EphemeralReasonConcurrencyLease] != 1 || countsKept[EphemeralReasonConcurrencyLease] != 1 {
			t.Errorf("skip counts diverged between skip=true/false: %v vs %v — classifier must be consulted identically in both modes", countsSkipped, countsKept)
		}
	})
}

func TestOpenBackupArchive(t *testing.T) {
	t.Run("VerifiedArchiveOpensCleanly", func(t *testing.T) {
		path := buildFixtureArchive(t)
		zr, manifest, err := OpenBackupArchive(path)
		if err != nil {
			t.Fatalf("OpenBackupArchive() error = %v, want nil", err)
		}
		defer zr.Close()
		if manifest.Version != BackupManifestVersion {
			t.Errorf("manifest.Version = %d, want %d", manifest.Version, BackupManifestVersion)
		}
	})

	t.Run("CorruptedArchiveRefusesToOpen", func(t *testing.T) {
		path := buildFixtureArchive(t)
		corruptByte(t, path, []byte("row1"))
		zr, _, err := OpenBackupArchive(path)
		if err == nil {
			t.Fatal("OpenBackupArchive(corrupted archive) error = nil, want non-nil")
		}
		if zr != nil {
			t.Error("OpenBackupArchive(corrupted archive) returned a non-nil *zip.ReadCloser, want nil")
		}
	})
}

func TestReadTableItems(t *testing.T) {
	path := buildFixtureArchive(t)
	zr, _, err := OpenBackupArchive(path)
	if err != nil {
		t.Fatalf("OpenBackupArchive() error = %v, want nil", err)
	}
	defer zr.Close()

	// testBackupDeps' shared fakeDynamoScanAPI serves its one page (2 items)
	// to whichever logical table is scanned FIRST in WriteBackupArchive's
	// sorted iteration order ("auth-authjs" < "auth-electro" < "voice-usage");
	// every table scanned after that sees an exhausted fake (0 items). Read
	// the table that actually got rows.
	items, err := ReadTableItems(zr, "dynamodb/auth-authjs.jsonl")
	if err != nil {
		t.Fatalf("ReadTableItems() error = %v, want nil", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
}

// --------------------------------------------------------------------------
// fakeDynamoBatchWriteAPI — hand-rolled, in-memory dynamoBatchWriteAPI fake.
// store is stateful across calls (keyed by "pk|sk") so an idempotence
// assertion (re-running a restore leaves one copy, not two) is meaningful.
// behaviors, if set, lets a specific call index override the default
// succeed-everything response (used to simulate UnprocessedItems and
// throttling-then-success).

func itemKey(item map[string]types.AttributeValue) string {
	return stringAttr(item, "pk") + "|" + stringAttr(item, "sk")
}

type fakeDynamoBatchWriteAPI struct {
	t         *testing.T // non-nil -> any call fails the test (dry-run "must not be called" fakes)
	store     map[string]map[string]types.AttributeValue
	callSizes []int
	behaviors []func(reqs []types.WriteRequest) (*dynamodb.BatchWriteItemOutput, error)
}

func (f *fakeDynamoBatchWriteAPI) BatchWriteItem(_ context.Context, in *dynamodb.BatchWriteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	if f.t != nil {
		f.t.Fatal("BatchWriteItem called, want zero calls (dry-run must not write)")
	}
	var reqs []types.WriteRequest
	for _, r := range in.RequestItems {
		reqs = r
	}
	idx := len(f.callSizes)
	f.callSizes = append(f.callSizes, len(reqs))

	if idx < len(f.behaviors) && f.behaviors[idx] != nil {
		return f.behaviors[idx](reqs)
	}
	if f.store == nil {
		f.store = map[string]map[string]types.AttributeValue{}
	}
	for _, r := range reqs {
		if r.PutRequest != nil {
			f.store[itemKey(r.PutRequest.Item)] = r.PutRequest.Item
		}
	}
	return &dynamodb.BatchWriteItemOutput{UnprocessedItems: map[string][]types.WriteRequest{}}, nil
}

func makeItems(n int) []map[string]types.AttributeValue {
	items := make([]map[string]types.AttributeValue, n)
	for i := range items {
		items[i] = map[string]types.AttributeValue{
			"pk": strAV(fmt.Sprintf("item#%d", i)),
			"sk": strAV("row#"),
		}
	}
	return items
}

func TestRestoreTable(t *testing.T) {
	t.Run("SixtyItemsProduceThreeBatchesOf25_25_10", func(t *testing.T) {
		api := &fakeDynamoBatchWriteAPI{}
		items := makeItems(60)
		written, _, err := RestoreTable(context.Background(), api, "kmv-test", items, RestoreOptions{}, func() time.Time { return restoreTestNow }, "no-such-table")
		if err != nil {
			t.Fatalf("RestoreTable() error = %v, want nil", err)
		}
		if written != 60 {
			t.Errorf("written = %d, want 60", written)
		}
		wantSizes := []int{25, 25, 10}
		if len(api.callSizes) != len(wantSizes) {
			t.Fatalf("call count = %d, want %d (sizes %v)", len(api.callSizes), len(wantSizes), api.callSizes)
		}
		for i, want := range wantSizes {
			if api.callSizes[i] != want {
				t.Errorf("call[%d] size = %d, want %d", i, api.callSizes[i], want)
			}
		}
	})

	t.Run("UnprocessedItemsAreRetriedNotDuplicated", func(t *testing.T) {
		items := makeItems(3)
		api := &fakeDynamoBatchWriteAPI{store: map[string]map[string]types.AttributeValue{}}
		api.behaviors = []func([]types.WriteRequest) (*dynamodb.BatchWriteItemOutput, error){
			// First attempt: store the first two requests (as a real
			// partial-batch write would), report the last as unprocessed.
			func(reqs []types.WriteRequest) (*dynamodb.BatchWriteItemOutput, error) {
				for _, r := range reqs[:len(reqs)-1] {
					if r.PutRequest != nil {
						api.store[itemKey(r.PutRequest.Item)] = r.PutRequest.Item
					}
				}
				return &dynamodb.BatchWriteItemOutput{
					UnprocessedItems: map[string][]types.WriteRequest{"kmv-test": reqs[len(reqs)-1:]},
				}, nil
			},
			// Retry: nil -> default succeed-everything handler, invoked
			// with only the one previously-unprocessed item.
			nil,
		}
		written, _, err := RestoreTable(context.Background(), api, "kmv-test", items, RestoreOptions{}, func() time.Time { return restoreTestNow }, "no-such-table")
		if err != nil {
			t.Fatalf("RestoreTable() error = %v, want nil", err)
		}
		if written != 3 {
			t.Errorf("written = %d, want 3", written)
		}
		if len(api.store) != 3 {
			t.Errorf("len(store) = %d, want 3 (no duplicates)", len(api.store))
		}
		if len(api.callSizes) != 2 {
			t.Fatalf("call count = %d, want 2 (initial + one retry)", len(api.callSizes))
		}
	})

	t.Run("ThrottlingRetriesTwiceThenSucceeds", func(t *testing.T) {
		items := makeItems(2)
		var calls int
		api := &fakeDynamoBatchWriteAPI{
			behaviors: []func([]types.WriteRequest) (*dynamodb.BatchWriteItemOutput, error){
				func(reqs []types.WriteRequest) (*dynamodb.BatchWriteItemOutput, error) {
					calls++
					return nil, &types.ThrottlingException{Message: aws.String("slow down")}
				},
				func(reqs []types.WriteRequest) (*dynamodb.BatchWriteItemOutput, error) {
					calls++
					return nil, &types.ThrottlingException{Message: aws.String("slow down")}
				},
				nil, // third attempt: default success-all behavior
			},
		}
		written, _, err := RestoreTable(context.Background(), api, "kmv-test", items, RestoreOptions{}, func() time.Time { return restoreTestNow }, "no-such-table")
		if err != nil {
			t.Fatalf("RestoreTable() error = %v, want nil", err)
		}
		if written != 2 {
			t.Errorf("written = %d, want 2", written)
		}
		if calls != 2 {
			t.Errorf("throttling behavior invocations = %d, want 2", calls)
		}
		if len(api.callSizes) != 3 {
			t.Errorf("total attempts = %d, want 3 (2 throttled + 1 success)", len(api.callSizes))
		}
		if len(api.callSizes) > restoreMaxAttempts {
			t.Errorf("total attempts = %d, exceeds restoreMaxAttempts %d", len(api.callSizes), restoreMaxAttempts)
		}
	})

	t.Run("RerunningConverges_OneCopyNotTwo", func(t *testing.T) {
		api := &fakeDynamoBatchWriteAPI{}
		items := makeItems(5)
		now := func() time.Time { return restoreTestNow }
		if _, _, err := RestoreTable(context.Background(), api, "kmv-test", items, RestoreOptions{}, now, "no-such-table"); err != nil {
			t.Fatalf("first RestoreTable() error = %v, want nil", err)
		}
		if _, _, err := RestoreTable(context.Background(), api, "kmv-test", items, RestoreOptions{}, now, "no-such-table"); err != nil {
			t.Fatalf("second RestoreTable() error = %v, want nil", err)
		}
		if len(api.store) != 5 {
			t.Errorf("len(store) after two runs = %d, want 5 (one copy of each item)", len(api.store))
		}
	})

	t.Run("DryRunNeverCallsBatchWriteItem", func(t *testing.T) {
		api := &fakeDynamoBatchWriteAPI{t: t}
		items := makeItems(10)
		written, skipped, err := RestoreTable(context.Background(), api, "kmv-test", items, RestoreOptions{DryRun: true}, func() time.Time { return restoreTestNow }, "no-such-table")
		if err != nil {
			t.Fatalf("RestoreTable() error = %v, want nil", err)
		}
		if written != 10 {
			t.Errorf("written = %d, want 10 (dry-run still counts)", written)
		}
		if skipped == nil {
			t.Error("skipped map is nil, want non-nil (even if empty)")
		}
	})
}

func TestRestoreLedger(t *testing.T) {
	archivePath := buildFixtureArchive(t)
	zr, _, err := OpenBackupArchive(archivePath)
	if err != nil {
		t.Fatalf("OpenBackupArchive() error = %v, want nil", err)
	}
	defer zr.Close()

	t.Run("RestoresEveryLedgerObject", func(t *testing.T) {
		api := &fakePutObjectAPI{}
		objects, bytesTotal, err := RestoreLedger(context.Background(), api, zr, restoreLedgerPrefix, "kmv-ledger-restored", RestoreOptions{})
		if err != nil {
			t.Fatalf("RestoreLedger() error = %v, want nil", err)
		}
		if objects != 2 {
			t.Errorf("objects = %d, want 2", objects)
		}
		if bytesTotal == 0 {
			t.Error("bytesTotal = 0, want > 0")
		}
		if len(api.puts) != 2 {
			t.Fatalf("PutObject call count = %d, want 2", len(api.puts))
		}
	})

	t.Run("DryRunNeverCallsPutObject", func(t *testing.T) {
		api := &fakePutObjectAPI{t: t}
		objects, _, err := RestoreLedger(context.Background(), api, zr, restoreLedgerPrefix, "kmv-ledger-restored", RestoreOptions{DryRun: true})
		if err != nil {
			t.Fatalf("RestoreLedger() error = %v, want nil", err)
		}
		if objects != 2 {
			t.Errorf("objects = %d, want 2 (dry-run still counts)", objects)
		}
	})
}

// fakePutObjectAPI — hand-rolled, in-memory s3PutAPI fake.
type fakePutObjectAPI struct {
	t    *testing.T
	puts map[string][]byte
}

func (f *fakePutObjectAPI) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if f.t != nil {
		f.t.Fatal("PutObject called, want zero calls (dry-run must not write)")
	}
	if f.puts == nil {
		f.puts = map[string][]byte{}
	}
	body, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.puts[aws.ToString(in.Key)] = body
	return &s3.PutObjectOutput{}, nil
}

func TestRunRestore(t *testing.T) {
	t.Run("MissingTableDestinationRefusesBeforeAnyWrite", func(t *testing.T) {
		archivePath := buildFixtureArchive(t)
		dynamoFake := &fakeDynamoBatchWriteAPI{t: t}
		s3Fake := &fakePutObjectAPI{t: t}
		deps := RestoreDeps{
			Dynamo: dynamoFake,
			S3:     s3Fake,
			Targets: LiveTargets{
				TableNames: map[string]string{
					"auth-electro": "kmv-auth-electro",
					"auth-authjs":  "kmv-auth-authjs",
					// voice-usage deliberately absent
				},
				LedgerBucket: "kmv-ledger-restored",
			},
			Now: func() time.Time { return restoreTestNow },
		}
		_, err := RunRestore(context.Background(), deps, archivePath, RestoreOptions{})
		if err == nil {
			t.Fatal("RunRestore() error = nil, want non-nil (missing voice-usage destination)")
		}
		if !strings.Contains(err.Error(), "voice-usage") {
			t.Errorf("error %q does not mention voice-usage", err.Error())
		}
		if !strings.Contains(err.Error(), "terragrunt apply") {
			t.Errorf("error %q does not mention terragrunt apply", err.Error())
		}
	})
}

// --------------------------------------------------------------------------
// perTableDynamoScanAPI — a dynamoScanAPI fake that returns different fixed
// items per requested table name (unlike fakeDynamoScanAPI's single shared
// queue), so a round-trip test can seed a distinct durable+ephemeral row mix
// per logical table.

type perTableDynamoScanAPI struct {
	itemsByTable map[string][]map[string]types.AttributeValue
}

func (f *perTableDynamoScanAPI) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	return &dynamodb.ScanOutput{Items: f.itemsByTable[aws.ToString(in.TableName)]}, nil
}

// buildRoundTripFixture writes a backup archive seeded with a deliberate mix
// of durable and ephemeral rows across all three tables, plus one ledger
// object, and returns the archive path alongside every fixture item so
// assertions can key into a restore fake's store by itemKey.
func buildRoundTripFixture(t *testing.T) (archivePath string, heartbeat, daily, oidcSession, accessCode, authjsSession, authjsUser map[string]types.AttributeValue) {
	t.Helper()
	now := restoreTestNow

	heartbeat = map[string]types.AttributeValue{"pk": strAV("session#user-1"), "sk": strAV("heartbeat#sess-1")}
	daily = map[string]types.AttributeValue{"pk": strAV("user#user-1"), "sk": strAV("day#2026-08-20")}

	oidcSession = map[string]types.AttributeValue{"pk": strAV(oidcModelPKSession), "sk": strAV("$oidcmodel_1#id_abc123")}
	accessCode = map[string]types.AttributeValue{"pk": strAV("code#greenhouse1234"), "sk": strAV("code#")}

	authjsSession = map[string]types.AttributeValue{"pk": strAV("USER#user-1"), "sk": strAV("SESSION#tok-1")}
	authjsUser = map[string]types.AttributeValue{"pk": strAV("USER#user-1"), "sk": strAV("USER#user-1")}

	dyn := &perTableDynamoScanAPI{itemsByTable: map[string][]map[string]types.AttributeValue{
		"kmv-voice-usage":  {heartbeat, daily},
		"kmv-auth-electro": {oidcSession, accessCode},
		"kmv-auth-authjs":  {authjsSession, authjsUser},
	}}

	ledgerBody := []byte("hello ledger")
	s3api := &fakeS3ListGetAPI{
		objects: map[string][]byte{"transcripts/a.json": ledgerBody},
		pages:   [][]fakeS3Object{{{key: "transcripts/a.json", body: ledgerBody}}},
	}

	backupDeps := BackupDeps{
		Dynamo: dyn,
		S3:     s3api,
		SSM:    &fakeSSMInventoryAPI{},
		DIDs:   DIDLister(func(context.Context) ([]InboundDIDRecord, error) { return nil, nil }),
		Targets: LiveTargets{
			TableNames: map[string]string{
				"voice-usage":  "kmv-voice-usage",
				"auth-electro": "kmv-auth-electro",
				"auth-authjs":  "kmv-auth-authjs",
			},
			LedgerBucket: "kmv-ledger-source",
		},
		AWSAccountID: "123456789012",
		Region:       "us-east-1",
		GitSHA:       "deadbeef",
		Now:          func() time.Time { return now },
	}

	res, err := WriteBackupArchive(context.Background(), backupDeps, BackupOptions{OutDir: t.TempDir()})
	if err != nil {
		t.Fatalf("WriteBackupArchive() error = %v, want nil", err)
	}
	return res.Path, heartbeat, daily, oidcSession, accessCode, authjsSession, authjsUser
}

func restoreTargetsWithSuffix(suffix string) LiveTargets {
	return LiveTargets{
		TableNames: map[string]string{
			"voice-usage":  "kmv-voice-usage" + suffix,
			"auth-electro": "kmv-auth-electro" + suffix,
			"auth-authjs":  "kmv-auth-authjs" + suffix,
		},
		LedgerBucket: "kmv-ledger" + suffix,
	}
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	t.Run("DefaultSkipEphemeral_DurableSurvivesEphemeralDropped", func(t *testing.T) {
		archivePath, heartbeat, daily, oidcSession, accessCode, authjsSession, authjsUser := buildRoundTripFixture(t)

		restoreDynamo := &fakeDynamoBatchWriteAPI{}
		restoreS3 := &fakePutObjectAPI{}
		restoreDeps := RestoreDeps{
			Dynamo:  restoreDynamo,
			S3:      restoreS3,
			Targets: restoreTargetsWithSuffix("-restored"),
			Now:     func() time.Time { return restoreTestNow },
		}

		report, err := RunRestore(context.Background(), restoreDeps, archivePath, RestoreOptions{SkipEphemeral: true})
		if err != nil {
			t.Fatalf("RunRestore() error = %v, want nil", err)
		}

		mustBePresent := map[string]map[string]types.AttributeValue{"daily": daily, "accessCode": accessCode, "authjsUser": authjsUser}
		for name, item := range mustBePresent {
			if _, ok := restoreDynamo.store[itemKey(item)]; !ok {
				t.Errorf("%s missing after restore, want present (durable row)", name)
			}
		}
		mustBeAbsent := map[string]map[string]types.AttributeValue{"heartbeat": heartbeat, "oidcSession": oidcSession, "authjsSession": authjsSession}
		for name, item := range mustBeAbsent {
			if _, ok := restoreDynamo.store[itemKey(item)]; ok {
				t.Errorf("%s present after restore, want dropped (ephemeral row, D-11)", name)
			}
		}
		if len(restoreS3.puts) != 1 {
			t.Errorf("ledger PutObject calls = %d, want 1", len(restoreS3.puts))
		}
		if report.LedgerObjects != 1 {
			t.Errorf("report.LedgerObjects = %d, want 1", report.LedgerObjects)
		}
	})

	t.Run("SkipEphemeralDisabled_EphemeralRowsRestoredToo", func(t *testing.T) {
		archivePath, heartbeat, _, oidcSession, _, authjsSession, _ := buildRoundTripFixture(t)

		restoreDynamo := &fakeDynamoBatchWriteAPI{}
		restoreDeps := RestoreDeps{
			Dynamo:  restoreDynamo,
			S3:      &fakePutObjectAPI{},
			Targets: restoreTargetsWithSuffix("-restored"),
			Now:     func() time.Time { return restoreTestNow },
		}

		if _, err := RunRestore(context.Background(), restoreDeps, archivePath, RestoreOptions{SkipEphemeral: false}); err != nil {
			t.Fatalf("RunRestore() error = %v, want nil", err)
		}

		for name, item := range map[string]map[string]types.AttributeValue{
			"heartbeat":     heartbeat,
			"oidcSession":   oidcSession,
			"authjsSession": authjsSession,
		} {
			if _, ok := restoreDynamo.store[itemKey(item)]; !ok {
				t.Errorf("%s missing after restore with SkipEphemeral=false, want present", name)
			}
		}
	})

	t.Run("VerificationExercisedBeforeRestore_CorruptedArchiveRefuses", func(t *testing.T) {
		archivePath, _, _, _, _, _, _ := buildRoundTripFixture(t)

		// A fresh archive must verify clean — checksum verification is
		// exercised here, not merely assumed.
		if _, err := VerifyBackupArchive(archivePath); err != nil {
			t.Fatalf("VerifyBackupArchive(fresh archive) error = %v, want nil", err)
		}

		corruptByte(t, archivePath, []byte("USER#user-1"))

		restoreDeps := RestoreDeps{
			Dynamo:  &fakeDynamoBatchWriteAPI{t: t},
			S3:      &fakePutObjectAPI{t: t},
			Targets: restoreTargetsWithSuffix("-restored"),
			Now:     func() time.Time { return restoreTestNow },
		}
		if _, err := RunRestore(context.Background(), restoreDeps, archivePath, RestoreOptions{}); err == nil {
			t.Fatal("RunRestore(corrupted archive) error = nil, want non-nil — verification must run before any write")
		}
	})
}

func TestNewRestoreCmd(t *testing.T) {
	cmd := NewRestoreCmd(&Config{})
	if cmd.Use != "restore <zip>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "restore <zip>")
	}
	for _, name := range []string{"tables", "ledger", "dry-run", "skip-ephemeral"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag --%s not registered", name)
		}
	}
	if f := cmd.Flags().Lookup("skip-ephemeral"); f == nil || f.DefValue != "true" {
		t.Errorf("--skip-ephemeral default = %v, want %q", f, "true")
	}
}

func TestPrintRestoreReport(t *testing.T) {
	t.Run("DryRunNamesTablesWriteSkipAndLedgerCounts", func(t *testing.T) {
		report := RestoreReport{
			ArchivePath:        "backups/kmv-backup-20260820T000000Z.zip",
			ManifestTableNames: map[string]string{"voice-usage": "kmv-voice-usage"},
			ManifestBucket:     "kmv-ledger-abc123",
			ResolvedTableNames: map[string]string{"voice-usage": "kmv-voice-usage"},
			ResolvedBucket:     "kmv-ledger-abc123",
			TableWrites:        map[string]int64{"voice-usage": 5},
			TableSkipped:       map[string]map[EphemeralReason]int64{"voice-usage": {EphemeralReasonConcurrencyLease: 2}},
			LedgerObjects:      7,
			LedgerBytes:        1024,
			DryRun:             true,
		}
		var buf bytes.Buffer
		PrintRestoreReport(&buf, report)
		out := buf.String()
		for _, want := range []string{"DRY RUN", "voice-usage", "written=5", "concurrency lease", "2", "7 object"} {
			if !strings.Contains(out, want) {
				t.Errorf("output does not contain %q:\n%s", want, out)
			}
		}
	})

	t.Run("ManifestAndResolvedBucketNamesDistinctlyLabelledWhenDifferent", func(t *testing.T) {
		report := RestoreReport{
			ArchivePath:        "backups/kmv-backup-20260820T000000Z.zip",
			ManifestTableNames: map[string]string{},
			ManifestBucket:     "kmv-ledger-old-random-id",
			ResolvedTableNames: map[string]string{},
			ResolvedBucket:     "kmv-ledger-new-random-id",
			TableWrites:        map[string]int64{},
			TableSkipped:       map[string]map[EphemeralReason]int64{},
		}
		var buf bytes.Buffer
		PrintRestoreReport(&buf, report)
		out := buf.String()
		if !strings.Contains(out, "kmv-ledger-old-random-id") {
			t.Error("output missing the manifest (provenance) bucket name")
		}
		if !strings.Contains(out, "kmv-ledger-new-random-id") {
			t.Error("output missing the resolved (destination) bucket name")
		}
		if !strings.Contains(out, "PROVENANCE") || !strings.Contains(out, "DESTINATIONS") {
			t.Error("output does not distinctly label provenance vs destination blocks")
		}
	})
}
