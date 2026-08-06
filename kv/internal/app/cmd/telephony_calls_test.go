package cmd

import (
	"context"
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
