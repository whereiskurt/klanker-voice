package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-voice/kv/internal/app/electro"
)

// AccessCodeRecord is the read-side shape returned by ListAccessCodes — the
// subset of AccessCode attributes an operator needs to see.
type AccessCodeRecord struct {
	Code            string `json:"code" dynamodbav:"code"`
	TierID          string `json:"tierId" dynamodbav:"tierId"`
	Group           string `json:"group,omitempty" dynamodbav:"group"`
	ExpiresAt       *int64 `json:"expiresAt,omitempty" dynamodbav:"expiresAt"`
	MaxRedemptions  *int64 `json:"maxRedemptions,omitempty" dynamodbav:"maxRedemptions"`
	RedemptionCount int64  `json:"redemptionCount" dynamodbav:"redemptionCount"`
	CreatedAt       int64  `json:"createdAt" dynamodbav:"createdAt"`
}

// validateCodeCharset rejects control characters and empty codes before any
// write (T-03-10: kv must not inject malformed key material into the
// electro table's pk/sk/gsi1 strings).
func validateCodeCharset(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("code must not be blank")
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return fmt.Errorf("code %q contains a control character", raw)
		}
	}
	return nil
}

// CreateAccessCode writes a new AccessCode item to table via PutItem,
// building the item with electro.NewAccessCodeItem so its pk/sk/gsi1 keys
// and ElectroDB bookkeeping markers exactly match the webapp's entity.
func CreateAccessCode(ctx context.Context, client *dynamodb.Client, table, code, tierID, group string, expiresAt, maxRedemptions *int64) error {
	if err := validateCodeCharset(code); err != nil {
		return err
	}
	if err := validateCodeCharset(tierID); err != nil {
		return fmt.Errorf("invalid tier id: %w", err)
	}
	item := electro.NewAccessCodeItem(code, tierID, group, expiresAt, maxRedemptions)
	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(table),
		Item:      item.Marshal(),
	})
	if err != nil {
		return fmt.Errorf("put access code %q: %w", code, err)
	}
	return nil
}

// ListAccessCodes queries the gsi1pk-gsi1sk-index "accesscodes#" partition —
// the same GSI/partition the webapp's AccessCode.all() access pattern reads.
func ListAccessCodes(ctx context.Context, client *dynamodb.Client, table string) ([]AccessCodeRecord, error) {
	var out []AccessCodeRecord
	var lastKey map[string]types.AttributeValue
	for {
		resp, err := client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(table),
			IndexName:              aws.String(electro.GSI1IndexName),
			KeyConditionExpression: aws.String("gsi1pk = :pk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": &types.AttributeValueMemberS{Value: electro.AccessCodeGSI1PK()},
			},
			ExclusiveStartKey: lastKey,
		})
		if err != nil {
			return nil, fmt.Errorf("query access codes: %w", err)
		}
		var page []AccessCodeRecord
		if err := attributevalue.UnmarshalListOfMaps(resp.Items, &page); err != nil {
			return nil, fmt.Errorf("unmarshal access codes: %w", err)
		}
		out = append(out, page...)
		if resp.LastEvaluatedKey == nil {
			break
		}
		lastKey = resp.LastEvaluatedKey
	}
	return out, nil
}

// ExpireAccessCode soft-expires a code by setting expiresAt to now (epoch
// ms) via UpdateItem, rather than deleting the row — preserves
// redemptionCount history.
func ExpireAccessCode(ctx context.Context, client *dynamodb.Client, table, code string) error {
	if err := validateCodeCharset(code); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
			"sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
		},
		UpdateExpression: aws.String("SET expiresAt = :now"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":now": &types.AttributeValueMemberN{Value: strconv.FormatInt(now, 10)},
		},
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})
	if err != nil {
		return fmt.Errorf("expire code %q: %w", code, err)
	}
	return nil
}

// NewCodeCmd builds the "kv code" parent command with create/list/expire
// subcommands (KV-01).
func NewCodeCmd(cfg *Config) *cobra.Command {
	codeCmd := &cobra.Command{
		Use:   "code",
		Short: "Manage access codes (create, list, expire)",
	}

	var (
		tier           string
		group          string
		expiresRFC3339 string
		maxRedemptions int
	)

	create := &cobra.Command{
		Use:   "create <code>",
		Short: "Create an access code mapped to a tier",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if tier == "" {
				return fmt.Errorf("--tier is required")
			}
			var expiresAt *int64
			if expiresRFC3339 != "" {
				t, err := time.Parse(time.RFC3339, expiresRFC3339)
				if err != nil {
					return fmt.Errorf("--expires must be RFC3339: %w", err)
				}
				ms := t.UnixMilli()
				expiresAt = &ms
			}
			var maxPtr *int64
			if c.Flags().Changed("max") {
				m := int64(maxRedemptions)
				maxPtr = &m
			}
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			if err := CreateAccessCode(c.Context(), client, cfg.Table, args[0], tier, group, expiresAt, maxPtr); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "created code %q -> tier %q\n", electro.NormalizeCode(args[0]), electro.NormalizeTierID(tier))
			return nil
		},
	}
	create.Flags().StringVar(&tier, "tier", "", "tier id this code grants (required)")
	create.Flags().StringVar(&group, "group", "", "optional group label")
	create.Flags().StringVar(&expiresRFC3339, "expires", "", "optional expiry, RFC3339 (e.g. 2026-12-31T00:00:00Z)")
	create.Flags().IntVar(&maxRedemptions, "max", 0, "optional max unique-user redemptions (unlimited if unset)")
	codeCmd.AddCommand(create)

	var asJSON bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List access codes",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			records, err := ListAccessCodes(c.Context(), client, cfg.Table)
			if err != nil {
				return err
			}
			return printAccessCodes(c, records, asJSON)
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	codeCmd.AddCommand(list)

	expire := &cobra.Command{
		Use:   "expire <code>",
		Short: "Soft-expire an access code (sets expiresAt = now)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			if err := ExpireAccessCode(c.Context(), client, cfg.Table, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "expired code %q\n", electro.NormalizeCode(args[0]))
			return nil
		},
	}
	codeCmd.AddCommand(expire)

	return codeCmd
}

func printAccessCodes(c *cobra.Command, records []AccessCodeRecord, asJSON bool) error {
	out := c.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(records)
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "CODE\tTIER\tGROUP\tEXPIRES\tMAX\tREDEEMED")
	for _, r := range records {
		expires := "-"
		if r.ExpiresAt != nil {
			expires = time.UnixMilli(*r.ExpiresAt).UTC().Format(time.RFC3339)
		}
		max := "unlimited"
		if r.MaxRedemptions != nil {
			max = strconv.FormatInt(*r.MaxRedemptions, 10)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\n", r.Code, r.TierID, r.Group, expires, max, r.RedemptionCount)
	}
	return w.Flush()
}
