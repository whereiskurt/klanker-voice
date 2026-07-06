package cmd

// Usage/killswitch tests (04-06, KV-03/KV-04):
//
//  1. Pure key-string table tests proving electro's Usage* key templates
//     equal apps/auth/webapp/src/entities/usage.ts / apps/voice/src/
//     klanker_voice/quota.py's own key-building functions byte-for-byte
//     (T-04-11) — always-on, no external dependency.
//  2. Integration tests against a real dynamodb-local kmv-voice-usage table
//     (same endpoint/skip-if-unreachable pattern as roundtrip_test.go):
//     usage reads (rollup + daily + history). Killswitch on/off/status
//     tests are appended by Task 2.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-voice/kv/internal/app/electro"
)

const usageRoundTripTable = "kmv-voice-usage"

// --- pure key-string compat tests (T-04-11) ---

// TestUsageKeyCompat_Heartbeat asserts UsageHeartbeatPK/SK equal usage.ts's
// UsageHeartbeat templates ("session#${userId}" / "heartbeat#${sessionId}"),
// which quota.py's `_heartbeat_pk`/`_heartbeat_sk` also reproduce.
func TestUsageKeyCompat_Heartbeat(t *testing.T) {
	cases := []struct {
		name      string
		userID    string
		sessionID string
		wantPK    string
		wantSK    string
	}{
		{"simple", "user-123", "session-abc", "session#user-123", "heartbeat#session-abc"},
		{"uuid-shaped", "u-9f8e7d", "sess-0011", "session#u-9f8e7d", "heartbeat#sess-0011"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := electro.UsageHeartbeatPK(tc.userID); got != tc.wantPK {
				t.Errorf("UsageHeartbeatPK(%q) = %q, want %q", tc.userID, got, tc.wantPK)
			}
			if got := electro.UsageHeartbeatSK(tc.sessionID); got != tc.wantSK {
				t.Errorf("UsageHeartbeatSK(%q) = %q, want %q", tc.sessionID, got, tc.wantSK)
			}
		})
	}
}

// TestUsageKeyCompat_Daily asserts UsageDailyPK/SK equal usage.ts's
// UsageDaily templates ("user#${userId}" / "day#${day}"), matching
// quota.py's `_daily_pk`/`_daily_sk`.
func TestUsageKeyCompat_Daily(t *testing.T) {
	cases := []struct {
		name   string
		userID string
		day    string
		wantPK string
		wantSK string
	}{
		{"simple", "user-123", "2026-07-05", "user#user-123", "day#2026-07-05"},
		{"year-boundary", "user-456", "2026-01-01", "user#user-456", "day#2026-01-01"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := electro.UsageDailyPK(tc.userID); got != tc.wantPK {
				t.Errorf("UsageDailyPK(%q) = %q, want %q", tc.userID, got, tc.wantPK)
			}
			if got := electro.UsageDailySK(tc.day); got != tc.wantSK {
				t.Errorf("UsageDailySK(%q) = %q, want %q", tc.day, got, tc.wantSK)
			}
		})
	}
}

// TestUsageKeyCompat_Rollup asserts UsageRollupPK/SK equal usage.ts's
// UsageRollup templates ("rollup#" / "day#${day}"), matching quota.py's
// `ROLLUP_PK`/`_rollup_sk`.
func TestUsageKeyCompat_Rollup(t *testing.T) {
	if got := electro.UsageRollupPK(); got != "rollup#" {
		t.Errorf("UsageRollupPK() = %q, want %q", got, "rollup#")
	}
	cases := []struct {
		day    string
		wantSK string
	}{
		{"2026-07-05", "day#2026-07-05"},
		{"2026-12-31", "day#2026-12-31"},
	}
	for _, tc := range cases {
		if got := electro.UsageRollupSK(tc.day); got != tc.wantSK {
			t.Errorf("UsageRollupSK(%q) = %q, want %q", tc.day, got, tc.wantSK)
		}
	}
}

// TestUsageKeyCompat_Control asserts UsageControlPK/SK equal usage.ts's
// UsageControl templates ("control#" / "killswitch#"), matching quota.py's
// `CONTROL_PK`/`CONTROL_SK` — the exact item /api/offer's start gate reads
// on every session (D-08). A mismatch here is T-04-11's silent-ineffective
// kill-switch scenario.
func TestUsageKeyCompat_Control(t *testing.T) {
	if got := electro.UsageControlPK(); got != "control#" {
		t.Errorf("UsageControlPK() = %q, want %q", got, "control#")
	}
	if got := electro.UsageControlSK(); got != "killswitch#" {
		t.Errorf("UsageControlSK() = %q, want %q", got, "killswitch#")
	}
}

// TestUsageDayString asserts the yyyy-mm-dd (UTC) day-key format used by
// UsageDaily/UsageRollup sort keys, including a UTC-boundary case (a time
// that would format to a different calendar day in most US timezones).
func TestUsageDayString(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"mid-day-utc", time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC), "2026-07-05"},
		{"non-utc-input-normalized", time.Date(2026, 7, 5, 23, 30, 0, 0, time.FixedZone("EST", -5*3600)), "2026-07-06"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := electro.UsageDayString(tc.in); got != tc.want {
				t.Errorf("UsageDayString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// --- dynamodb-local integration tests ---

// usageDynamoClient mirrors roundtrip_test.go's testDynamoClient, but probes
// the voice service's own kmv-voice-usage table instead of kmv-auth-electro.
// Skips (not fails) if dynamodb-local or the table is unreachable, so
// `go test ./...` stays green in sandboxes without a running container.
func usageDynamoClient(t *testing.T) *dynamodb.Client {
	t.Helper()
	endpoint := "http://localhost:8888"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("local", "local", "")),
	)
	if err != nil {
		t.Skipf("skipping usage/killswitch test: could not load aws config: %v", err)
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	if _, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(usageRoundTripTable),
	}); err != nil {
		t.Skipf("skipping usage/killswitch test: dynamodb-local table %q unreachable: %v", usageRoundTripTable, err)
	}
	return client
}

// resetControlItem forces the shared control#/killswitch# item to a known
// disengaged, reason-free state before and after a test — mirrors
// apps/voice/tests/test_quota.py's own reset_control_item fixture so this
// suite never leaks kill-switch state into another test or a stray local
// run.
func resetControlItem(t *testing.T, client *dynamodb.Client) {
	t.Helper()
	ctx := context.Background()
	put := func() {
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(usageRoundTripTable),
			Item: map[string]types.AttributeValue{
				"pk":      &types.AttributeValueMemberS{Value: electro.UsageControlPK()},
				"sk":      &types.AttributeValueMemberS{Value: electro.UsageControlSK()},
				"engaged": &types.AttributeValueMemberBOOL{Value: false},
			},
		})
		if err != nil {
			t.Fatalf("resetControlItem: %v", err)
		}
	}
	put()
	t.Cleanup(put)
}

// deleteUsageItem is a t.Cleanup helper for daily/rollup test items this
// suite writes directly (not via kv, to simulate quota.py's own writes).
func deleteUsageItem(t *testing.T, client *dynamodb.Client, pk, sk string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = client.DeleteItem(context.Background(), &dynamodb.DeleteItemInput{
			TableName: aws.String(usageRoundTripTable),
			Key: map[string]types.AttributeValue{
				"pk": &types.AttributeValueMemberS{Value: pk},
				"sk": &types.AttributeValueMemberS{Value: sk},
			},
		})
	})
}

// TestUsage_ReadRollup_FreshDay proves a day with no traffic yet (no rollup
// item written) reads as a zero-value record, not an error — the common
// case for `kv usage today` run before any session has happened today.
func TestUsage_ReadRollup_FreshDay(t *testing.T) {
	client := usageDynamoClient(t)
	day := "1999-01-01-" + randomSuffix() // a day guaranteed to have no item

	record, err := ReadUsageRollup(context.Background(), client, usageRoundTripTable, day)
	if err != nil {
		t.Fatalf("ReadUsageRollup: %v", err)
	}
	if record.TotalSeconds != 0 || record.SessionCount != 0 || record.EstCost != 0 {
		t.Errorf("fresh-day rollup = %+v, want all-zero", record)
	}
	if record.Day != day {
		t.Errorf("record.Day = %q, want %q", record.Day, day)
	}
}

// TestUsage_RollupRoundTrip: a rollup item written in the exact shape
// quota.py's record_tick would write (pk="rollup#", sk="day#${day}",
// totalSeconds/sessionCount/estCost) is read back correctly by
// ReadUsageRollup — the KV-03 O(1) site-wide view.
func TestUsage_RollupRoundTrip(t *testing.T) {
	client := usageDynamoClient(t)
	ctx := context.Background()
	day := "2099-06-15-" + randomSuffix()
	pk := electro.UsageRollupPK()
	sk := electro.UsageRollupSK(day)
	deleteUsageItem(t, client, pk, sk)

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(usageRoundTripTable),
		Item: map[string]types.AttributeValue{
			"pk":           &types.AttributeValueMemberS{Value: pk},
			"sk":           &types.AttributeValueMemberS{Value: sk},
			"totalSeconds": &types.AttributeValueMemberN{Value: "342"},
			"sessionCount": &types.AttributeValueMemberN{Value: "7"},
			"estCost":      &types.AttributeValueMemberN{Value: "1.71"},
		},
	})
	if err != nil {
		t.Fatalf("seed rollup PutItem: %v", err)
	}

	record, err := ReadUsageRollup(ctx, client, usageRoundTripTable, day)
	if err != nil {
		t.Fatalf("ReadUsageRollup: %v", err)
	}
	if record.TotalSeconds != 342 {
		t.Errorf("TotalSeconds = %d, want 342", record.TotalSeconds)
	}
	if record.SessionCount != 7 {
		t.Errorf("SessionCount = %d, want 7", record.SessionCount)
	}
	if record.EstCost != 1.71 {
		t.Errorf("EstCost = %v, want 1.71", record.EstCost)
	}
}

// TestUsage_DailyRoundTrip: a daily-per-user item written in the exact shape
// quota.py's record_tick would write (pk="user#${userId}",
// sk="day#${day}", secondsUsed) is read back correctly by ReadUsageDaily.
func TestUsage_DailyRoundTrip(t *testing.T) {
	client := usageDynamoClient(t)
	ctx := context.Background()
	userID := "kv-test-user-" + randomSuffix()
	day := "2099-06-15"
	pk := electro.UsageDailyPK(userID)
	sk := electro.UsageDailySK(day)
	deleteUsageItem(t, client, pk, sk)

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(usageRoundTripTable),
		Item: map[string]types.AttributeValue{
			"pk":          &types.AttributeValueMemberS{Value: pk},
			"sk":          &types.AttributeValueMemberS{Value: sk},
			"secondsUsed": &types.AttributeValueMemberN{Value: "90"},
		},
	})
	if err != nil {
		t.Fatalf("seed daily PutItem: %v", err)
	}

	record, err := ReadUsageDaily(ctx, client, usageRoundTripTable, userID, day)
	if err != nil {
		t.Fatalf("ReadUsageDaily: %v", err)
	}
	if record.SecondsUsed != 90 {
		t.Errorf("SecondsUsed = %d, want 90", record.SecondsUsed)
	}
	if record.UserID != userID || record.Day != day {
		t.Errorf("record = %+v, want userID=%q day=%q", record, userID, day)
	}
}

// TestUsage_History: three daily items across three different days in one
// user's partition; ListUsageHistory with --days=2 returns exactly the two
// most recent days, most-recent-first, via a single-partition Query (no
// table scan).
func TestUsage_History(t *testing.T) {
	client := usageDynamoClient(t)
	ctx := context.Background()
	userID := "kv-history-user-" + randomSuffix()
	days := []string{"2099-01-01", "2099-01-02", "2099-01-03"}

	for i, day := range days {
		pk := electro.UsageDailyPK(userID)
		sk := electro.UsageDailySK(day)
		deleteUsageItem(t, client, pk, sk)
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(usageRoundTripTable),
			Item: map[string]types.AttributeValue{
				"pk":          &types.AttributeValueMemberS{Value: pk},
				"sk":          &types.AttributeValueMemberS{Value: sk},
				"secondsUsed": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", 60*(i+1))},
			},
		})
		if err != nil {
			t.Fatalf("seed daily item %q: %v", day, err)
		}
	}

	records, err := ListUsageHistory(ctx, client, usageRoundTripTable, userID, 2)
	if err != nil {
		t.Fatalf("ListUsageHistory: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(records))
	}
	if records[0].Day != "2099-01-03" || records[1].Day != "2099-01-02" {
		t.Errorf("records = %+v, want most-recent-first [2099-01-03, 2099-01-02]", records)
	}
}

// TestKillswitch_OnThenStatusEngaged: `kv killswitch on` conditionally
// engages the control item; `status` reflects engaged=true with the given
// reason.
func TestKillswitch_OnThenStatusEngaged(t *testing.T) {
	client := usageDynamoClient(t)
	resetControlItem(t, client)
	ctx := context.Background()

	flipped, err := EngageKillswitch(ctx, client, usageRoundTripTable, "verify")
	if err != nil {
		t.Fatalf("EngageKillswitch: %v", err)
	}
	if !flipped {
		t.Fatalf("EngageKillswitch: flipped = false, want true (first engage)")
	}

	status, err := ReadKillswitchStatus(ctx, client, usageRoundTripTable)
	if err != nil {
		t.Fatalf("ReadKillswitchStatus: %v", err)
	}
	if !status.Engaged {
		t.Errorf("status.Engaged = false, want true")
	}
	if status.Reason != "verify" {
		t.Errorf("status.Reason = %q, want %q", status.Reason, "verify")
	}
}

// TestKillswitch_OffThenStatusDisengaged: after an on, `kv killswitch off`
// conditionally disengages and clears the reason (D-09 explicit operator
// reset) — status.Reason must be empty afterward, even after an
// auto-trip-shaped reason.
func TestKillswitch_OffThenStatusDisengaged(t *testing.T) {
	client := usageDynamoClient(t)
	resetControlItem(t, client)
	ctx := context.Background()

	if _, err := EngageKillswitch(ctx, client, usageRoundTripTable, "auto-trip"); err != nil {
		t.Fatalf("EngageKillswitch: %v", err)
	}

	flipped, err := DisengageKillswitch(ctx, client, usageRoundTripTable)
	if err != nil {
		t.Fatalf("DisengageKillswitch: %v", err)
	}
	if !flipped {
		t.Fatalf("DisengageKillswitch: flipped = false, want true (was engaged)")
	}

	status, err := ReadKillswitchStatus(ctx, client, usageRoundTripTable)
	if err != nil {
		t.Fatalf("ReadKillswitchStatus: %v", err)
	}
	if status.Engaged {
		t.Errorf("status.Engaged = true, want false")
	}
	if status.Reason != "" {
		t.Errorf("status.Reason = %q, want empty (D-09 explicit reset must clear the auto-trip reason)", status.Reason)
	}
}

// TestKillswitch_RedundantOnNoOp: a second `on` while already engaged is a
// harmless no-op (flipped=false, no error) — the conditional write's
// ConditionalCheckFailedException must never surface as a command error.
func TestKillswitch_RedundantOnNoOp(t *testing.T) {
	client := usageDynamoClient(t)
	resetControlItem(t, client)
	ctx := context.Background()

	first, err := EngageKillswitch(ctx, client, usageRoundTripTable, "operator")
	if err != nil {
		t.Fatalf("first EngageKillswitch: %v", err)
	}
	if !first {
		t.Fatalf("first EngageKillswitch: flipped = false, want true")
	}

	second, err := EngageKillswitch(ctx, client, usageRoundTripTable, "operator")
	if err != nil {
		t.Fatalf("redundant EngageKillswitch returned an error, want harmless no-op: %v", err)
	}
	if second {
		t.Errorf("redundant EngageKillswitch: flipped = true, want false (already engaged)")
	}
}

// TestKillswitch_RedundantOffNoOp: an `off` while already disengaged (or
// never engaged) is a harmless no-op.
func TestKillswitch_RedundantOffNoOp(t *testing.T) {
	client := usageDynamoClient(t)
	resetControlItem(t, client)
	ctx := context.Background()

	flipped, err := DisengageKillswitch(ctx, client, usageRoundTripTable)
	if err != nil {
		t.Fatalf("DisengageKillswitch returned an error, want harmless no-op: %v", err)
	}
	if flipped {
		t.Errorf("DisengageKillswitch: flipped = true, want false (already disengaged)")
	}
}
