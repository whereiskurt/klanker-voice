// Package cmd — `kv telephony calls`: the identity-bearing sibling of
// `kv telephony stats` (quick task 260806-cm9). `stats` is deliberately
// caller-anonymous (D-09): it reports distinct-caller COUNTS from
// game_call_event teardown telemetry and structurally carries no caller
// field. `calls` is the opposite by design — raw caller numbers ARE the
// product here, answering "who called which number and when" for an
// operator during a live event. It joins TWO log-line families by ARI
// channel id (game_call_event teardown telemetry, which only exists since
// ~2026-07-28 and does not cover every call, plus the longer-lived
// on_stasis_start lines, which reach back to first deploy) so a call that
// produced a stasis line but died before teardown still shows up.
package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// --------------------------------------------------------------------------
// Constants.

// stasisMarker is the stable prefix every on_stasis_start log line starts
// with (controller.py:1111/1118/1128/1157/1181/1331 — the identity line, the
// dialed-DID line, the external-media-leg exclusion, the unexpected-context
// warning, the media/bridge-failure line, and the quota-denied line all
// share this prefix).
const stasisMarker = "on_stasis_start:"

// externalMediaMarker identifies the one on_stasis_start shape that is NOT a
// call: the internal UnicastRTP external-media leg re-entering the same
// Stasis app (controller.py:1109-1114). Excluded outright by parseStasisLine.
const externalMediaMarker = "ignoring external-media leg"

// callsQueryLimit bounds the Insights `limit` clause for `kv telephony
// calls` — higher than statsQueryLimit because this query spans two line
// families (up to two on_stasis_start lines plus one game_call_event line
// per real call) over a longer history.
const callsQueryLimit = 20000

// defaultCallsSince is `kv telephony calls`'s default --since window.
const defaultCallsSince = 24 * time.Hour

// defaultCallsNewWithin is `kv telephony calls`'s default --new-within tail
// for the "new caller" view.
const defaultCallsNewWithin = time.Hour

// noneToken is the literal placeholder controller.py logs when dialed_did
// resolution was attempted but came up empty (`dialed_did or '<none>'`,
// controller.py:1157).
const noneToken = "<none>"

// --------------------------------------------------------------------------
// CloudWatch Logs Insights query (reuses the offline-test seam +
// lifecycle extracted into runInsightsQueryString in telephony_stats.go).

// RunCallsInsightsQuery runs a Logs Insights query over [start, end] against
// logGroup, filtering to BOTH the on_stasis_start family and the
// game_call_event family, and returns every matched row's @message value via
// the same poll lifecycle RunInsightsQuery uses. The `fields @timestamp,
// @message` clause and the `sort @timestamp desc` are harmless here even
// though @timestamp is never a reliable event time (batched CloudWatch
// ingestion, see channelEpoch/loguruTimestamp below): extractMessages
// (telephony_stats.go) returns ONLY the @message field, so @timestamp is
// structurally unreachable by every parser in this file — the sort/fields
// clause merely bounds which lines the `limit` truncation keeps, it plays no
// role in this report's actual event ordering.
func RunCallsInsightsQuery(
	ctx context.Context,
	api logsInsightsAPI,
	logGroup string,
	start, end time.Time,
	pollInterval time.Duration,
) ([]string, error) {
	queryString := fmt.Sprintf(
		"fields @timestamp, @message | filter @message like /on_stasis_start:|%s / | sort @timestamp desc | limit %d",
		callEventMarker, callsQueryLimit,
	)
	return runInsightsQueryString(ctx, api, logGroup, start, end, pollInterval, queryString)
}

// --------------------------------------------------------------------------
// on_stasis_start line parsing.

// stasisRecord is what one on_stasis_start log line yields.
// ResolutionSeen is true whenever the line carried a dialed_did= key at all
// (even when its value is the noneToken placeholder) — that is what
// distinguishes an untagged-but-resolution-era call from a genuinely
// pre-resolution one (CONTEXT: two different unknowns, never merge them).
type stasisRecord struct {
	Channel        string
	Caller         string
	DialedDID      string
	ResolutionSeen bool
}

// parseStasisLine parses one on_stasis_start log line into a stasisRecord.
// Returns ok=false for: a non-stasis line, the external-media-leg exclusion
// line, and the three keyless on_stasis_start lines (the unexpected-context
// warning, the media/bridge-failure line, the quota-denied line) — none of
// those carry a caller or a dialed_did key, which is the single rule this
// function uses to exclude them, rather than a hand-maintained blocklist of
// message shapes.
//
// Only the `channel`, `caller`, and `dialed_did` tokens are read; `did=`
// (the sub-account name, not a dialed number), `exten=`, `cidname=`, and
// `sip_to=` are deliberately ignored — their values can be Python-repr
// quoted and contain spaces (e.g. cidname='<none>'), which is exactly why
// this scans recognized `key=value` tokens rather than parsing positionally.
func parseStasisLine(msg string) (stasisRecord, bool) {
	idx := strings.Index(msg, stasisMarker)
	if idx == -1 {
		return stasisRecord{}, false
	}
	if strings.Contains(msg, externalMediaMarker) {
		return stasisRecord{}, false
	}
	rest := msg[idx+len(stasisMarker):]

	var rec stasisRecord
	for _, tok := range strings.Fields(rest) {
		key, value, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		value = trimReprQuotes(value)
		switch key {
		case "channel":
			rec.Channel = value
		case "caller":
			rec.Caller = value
		case "dialed_did":
			rec.ResolutionSeen = true
			if value == noneToken {
				rec.DialedDID = ""
			} else {
				rec.DialedDID = value
			}
		}
	}

	if rec.Channel == "" {
		return stasisRecord{}, false
	}
	if rec.Caller == "" && !rec.ResolutionSeen {
		return stasisRecord{}, false
	}
	return rec, true
}

// trimReprQuotes strips one matched pair of surrounding `'` or `"` off value
// (Python's `!r` repr wraps a string value in one or the other). Values from
// controller.py's identity/dialed-DID lines are NOT repr-quoted
// (verified: controller.py:1128, 1157 — only exten/cidname/sip_to on the
// dialed-DID line get `!r`, and this parser never reads those keys), so this
// is a defensive no-op for the fields we do read — kept because it costs
// nothing and protects against a future logging-format tweak.
func trimReprQuotes(value string) string {
	if len(value) < 2 {
		return value
	}
	first, last := value[0], value[len(value)-1]
	if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
		return value[1 : len(value)-1]
	}
	return value
}

// --------------------------------------------------------------------------
// Event-time derivation — NEVER the CloudWatch ingestion @timestamp
// (CONTEXT's proven batching trap: three genuinely distinct calls ~18s/~29s
// apart all shared one @timestamp).

// minPlausibleEpoch/maxPlausibleEpoch bound channelEpoch's accepted range so
// a malformed channel id can never produce an absurd (1970 or year-3000)
// row. 1600000000 is 2020-09-13, well before this project existed.
const minPlausibleEpoch = 1600000000

// channelEpoch parses the epoch-seconds prefix of an ARI channel id (the
// substring before the first `.`, e.g. "1785303830.6" -> 1785303830) and
// returns it as a UTC time. Rejects anything outside
// [minPlausibleEpoch, now+24h] rather than returning a nonsense date for a
// malformed id.
func channelEpoch(channelID string) (time.Time, bool) {
	prefix, _, _ := strings.Cut(channelID, ".")
	if prefix == "" {
		return time.Time{}, false
	}
	sec, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	max := time.Now().Add(24 * time.Hour).Unix()
	if sec < minPlausibleEpoch || sec > max {
		return time.Time{}, false
	}
	return time.Unix(sec, 0).UTC(), true
}

// loguruTimestampLayout is loguru's DEFAULT sink format's leading timestamp
// (apps/voice/src installs no custom logger.add/logger.remove sink, so the
// default format is in force on every line): "YYYY-MM-DD HH:mm:ss.SSS | ...".
const loguruTimestampLayout = "2006-01-02 15:04:05.000"

// loguruTimestamp parses a leading loguru-default-format timestamp off msg,
// requiring the remainder to begin with " |" (the separator before the
// level field) so an arbitrary digit-leading message is never misread as a
// timestamp. Returns the parsed time in UTC.
func loguruTimestamp(msg string) (time.Time, bool) {
	if len(msg) < len(loguruTimestampLayout) {
		return time.Time{}, false
	}
	stamp := msg[:len(loguruTimestampLayout)]
	rest := msg[len(loguruTimestampLayout):]
	if !strings.HasPrefix(rest, " |") {
		return time.Time{}, false
	}
	t, err := time.Parse(loguruTimestampLayout, stamp)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// --------------------------------------------------------------------------
// DID -> tag labelling ([telephony.cid_prefix_dids], no new TOML dependency
// — reuses telephony.go's hand-rolled parseTOMLScalarLine).

// parseCIDPrefixDIDs reads the [telephony.cid_prefix_dids] table out of
// path (defaultTelephonyConfigPath in normal use) and returns it inverted:
// keyed by the bare-digit DID, valued by its tag (e.g. "KVD3234"). The
// report groups by DID, so DID-as-key is the useful shape here — the TOML
// itself is written tag-as-key ("KVD3234" = "7254043234").
//
// Best-effort throughout, mirroring readTelephonyGames: a missing or
// unreadable file returns a non-nil empty map, never an error.
//
// parseTOMLScalarLine (telephony.go) only strips quotes off the VALUE side
// of a "key = value" line; this table's keys are ALSO quoted
// ("KVD3234" = "7254043234"), so this function additionally trims a
// matched pair of surrounding double quotes off the key.
func parseCIDPrefixDIDs(path string) map[string]string {
	tagByDID := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return tagByDID
	}
	defer f.Close()

	inBlock := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if line == "[telephony.cid_prefix_dids]" || strings.HasPrefix(line, "[telephony.cid_prefix_dids]") {
				inBlock = true
				continue
			}
			if inBlock {
				break
			}
			continue
		}
		if !inBlock {
			continue
		}
		key, value, ok := parseTOMLScalarLine(line)
		if !ok {
			continue
		}
		tag := strings.Trim(key, `"`)
		if value == "" || tag == "" {
			continue
		}
		tagByDID[value] = tag
	}
	_ = scanner.Err()
	return tagByDID
}
