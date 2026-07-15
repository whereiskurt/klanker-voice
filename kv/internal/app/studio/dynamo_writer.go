package studio

// Duplicated DynamoDB write primitives for the studio console's routing
// surface (RULE-02 create/edit/delete, RULE-04 block). package studio must
// not import package cmd (import cycle — see validate.go's package doc
// comment), so every function here is a documented duplicate of a cmd/*.go
// writer, or — for UpdateAccessCodeTier and UpdateTierLimits — a genuinely
// new function that does not exist in cmd today (16-RESEARCH.md Pattern 1 /
// Pitfall 1; 18-RESEARCH.md Pitfall 1 for UpdateTierLimits).
//
// Discipline enforced throughout this file:
//   - Edits to an EXISTING AccessCode use UpdateItem with a narrow SET/REMOVE
//     expression and ConditionExpression: attribute_exists(pk) — mirroring
//     cmd/code.go's AddPhoneMapping/RemovePhoneMapping/EnableBypass exactly.
//     NEVER PutItem on an existing item (that silently deletes side
//     attributes not present in the new item's Marshal() — Pitfall 1).
//   - Genuinely new items (PutAccessCode/PutTier) use PutItem, built via
//     electro.NewAccessCodeItem/electro.NewTierItem so the item SHAPE stays
//     byte-identical to cmd's CreateAccessCode/DefineTier, with one
//     deliberate, documented divergence: ConditionExpression:
//     attribute_not_exists(pk). This is stricter than the CLI (which
//     silently overwrites on a second create), per 16-RESEARCH.md Q5 — the
//     item bytes are unchanged, only write safety improves.

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-voice/kv/internal/app/electro"
)

// DynamoWriteAPI is the narrow subset of *dynamodb.Client this package needs
// to mutate AccessCode/Tier items — PutItem, UpdateItem, DeleteItem — so
// unit tests inject an in-memory recording fake instead of a live table,
// mirroring dynamo_adapter.go's DynamoReadAPI shape.
type DynamoWriteAPI interface {
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

// PutAccessCode writes a brand-new AccessCode item to table via PutItem,
// building the item with electro.NewAccessCodeItem so its pk/sk/gsi1 keys
// and ElectroDB bookkeeping markers exactly match cmd.CreateAccessCode's
// item shape (and, transitively, the webapp's ElectroDB entity). Unlike
// cmd.CreateAccessCode, this guards with ConditionExpression:
// attribute_not_exists(pk) — a deliberate, documented divergence (item shape
// unchanged; write safety stricter) per 16-RESEARCH.md Q5. Reserved for
// genuinely new codes; never call this to edit an existing code (use
// UpdateAccessCodeTier / SetPhoneMapping / RemovePhoneMapping instead —
// Pitfall 1).
func PutAccessCode(ctx context.Context, w DynamoWriteAPI, table, code, tierID, group string, expiresAt, maxRedemptions *int64) error {
	if err := validateCodeCharset(code); err != nil {
		return err
	}
	if err := validateTierID(tierID); err != nil {
		return err
	}
	item := electro.NewAccessCodeItem(code, tierID, group, expiresAt, maxRedemptions)
	_, err := w.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(table),
		Item:                item.Marshal(),
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	})
	if err != nil {
		return fmt.Errorf("put access code %q: %w", code, err)
	}
	return nil
}

// PutTier writes a brand-new Tier item to table via PutItem, building the
// item with electro.NewTierItem so it exactly matches cmd.DefineTier's item
// shape. Adds ConditionExpression: attribute_not_exists(pk) — the same
// create-only divergence PutAccessCode documents. Reserved for genuinely new
// tiers; never call this to edit an existing tier's limits — use
// UpdateTierLimits instead (18-RESEARCH.md Pitfall 1 / P-03-tier-update-not-put).
func PutTier(ctx context.Context, w DynamoWriteAPI, table, tierID, group string, sessionMaxSecs, periodMaxSecs, maxConcurrent int64) error {
	if err := validateTierID(tierID); err != nil {
		return err
	}
	item := electro.NewTierItem(tierID, group, sessionMaxSecs, periodMaxSecs, maxConcurrent)
	_, err := w.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(table),
		Item:                item.Marshal(),
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	})
	if err != nil {
		return fmt.Errorf("put tier %q: %w", tierID, err)
	}
	return nil
}

// UpdateAccessCodeTier repoints an EXISTING code's tierId at a different
// tier via a narrow UpdateItem — SET tierId only, ConditionExpression:
// attribute_exists(pk) — mirroring cmd.AddPhoneMapping's UpdateItem pattern
// exactly (never cmd.CreateAccessCode's PutItem, which would silently wipe
// phone/bypass/gsi2/gsi3 side attributes — 16-RESEARCH.md Pitfall 1). This
// function does not exist anywhere in cmd today; it is the single function
// BOTH the RULE-02 "edit grant" write path and the RULE-04 "block a number"
// write path call (block = UpdateAccessCodeTier(code, "no-access"), a
// zero-limit tier — no pipeline changes, see 16-RESEARCH.md Code Examples).
func UpdateAccessCodeTier(ctx context.Context, w DynamoWriteAPI, table, code, tierID string) error {
	if err := validateCodeCharset(code); err != nil {
		return err
	}
	if err := validateTierID(tierID); err != nil {
		return err
	}
	_, err := w.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
			"sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
		},
		UpdateExpression: aws.String("SET tierId = :t"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":t": &types.AttributeValueMemberS{Value: electro.NormalizeTierID(tierID)},
		},
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})
	if err != nil {
		return fmt.Errorf("update tier for code %q: %w", code, err)
	}
	return nil
}

// UpdateTierLimits edits an EXISTING tier's three DynamoDB limit attributes
// (sessionMaxSeconds/periodMaxSeconds/maxConcurrent) in place via a narrow
// UpdateItem — SET the three limits only, ConditionExpression:
// attribute_exists(pk) — mirroring UpdateAccessCodeTier exactly (18-RESEARCH.md
// Pitfall 1 / Pattern: the same "surgical UpdateItem, never a bare PutItem"
// discipline this file documents throughout). This is the ONE function that
// may edit an existing tier's limits; PutTier remains create-only
// (P-03-tier-update-not-put) — calling PutTier on a tierId this function
// would otherwise target fails the create-only guard rather than silently
// wiping any tier side-attribute PutTier's Marshal() doesn't carry.
func UpdateTierLimits(ctx context.Context, w DynamoWriteAPI, table, tierID string, sessionMaxSecs, periodMaxSecs, maxConcurrent int64) error {
	if err := validateTierID(tierID); err != nil {
		return err
	}
	_, err := w.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.TierPK(tierID)},
			"sk": &types.AttributeValueMemberS{Value: electro.TierSK()},
		},
		UpdateExpression: aws.String("SET sessionMaxSeconds = :s, periodMaxSeconds = :p, maxConcurrent = :c"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s": &types.AttributeValueMemberN{Value: itoa(sessionMaxSecs)},
			":p": &types.AttributeValueMemberN{Value: itoa(periodMaxSecs)},
			":c": &types.AttributeValueMemberN{Value: itoa(maxConcurrent)},
		},
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})
	if err != nil {
		return fmt.Errorf("update tier limits for %q: %w", tierID, err)
	}
	return nil
}

// itoa formats an int64 as a DynamoDB numeric attribute string — a local
// copy of electro.itoa (unexported there), matching this file's documented
// duplication convention for narrow write-path helpers.
func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

// SetPhoneMapping maps a normalized E.164 phone number to an EXISTING code:
// duplicates cmd.AddPhoneMapping (cmd/code.go:281-306) verbatim —
// UpdateItem SETting phone, phoneEnabled, and the sparse gsi3 key
// attributes, ConditionExpression: attribute_exists(pk). Callers must pass
// an already-normalized phone (via normalizeE164) — this function does not
// normalize.
func SetPhoneMapping(ctx context.Context, w DynamoWriteAPI, table, code, normalizedPhone string) error {
	if err := validateCodeCharset(code); err != nil {
		return err
	}
	_, err := w.UpdateItem(ctx, &dynamodb.UpdateItemInput{
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
		return fmt.Errorf("set phone mapping for code %q: %w", code, err)
	}
	return nil
}

// RemovePhoneMapping drops an EXISTING code's phone mapping: duplicates
// cmd.RemovePhoneMapping (cmd/code.go:312-329) verbatim — UpdateItem
// REMOVEing phone, phoneEnabled, gsi3pk, gsi3sk, ConditionExpression:
// attribute_exists(pk).
func RemovePhoneMapping(ctx context.Context, w DynamoWriteAPI, table, code string) error {
	if err := validateCodeCharset(code); err != nil {
		return err
	}
	_, err := w.UpdateItem(ctx, &dynamodb.UpdateItemInput{
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

// DeleteAccessCode deletes an EXISTING code's primary item via DeleteItem
// (RULE-02 delete), ConditionExpression: attribute_exists(pk) so deleting an
// already-gone code fails loudly rather than silently no-op-succeeding.
func DeleteAccessCode(ctx context.Context, w DynamoWriteAPI, table, code string) error {
	if err := validateCodeCharset(code); err != nil {
		return err
	}
	_, err := w.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
			"sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
		},
		ConditionExpression: aws.String("attribute_exists(pk)"),
	})
	if err != nil {
		return fmt.Errorf("delete access code %q: %w", code, err)
	}
	return nil
}
