package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// --------------------------------------------------------------------------
// Task 1: RunInsightsQuery stays byte-identical (extract-method regression)
// and RunCallsInsightsQuery matches both line families.

func TestRunInsightsQuery_ExactQueryString(t *testing.T) {
	fake := &fakeLogsInsightsClient{
		statuses: []types.QueryStatus{types.QueryStatusComplete},
	}
	if _, err := RunInsightsQuery(context.Background(), fake, "/ecs/telephony-edge", time.Now(), time.Now(), 0); err != nil {
		t.Fatalf("RunInsightsQuery error: %v", err)
	}
	want := "fields @timestamp, @message | filter @message like /game_call_event / | sort @timestamp desc | limit 10000"
	if fake.lastQueryString != want {
		t.Errorf("RunInsightsQuery query string = %q, want %q", fake.lastQueryString, want)
	}
}

func TestRunCallsInsightsQuery_MentionsBothLineFamilies(t *testing.T) {
	fake := &fakeLogsInsightsClient{
		statuses: []types.QueryStatus{types.QueryStatusComplete},
	}
	if _, err := RunCallsInsightsQuery(context.Background(), fake, "/ecs/telephony-edge", time.Now(), time.Now(), 0); err != nil {
		t.Fatalf("RunCallsInsightsQuery error: %v", err)
	}
	if !strings.Contains(fake.lastQueryString, stasisMarker) {
		t.Errorf("query string %q does not mention %q", fake.lastQueryString, stasisMarker)
	}
	if !strings.Contains(fake.lastQueryString, callEventMarker) {
		t.Errorf("query string %q does not mention %q", fake.lastQueryString, callEventMarker)
	}
}

// --------------------------------------------------------------------------
// Task 1: parseStasisLine over both real controller.py shapes, the
// external-media exclusion, and the three keyless lines.

// Real log-line shapes, built from controller.py's own f-strings
// (lines 1111, 1118, 1128, 1157, 1181, 1331), each prefixed with a
// realistic default-format loguru stamp
// ("YYYY-MM-DD HH:mm:ss.SSS | LEVEL    | module:func:line - ").
const (
	loguruPrefix = "2026-07-29 05:46:13.123 | INFO     | klanker_voice.telephony.controller:on_stasis_start:1128 - "

	identityLine        = loguruPrefix + "on_stasis_start: channel=1785303830.6 caller=+15197101515 did=557010_klanker-pbx"
	dialedResolvedLine  = loguruPrefix + "on_stasis_start: channel=1785303830.6 dialed_did=7254043234 exten='1234' cidname='KVD3234' sip_to='557010_klanker-pbx'"
	dialedNoneLine      = loguruPrefix + "on_stasis_start: channel=1785303899.1 dialed_did=<none> exten='9999' cidname='<none>' sip_to='557010_klanker-pbx'"
	externalMediaLine   = loguruPrefix + "on_stasis_start: ignoring external-media leg channel='1785303830.7' name='UnicastRTP/foo'"
	unexpectedCtxLine   = loguruPrefix + "on_stasis_start: unexpected app='wrongapp' context='wrongctx' channel='1785303830.9'; hanging up, no allocation"
	mediaBridgeFailLine = loguruPrefix + "on_stasis_start: failed to establish media/bridge for channel=1785303831.0"
	quotaDeniedLine     = loguruPrefix + "on_stasis_start: quota denied (max_concurrent_calls) channel=1785303831.1"
)

func TestParseStasisLine_IdentityShape(t *testing.T) {
	rec, ok := parseStasisLine(identityLine)
	if !ok {
		t.Fatalf("parseStasisLine(identityLine) ok = false, want true")
	}
	if rec.Channel != "1785303830.6" {
		t.Errorf("Channel = %q, want 1785303830.6", rec.Channel)
	}
	if rec.Caller != "+15197101515" {
		t.Errorf("Caller = %q, want +15197101515", rec.Caller)
	}
	if rec.DialedDID != "" || rec.ResolutionSeen {
		t.Errorf("identity shape must not report DID resolution: DialedDID=%q ResolutionSeen=%v", rec.DialedDID, rec.ResolutionSeen)
	}
}

func TestParseStasisLine_DialedDIDResolvedShape(t *testing.T) {
	rec, ok := parseStasisLine(dialedResolvedLine)
	if !ok {
		t.Fatalf("parseStasisLine(dialedResolvedLine) ok = false, want true")
	}
	if rec.Channel != "1785303830.6" {
		t.Errorf("Channel = %q, want 1785303830.6", rec.Channel)
	}
	if rec.DialedDID != "7254043234" {
		t.Errorf("DialedDID = %q, want 7254043234", rec.DialedDID)
	}
	if !rec.ResolutionSeen {
		t.Error("ResolutionSeen = false, want true (dialed_did key was present)")
	}
	if rec.Caller != "" {
		t.Errorf("Caller = %q, want empty (dialed-DID line never carries a caller)", rec.Caller)
	}
}

func TestParseStasisLine_DialedDIDNonePlaceholder(t *testing.T) {
	rec, ok := parseStasisLine(dialedNoneLine)
	if !ok {
		t.Fatalf("parseStasisLine(dialedNoneLine) ok = false, want true (resolution was attempted)")
	}
	if rec.DialedDID != "" {
		t.Errorf("DialedDID = %q, want empty for the <none> placeholder", rec.DialedDID)
	}
	if !rec.ResolutionSeen {
		t.Error("ResolutionSeen = false, want true even though the DID resolved empty")
	}
}

func TestParseStasisLine_ExternalMediaLegExcluded(t *testing.T) {
	if _, ok := parseStasisLine(externalMediaLine); ok {
		t.Error("parseStasisLine(externalMediaLine) ok = true, want false -- the UnicastRTP leg is not a call")
	}
}

func TestParseStasisLine_KeylessLinesExcluded(t *testing.T) {
	for name, line := range map[string]string{
		"unexpected-context warning": unexpectedCtxLine,
		"media/bridge failure":       mediaBridgeFailLine,
		"quota denied":               quotaDeniedLine,
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := parseStasisLine(line); ok {
				t.Errorf("parseStasisLine(%s) ok = true, want false -- no caller or dialed_did key present", name)
			}
		})
	}
}

func TestParseStasisLine_NonStasisLineExcluded(t *testing.T) {
	if _, ok := parseStasisLine(`game_call_event {"call_id":"c1"}`); ok {
		t.Error("parseStasisLine(game_call_event line) ok = true, want false")
	}
}

// trimReprQuotes is defensive: real controller.py lines never repr-quote
// channel/caller/dialed_did, but a synthetic repr-quoted value must still
// unquote cleanly, per the plan's explicit behavior requirement.
func TestParseStasisLine_UnquotesReprWrappedValue(t *testing.T) {
	line := loguruPrefix + `on_stasis_start: channel=1785303830.6 caller='+15550001234'`
	rec, ok := parseStasisLine(line)
	if !ok {
		t.Fatalf("parseStasisLine ok = false, want true")
	}
	if rec.Caller != "+15550001234" {
		t.Errorf("Caller = %q, want +15550001234 (repr quotes stripped)", rec.Caller)
	}
}

// --------------------------------------------------------------------------
// Task 1: channelEpoch.

func TestChannelEpoch_ValidPrefix(t *testing.T) {
	got, ok := channelEpoch("1785303830.6")
	if !ok {
		t.Fatalf("channelEpoch ok = false, want true")
	}
	want := time.Unix(1785303830, 0).UTC()
	if !got.Equal(want) {
		t.Errorf("channelEpoch = %v, want %v", got, want)
	}
}

func TestChannelEpoch_RejectsJunk(t *testing.T) {
	for name, id := range map[string]string{
		"non-numeric":           "abc.6",
		"empty":                 "",
		"no dot, non-numeric":   "notanid",
		"below plausible range": "12345.6",
		"far future":            "9999999999.0",
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := channelEpoch(id); ok {
				t.Errorf("channelEpoch(%q) ok = true, want false", id)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Task 1: loguruTimestamp.

func TestLoguruTimestamp_ValidLeadingStamp(t *testing.T) {
	got, ok := loguruTimestamp(identityLine)
	if !ok {
		t.Fatalf("loguruTimestamp ok = false, want true")
	}
	want := time.Date(2026, 7, 29, 5, 46, 13, 123_000_000, time.UTC)
	if !got.Equal(want) {
		t.Errorf("loguruTimestamp = %v, want %v", got, want)
	}
}

func TestLoguruTimestamp_RejectsLineWithNoLeadingStamp(t *testing.T) {
	for name, line := range map[string]string{
		"marker-only line":  `game_call_event {"call_id":"c1"}`,
		"digits but no sep": "2026-07-29 05:46:13.123 on_stasis_start: channel=1.2",
		"too short":         "2026-07-29",
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := loguruTimestamp(line); ok {
				t.Errorf("loguruTimestamp(%q) ok = true, want false", line)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Task 1: parseCIDPrefixDIDs.

func TestParseCIDPrefixDIDs_ReadsTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telephony.toml")
	content := `[telephony]
enabled = true

[telephony.cid_prefix_dids]
"KVD3234" = "7254043234"
"KVD3283" = "7254043283"
"KVD1800" = "8559164636"

[[telephony.announcement]]
dids = ["7254043234"]
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	got := parseCIDPrefixDIDs(path)
	want := map[string]string{
		"7254043234": "KVD3234",
		"7254043283": "KVD3283",
		"8559164636": "KVD1800",
	}
	if len(got) != len(want) {
		t.Fatalf("parseCIDPrefixDIDs() = %+v, want %+v", got, want)
	}
	for did, tag := range want {
		if got[did] != tag {
			t.Errorf("tagByDID[%q] = %q, want %q", did, got[did], tag)
		}
	}
}

func TestParseCIDPrefixDIDs_MissingFileYieldsEmptyMap(t *testing.T) {
	got := parseCIDPrefixDIDs(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if got == nil {
		t.Fatal("parseCIDPrefixDIDs() = nil, want a non-nil empty map")
	}
	if len(got) != 0 {
		t.Errorf("parseCIDPrefixDIDs() = %+v, want empty", got)
	}
}

// TestParseCIDPrefixDIDs_ShippedConfig is a light sanity check against the
// repo's real telephony.toml (skips gracefully if not present, e.g. a
// stripped-down CI checkout), asserting by presence-of-known-tags rather
// than a literal DID digit count so a future number change stays a pure
// TOML edit (mirrors the discipline the shipped-config tests elsewhere in
// this package already use).
func TestParseCIDPrefixDIDs_ShippedConfig(t *testing.T) {
	// kv/internal/app/cmd -> kv/internal/app -> kv/internal -> kv -> repo
	// root -> apps/voice/configs/telephony.toml.
	path := "../../../../apps/voice/configs/telephony.toml"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("shipped telephony.toml not present: %v", err)
	}
	got := parseCIDPrefixDIDs(path)
	if len(got) == 0 {
		t.Fatal("parseCIDPrefixDIDs(shipped config) = empty, want at least the known KVD tags")
	}
	if _, ok := got["7254043234"]; !ok {
		t.Errorf("parseCIDPrefixDIDs(shipped config) missing 7254043234 (KVD3234), got %+v", got)
	}
}

// --------------------------------------------------------------------------
// Task 2: JoinCallRecords + BuildCallsReport.

// gameCallEventLine builds a realistic teardown log line: a loguru default-
// format leading stamp followed by the marker + JSON payload, mirroring how
// telephony-edge actually logs it (call_event.py's build_call_event feeds
// straight into logger.info(...)).
func gameCallEventLine(stamp, callID, dialedDID, callerID, outcome string, digits, words int, duration float64) string {
	return stamp + " | INFO     | klanker_voice.telephony.call_event:emit:42 - " +
		callEventMarker + ` {"call_id":"` + callID + `","dialed_did":"` + dialedDID +
		`","caller_id":"` + callerID + `","otp_only":false,"outcome":"` + outcome +
		`","digits_entered":` + fmt.Sprintf("%d", digits) + `,"words_heard":` + fmt.Sprintf("%d", words) +
		`,"seconds_to_outcome":null,"duration_seconds":` + fmt.Sprintf("%.1f", duration) + `}`
}

// channelJoinFixture builds one shared set of raw log-line strings
// exercising every JoinCallRecords/BuildCallsReport behavior in one pass:
//   - three channels deliberately given the SAME loguru stamp but channel
//     epochs ~18s and ~29s apart (the batched-ingestion regression: real
//     CloudWatch @timestamp values collide this way, proven by CONTEXT --
//     ordering must follow the channel epoch, never any shared secondary
//     signal);
//   - a stasis-only channel with no dialed-DID line and no teardown (both
//     "no teardown" AND "pre-resolution" in one fixture);
//   - a teardown-only orphan channel (query-truncation case: no stasis
//     line ever seen for it);
//   - a none-placeholder channel (resolution attempted, came up empty --
//     the untagged-concierge bucket, distinct from pre-resolution);
//   - a tagged DID channel and an untagged DID channel, each with a full
//     stasis + teardown pair.
func channelJoinFixture() []string {
	const batchedStamp = "2026-07-29 05:46:13.000"
	return []string{
		// Batched-ingestion trio: epochs 1785303830 / 1785303848 (+18s) /
		// 1785303877 (+29s more), all sharing one loguru stamp.
		batchedStamp + " | INFO     | klanker_voice.telephony.controller:on_stasis_start:1128 - " +
			"on_stasis_start: channel=1785303877.8 caller=+13124432920 did=557010_klanker-pbx",
		batchedStamp + " | INFO     | klanker_voice.telephony.controller:on_stasis_start:1128 - " +
			"on_stasis_start: channel=1785303830.6 caller=+15197101515 did=557010_klanker-pbx",
		batchedStamp + " | INFO     | klanker_voice.telephony.controller:on_stasis_start:1128 - " +
			"on_stasis_start: channel=1785303848.7 caller=+16479213102 did=557010_klanker-pbx",

		// Stasis-only, pre-resolution, no teardown.
		"2026-07-12 08:00:00.000 | INFO     | klanker_voice.telephony.controller:on_stasis_start:1128 - " +
			"on_stasis_start: channel=1752307200.1 caller=+18022337051 did=557010_klanker-pbx",

		// Teardown-only orphan (query truncation -- no stasis line at all).
		gameCallEventLine("2026-07-29 06:00:00.000", "1785306000.2", "7254043234", "+14167979698", "concierge_unlock_dtmf", 4, 0, 45.0),

		// None-placeholder: resolution attempted, resolved empty.
		"2026-07-20 09:00:00.000 | INFO     | klanker_voice.telephony.controller:on_stasis_start:1128 - " +
			"on_stasis_start: channel=1752998400.3 caller=+16135313189 did=557010_klanker-pbx",
		"2026-07-20 09:00:00.100 | INFO     | klanker_voice.telephony.controller:on_stasis_start:1157 - " +
			"on_stasis_start: channel=1752998400.3 dialed_did=<none> exten='1234' cidname='<none>' sip_to='557010_klanker-pbx'",

		// Tagged DID: identity + dialed-DID + teardown.
		"2026-07-29 07:00:00.000 | INFO     | klanker_voice.telephony.controller:on_stasis_start:1128 - " +
			"on_stasis_start: channel=1785309600.4 caller=+61437008930 did=557010_klanker-pbx",
		"2026-07-29 07:00:00.100 | INFO     | klanker_voice.telephony.controller:on_stasis_start:1157 - " +
			"on_stasis_start: channel=1785309600.4 dialed_did=7254043234 exten='1234' cidname='KVD3234' sip_to='557010_klanker-pbx'",
		gameCallEventLine("2026-07-29 07:00:30.000", "1785309600.4", "7254043234", "+61437008930", "concierge_unlock_dtmf", 4, 0, 30.0),

		// Untagged DID (absent from tagByDID): identity + dialed-DID + teardown.
		"2026-07-29 08:00:00.000 | INFO     | klanker_voice.telephony.controller:on_stasis_start:1128 - " +
			"on_stasis_start: channel=1785313200.5 caller=+12672520810 did=557010_klanker-pbx",
		"2026-07-29 08:00:00.100 | INFO     | klanker_voice.telephony.controller:on_stasis_start:1157 - " +
			"on_stasis_start: channel=1785313200.5 dialed_did=9995551234 exten='1234' cidname='<none>' sip_to='557010_klanker-pbx'",
		gameCallEventLine("2026-07-29 08:00:20.000", "1785313200.5", "9995551234", "+12672520810", "early_hangup", 0, 3, 20.0),
	}
}

func fixtureTagByDID() map[string]string {
	return map[string]string{"7254043234": "KVD3234"}
}

func TestJoinCallRecords_BatchedIngestionOrdersByChannelEpoch(t *testing.T) {
	records := JoinCallRecords(channelJoinFixture(), fixtureTagByDID())

	// The three batched channels must appear in EPOCH order (830 < 848 <
	// 877), never input order (877, 830, 848 as fed above) and never
	// grouped by their shared loguru stamp.
	var gotOrder []string
	wantOrder := []string{"1785303830.6", "1785303848.7", "1785303877.8"}
	for _, r := range records {
		for _, want := range wantOrder {
			if r.ChannelID == want {
				gotOrder = append(gotOrder, r.ChannelID)
			}
		}
	}
	if len(gotOrder) != 3 {
		t.Fatalf("found %d of the 3 batched channels in records, want 3 (got %+v)", len(gotOrder), records)
	}
	for i, want := range wantOrder {
		if gotOrder[i] != want {
			t.Errorf("batched-channel order[%d] = %q, want %q (got order %v)", i, gotOrder[i], want, gotOrder)
		}
	}
	for _, r := range records {
		if r.ChannelID == "1785303830.6" && r.TimeSource != "channel-id" {
			t.Errorf("TimeSource = %q, want channel-id", r.TimeSource)
		}
	}
}

func TestJoinCallRecords_StasisOnlyChannelHasNoTeardownAndIsPreResolution(t *testing.T) {
	records := JoinCallRecords(channelJoinFixture(), fixtureTagByDID())
	rec := findRecord(t, records, "1752307200.1")
	if rec.HasTeardown {
		t.Error("HasTeardown = true, want false (stasis-only channel)")
	}
	if rec.Outcome != "" {
		t.Errorf("Outcome = %q, want empty", rec.Outcome)
	}
	if rec.DurationSeconds != 0 {
		t.Errorf("DurationSeconds = %v, want 0", rec.DurationSeconds)
	}
	if rec.DIDLabel != preResolutionDIDLabel {
		t.Errorf("DIDLabel = %q, want %q", rec.DIDLabel, preResolutionDIDLabel)
	}
	if rec.Caller != "+18022337051" {
		t.Errorf("Caller = %q, want +18022337051 (raw caller number must appear verbatim)", rec.Caller)
	}
}

func TestJoinCallRecords_OrphanTeardownSourcedFromTeardownPayload(t *testing.T) {
	records := JoinCallRecords(channelJoinFixture(), fixtureTagByDID())
	rec := findRecord(t, records, "1785306000.2")
	if !rec.HasTeardown {
		t.Error("HasTeardown = false, want true")
	}
	if rec.Caller != "+14167979698" {
		t.Errorf("Caller = %q, want +14167979698 (sourced from teardown payload)", rec.Caller)
	}
	if rec.DialedDID != "7254043234" {
		t.Errorf("DialedDID = %q, want 7254043234", rec.DialedDID)
	}
	if rec.Outcome != "concierge_unlock_dtmf" {
		t.Errorf("Outcome = %q, want concierge_unlock_dtmf", rec.Outcome)
	}
}

func TestJoinCallRecords_NonePlaceholderIsUntaggedNotPreResolution(t *testing.T) {
	records := JoinCallRecords(channelJoinFixture(), fixtureTagByDID())
	rec := findRecord(t, records, "1752998400.3")
	if rec.DIDLabel != untaggedDIDLabel {
		t.Errorf("DIDLabel = %q, want %q", rec.DIDLabel, untaggedDIDLabel)
	}
	if rec.DIDLabel == preResolutionDIDLabel {
		t.Error("a none-placeholder resolution must never be mislabelled as pre-resolution")
	}
}

func TestJoinCallRecords_TaggedAndUntaggedDIDLabels(t *testing.T) {
	records := JoinCallRecords(channelJoinFixture(), fixtureTagByDID())

	tagged := findRecord(t, records, "1785309600.4")
	if tagged.DIDLabel != "7254043234 (KVD3234)" {
		t.Errorf("tagged DIDLabel = %q, want %q", tagged.DIDLabel, "7254043234 (KVD3234)")
	}

	untagged := findRecord(t, records, "1785313200.5")
	if untagged.DIDLabel != "9995551234" {
		t.Errorf("untagged DIDLabel = %q, want bare digits %q", untagged.DIDLabel, "9995551234")
	}
}

func TestJoinCallRecords_TwoStasisLinesAndTeardownCollapseToOneRecord(t *testing.T) {
	records := JoinCallRecords(channelJoinFixture(), fixtureTagByDID())
	count := 0
	for _, r := range records {
		if r.ChannelID == "1785309600.4" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("channel 1785309600.4 appears %d times, want exactly 1 (identity line + dialed-DID line + teardown must collapse)", count)
	}
}

func findRecord(t *testing.T, records []CallRecord, channelID string) CallRecord {
	t.Helper()
	for _, r := range records {
		if r.ChannelID == channelID {
			return r
		}
	}
	t.Fatalf("no record for channel %q in %+v", channelID, records)
	return CallRecord{}
}

// --------------------------------------------------------------------------
// Required addition (quick task 260806-cm9, plan-checker follow-up): the
// loguru-timestamp fallback rung (channelEpoch fails, loguruTimestamp
// succeeds) and the all-rungs-fail unknown-time case. Neither Task 1's
// isolated loguruTimestamp() tests nor the channel-epoch-primary path above
// drove a channel through this middle rung of the TimeSource chain.

func TestJoinCallRecords_LoguruTimestampFallbackRung(t *testing.T) {
	// "bad.1" is not a plausible epoch (channelEpoch rejects it: below
	// minPlausibleEpoch), so this channel must fall through to the
	// loguru-timestamp rung. Two lines on the same channel, at two
	// different loguru stamps -- the EARLIEST must win.
	messages := []string{
		"2026-07-29 10:00:00.000 | INFO     | klanker_voice.telephony.controller:on_stasis_start:1157 - " +
			"on_stasis_start: channel=bad.1 dialed_did=7254043234 exten='1234' cidname='KVD3234' sip_to='557010_klanker-pbx'",
		"2026-07-29 09:30:00.000 | INFO     | klanker_voice.telephony.controller:on_stasis_start:1128 - " +
			"on_stasis_start: channel=bad.1 caller=+15550009999 did=557010_klanker-pbx",
	}
	records := JoinCallRecords(messages, fixtureTagByDID())
	rec := findRecord(t, records, "bad.1")
	if rec.TimeSource != "log-timestamp" {
		t.Fatalf("TimeSource = %q, want log-timestamp", rec.TimeSource)
	}
	want := time.Date(2026, 7, 29, 9, 30, 0, 0, time.UTC)
	if !rec.StartedAt.Equal(want) {
		t.Errorf("StartedAt = %v, want the EARLIEST of the two loguru stamps (%v)", rec.StartedAt, want)
	}
}

func TestJoinCallRecords_AllRungsFailSortsFirst(t *testing.T) {
	// A teardown-only line with a channel id that channelEpoch rejects and
	// no loguru-prefixed line ever seen for it (the bare game_call_event
	// fixtures elsewhere in this package carry no leading timestamp) must
	// fall all the way through to "unknown".
	messages := []string{
		`game_call_event {"call_id":"bad-channel","dialed_did":"7254043234","caller_id":"+15550001111","otp_only":false,"outcome":"early_hangup","digits_entered":0,"words_heard":0,"seconds_to_outcome":null,"duration_seconds":2.0}`,
		"2026-07-29 07:00:00.000 | INFO     | klanker_voice.telephony.controller:on_stasis_start:1128 - " +
			"on_stasis_start: channel=1785309600.4 caller=+61437008930 did=557010_klanker-pbx",
	}
	records := JoinCallRecords(messages, fixtureTagByDID())
	rec := findRecord(t, records, "bad-channel")
	if rec.TimeSource != "unknown" {
		t.Fatalf("TimeSource = %q, want unknown", rec.TimeSource)
	}
	if !rec.StartedAt.IsZero() {
		t.Errorf("StartedAt = %v, want the zero time", rec.StartedAt)
	}
	if records[0].ChannelID != "bad-channel" {
		t.Errorf("records[0].ChannelID = %q, want bad-channel (an unknown-time record sorts first)", records[0].ChannelID)
	}
}

// --------------------------------------------------------------------------
// BuildCallsReport.

func TestBuildCallsReport_AllFourViewsAndTotals(t *testing.T) {
	records := JoinCallRecords(channelJoinFixture(), fixtureTagByDID())
	windowEnd := time.Now()
	report := BuildCallsReport(records, "", "", windowEnd, 24*time.Hour)

	if report.Totals.Calls != len(records) {
		t.Errorf("Totals.Calls = %d, want %d", report.Totals.Calls, len(records))
	}
	// The 3 batched-ingestion channels + the stasis-only channel + the
	// none-placeholder channel never produce a teardown line in this
	// fixture; the orphan and the two DID-labelled channels do.
	if report.Totals.WithoutTeardown != 5 {
		t.Errorf("Totals.WithoutTeardown = %d, want 5", report.Totals.WithoutTeardown)
	}
	if len(report.Callers) == 0 {
		t.Fatal("report.Callers is empty, want at least one caller rollup")
	}
	if len(report.Numbers) == 0 {
		t.Fatal("report.Numbers is empty, want at least one number rollup")
	}
	// A caller number must appear verbatim in the callers view.
	found := false
	for _, c := range report.Callers {
		if c.Caller == "+61437008930" {
			found = true
		}
	}
	if !found {
		t.Error("report.Callers does not contain the raw caller number +61437008930")
	}
}

func TestBuildCallsReport_DIDFilterNarrowsConsistently(t *testing.T) {
	records := JoinCallRecords(channelJoinFixture(), fixtureTagByDID())
	report := BuildCallsReport(records, "7254043234", "", time.Now(), 24*time.Hour)

	for _, c := range report.Calls {
		if c.DialedDID != "7254043234" {
			t.Errorf("filtered Calls contains DialedDID=%q, want only 7254043234", c.DialedDID)
		}
	}
	if report.Totals.Calls != len(report.Calls) {
		t.Errorf("Totals.Calls = %d, want %d (must match the filtered Calls length)", report.Totals.Calls, len(report.Calls))
	}
	for _, n := range report.Numbers {
		if n.DIDLabel != "7254043234 (KVD3234)" {
			t.Errorf("filtered Numbers contains an unexpected DID label %q", n.DIDLabel)
		}
	}
}

func TestBuildCallsReport_CallerFilterNarrowsConsistently(t *testing.T) {
	records := JoinCallRecords(channelJoinFixture(), fixtureTagByDID())
	report := BuildCallsReport(records, "", "+61437008930", time.Now(), 24*time.Hour)

	if len(report.Calls) != 1 {
		t.Fatalf("len(Calls) = %d, want 1", len(report.Calls))
	}
	if report.Calls[0].Caller != "+61437008930" {
		t.Errorf("Calls[0].Caller = %q, want +61437008930", report.Calls[0].Caller)
	}
	if len(report.Callers) != 1 || report.Callers[0].Caller != "+61437008930" {
		t.Errorf("report.Callers = %+v, want exactly one rollup for +61437008930", report.Callers)
	}
}

func TestBuildCallsReport_NewWithinSplitsInsideAndOutsideTail(t *testing.T) {
	windowEnd := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	records := []CallRecord{
		{ChannelID: "c-old", Caller: "+1old", DialedDID: "1", DIDLabel: "1", StartedAt: windowEnd.Add(-10 * time.Hour), TimeSource: "channel-id"},
		{ChannelID: "c-new", Caller: "+1new", DialedDID: "1", DIDLabel: "1", StartedAt: windowEnd.Add(-30 * time.Minute), TimeSource: "channel-id"},
	}
	report := BuildCallsReport(records, "", "", windowEnd, time.Hour)

	if len(report.NewCallers) != 1 || report.NewCallers[0].Caller != "+1new" {
		t.Errorf("report.NewCallers = %+v, want exactly one entry for +1new", report.NewCallers)
	}
	for _, c := range report.Callers {
		if c.Caller == "+1old" && c.IsNew {
			t.Error("caller +1old marked IsNew, want false (outside the 1h tail)")
		}
		if c.Caller == "+1new" && !c.IsNew {
			t.Error("caller +1new marked !IsNew, want true (inside the 1h tail)")
		}
	}
}

func TestBuildCallsReport_EmptyRecordsYieldsNonNilSlices(t *testing.T) {
	report := BuildCallsReport(nil, "", "", time.Now(), time.Hour)
	if report.Calls == nil || report.Callers == nil || report.Numbers == nil || report.NewCallers == nil {
		t.Errorf("BuildCallsReport(nil) produced a nil slice: %+v", report)
	}
}
