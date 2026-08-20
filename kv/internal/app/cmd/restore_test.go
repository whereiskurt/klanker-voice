package cmd

import (
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
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
