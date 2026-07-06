package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-voice/kv/internal/app/electro"
)

// defaultUsageHistoryDays is `kv usage history`'s default --days window when
// unset.
const defaultUsageHistoryDays = 7

// UsageDailyRecord is the read-side shape of a UsageDaily item — one user's
// seconds-used for a single day (usage.ts UsageDaily / quota.py's daily
// item).
type UsageDailyRecord struct {
	UserID      string `json:"userId" dynamodbav:"userId"`
	Day         string `json:"day" dynamodbav:"day"`
	SecondsUsed int64  `json:"secondsUsed" dynamodbav:"secondsUsed"`
	UpdatedAt   int64  `json:"updatedAt,omitempty" dynamodbav:"updatedAt"`
}

// UsageRollupRecord is the read-side shape of the site-wide UsageRollup item
// for a single day — the O(1) global-total read (KV-03, D-10).
type UsageRollupRecord struct {
	Day          string  `json:"day" dynamodbav:"day"`
	TotalSeconds int64   `json:"totalSeconds" dynamodbav:"totalSeconds"`
	SessionCount int64   `json:"sessionCount" dynamodbav:"sessionCount"`
	EstCost      float64 `json:"estCost" dynamodbav:"estCost"`
	UpdatedAt    int64   `json:"updatedAt,omitempty" dynamodbav:"updatedAt"`
}

// ReadUsageRollup GetItems the global daily rollup item (pk="rollup#",
// sk="day#${day}") — an O(1) read, no table scan (KV-03, D-10). A day with
// no traffic yet (fresh table / no sessions today) returns a zero-value
// record rather than an error.
func ReadUsageRollup(ctx context.Context, client *dynamodb.Client, table, day string) (UsageRollupRecord, error) {
	resp, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.UsageRollupPK()},
			"sk": &types.AttributeValueMemberS{Value: electro.UsageRollupSK(day)},
		},
	})
	if err != nil {
		return UsageRollupRecord{}, fmt.Errorf("get rollup for day %q: %w", day, err)
	}
	record := UsageRollupRecord{Day: day}
	if resp.Item == nil {
		return record, nil
	}
	if err := attributevalue.UnmarshalMap(resp.Item, &record); err != nil {
		return UsageRollupRecord{}, fmt.Errorf("unmarshal rollup for day %q: %w", day, err)
	}
	record.Day = day
	return record, nil
}

// ReadUsageDaily GetItems one user's daily usage item (pk="user#${userId}",
// sk="day#${day}"). A user with no usage yet on that day returns a
// zero-value record rather than an error.
func ReadUsageDaily(ctx context.Context, client *dynamodb.Client, table, userID, day string) (UsageDailyRecord, error) {
	resp, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.UsageDailyPK(userID)},
			"sk": &types.AttributeValueMemberS{Value: electro.UsageDailySK(day)},
		},
	})
	if err != nil {
		return UsageDailyRecord{}, fmt.Errorf("get daily usage for user %q day %q: %w", userID, day, err)
	}
	record := UsageDailyRecord{UserID: userID, Day: day}
	if resp.Item == nil {
		return record, nil
	}
	if err := attributevalue.UnmarshalMap(resp.Item, &record); err != nil {
		return UsageDailyRecord{}, fmt.Errorf("unmarshal daily usage for user %q day %q: %w", userID, day, err)
	}
	record.UserID = userID
	record.Day = day
	return record, nil
}

// ListUsageHistory Queries a user's most recent daily usage items (pk =
// "user#${userId}", sk begins_with "day#") — a single-partition Query, no
// table scan. Results are returned most-recent-day-first, capped at days.
func ListUsageHistory(ctx context.Context, client *dynamodb.Client, table, userID string, days int32) ([]UsageDailyRecord, error) {
	if days <= 0 {
		days = defaultUsageHistoryDays
	}
	resp, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(table),
		KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :skPrefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":       &types.AttributeValueMemberS{Value: electro.UsageDailyPK(userID)},
			":skPrefix": &types.AttributeValueMemberS{Value: "day#"},
		},
		ScanIndexForward: aws.Bool(false), // most recent day first
		Limit:            aws.Int32(days),
	})
	if err != nil {
		return nil, fmt.Errorf("query usage history for user %q: %w", userID, err)
	}
	var out []UsageDailyRecord
	if err := attributevalue.UnmarshalListOfMaps(resp.Items, &out); err != nil {
		return nil, fmt.Errorf("unmarshal usage history for user %q: %w", userID, err)
	}
	// quota.py's record_tick never writes a "day" attribute on the daily
	// item — the day lives only in the sort key ("day#${day}") — so it's
	// derived here from each returned item's sk rather than expected from
	// the unmarshaled struct.
	for i, item := range resp.Items {
		out[i].UserID = userID
		if sk, ok := item["sk"].(*types.AttributeValueMemberS); ok {
			out[i].Day = strings.TrimPrefix(sk.Value, "day#")
		}
	}
	return out, nil
}

// NewUsageCmd builds the "kv usage" parent command with today/history
// subcommands (KV-03): view today's usage per user and the site-wide O(1)
// rollup, and query a user's recent-day history — no table scan.
func NewUsageCmd(cfg *Config) *cobra.Command {
	usageCmd := &cobra.Command{
		Use:   "usage",
		Short: "View voice usage (per-user + site-wide daily rollup)",
	}

	var (
		userID string
		asJSON bool
	)

	today := &cobra.Command{
		Use:   "today",
		Short: "Show today's usage — the site-wide rollup, or one user's day with --user-id",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			day := electro.UsageDayString(time.Now())
			if userID != "" {
				record, err := ReadUsageDaily(c.Context(), client, cfg.UsageTable, userID, day)
				if err != nil {
					return err
				}
				return printUsageDaily(c, record, asJSON)
			}
			record, err := ReadUsageRollup(c.Context(), client, cfg.UsageTable, day)
			if err != nil {
				return err
			}
			return printUsageRollup(c, record, asJSON)
		},
	}
	today.Flags().StringVar(&userID, "user-id", "", "show this user's daily usage instead of the site-wide rollup")
	today.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	usageCmd.AddCommand(today)

	var historyDays int32
	var historyJSON bool
	history := &cobra.Command{
		Use:   "history <user-id>",
		Short: "Show a user's recent daily usage history",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			records, err := ListUsageHistory(c.Context(), client, cfg.UsageTable, args[0], historyDays)
			if err != nil {
				return err
			}
			return printUsageHistory(c, records, historyJSON)
		},
	}
	history.Flags().Int32Var(&historyDays, "days", defaultUsageHistoryDays, "number of most-recent days to show")
	history.Flags().BoolVar(&historyJSON, "json", false, "output as JSON")
	usageCmd.AddCommand(history)

	return usageCmd
}

func printUsageRollup(c *cobra.Command, record UsageRollupRecord, asJSON bool) error {
	out := c.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(record)
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "DAY\tTOTAL-SECONDS\tSESSION-COUNT\tEST-COST")
	fmt.Fprintf(w, "%s\t%d\t%d\t$%.2f\n", record.Day, record.TotalSeconds, record.SessionCount, record.EstCost)
	return w.Flush()
}

func printUsageDaily(c *cobra.Command, record UsageDailyRecord, asJSON bool) error {
	out := c.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(record)
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "USER\tDAY\tSECONDS-USED")
	fmt.Fprintf(w, "%s\t%s\t%d\n", record.UserID, record.Day, record.SecondsUsed)
	return w.Flush()
}

func printUsageHistory(c *cobra.Command, records []UsageDailyRecord, asJSON bool) error {
	out := c.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(records)
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "USER\tDAY\tSECONDS-USED")
	for _, r := range records {
		fmt.Fprintf(w, "%s\t%s\t%d\n", r.UserID, r.Day, r.SecondsUsed)
	}
	return w.Flush()
}
