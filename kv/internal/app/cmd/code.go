package cmd

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
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

// bypassTokenAlphabet is the base62 charset (a-zA-Z0-9) for bypass /join
// tokens. base62 keeps tokens URL-safe with no escaping needed in the
// /use1/join/<token> path segment.
const bypassTokenAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// bypassTokenLength is the number of base62 chars in a bypass token.
// 12 chars of base62 ≈ 71 bits of entropy — unguessable for a shared,
// operator-issued conference link, while staying short enough to paste.
const bypassTokenLength = 12

// generateBypassToken returns a cryptographically-random 12-char base62 token.
// Uses rejection sampling over the largest multiple of len(alphabet) so the
// distribution is unbiased (no modulo bias).
func generateBypassToken() (string, error) {
	const n = byte(len(bypassTokenAlphabet)) // 62
	maxUnbiased := byte(256 - (256 % int(n))) // 248: reject bytes >= 248
	out := make([]byte, 0, bypassTokenLength)
	buf := make([]byte, 1)
	for len(out) < bypassTokenLength {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("generate bypass token: %w", err)
		}
		if buf[0] >= maxUnbiased {
			continue // reject to avoid modulo bias
		}
		out = append(out, bypassTokenAlphabet[buf[0]%n])
	}
	return string(out), nil
}

// EnableBypass turns on bypass /join for a code: it generates a fresh random
// bypass token and UpdateItems the code's primary item, SETting bypassEnabled,
// bypassToken, and the sparse gsi2 key attributes (gsi2pk/gsi2sk) so the
// webapp's resolveBypassToken query finds it. Calling it again ROTATES the
// token (overwrites with a new one). Returns the new token.
func EnableBypass(ctx context.Context, client *dynamodb.Client, table, code string) (string, error) {
	if err := validateCodeCharset(code); err != nil {
		return "", err
	}
	token, err := generateBypassToken()
	if err != nil {
		return "", err
	}
	_, err = client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
			"sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
		},
		UpdateExpression: aws.String(
			"SET bypassEnabled = :t, bypassToken = :tok, gsi2pk = :g2pk, gsi2sk = :g2sk",
		),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":t":    &types.AttributeValueMemberBOOL{Value: true},
			":tok":  &types.AttributeValueMemberS{Value: token},
			":g2pk": &types.AttributeValueMemberS{Value: electro.AccessCodeGSI2PK(token)},
			":g2sk": &types.AttributeValueMemberS{Value: electro.AccessCodeGSI2SK()},
		},
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})
	if err != nil {
		return "", fmt.Errorf("enable bypass for code %q: %w", code, err)
	}
	return token, nil
}

// bypassJoinURL builds the shareable auto-login URL an operator hands out. The
// /join route lives on the AUTH app (Next.js App Router route), mounted under
// the region basePath — so the canonical URL is
// https://auth.klankermaker.ai/use1/join/<token>. Origin/region are overridable
// via KV_AUTH_ORIGIN / REGION_SHORT for non-prod deployments.
func bypassJoinURL(token string) string {
	origin := os.Getenv("KV_AUTH_ORIGIN")
	if origin == "" {
		origin = "https://auth.klankermaker.ai"
	}
	region := os.Getenv("REGION_SHORT")
	if region == "" {
		region = "use1"
	}
	return fmt.Sprintf("%s/%s/join/%s", origin, region, token)
}

// DisableBypass turns off bypass /join for a code: it REMOVEs bypassEnabled,
// bypassToken, and the gsi2 key attributes, dropping the code out of the sparse
// byBypassToken index so any existing /join link 404s immediately.
func DisableBypass(ctx context.Context, client *dynamodb.Client, table, code string) error {
	if err := validateCodeCharset(code); err != nil {
		return err
	}
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
			"sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
		},
		UpdateExpression:    aws.String("REMOVE bypassEnabled, bypassToken, gsi2pk, gsi2sk"),
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})
	if err != nil {
		return fmt.Errorf("disable bypass for code %q: %w", code, err)
	}
	return nil
}

// normalizeE164 reproduces apps/auth/webapp/src/lib/phone-normalization.ts's
// normalizeE164 canonical output rule byte-for-byte (12-RESEARCH.md Pitfall
// 3: divergent normalization silently breaks the byPhone GSI lookup): strip
// everything but digits and '+' characters, drop the leading '+' (re-added
// at the end), drop a leading trunk '0' run, and treat a bare 10-digit
// North-American local number as needing a prepended country code '1'.
//
// Unlike the TS helper — which is a passive ElectroDB `set` transform and
// must never throw, so it returns "" for blank/unmappable input — this Go
// helper returns an error on blank/no-digit input: `kv code phone` is an
// interactive CLI where a rejected number should stop the command, not
// silently write an empty phone key. For every other input the two
// normalizers produce byte-identical output (proven by the parity test in
// code_test.go).
func normalizeE164(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("phone number must not be blank")
	}

	var b strings.Builder
	for _, r := range trimmed {
		if r == '+' || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	cleaned := b.String()
	if cleaned == "" {
		return "", fmt.Errorf("phone number %q contains no digits", raw)
	}

	cleaned = strings.TrimPrefix(cleaned, "+")
	cleaned = strings.TrimLeft(cleaned, "0")

	if len(cleaned) == 10 {
		cleaned = "1" + cleaned
	}

	return "+" + cleaned, nil
}

// AddPhoneMapping maps a normalized E.164 phone number to a code: it
// UpdateItems the code's primary item, SETting phone, phoneEnabled, and the
// sparse gsi3 key attributes (gsi3pk/gsi3sk) so the webapp's
// resolvePhoneToCode query finds it. Mirrors EnableBypass exactly. Calling it
// again on the same code OVERWRITES the mapping with the new phone number.
func AddPhoneMapping(ctx context.Context, client *dynamodb.Client, table, code, normalizedPhone string) error {
	if err := validateCodeCharset(code); err != nil {
		return err
	}
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
			"sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
		},
		UpdateExpression: aws.String(
			"SET phone = :phone, phoneEnabled = :t, gsi3pk = :g3pk, gsi3sk = :g3sk",
		),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":phone": &types.AttributeValueMemberS{Value: normalizedPhone},
			":t":     &types.AttributeValueMemberBOOL{Value: true},
			":g3pk":  &types.AttributeValueMemberS{Value: electro.AccessCodeGSI3PK(normalizedPhone)},
			":g3sk":  &types.AttributeValueMemberS{Value: electro.AccessCodeGSI3SK()},
		},
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})
	if err != nil {
		return fmt.Errorf("add phone mapping for code %q: %w", code, err)
	}
	return nil
}

// RemovePhoneMapping drops a code's phone mapping: it REMOVEs phone,
// phoneEnabled, and the gsi3 key attributes, dropping the code out of the
// sparse byPhone index so any caller-ID lookup for its old number stops
// matching immediately. Mirrors DisableBypass exactly.
func RemovePhoneMapping(ctx context.Context, client *dynamodb.Client, table, code string) error {
	if err := validateCodeCharset(code); err != nil {
		return err
	}
	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
			"sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
		},
		UpdateExpression:    aws.String("REMOVE phone, phoneEnabled, gsi3pk, gsi3sk"),
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})
	if err != nil {
		return fmt.Errorf("remove phone mapping for code %q: %w", code, err)
	}
	return nil
}

// NewCodeCmd builds the "kv code" parent command with create/list/expire/bypass
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

	var (
		bypassDisable bool
		bypassRotate  bool
	)
	bypass := &cobra.Command{
		Use:   "bypass <code>",
		Short: "Enable, rotate, or disable the bypass /join auto-login link for a code",
		Long: "Manage the per-code bypass /join auto-login token.\n\n" +
			"  kv code bypass <code>            enable bypass (mint a token) and print the URL\n" +
			"  kv code bypass <code> --rotate   mint a NEW token (invalidates the old link)\n" +
			"  kv code bypass <code> --disable  turn bypass off (the /join link 404s)\n\n" +
			"The shareable URL points at the auth app's /join route:\n" +
			"  https://auth.klankermaker.ai/use1/join/<token>",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if bypassDisable && bypassRotate {
				return fmt.Errorf("--disable and --rotate are mutually exclusive")
			}
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			code := args[0]
			if bypassDisable {
				if err := DisableBypass(c.Context(), client, cfg.Table, code); err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "disabled bypass for code %q\n", electro.NormalizeCode(code))
				return nil
			}
			// enable (default) or rotate — both call EnableBypass, which mints a
			// fresh token (rotate is just an explicit re-enable that overwrites).
			token, err := EnableBypass(c.Context(), client, cfg.Table, code)
			if err != nil {
				return err
			}
			verb := "enabled"
			if bypassRotate {
				verb = "rotated"
			}
			fmt.Fprintf(c.OutOrStdout(), "%s bypass for code %q\n", verb, electro.NormalizeCode(code))
			fmt.Fprintf(c.OutOrStdout(), "join URL: %s\n", bypassJoinURL(token))
			return nil
		},
	}
	bypass.Flags().BoolVar(&bypassDisable, "disable", false, "disable bypass /join for this code")
	bypass.Flags().BoolVar(&bypassRotate, "rotate", false, "rotate to a fresh token (invalidates the previous link)")
	codeCmd.AddCommand(bypass)

	var (
		phoneAdd    string
		phoneRemove bool
	)
	phone := &cobra.Command{
		Use:   "phone <code>",
		Short: "Map or unmap a caller-ID phone number for a code (§23 VoIP.ms mint path)",
		Long: "Manage the per-code caller-ID phone mapping used by the §23 VoIP.ms\n" +
			"inbound-DID mint path.\n\n" +
			"  kv code phone <code> --add <e164>   map a phone number to this code\n" +
			"  kv code phone <code> --remove       unmap the phone number\n\n" +
			"The number is normalized to E.164 before it becomes the sparse gsi3\n" +
			"byPhone key — the exact canonical form the auth-app resolver expects.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if phoneAdd != "" && phoneRemove {
				return fmt.Errorf("--add and --remove are mutually exclusive")
			}
			if phoneAdd == "" && !phoneRemove {
				return fmt.Errorf("--add <e164> or --remove is required")
			}
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			code := args[0]
			if phoneRemove {
				if err := RemovePhoneMapping(c.Context(), client, cfg.Table, code); err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "removed phone mapping for code %q\n", electro.NormalizeCode(code))
				return nil
			}
			normalized, err := normalizeE164(phoneAdd)
			if err != nil {
				return fmt.Errorf("invalid phone number: %w", err)
			}
			if err := AddPhoneMapping(c.Context(), client, cfg.Table, code, normalized); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "mapped code %q -> %s\n", electro.NormalizeCode(code), normalized)
			return nil
		},
	}
	phone.Flags().StringVar(&phoneAdd, "add", "", "E.164 (or messy raw) phone number to map, e.g. \"+1 (416) 555-1234\"")
	phone.Flags().BoolVar(&phoneRemove, "remove", false, "remove the phone mapping for this code")
	codeCmd.AddCommand(phone)

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
