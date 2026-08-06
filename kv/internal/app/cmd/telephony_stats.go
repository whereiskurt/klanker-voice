// Package cmd — `kv telephony stats`: a per-DID call summary (quick task
// 260727-v5e) built from telephony-edge's `game_call_event` log lines via a
// CloudWatch Logs Insights query. Follows telephony.go's established shape
// exactly: a narrow injectable interface for the offline-test seam, pure
// read/aggregate functions, a `print*` renderer taking (c, report, asJSON),
// and total degradation on partial failure (one malformed log line never
// kills the report).
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/spf13/cobra"
)

// --------------------------------------------------------------------------
// Constants.

// callEventMarker is the stable prefix every `game_call_event` log line
// starts with. MUST match the Python constant CALL_EVENT_MARKER in
// apps/voice/src/klanker_voice/telephony/call_event.py — guarded by
// TestCallEventMarkerMatchesPython (telephony_stats_test.go), which reads
// that Python source file directly, so renaming the marker on one side
// fails the other side's build.
const callEventMarker = "game_call_event"

// defaultTelephonyLogGroup is the telephony-edge ECS task's log group
// (D-08), derived from infra/terraform/modules/ecs-task/v1.0.0/main.tf's
// `awslogs-group` = "/ecs/${container.name}-${family}" (both the container
// name and the task family are "telephony-edge-use1-kmv" for this task).
const defaultTelephonyLogGroup = "/ecs/telephony-edge-telephony-edge-use1-kmv"

// defaultStatsSince is `kv telephony stats`'s default --since window.
const defaultStatsSince = 24 * time.Hour

// statsQueryLimit bounds the Insights `limit` clause — far above the
// expected few hundred calls per event (CONTEXT: "favor simple/robust over
// clever" at this scale).
const statsQueryLimit = 10000

// defaultStatsPollInterval is the real-world wait between GetQueryResults
// polls. Tests inject a near-zero interval so no real time elapses.
const defaultStatsPollInterval = 2 * time.Second

// statsPollBudget bounds RunInsightsQuery's poll loop so a permanently
// "Running" fake (or a genuinely stuck query) can never hang forever.
const statsPollBudget = 60

// unresolvedDIDLabel is the row label for an event whose dialed_did is the
// empty string (a To:/CID-name parse miss) — never an empty row.
const unresolvedDIDLabel = "(unresolved)"

// totalsRowLabel is the DID column label for the totals row.
const totalsRowLabel = "TOTAL"

// --------------------------------------------------------------------------
// CallEvent — mirrors apps/voice/.../call_event.py's build_call_event
// payload one-for-one (D-03), matching json tags field-for-field.

// CallEvent is one parsed game_call_event line. INTERNAL: CallerID is held
// only long enough for AggregateCallStats' local distinct-caller count — it
// is never rendered to the operator or serialized in the report output
// (D-09; see DIDStats/TelephonyStatsReport, which structurally carry no
// caller field at all).
type CallEvent struct {
	CallID           string   `json:"call_id"`
	DialedDID        string   `json:"dialed_did"`
	CallerID         string   `json:"caller_id"`
	OTPOnly          bool     `json:"otp_only"`
	Outcome          string   `json:"outcome"`
	DigitsEntered    int      `json:"digits_entered"`
	WordsHeard       int      `json:"words_heard"`
	SecondsToOutcome *float64 `json:"seconds_to_outcome"`
	DurationSeconds  float64  `json:"duration_seconds"`
}

// ParseCallEvents extracts one CallEvent per marker-bearing log message,
// silently skipping both non-marker lines and marker lines whose JSON is
// malformed — one bad line must never kill the report (mirrors
// readTelephonySecrets/readInboundDIDs' refusal to let one section's
// failure fail the whole command).
func ParseCallEvents(messages []string) []CallEvent {
	events := make([]CallEvent, 0, len(messages))
	marker := callEventMarker + " "
	for _, msg := range messages {
		idx := strings.Index(msg, marker)
		if idx == -1 {
			continue
		}
		var event CallEvent
		if err := json.Unmarshal([]byte(msg[idx+len(marker):]), &event); err != nil {
			continue
		}
		events = append(events, event)
	}
	return events
}

// --------------------------------------------------------------------------
// CloudWatch Logs Insights query (the offline-test seam).

// logsInsightsAPI is the narrow subset of *cloudwatchlogs.Client this file
// needs, so tests can inject an in-memory fake instead of a real CloudWatch
// Logs connection (mirrors telephonyScanAPI/ssmGetParameterAPI).
type logsInsightsAPI interface {
	StartQuery(ctx context.Context, params *cloudwatchlogs.StartQueryInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.StartQueryOutput, error)
	GetQueryResults(ctx context.Context, params *cloudwatchlogs.GetQueryResultsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetQueryResultsOutput, error)
}

// RunInsightsQuery runs a Logs Insights query over [start, end] against
// logGroup, filtering to game_call_event lines, and polls GetQueryResults
// until the query reaches a terminal status. On Complete, returns every
// matched row's @message value. Failed/Cancelled/Timeout is an error, NEVER
// a silent empty result — an operator must be told the query itself broke,
// not shown a misleadingly-empty report.
//
// Quick task 260806-cm9: the poll lifecycle below has been extracted into
// runInsightsQueryString so `kv telephony calls` can reuse it with a
// different filter (the on_stasis_start family). This function is now a
// two-line wrapper that builds the IDENTICAL query string it always has and
// delegates — its exported signature, doc-comment intent, and returned
// values are unchanged. The exact string is pinned by
// TestRunInsightsQuery_ExactQueryString (telephony_calls_test.go) so any
// future drift here fails a test, not just a review.
func RunInsightsQuery(
	ctx context.Context,
	api logsInsightsAPI,
	logGroup string,
	start, end time.Time,
	pollInterval time.Duration,
) ([]string, error) {
	queryString := fmt.Sprintf(
		"fields @timestamp, @message | filter @message like /%s / | sort @timestamp desc | limit %d",
		callEventMarker, statsQueryLimit,
	)
	return runInsightsQueryString(ctx, api, logGroup, start, end, pollInterval, queryString)
}

// runInsightsQueryString is RunInsightsQuery's Insights lifecycle (start +
// poll + terminal-status handling), extracted so it can be shared with
// RunCallsInsightsQuery (telephony_calls.go) — the only thing that varies
// between the two callers is the query string itself. Between polls, selects
// on ctx.Done() and time.After(pollInterval) so tests can pass a
// zero/near-zero interval and no real time elapses; statsPollBudget bounds
// the loop so a permanently-Running fake can never hang the suite.
func runInsightsQueryString(
	ctx context.Context,
	api logsInsightsAPI,
	logGroup string,
	start, end time.Time,
	pollInterval time.Duration,
	queryString string,
) ([]string, error) {
	startOut, err := api.StartQuery(ctx, &cloudwatchlogs.StartQueryInput{
		LogGroupName: aws.String(logGroup),
		StartTime:    aws.Int64(start.Unix()),
		EndTime:      aws.Int64(end.Unix()),
		QueryString:  aws.String(queryString),
	})
	if err != nil {
		return nil, fmt.Errorf("start insights query: %w", err)
	}
	queryID := aws.ToString(startOut.QueryId)

	for attempt := 0; attempt < statsPollBudget; attempt++ {
		resultsOut, err := api.GetQueryResults(ctx, &cloudwatchlogs.GetQueryResultsInput{
			QueryId: aws.String(queryID),
		})
		if err != nil {
			return nil, fmt.Errorf("get insights query results: %w", err)
		}
		switch resultsOut.Status {
		case types.QueryStatusComplete:
			return extractMessages(resultsOut.Results), nil
		case types.QueryStatusFailed, types.QueryStatusCancelled, types.QueryStatusTimeout:
			return nil, fmt.Errorf("insights query %s ended with status %s", queryID, resultsOut.Status)
		default:
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(pollInterval):
			}
		}
	}
	return nil, fmt.Errorf("insights query %s: exceeded poll budget (%d attempts)", queryID, statsPollBudget)
}

// extractMessages pulls the "@message" field value out of each Insights
// result row.
func extractMessages(rows [][]types.ResultField) []string {
	messages := make([]string, 0, len(rows))
	for _, row := range rows {
		for _, field := range row {
			if aws.ToString(field.Field) == "@message" {
				messages = append(messages, aws.ToString(field.Value))
				break
			}
		}
	}
	return messages
}

// --------------------------------------------------------------------------
// Aggregation (client-side, over a filtered Insights result set — CONTEXT's
// own "favor simple and robust over a clever `stats` query" discretion).

// DIDStats is one per-DID (or the totals) row of the report. Deliberately
// has NO caller-identifying field — that structural omission, not a
// formatting rule, is what makes D-09 enforceable.
type DIDStats struct {
	DID                    string         `json:"did"`
	Calls                  int            `json:"calls"`
	DistinctCallers        int            `json:"distinctCallers"`
	Outcomes               map[string]int `json:"outcomes"`
	MedianSecondsToOutcome *float64       `json:"medianSecondsToOutcome"`
	MaxSecondsToOutcome    *float64       `json:"maxSecondsToOutcome"`
	MedianDurationSeconds  float64        `json:"medianDurationSeconds"`
}

// TelephonyStatsReport is the full `kv telephony stats` output shape.
// LogGroup/Since are filled in by the command's RunE (AggregateCallStats
// itself only knows about events + the DID filter).
type TelephonyStatsReport struct {
	LogGroup  string     `json:"logGroup"`
	Since     string     `json:"since"`
	DIDFilter string     `json:"didFilter,omitempty"`
	PerDID    []DIDStats `json:"perDid"`
	Totals    DIDStats   `json:"totals"`
}

// AggregateCallStats groups events by dialed_did (substituting
// unresolvedDIDLabel for an empty dialed_did — never an empty row), computes
// per-DID call count / outcome tally / distinct-caller count / median+max
// seconds_to_outcome / median duration_seconds, and a totals row over the
// same (optionally did-filtered) event set. didFilter, when non-empty,
// narrows BOTH the per-DID rows and the totals row (compared against the
// raw dialed_did value, so filtering to a resolved DID works). PerDID is
// sorted by DID ascending so output is deterministic.
func AggregateCallStats(events []CallEvent, didFilter string) TelephonyStatsReport {
	filtered := make([]CallEvent, 0, len(events))
	for _, e := range events {
		if didFilter != "" && e.DialedDID != didFilter {
			continue
		}
		filtered = append(filtered, e)
	}

	groups := map[string][]CallEvent{}
	for _, e := range filtered {
		label := e.DialedDID
		if label == "" {
			label = unresolvedDIDLabel
		}
		groups[label] = append(groups[label], e)
	}

	dids := make([]string, 0, len(groups))
	for did := range groups {
		dids = append(dids, did)
	}
	sort.Strings(dids)

	perDID := make([]DIDStats, 0, len(dids))
	for _, did := range dids {
		perDID = append(perDID, buildDIDStats(did, groups[did]))
	}

	return TelephonyStatsReport{
		DIDFilter: didFilter,
		PerDID:    perDID,
		Totals:    buildDIDStats(totalsRowLabel, filtered),
	}
}

// buildDIDStats computes one DIDStats row over events (either one DID's
// events, or the full filtered set for the totals row).
func buildDIDStats(label string, events []CallEvent) DIDStats {
	stats := DIDStats{
		DID:      label,
		Calls:    len(events),
		Outcomes: map[string]int{},
	}
	callers := map[string]struct{}{}
	secondsToOutcome := make([]float64, 0, len(events))
	durations := make([]float64, 0, len(events))
	for _, e := range events {
		stats.Outcomes[e.Outcome]++
		if e.CallerID != "" {
			callers[e.CallerID] = struct{}{}
		}
		if e.SecondsToOutcome != nil {
			secondsToOutcome = append(secondsToOutcome, *e.SecondsToOutcome)
		}
		durations = append(durations, e.DurationSeconds)
	}
	stats.DistinctCallers = len(callers)
	stats.MedianSecondsToOutcome = medianFloat(secondsToOutcome)
	stats.MaxSecondsToOutcome = maxFloat(secondsToOutcome)
	if median := medianFloat(durations); median != nil {
		stats.MedianDurationSeconds = *median
	}
	return stats
}

// medianFloat returns the rounded-to-one-decimal median of values (mean of
// the two central values for an even count), or nil for an empty slice.
// Does not mutate values.
func medianFloat(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	n := len(sorted)
	var median float64
	if n%2 == 1 {
		median = sorted[n/2]
	} else {
		median = (sorted[n/2-1] + sorted[n/2]) / 2
	}
	median = roundOneDecimal(median)
	return &median
}

// maxFloat returns the rounded-to-one-decimal maximum of values, or nil for
// an empty slice.
func maxFloat(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	max := values[0]
	for _, v := range values[1:] {
		if v > max {
			max = v
		}
	}
	max = roundOneDecimal(max)
	return &max
}

func roundOneDecimal(v float64) float64 {
	return math.Round(v*10) / 10
}

// --------------------------------------------------------------------------
// AWS client + rendering.

// CloudWatchLogsClient builds an aws-sdk-go-v2 CloudWatch Logs client from
// the Config via the shared loadAWS helper (root.go), mirroring
// SSMClient/DynamoClient — no endpoint override needed.
func (c *Config) CloudWatchLogsClient(ctx context.Context) (*cloudwatchlogs.Client, error) {
	cfg, err := c.loadAWS(ctx)
	if err != nil {
		return nil, err
	}
	return cloudwatchlogs.NewFromConfig(cfg), nil
}

// printTelephonyStats renders the report: --json encodes it with a
// two-space indent exactly like printTelephony; otherwise a tabwriter table
// with one row per DID plus a TOTAL row. Nil medians render as "-". The
// outcome map renders as space-joined "label=count" pairs sorted by label
// so output is deterministic. Neither rendering path ever touches a caller
// number — DIDStats/TelephonyStatsReport structurally carry none (D-09).
func printTelephonyStats(c *cobra.Command, report TelephonyStatsReport, asJSON bool) error {
	out := c.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	if len(report.PerDID) == 0 {
		fmt.Fprintf(
			out,
			"No call events found (log group %s, window %s%s)\n",
			report.LogGroup, report.Since, didFilterSuffix(report.DIDFilter),
		)
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "DID\tCALLS\tCALLERS\tMED-TO-OUTCOME\tMAX-TO-OUTCOME\tMED-DURATION\tOUTCOMES")
	for _, d := range report.PerDID {
		fmt.Fprintf(
			w, "%s\t%d\t%d\t%s\t%s\t%.1f\t%s\n",
			d.DID, d.Calls, d.DistinctCallers,
			formatNilableSeconds(d.MedianSecondsToOutcome), formatNilableSeconds(d.MaxSecondsToOutcome),
			d.MedianDurationSeconds, formatOutcomes(d.Outcomes),
		)
	}
	t := report.Totals
	fmt.Fprintf(
		w, "%s\t%d\t%d\t%s\t%s\t%.1f\t%s\n",
		totalsRowLabel, t.Calls, t.DistinctCallers,
		formatNilableSeconds(t.MedianSecondsToOutcome), formatNilableSeconds(t.MaxSecondsToOutcome),
		t.MedianDurationSeconds, formatOutcomes(t.Outcomes),
	)
	return w.Flush()
}

func didFilterSuffix(didFilter string) string {
	if didFilter == "" {
		return ""
	}
	return fmt.Sprintf(", did=%s", didFilter)
}

func formatNilableSeconds(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.1f", *v)
}

func formatOutcomes(outcomes map[string]int) string {
	if len(outcomes) == 0 {
		return "-"
	}
	labels := make([]string, 0, len(outcomes))
	for label := range outcomes {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	parts := make([]string, 0, len(labels))
	for _, label := range labels {
		parts = append(parts, fmt.Sprintf("%s=%d", label, outcomes[label]))
	}
	return strings.Join(parts, " ")
}

// --------------------------------------------------------------------------
// Command registration.

// newTelephonyStatsCmd builds the "kv telephony stats" subcommand,
// registered onto telephonyCmd by NewTelephonyCmd (telephony.go).
func newTelephonyStatsCmd(cfg *Config) *cobra.Command {
	var (
		since    time.Duration
		did      string
		asJSON   bool
		logGroup string
	)
	stats := &cobra.Command{
		Use:   "stats",
		Short: "Per-DID call summary from telephony-edge's game_call_event log lines (CloudWatch Logs Insights)",
		Long: "kv telephony stats answers, during the event: how many people are\n" +
			"calling each number, are they getting through the code entry, and\n" +
			"where do they give up? It runs a CloudWatch Logs Insights query over\n" +
			"the telephony-edge log group, parses every game_call_event line, and\n" +
			"renders per-DID call counts, an outcome breakdown, median + max\n" +
			"seconds-to-outcome, median duration, and a distinct-caller COUNT\n" +
			"(never a raw caller number) plus a totals row. For raw caller\n" +
			"numbers -- who called which number and when -- see `kv telephony calls`.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			client, err := cfg.CloudWatchLogsClient(c.Context())
			if err != nil {
				return err
			}
			end := time.Now()
			start := end.Add(-since)
			messages, err := RunInsightsQuery(c.Context(), client, logGroup, start, end, defaultStatsPollInterval)
			if err != nil {
				return err
			}
			events := ParseCallEvents(messages)
			report := AggregateCallStats(events, did)
			report.LogGroup = logGroup
			report.Since = since.String()
			return printTelephonyStats(c, report, asJSON)
		},
	}
	stats.Flags().DurationVar(&since, "since", defaultStatsSince, "how far back to query (e.g. 24h, 90m)")
	stats.Flags().StringVar(&did, "did", "", "filter to a single dialed DID")
	stats.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	stats.Flags().StringVar(&logGroup, "log-group", defaultTelephonyLogGroup, "CloudWatch Logs group to query")
	return stats
}
