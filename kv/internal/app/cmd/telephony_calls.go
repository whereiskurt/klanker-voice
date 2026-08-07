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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
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

// --------------------------------------------------------------------------
// DID-label buckets (resolution-era untagged vs. genuinely pre-resolution —
// two different unknowns, CONTEXT is explicit these must never be merged or
// given an invented number).

// untaggedDIDLabel is the bucket for a resolution-era call (a dialed_did key
// was present on its on_stasis_start line, or its teardown event carried a
// non-empty dialed_did) whose DID nonetheless resolved empty — in practice
// the 613/347 concierge lines, which have no callerid_prefix configured.
const untaggedDIDLabel = "concierge (untagged DIDs)"

// preResolutionDIDLabel is the bucket for a call that predates the
// Approach-C CID-prefix resolution deploy (~2026-07-17) entirely — its
// on_stasis_start line never carried a dialed_did key at all, so nothing
// was even attempted.
const preResolutionDIDLabel = "unknown (pre-resolution)"

// --------------------------------------------------------------------------
// Report types.

// CallRecord is one joined call, keyed by ARI channel id. Unlike DIDStats
// (telephony_stats.go), this type deliberately DOES carry the caller
// number — `stats` omits it structurally by design (D-09); `calls` is the
// command where the operator gets identity, so the omission there is a
// per-command contract, not a package-wide rule.
type CallRecord struct {
	ChannelID       string    `json:"channelId"`
	Caller          string    `json:"caller"`
	DialedDID       string    `json:"dialedDid"`
	DIDLabel        string    `json:"didLabel"`
	Outcome         string    `json:"outcome"`
	TimeSource      string    `json:"timeSource"`
	StartedAt       time.Time `json:"startedAt"`
	DigitsEntered   int       `json:"digitsEntered"`
	WordsHeard      int       `json:"wordsHeard"`
	DurationSeconds float64   `json:"durationSeconds"`
	HasTeardown     bool      `json:"hasTeardown"`
}

// DIDCount is one DID-label's call count within a CallerRollup's PerDID
// breakdown.
type DIDCount struct {
	DIDLabel string `json:"didLabel"`
	Calls    int    `json:"calls"`
}

// CallerRollup is one caller's row in the per-caller view (and, filtered to
// IsNew, the new-caller view).
type CallerRollup struct {
	Caller    string     `json:"caller"`
	Calls     int        `json:"calls"`
	FirstSeen time.Time  `json:"firstSeen"`
	LastSeen  time.Time  `json:"lastSeen"`
	PerDID    []DIDCount `json:"perDid"`
	IsNew     bool       `json:"isNew"`
}

// NumberRollup is one DID's row in the per-number view.
type NumberRollup struct {
	DIDLabel        string    `json:"didLabel"`
	DialedDID       string    `json:"dialedDid"`
	Calls           int       `json:"calls"`
	DistinctCallers int       `json:"distinctCallers"`
	FirstSeen       time.Time `json:"firstSeen"`
	LastSeen        time.Time `json:"lastSeen"`
}

// CallsTotals is the totals row shared by every view.
type CallsTotals struct {
	Calls           int `json:"calls"`
	DistinctCallers int `json:"distinctCallers"`
	DistinctDIDs    int `json:"distinctDids"`
	WithoutTeardown int `json:"withoutTeardown"`
}

// TelephonyCallsReport is the full `kv telephony calls` output shape.
// --json always encodes the WHOLE report regardless of which view is being
// rendered, so a script never has to re-run the command per view.
type TelephonyCallsReport struct {
	LogGroup     string         `json:"logGroup"`
	Since        string         `json:"since"`
	NewWithin    string         `json:"newWithin"`
	View         string         `json:"view"`
	DIDFilter    string         `json:"didFilter,omitempty"`
	CallerFilter string         `json:"callerFilter,omitempty"`
	Calls        []CallRecord   `json:"calls"`
	Callers      []CallerRollup `json:"callers"`
	Numbers      []NumberRollup `json:"numbers"`
	NewCallers   []CallerRollup `json:"newCallers"`
	Totals       CallsTotals    `json:"totals"`
}

// --------------------------------------------------------------------------
// Joining.

// callAccum is JoinCallRecords' per-channel scratch state — the CallRecord
// under construction, plus the two pieces of state that decide its
// TimeSource/DIDLabel once every message has been folded in.
type callAccum struct {
	rec            CallRecord
	resolutionSeen bool
	loguruTimes    []time.Time
}

// JoinCallRecords merges every on_stasis_start line and every
// game_call_event line into one CallRecord per ARI channel id — the join
// key proven exact by controller.py's `call_id=sip_channel_id` at every
// game_call_event emission site. Returns records sorted ascending by
// StartedAt (tie-broken by ChannelID) so output is deterministic; a record
// whose time could not be derived at all sorts first (its zero StartedAt is
// earlier than every real time) and is still included, never dropped.
func JoinCallRecords(messages []string, tagByDID map[string]string) []CallRecord {
	byChannel := map[string]*callAccum{}
	var order []string
	getOrCreate := func(channel string) *callAccum {
		a, ok := byChannel[channel]
		if !ok {
			a = &callAccum{rec: CallRecord{ChannelID: channel}}
			byChannel[channel] = a
			order = append(order, channel)
		}
		return a
	}

	for _, msg := range messages {
		var channel string

		if rec, ok := parseStasisLine(msg); ok {
			channel = rec.Channel
			a := getOrCreate(channel)
			if rec.Caller != "" {
				a.rec.Caller = rec.Caller
			}
			if rec.ResolutionSeen {
				a.resolutionSeen = true
				if rec.DialedDID != "" {
					a.rec.DialedDID = rec.DialedDID
				}
			}
		}

		// Reuse the EXISTING game_call_event decoder (telephony_stats.go)
		// so there is exactly one game_call_event JSON parser in the
		// package — pass a one-element slice since this file processes
		// one raw log message at a time.
		if events := ParseCallEvents([]string{msg}); len(events) == 1 {
			e := events[0]
			channel = e.CallID
			a := getOrCreate(channel)
			a.rec.HasTeardown = true
			a.rec.Outcome = e.Outcome
			a.rec.DigitsEntered = e.DigitsEntered
			a.rec.WordsHeard = e.WordsHeard
			a.rec.DurationSeconds = e.DurationSeconds
			if a.rec.Caller == "" && e.CallerID != "" {
				a.rec.Caller = e.CallerID
			}
			if a.rec.DialedDID == "" && e.DialedDID != "" {
				a.rec.DialedDID = e.DialedDID
				a.resolutionSeen = true
			}
		}

		if channel != "" {
			if t, ok := loguruTimestamp(msg); ok {
				a := getOrCreate(channel)
				a.loguruTimes = append(a.loguruTimes, t)
			}
		}
	}

	records := make([]CallRecord, 0, len(order))
	for _, channel := range order {
		a := byChannel[channel]
		rec := a.rec
		if t, ok := channelEpoch(channel); ok {
			rec.StartedAt = t
			rec.TimeSource = "channel-id"
		} else if earliest, ok := earliestTime(a.loguruTimes); ok {
			rec.StartedAt = earliest
			rec.TimeSource = "log-timestamp"
		} else {
			rec.TimeSource = "unknown"
		}
		rec.DIDLabel = didLabel(rec.DialedDID, a.resolutionSeen, tagByDID)
		records = append(records, rec)
	}

	sort.Slice(records, func(i, j int) bool {
		if !records[i].StartedAt.Equal(records[j].StartedAt) {
			return records[i].StartedAt.Before(records[j].StartedAt)
		}
		return records[i].ChannelID < records[j].ChannelID
	})
	return records
}

// earliestTime returns the earliest of times, or ok=false for an empty
// slice.
func earliestTime(times []time.Time) (time.Time, bool) {
	if len(times) == 0 {
		return time.Time{}, false
	}
	earliest := times[0]
	for _, t := range times[1:] {
		if t.Before(earliest) {
			earliest = t
		}
	}
	return earliest, true
}

// didLabel resolves a CallRecord's DID display label: a non-empty
// dialedDID renders as its bare digits, or "<digits> (<tag>)" when
// tagByDID has an entry for it; an empty dialedDID with resolutionSeen
// renders untaggedDIDLabel (resolution was attempted and came up empty);
// an empty dialedDID without it renders preResolutionDIDLabel (resolution
// was never attempted on this call at all — CONTEXT: two different
// unknowns, never merged).
func didLabel(dialedDID string, resolutionSeen bool, tagByDID map[string]string) string {
	if dialedDID != "" {
		if tag, ok := tagByDID[dialedDID]; ok {
			return fmt.Sprintf("%s (%s)", dialedDID, tag)
		}
		return dialedDID
	}
	if resolutionSeen {
		return untaggedDIDLabel
	}
	return preResolutionDIDLabel
}

// --------------------------------------------------------------------------
// Report building.

// BuildCallsReport applies didFilter/callerFilter (each narrowing every
// view and the totals consistently) and groups records into all four
// views. Always returns non-nil slices so the --json encoding is stable
// (an empty view still encodes as `[]`, never `null`).
func BuildCallsReport(records []CallRecord, didFilter, callerFilter string, windowEnd time.Time, newWithin time.Duration) TelephonyCallsReport {
	filtered := make([]CallRecord, 0, len(records))
	for _, r := range records {
		if didFilter != "" && r.DialedDID != didFilter {
			continue
		}
		if callerFilter != "" && r.Caller != callerFilter {
			continue
		}
		filtered = append(filtered, r)
	}

	callerGroups := map[string][]CallRecord{}
	var callerOrder []string
	numberGroups := map[string][]CallRecord{}
	var numberOrder []string
	distinctCallers := map[string]struct{}{}
	withoutTeardown := 0
	for _, r := range filtered {
		if _, ok := callerGroups[r.Caller]; !ok {
			callerOrder = append(callerOrder, r.Caller)
		}
		callerGroups[r.Caller] = append(callerGroups[r.Caller], r)

		if _, ok := numberGroups[r.DIDLabel]; !ok {
			numberOrder = append(numberOrder, r.DIDLabel)
		}
		numberGroups[r.DIDLabel] = append(numberGroups[r.DIDLabel], r)

		distinctCallers[r.Caller] = struct{}{}
		if !r.HasTeardown {
			withoutTeardown++
		}
	}

	callers := make([]CallerRollup, 0, len(callerOrder))
	for _, caller := range callerOrder {
		callers = append(callers, buildCallerRollup(caller, callerGroups[caller], windowEnd, newWithin))
	}
	sort.Slice(callers, func(i, j int) bool {
		if callers[i].Calls != callers[j].Calls {
			return callers[i].Calls > callers[j].Calls
		}
		return callers[i].Caller < callers[j].Caller
	})

	numbers := make([]NumberRollup, 0, len(numberOrder))
	for _, label := range numberOrder {
		numbers = append(numbers, buildNumberRollup(label, numberGroups[label]))
	}
	sort.Slice(numbers, func(i, j int) bool {
		if numbers[i].Calls != numbers[j].Calls {
			return numbers[i].Calls > numbers[j].Calls
		}
		return numbers[i].DIDLabel < numbers[j].DIDLabel
	})

	newCallers := make([]CallerRollup, 0)
	for _, c := range callers {
		if c.IsNew {
			newCallers = append(newCallers, c)
		}
	}

	return TelephonyCallsReport{
		DIDFilter:    didFilter,
		CallerFilter: callerFilter,
		Calls:        filtered,
		Callers:      callers,
		Numbers:      numbers,
		NewCallers:   newCallers,
		Totals: CallsTotals{
			Calls:           len(filtered),
			DistinctCallers: len(distinctCallers),
			DistinctDIDs:    len(numberGroups),
			WithoutTeardown: withoutTeardown,
		},
	}
}

// buildCallerRollup aggregates one caller's records into a CallerRollup:
// call count, first/last seen (over records with a known StartedAt only),
// and a per-DID breakdown sorted by count descending then label ascending.
// IsNew is true only when the caller has a known first-seen time AND that
// time falls within the newWithin tail of windowEnd.
func buildCallerRollup(caller string, group []CallRecord, windowEnd time.Time, newWithin time.Duration) CallerRollup {
	rollup := CallerRollup{Caller: caller, Calls: len(group), PerDID: []DIDCount{}}

	didCounts := map[string]int{}
	var didOrder []string
	var first, last time.Time
	haveKnownTime := false
	for _, r := range group {
		if _, ok := didCounts[r.DIDLabel]; !ok {
			didOrder = append(didOrder, r.DIDLabel)
		}
		didCounts[r.DIDLabel]++

		if !r.StartedAt.IsZero() {
			if !haveKnownTime || r.StartedAt.Before(first) {
				first = r.StartedAt
			}
			if !haveKnownTime || r.StartedAt.After(last) {
				last = r.StartedAt
			}
			haveKnownTime = true
		}
	}
	rollup.FirstSeen = first
	rollup.LastSeen = last
	for _, label := range didOrder {
		rollup.PerDID = append(rollup.PerDID, DIDCount{DIDLabel: label, Calls: didCounts[label]})
	}
	sort.Slice(rollup.PerDID, func(i, j int) bool {
		if rollup.PerDID[i].Calls != rollup.PerDID[j].Calls {
			return rollup.PerDID[i].Calls > rollup.PerDID[j].Calls
		}
		return rollup.PerDID[i].DIDLabel < rollup.PerDID[j].DIDLabel
	})

	rollup.IsNew = haveKnownTime && rollup.FirstSeen.After(windowEnd.Add(-newWithin))
	return rollup
}

// buildNumberRollup aggregates one DID label's records into a NumberRollup:
// call count, distinct-caller count, and active range (over records with a
// known StartedAt only). DialedDID is carried from the group's first
// record — every record in a DIDLabel group shares the same raw DialedDID,
// since DIDLabel is itself derived from DialedDID (+ resolution state), so
// this is empty for both the pre-resolution and untagged buckets, never an
// invented number.
func buildNumberRollup(label string, group []CallRecord) NumberRollup {
	rollup := NumberRollup{DIDLabel: label, Calls: len(group)}
	if len(group) > 0 {
		rollup.DialedDID = group[0].DialedDID
	}

	callers := map[string]struct{}{}
	var first, last time.Time
	haveKnownTime := false
	for _, r := range group {
		if r.Caller != "" {
			callers[r.Caller] = struct{}{}
		}
		if !r.StartedAt.IsZero() {
			if !haveKnownTime || r.StartedAt.Before(first) {
				first = r.StartedAt
			}
			if !haveKnownTime || r.StartedAt.After(last) {
				last = r.StartedAt
			}
			haveKnownTime = true
		}
	}
	rollup.DistinctCallers = len(callers)
	rollup.FirstSeen = first
	rollup.LastSeen = last
	return rollup
}

// --------------------------------------------------------------------------
// Rendering.

// callsViewAll renders every view in one pass (the default when --view=all).
const callsViewAll = "all"

// callsValidViews is the allow-list newTelephonyCallsCmd's RunE validates
// --view against BEFORE making any AWS call.
var callsValidViews = []string{"callers", "calls", "numbers", "new", callsViewAll}

// formatCallTime formats t for table/heading display: "2006-01-02
// 15:04:05Z" in UTC, or "-" for the zero (unknown) time.
func formatCallTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04:05") + "Z"
}

// printTelephonyCalls renders report. When asJSON, the WHOLE report is
// encoded with a two-space indent regardless of view — every view's
// underlying data ships in one document, deliberately, so a script never
// has to re-run the command once per view. Otherwise only the selected
// view renders (or all four, in order callers/calls/numbers/new, when
// view=="all"), each as a tabwriter table matching printTelephonyStats'
// settings under a short section heading.
func printTelephonyCalls(c *cobra.Command, report TelephonyCallsReport, view string, asJSON bool) error {
	out := c.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	if report.Totals.Calls == 0 {
		fmt.Fprintf(
			out,
			"No calls found (log group %s, window %s%s%s)\n",
			report.LogGroup, report.Since, didFilterSuffix(report.DIDFilter), callerFilterSuffix(report.CallerFilter),
		)
		return nil
	}

	renderCallers := view == "callers" || view == callsViewAll
	renderCalls := view == "calls" || view == callsViewAll
	renderNumbers := view == "numbers" || view == callsViewAll
	renderNew := view == "new" || view == callsViewAll

	first := true
	sectionBreak := func() {
		if !first {
			fmt.Fprintln(out)
		}
		first = false
	}

	if renderCallers {
		sectionBreak()
		fmt.Fprintln(out, "Callers:")
		printCallerRollupTable(out, report.Callers)
	}
	if renderCalls {
		sectionBreak()
		fmt.Fprintln(out, "Calls:")
		printCallLogTable(out, report.Calls)
	}
	if renderNumbers {
		sectionBreak()
		fmt.Fprintln(out, "Numbers:")
		printNumberRollupTable(out, report.Numbers)
	}
	if renderNew {
		sectionBreak()
		fmt.Fprintf(
			out,
			"New callers (first seen within %s of the %s queried window -- a wider --since gives a stronger baseline):\n",
			report.NewWithin, report.Since,
		)
		printCallerRollupTable(out, report.NewCallers)
	}
	return nil
}

func printCallerRollupTable(out io.Writer, rollups []CallerRollup) {
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "CALLER\tCALLS\tFIRST SEEN\tLAST SEEN\tDIDS")
	for _, r := range rollups {
		fmt.Fprintf(
			w, "%s\t%d\t%s\t%s\t%s\n",
			r.Caller, r.Calls, formatCallTime(r.FirstSeen), formatCallTime(r.LastSeen), formatDIDCounts(r.PerDID),
		)
	}
	w.Flush()
}

func printCallLogTable(out io.Writer, calls []CallRecord) {
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "WHEN\tSRC\tCALLER\tDID\tOUTCOME\tDIGITS\tWORDS\tDURATION")
	for _, r := range calls {
		outcome := r.Outcome
		duration := fmt.Sprintf("%.1f", r.DurationSeconds)
		if !r.HasTeardown {
			outcome = "(no teardown event)"
			duration = "-"
		}
		fmt.Fprintf(
			w, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
			formatCallTime(r.StartedAt), r.TimeSource, r.Caller, r.DIDLabel, outcome, r.DigitsEntered, r.WordsHeard, duration,
		)
	}
	w.Flush()
}

func printNumberRollupTable(out io.Writer, numbers []NumberRollup) {
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "DID\tCALLS\tCALLERS\tFIRST SEEN\tLAST SEEN")
	for _, n := range numbers {
		fmt.Fprintf(
			w, "%s\t%d\t%d\t%s\t%s\n",
			n.DIDLabel, n.Calls, n.DistinctCallers, formatCallTime(n.FirstSeen), formatCallTime(n.LastSeen),
		)
	}
	w.Flush()
}

// formatDIDCounts renders a CallerRollup's PerDID breakdown as space-joined
// "label=count" pairs (already sorted by BuildCallsReport).
func formatDIDCounts(perDID []DIDCount) string {
	if len(perDID) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(perDID))
	for _, d := range perDID {
		parts = append(parts, fmt.Sprintf("%s=%d", d.DIDLabel, d.Calls))
	}
	return strings.Join(parts, " ")
}

func callerFilterSuffix(callerFilter string) string {
	if callerFilter == "" {
		return ""
	}
	return fmt.Sprintf(", caller=%s", callerFilter)
}

// --------------------------------------------------------------------------
// Command registration.

// newTelephonyCallsCmd builds the "kv telephony calls" subcommand,
// registered onto telephonyCmd by NewTelephonyCmd (telephony.go). Quick
// task 260806-cm9.
func newTelephonyCallsCmd(cfg *Config) *cobra.Command {
	var (
		since      time.Duration
		newWithin  time.Duration
		did        string
		caller     string
		view       string
		asJSON     bool
		logGroup   string
		configPath string
	)
	calls := &cobra.Command{
		Use:   "calls",
		Short: "Operator call report -- who called which number and when (raw caller numbers)",
		Long: "kv telephony calls is the identity-bearing sibling of `kv telephony\n" +
			"stats`: `stats` is the caller-anonymous rollup that reports distinct-\n" +
			"caller COUNTS from game_call_event teardown telemetry and never a raw\n" +
			"number; `calls` always shows raw caller numbers -- there is no flag\n" +
			"to hide them. It joins telephony-edge's on_stasis_start log lines\n" +
			"(full history, back to first deploy) with game_call_event teardown\n" +
			"lines (only since ~2026-07-28, and not on every call) by ARI channel\n" +
			"id, so history reaches further back than teardown telemetry alone\n" +
			"would allow, and a call that never reached teardown still appears.\n\n" +
			"Four views, selected with --view:\n" +
			"  callers  per-caller rollup: count, first/last seen, DID breakdown (default)\n" +
			"  calls    chronological per-call log: when, caller, DID, outcome, digits/words, duration\n" +
			"  numbers  per-number rollup: call count, distinct callers, active range\n" +
			"  new      callers whose first-seen falls inside --new-within of the queried window\n" +
			"  all      renders all four in one pass\n\n" +
			"--json always emits the full result set (every view's data), regardless of --view.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if !isValidCallsView(view) {
				return fmt.Errorf("invalid --view %q: must be one of %s", view, strings.Join(callsValidViews, ", "))
			}
			client, err := cfg.CloudWatchLogsClient(c.Context())
			if err != nil {
				return err
			}
			end := time.Now()
			start := end.Add(-since)
			messages, err := RunCallsInsightsQuery(c.Context(), client, logGroup, start, end, defaultStatsPollInterval)
			if err != nil {
				return err
			}
			tagByDID := parseCIDPrefixDIDs(configPath)
			records := JoinCallRecords(messages, tagByDID)
			report := BuildCallsReport(records, did, caller, end, newWithin)
			report.LogGroup = logGroup
			report.Since = since.String()
			report.NewWithin = newWithin.String()
			report.View = view
			return printTelephonyCalls(c, report, view, asJSON)
		},
	}
	calls.Flags().DurationVar(&since, "since", defaultCallsSince, "how far back to query (e.g. 24h, 90m)")
	calls.Flags().DurationVar(&newWithin, "new-within", defaultCallsNewWithin, "how recent a caller's first-seen must be to count as \"new\" (--view new)")
	calls.Flags().StringVar(&did, "did", "", "filter to a single dialed DID")
	calls.Flags().StringVar(&caller, "caller", "", "filter to a single raw caller number")
	calls.Flags().StringVar(&view, "view", "callers", "which view to render: callers, calls, numbers, new, all")
	calls.Flags().BoolVar(&asJSON, "json", false, "output as JSON (always emits every view's data)")
	calls.Flags().StringVar(&logGroup, "log-group", defaultTelephonyLogGroup, "CloudWatch Logs group to query")
	calls.Flags().StringVar(&configPath, "config", defaultTelephonyConfigPath, "path to the telephony pipeline TOML config (for the [telephony.cid_prefix_dids] DID labels)")
	return calls
}

func isValidCallsView(view string) bool {
	for _, v := range callsValidViews {
		if v == view {
			return true
		}
	}
	return false
}
