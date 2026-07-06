package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-voice/kv/internal/app/electro"
)

// defaultKillswitchReason is the reason recorded on `kv killswitch on` when
// --reason is not given — distinguishes an operator-initiated engage from
// the voice service's own "auto-trip" reason (D-09).
const defaultKillswitchReason = "operator"

// KillswitchStatus is the read-side shape of the UsageControl item (D-08):
// the single item /api/offer's start gate reads on every session, and the
// same item the D-09 auto-trip and this command's on/off conditionally
// flip.
type KillswitchStatus struct {
	Engaged        bool    `json:"engaged" dynamodbav:"engaged"`
	Reason         string  `json:"reason,omitempty" dynamodbav:"reason"`
	CeilingSeconds float64 `json:"ceilingSeconds,omitempty" dynamodbav:"ceilingSeconds"`
	CeilingDollars float64 `json:"ceilingDollars,omitempty" dynamodbav:"ceilingDollars"`
	UpdatedAt      int64   `json:"updatedAt,omitempty" dynamodbav:"updatedAt"`
}

// ReadKillswitchStatus GetItems the control item (pk="control#",
// sk="killswitch#"). A never-written control item (fresh table) defaults to
// disengaged, matching quota.py's own read_control_item default.
func ReadKillswitchStatus(ctx context.Context, client *dynamodb.Client, table string) (KillswitchStatus, error) {
	resp, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.UsageControlPK()},
			"sk": &types.AttributeValueMemberS{Value: electro.UsageControlSK()},
		},
	})
	if err != nil {
		return KillswitchStatus{}, fmt.Errorf("get killswitch status: %w", err)
	}
	if resp.Item == nil {
		return KillswitchStatus{Engaged: false}, nil
	}
	var status KillswitchStatus
	if err := attributevalue.UnmarshalMap(resp.Item, &status); err != nil {
		return KillswitchStatus{}, fmt.Errorf("unmarshal killswitch status: %w", err)
	}
	return status, nil
}

// isConditionalCheckFailed reports whether err is (or wraps) a DynamoDB
// ConditionalCheckFailedException — the expected, non-error outcome of a
// redundant/idempotent on-or-off flip.
func isConditionalCheckFailed(err error) bool {
	var condErr *types.ConditionalCheckFailedException
	return errors.As(err, &condErr)
}

// EngageKillswitch conditionally sets the control item's engaged=true with
// reason + timestamp (KV-04, QUOT-04 manual arm). Idempotent: if the switch
// is already engaged, this is a harmless no-op — the conditional write fails
// with ConditionalCheckFailedException, which is swallowed and reported via
// the boolean return (flipped=false), not an error. The condition also
// allows creating the item fresh (attribute_not_exists(pk)) so the very
// first `kv killswitch on` on a brand-new table succeeds.
func EngageKillswitch(ctx context.Context, client *dynamodb.Client, table, reason string) (flipped bool, err error) {
	if reason == "" {
		reason = defaultKillswitchReason
	}
	_, err = client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.UsageControlPK()},
			"sk": &types.AttributeValueMemberS{Value: electro.UsageControlSK()},
		},
		UpdateExpression:    aws.String("SET engaged = :true, reason = :reason, updatedAt = :now, #edbe = :edbe, #edbv = :edbv"),
		ConditionExpression: aws.String("attribute_not_exists(pk) OR engaged = :false"),
		ExpressionAttributeNames: map[string]string{
			// __edb_e__/__edb_v__'s leading underscores aren't valid as a bare
			// token in a DynamoDB expression grammar — must be aliased.
			"#edbe": electro.EDBEntityAttr,
			"#edbv": electro.EDBVersionAttr,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":true":   &types.AttributeValueMemberBOOL{Value: true},
			":false":  &types.AttributeValueMemberBOOL{Value: false},
			":reason": &types.AttributeValueMemberS{Value: reason},
			":now":    &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", time.Now().Unix())},
			":edbe":   &types.AttributeValueMemberS{Value: electro.UsageControlEntityName},
			":edbv":   &types.AttributeValueMemberS{Value: electro.EDBVersion},
		},
	})
	if err != nil {
		if isConditionalCheckFailed(err) {
			return false, nil
		}
		return false, fmt.Errorf("engage killswitch: %w", err)
	}
	return true, nil
}

// DisengageKillswitch conditionally sets the control item's engaged=false
// and clears the reason (D-09: an explicit operator action resets an
// auto-trip). Idempotent: if already disengaged (or the item was never
// written), this is a harmless no-op (flipped=false, no error) — off never
// creates a fresh item, since "disengaged" is already the default state of
// a missing item.
func DisengageKillswitch(ctx context.Context, client *dynamodb.Client, table string) (flipped bool, err error) {
	_, err = client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.UsageControlPK()},
			"sk": &types.AttributeValueMemberS{Value: electro.UsageControlSK()},
		},
		UpdateExpression:    aws.String("SET engaged = :false, updatedAt = :now, #edbe = :edbe, #edbv = :edbv REMOVE reason"),
		ConditionExpression: aws.String("attribute_exists(pk) AND engaged = :true"),
		ExpressionAttributeNames: map[string]string{
			"#edbe": electro.EDBEntityAttr,
			"#edbv": electro.EDBVersionAttr,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":false": &types.AttributeValueMemberBOOL{Value: false},
			":true":  &types.AttributeValueMemberBOOL{Value: true},
			":now":   &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", time.Now().Unix())},
			":edbe":  &types.AttributeValueMemberS{Value: electro.UsageControlEntityName},
			":edbv":  &types.AttributeValueMemberS{Value: electro.EDBVersion},
		},
	})
	if err != nil {
		if isConditionalCheckFailed(err) {
			return false, nil
		}
		return false, fmt.Errorf("disengage killswitch: %w", err)
	}
	return true, nil
}

// NewKillswitchCmd builds the "kv killswitch" parent command with
// status/on/off subcommands (KV-04, QUOT-04): a conditional flip of the same
// control item /api/offer's start gate reads on every session (D-08), so a
// flip propagates near-instantly with no service restart.
func NewKillswitchCmd(cfg *Config) *cobra.Command {
	killswitchCmd := &cobra.Command{
		Use:   "killswitch",
		Short: "View or flip the site-wide voice kill-switch (D-08/D-09)",
	}

	var statusJSON bool
	status := &cobra.Command{
		Use:   "status",
		Short: "Show the kill-switch's current engaged/reason/ceiling state",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			result, err := ReadKillswitchStatus(c.Context(), client, cfg.UsageTable)
			if err != nil {
				return err
			}
			return printKillswitchStatus(c, result, statusJSON)
		},
	}
	status.Flags().BoolVar(&statusJSON, "json", false, "output as JSON")
	killswitchCmd.AddCommand(status)

	var onReason string
	on := &cobra.Command{
		Use:   "on",
		Short: "Engage the kill-switch (pauses every new voice session site-wide)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			flipped, err := EngageKillswitch(c.Context(), client, cfg.UsageTable, onReason)
			if err != nil {
				return err
			}
			if flipped {
				fmt.Fprintln(c.OutOrStdout(), "killswitch engaged")
			} else {
				fmt.Fprintln(c.OutOrStdout(), "killswitch already engaged (no-op)")
			}
			return nil
		},
	}
	on.Flags().StringVar(&onReason, "reason", "", "reason recorded on the control item (default \"operator\")")
	killswitchCmd.AddCommand(on)

	off := &cobra.Command{
		Use:   "off",
		Short: "Disengage the kill-switch and clear any auto-trip reason (D-09 explicit reset)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			flipped, err := DisengageKillswitch(c.Context(), client, cfg.UsageTable)
			if err != nil {
				return err
			}
			if flipped {
				fmt.Fprintln(c.OutOrStdout(), "killswitch disengaged")
			} else {
				fmt.Fprintln(c.OutOrStdout(), "killswitch already disengaged (no-op)")
			}
			return nil
		},
	}
	killswitchCmd.AddCommand(off)

	return killswitchCmd
}

func printKillswitchStatus(c *cobra.Command, status KillswitchStatus, asJSON bool) error {
	out := c.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(status)
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ENGAGED\tREASON\tCEILING-SECONDS\tCEILING-DOLLARS")
	fmt.Fprintf(w, "%t\t%s\t%.0f\t%.2f\n", status.Engaged, status.Reason, status.CeilingSeconds, status.CeilingDollars)
	return w.Flush()
}
