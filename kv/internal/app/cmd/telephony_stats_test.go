package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/spf13/cobra"
)

// --------------------------------------------------------------------------
// Fakes — all offline, no live AWS call, no network, mirroring
// telephony_test.go's fakeTelephonyScanClient / fakeSSMGetParameterClient
// style.

// fakeLogsInsightsClient implements logsInsightsAPI over a canned sequence
// of GetQueryResults statuses (or a configurable error at either call) — no
// real CloudWatch Logs connection is ever made.
type fakeLogsInsightsClient struct {
	startQueryErr error
	queryID       string

	// statuses is consumed one entry per GetQueryResults call; the last
	// entry repeats if more calls arrive than entries provided.
	statuses []types.QueryStatus
	messages []string
	getErr   error

	startQueryCalls int
	getResultsCalls int
}

func (f *fakeLogsInsightsClient) StartQuery(ctx context.Context, params *cloudwatchlogs.StartQueryInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.StartQueryOutput, error) {
	f.startQueryCalls++
	if f.startQueryErr != nil {
		return nil, f.startQueryErr
	}
	id := f.queryID
	if id == "" {
		id = "query-1"
	}
	return &cloudwatchlogs.StartQueryOutput{QueryId: aws.String(id)}, nil
}

func (f *fakeLogsInsightsClient) GetQueryResults(ctx context.Context, params *cloudwatchlogs.GetQueryResultsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetQueryResultsOutput, error) {
	idx := f.getResultsCalls
	f.getResultsCalls++
	if f.getErr != nil {
		return nil, f.getErr
	}
	status := types.QueryStatusComplete
	if len(f.statuses) > 0 {
		if idx < len(f.statuses) {
			status = f.statuses[idx]
		} else {
			status = f.statuses[len(f.statuses)-1]
		}
	}
	out := &cloudwatchlogs.GetQueryResultsOutput{Status: status}
	if status == types.QueryStatusComplete {
		rows := make([][]types.ResultField, 0, len(f.messages))
		for _, msg := range f.messages {
			rows = append(rows, []types.ResultField{
				{Field: aws.String("@timestamp"), Value: aws.String("2026-07-27T00:00:00.000Z")},
				{Field: aws.String("@message"), Value: aws.String(msg)},
			})
		}
		out.Results = rows
	}
	return out, nil
}

func fsec(v float64) *float64 { return &v }

// --------------------------------------------------------------------------
// Test 1: ParseCallEvents skips non-marker and malformed-marker lines.

func TestParseCallEvents_SkipsNonMarkerAndMalformedLines(t *testing.T) {
	messages := []string{
		`game_call_event {"call_id":"chan-1","dialed_did":"7254043234","caller_id":"+15550000123","otp_only":false,"outcome":"concierge_unlock_dtmf","digits_entered":4,"words_heard":0,"seconds_to_outcome":3.2,"duration_seconds":45.0}`,
		`_close_active_call: channel=chan-1 reason='ari channel destroyed'`,
		`game_call_event {this is not valid json`,
	}

	events := ParseCallEvents(messages)

	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1 (got %+v)", len(events), events)
	}
	if events[0].CallID != "chan-1" || events[0].Outcome != "concierge_unlock_dtmf" {
		t.Errorf("events[0] = %+v, want call_id=chan-1 outcome=concierge_unlock_dtmf", events[0])
	}
	if events[0].DigitsEntered != 4 {
		t.Errorf("DigitsEntered = %d, want 4", events[0].DigitsEntered)
	}
	if events[0].SecondsToOutcome == nil || *events[0].SecondsToOutcome != 3.2 {
		t.Errorf("SecondsToOutcome = %v, want 3.2", events[0].SecondsToOutcome)
	}
}

// --------------------------------------------------------------------------
// Test 2: AggregateCallStats over a two-DID fixture with known values.

func twoDIDFixture() []CallEvent {
	return []CallEvent{
		// DID A: three calls, two distinct callers (caller-1 appears twice).
		{CallID: "a1", DialedDID: "7254043234", CallerID: "caller-1", Outcome: "concierge_unlock_dtmf", SecondsToOutcome: fsec(10.0), DurationSeconds: 60.0},
		{CallID: "a2", DialedDID: "7254043234", CallerID: "caller-1", Outcome: "gate_timeout", SecondsToOutcome: nil, DurationSeconds: 30.0},
		{CallID: "a3", DialedDID: "7254043234", CallerID: "caller-2", Outcome: "concierge_unlock_dtmf", SecondsToOutcome: fsec(20.0), DurationSeconds: 90.0},
		// DID B: one call.
		{CallID: "b1", DialedDID: "7254048283", CallerID: "caller-3", Outcome: "announcement_code", SecondsToOutcome: fsec(5.0), DurationSeconds: 40.0},
	}
}

func TestAggregateCallStats_TwoDIDFixtureKnownValues(t *testing.T) {
	report := AggregateCallStats(twoDIDFixture(), "")

	if len(report.PerDID) != 2 {
		t.Fatalf("len(PerDID) = %d, want 2 (got %+v)", len(report.PerDID), report.PerDID)
	}
	// Sorted ascending by DID: "7254043234" < "7254048283".
	a, b := report.PerDID[0], report.PerDID[1]
	if a.DID != "7254043234" || b.DID != "7254048283" {
		t.Fatalf("PerDID DIDs = [%s, %s], want [7254043234, 7254048283]", a.DID, b.DID)
	}

	if a.Calls != 3 {
		t.Errorf("A.Calls = %d, want 3", a.Calls)
	}
	if a.DistinctCallers != 2 {
		t.Errorf("A.DistinctCallers = %d, want 2 (caller-1 appears twice)", a.DistinctCallers)
	}
	if a.Outcomes["concierge_unlock_dtmf"] != 2 || a.Outcomes["gate_timeout"] != 1 {
		t.Errorf("A.Outcomes = %+v, want concierge_unlock_dtmf=2 gate_timeout=1", a.Outcomes)
	}
	// Non-null seconds_to_outcome values are {10.0, 20.0} -> median 15.0, max 20.0.
	if a.MedianSecondsToOutcome == nil || *a.MedianSecondsToOutcome != 15.0 {
		t.Errorf("A.MedianSecondsToOutcome = %v, want 15.0", a.MedianSecondsToOutcome)
	}
	if a.MaxSecondsToOutcome == nil || *a.MaxSecondsToOutcome != 20.0 {
		t.Errorf("A.MaxSecondsToOutcome = %v, want 20.0", a.MaxSecondsToOutcome)
	}
	// durations {60.0, 30.0, 90.0} -> median 60.0.
	if a.MedianDurationSeconds != 60.0 {
		t.Errorf("A.MedianDurationSeconds = %v, want 60.0", a.MedianDurationSeconds)
	}

	if b.Calls != 1 || b.DistinctCallers != 1 {
		t.Errorf("B = %+v, want Calls=1 DistinctCallers=1", b)
	}

	if report.Totals.Calls != 4 {
		t.Errorf("Totals.Calls = %d, want 4", report.Totals.Calls)
	}
	if report.Totals.DistinctCallers != 3 {
		t.Errorf("Totals.DistinctCallers = %d, want 3", report.Totals.DistinctCallers)
	}
}

// --------------------------------------------------------------------------
// Test 3: an empty dialed_did lands under "(unresolved)".

func TestAggregateCallStats_EmptyDialedDIDLandsUnderUnresolvedLabel(t *testing.T) {
	events := []CallEvent{
		{CallID: "u1", DialedDID: "", CallerID: "caller-1", Outcome: "early_hangup", DurationSeconds: 5.0},
	}
	report := AggregateCallStats(events, "")

	if len(report.PerDID) != 1 {
		t.Fatalf("len(PerDID) = %d, want 1", len(report.PerDID))
	}
	if report.PerDID[0].DID != unresolvedDIDLabel {
		t.Errorf("PerDID[0].DID = %q, want %q", report.PerDID[0].DID, unresolvedDIDLabel)
	}
}

// --------------------------------------------------------------------------
// Test 4: --did filter narrows both the per-DID rows and the totals.

func TestAggregateCallStats_DIDFilterNarrowsRowsAndTotals(t *testing.T) {
	report := AggregateCallStats(twoDIDFixture(), "7254048283")

	if len(report.PerDID) != 1 {
		t.Fatalf("len(PerDID) = %d, want 1 (got %+v)", len(report.PerDID), report.PerDID)
	}
	if report.PerDID[0].DID != "7254048283" {
		t.Errorf("PerDID[0].DID = %q, want 7254048283", report.PerDID[0].DID)
	}
	if report.Totals.Calls != 1 {
		t.Errorf("Totals.Calls = %d, want 1", report.Totals.Calls)
	}
	if report.DIDFilter != "7254048283" {
		t.Errorf("DIDFilter = %q, want 7254048283", report.DIDFilter)
	}
}

// --------------------------------------------------------------------------
// Test 5: the D-09 hard gate — neither the table nor --json ever contains a
// caller phone number, while distinctCallers stays correct.

func TestPrintTelephonyStats_NeverRendersCallerNumber(t *testing.T) {
	const callerNumber = "+15550000123"
	events := []CallEvent{
		{CallID: "c1", DialedDID: "7254043234", CallerID: callerNumber, Outcome: "concierge_unlock_dtmf", SecondsToOutcome: fsec(4.0), DurationSeconds: 30.0},
		{CallID: "c2", DialedDID: "7254043234", CallerID: callerNumber, Outcome: "early_hangup", DurationSeconds: 10.0},
	}
	report := AggregateCallStats(events, "")
	report.LogGroup = "/ecs/telephony-edge-telephony-edge-use1-kmv"
	report.Since = "24h0m0s"

	if len(report.PerDID) != 1 || report.PerDID[0].DistinctCallers != 1 {
		t.Fatalf("PerDID = %+v, want one row with DistinctCallers=1", report.PerDID)
	}

	var tableBuf, jsonBuf bytes.Buffer

	tableCmd := &cobra.Command{}
	tableCmd.SetOut(&tableBuf)
	if err := printTelephonyStats(tableCmd, report, false); err != nil {
		t.Fatalf("printTelephonyStats(table) error: %v", err)
	}

	jsonCmd := &cobra.Command{}
	jsonCmd.SetOut(&jsonBuf)
	if err := printTelephonyStats(jsonCmd, report, true); err != nil {
		t.Fatalf("printTelephonyStats(json) error: %v", err)
	}

	for name, buf := range map[string]*bytes.Buffer{"table": &tableBuf, "json": &jsonBuf} {
		text := buf.String()
		if strings.Contains(text, callerNumber) {
			t.Errorf("%s output contains the raw caller number %q:\n%s", name, callerNumber, text)
		}
		if strings.Contains(text, "5550000123") {
			t.Errorf("%s output contains the bare caller digits:\n%s", name, text)
		}
	}

	// The JSON output must also be structurally free of any caller-like key.
	var decoded map[string]any
	if err := json.Unmarshal(jsonBuf.Bytes(), &decoded); err != nil {
		t.Fatalf("json.Unmarshal(--json output): %v", err)
	}
	if _, ok := decoded["callerId"]; ok {
		t.Error("--json output has a top-level callerId key, want none")
	}
}

// --------------------------------------------------------------------------
// Test 6: RunInsightsQuery polls through Running -> Complete, and surfaces a
// Failed status as an error.

func TestRunInsightsQuery_PollsThroughRunningToComplete(t *testing.T) {
	fake := &fakeLogsInsightsClient{
		statuses: []types.QueryStatus{types.QueryStatusRunning, types.QueryStatusComplete},
		messages: []string{`game_call_event {"call_id":"c1","dialed_did":"","caller_id":"","otp_only":false,"outcome":"early_hangup","digits_entered":0,"words_heard":0,"seconds_to_outcome":null,"duration_seconds":1.0}`},
	}

	messages, err := RunInsightsQuery(context.Background(), fake, "/ecs/telephony-edge", time.Now(), time.Now(), 0)
	if err != nil {
		t.Fatalf("RunInsightsQuery error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1 (got %+v)", len(messages), messages)
	}
	if fake.getResultsCalls < 2 {
		t.Errorf("GetQueryResults called %d times, want at least 2 (Running then Complete)", fake.getResultsCalls)
	}
}

func TestRunInsightsQuery_FailedStatusReturnsError(t *testing.T) {
	fake := &fakeLogsInsightsClient{
		statuses: []types.QueryStatus{types.QueryStatusFailed},
	}

	_, err := RunInsightsQuery(context.Background(), fake, "/ecs/telephony-edge", time.Now(), time.Now(), 0)
	if err == nil {
		t.Fatal("RunInsightsQuery error = nil, want a non-nil error for a Failed query status")
	}
}

func TestRunInsightsQuery_StartQueryErrorPropagates(t *testing.T) {
	fake := &fakeLogsInsightsClient{startQueryErr: errors.New("throttled")}

	_, err := RunInsightsQuery(context.Background(), fake, "/ecs/telephony-edge", time.Now(), time.Now(), 0)
	if err == nil {
		t.Fatal("RunInsightsQuery error = nil, want a non-nil error when StartQuery fails")
	}
}

// --------------------------------------------------------------------------
// Test 7: `stats` is registered under `kv telephony` and its help lists the
// four flags (mirrors TestTelephonyCmdHelpListsFlags / TestTelephonyRootRegistersCmd).

func TestTelephonyStatsCmdRegisteredWithExpectedFlags(t *testing.T) {
	cfg := &Config{}
	telephonyCmd := NewTelephonyCmd(cfg)
	var stats *cobra.Command
	for _, sub := range telephonyCmd.Commands() {
		if sub.Name() == "stats" {
			stats = sub
		}
	}
	if stats == nil {
		t.Fatal("kv telephony is missing the stats sub-command")
	}
	for _, flag := range []string{"since", "did", "json", "log-group"} {
		if stats.Flags().Lookup(flag) == nil {
			t.Errorf("kv telephony stats is missing expected flag --%s", flag)
		}
	}
}

// --------------------------------------------------------------------------
// Test 8: cross-language drift guard — the Go marker constant equals the
// Python CALL_EVENT_MARKER assignment, read directly out of source.

func TestCallEventMarkerMatchesPython(t *testing.T) {
	// kv/internal/app/cmd -> kv/internal/app -> kv/internal -> kv -> repo
	// root -> apps/voice/src/klanker_voice/telephony/call_event.py.
	const pythonPath = "../../../../apps/voice/src/klanker_voice/telephony/call_event.py"
	data, err := os.ReadFile(pythonPath)
	if err != nil {
		t.Fatalf("reading %s: %v (cross-language drift guard requires the Python source to be present)", pythonPath, err)
	}
	expected := `CALL_EVENT_MARKER = "` + callEventMarker + `"`
	if !strings.Contains(string(data), expected) {
		t.Errorf(
			"call_event.py does not contain %q -- the Go callEventMarker (%q) has drifted from the Python CALL_EVENT_MARKER",
			expected, callEventMarker,
		)
	}
}
