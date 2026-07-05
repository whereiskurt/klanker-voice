package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-voice/kv/internal/app/electro"
)

// TierRecord is the read-side shape returned by ListTiers.
type TierRecord struct {
	TierID            string `json:"tierId" dynamodbav:"tierId"`
	Group             string `json:"group,omitempty" dynamodbav:"group"`
	SessionMaxSeconds int64  `json:"sessionMaxSeconds" dynamodbav:"sessionMaxSeconds"`
	PeriodMaxSeconds  int64  `json:"periodMaxSeconds" dynamodbav:"periodMaxSeconds"`
	MaxConcurrent     int64  `json:"maxConcurrent" dynamodbav:"maxConcurrent"`
	CreatedAt         int64  `json:"createdAt" dynamodbav:"createdAt"`
}

// DefineTier writes (creates or replaces) a Tier item via PutItem, building
// the item with electro.NewTierItem so its pk/sk/gsi1 keys and ElectroDB
// bookkeeping markers exactly match the webapp's entity.
func DefineTier(ctx context.Context, client *dynamodb.Client, table, tierID, group string, sessionMaxSecs, periodMaxSecs, maxConcurrent int64) error {
	if err := validateCodeCharset(tierID); err != nil {
		return fmt.Errorf("invalid tier id: %w", err)
	}
	item := electro.NewTierItem(tierID, group, sessionMaxSecs, periodMaxSecs, maxConcurrent)
	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(table),
		Item:      item.Marshal(),
	})
	if err != nil {
		return fmt.Errorf("put tier %q: %w", tierID, err)
	}
	return nil
}

// ListTiers queries the gsi1pk-gsi1sk-index "tiers#" partition — the same
// GSI/partition the webapp's Tier.all() access pattern reads.
func ListTiers(ctx context.Context, client *dynamodb.Client, table string) ([]TierRecord, error) {
	var out []TierRecord
	var lastKey map[string]types.AttributeValue
	for {
		resp, err := client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(table),
			IndexName:              aws.String(electro.GSI1IndexName),
			KeyConditionExpression: aws.String("gsi1pk = :pk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: electro.TierGSI1PK()},
			},
			ExclusiveStartKey: lastKey,
		})
		if err != nil {
			return nil, fmt.Errorf("query tiers: %w", err)
		}
		var page []TierRecord
		if err := attributevalue.UnmarshalListOfMaps(resp.Items, &page); err != nil {
			return nil, fmt.Errorf("unmarshal tiers: %w", err)
		}
		out = append(out, page...)
		if resp.LastEvaluatedKey == nil {
			break
		}
		lastKey = resp.LastEvaluatedKey
	}
	return out, nil
}

// NewTierCmd builds the "kv tier" parent command with define/list
// subcommands (KV-02).
func NewTierCmd(cfg *Config) *cobra.Command {
	tierCmd := &cobra.Command{
		Use:   "tier",
		Short: "Manage tiers (session/period/concurrency limits)",
	}

	var (
		group         string
		sessionMax    int64
		periodMax     int64
		maxConcurrent int64
	)

	define := &cobra.Command{
		Use:   "define <tierId>",
		Short: "Define (create or replace) a tier",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			if err := DefineTier(c.Context(), client, cfg.Table, args[0], group, sessionMax, periodMax, maxConcurrent); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "defined tier %q (session-max=%ds period-max=%ds max-concurrent=%d)\n",
				electro.NormalizeTierID(args[0]), sessionMax, periodMax, maxConcurrent)
			return nil
		},
	}
	define.Flags().StringVar(&group, "group", "", "optional group label")
	define.Flags().Int64Var(&sessionMax, "session-max", 0, "max seconds per session (required)")
	define.Flags().Int64Var(&periodMax, "period-max", 0, "max seconds per rolling period (required)")
	define.Flags().Int64Var(&maxConcurrent, "max-concurrent", 1, "max concurrent sessions")
	tierCmd.AddCommand(define)

	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List tiers",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			records, err := ListTiers(c.Context(), client, cfg.Table)
			if err != nil {
				return err
			}
			return printTiers(c, records, asJSON)
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	tierCmd.AddCommand(list)

	return tierCmd
}

func printTiers(c *cobra.Command, records []TierRecord, asJSON bool) error {
	out := c.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(records)
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "TIER\tGROUP\tSESSION-MAX\tPERIOD-MAX\tMAX-CONCURRENT")
	for _, r := range records {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\n", r.TierID, r.Group, r.SessionMaxSeconds, r.PeriodMaxSeconds, r.MaxConcurrent)
	}
	return w.Flush()
}
