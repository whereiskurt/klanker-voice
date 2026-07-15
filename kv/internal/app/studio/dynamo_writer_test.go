package studio

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-voice/kv/internal/app/electro"
)

// fakeDynamoWriteAPI implements DynamoWriteAPI over an in-memory recorder —
// no live AWS call is ever made. It records every request so tests can
// assert the exact Item/UpdateExpression/ConditionExpression a writer used,
// and can be configured to return an error (simulating a
// ConditionalCheckFailedException) — mirrors dynamo_adapter_test.go's
// fakeDynamoReadAPI pattern.
type fakeDynamoWriteAPI struct {
	putCalls []*dynamodb.PutItemInput
	putErr   error

	updateCalls []*dynamodb.UpdateItemInput
	updateErr   error

	deleteCalls []*dynamodb.DeleteItemInput
	deleteErr   error
}

func (f *fakeDynamoWriteAPI) PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.putCalls = append(f.putCalls, params)
	if f.putErr != nil {
		return nil, f.putErr
	}
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeDynamoWriteAPI) UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updateCalls = append(f.updateCalls, params)
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

func (f *fakeDynamoWriteAPI) DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	f.deleteCalls = append(f.deleteCalls, params)
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &dynamodb.DeleteItemOutput{}, nil
}

// --------------------------------------------------------------------------
// PutAccessCode

func TestPutAccessCode_ByteIdenticalToMarshal(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	expires := int64(1234567890000)
	maxRedemptions := int64(5)

	if err := PutAccessCode(context.Background(), fake, "kmv-auth-electro", "greenhouse-guest", "kph-tier", "conference", &expires, &maxRedemptions); err != nil {
		t.Fatalf("PutAccessCode() error: %v", err)
	}
	if len(fake.putCalls) != 1 {
		t.Fatalf("PutItem called %d times, want 1", len(fake.putCalls))
	}
	call := fake.putCalls[0]

	if call.ConditionExpression == nil || *call.ConditionExpression != "attribute_not_exists(pk)" {
		t.Errorf("ConditionExpression = %v, want %q", call.ConditionExpression, "attribute_not_exists(pk)")
	}

	want := electro.NewAccessCodeItem("greenhouse-guest", "kph-tier", "conference", &expires, &maxRedemptions).Marshal()
	assertItemsEqualExceptCreatedAt(t, call.Item, want)
}

func TestPutAccessCode_RejectsExisting(t *testing.T) {
	fake := &fakeDynamoWriteAPI{putErr: errors.New("ConditionalCheckFailedException: the conditional request failed")}
	err := PutAccessCode(context.Background(), fake, "kmv-auth-electro", "existing-code", "kph-tier", "", nil, nil)
	if err == nil {
		t.Fatalf("PutAccessCode() = nil, want a conditional-check error")
	}
}

func TestPutAccessCode_RejectsInvalidInputBeforeAnyCall(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	if err := PutAccessCode(context.Background(), fake, "kmv-auth-electro", "", "kph-tier", "", nil, nil); err == nil {
		t.Fatalf("PutAccessCode(blank code) = nil, want an error")
	}
	if err := PutAccessCode(context.Background(), fake, "kmv-auth-electro", "greenhouse-guest", "", "", nil, nil); err == nil {
		t.Fatalf("PutAccessCode(blank tierId) = nil, want an error")
	}
	if len(fake.putCalls) != 0 {
		t.Fatalf("PutItem called %d times on invalid input, want 0", len(fake.putCalls))
	}
}

// --------------------------------------------------------------------------
// PutTier

func TestPutTier_ByteIdenticalToMarshal(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	if err := PutTier(context.Background(), fake, "kmv-auth-electro", "no-access", "", 0, 0, 0); err != nil {
		t.Fatalf("PutTier() error: %v", err)
	}
	if len(fake.putCalls) != 1 {
		t.Fatalf("PutItem called %d times, want 1", len(fake.putCalls))
	}
	call := fake.putCalls[0]
	if call.ConditionExpression == nil || *call.ConditionExpression != "attribute_not_exists(pk)" {
		t.Errorf("ConditionExpression = %v, want %q", call.ConditionExpression, "attribute_not_exists(pk)")
	}
	want := electro.NewTierItem("no-access", "", 0, 0, 0).Marshal()
	assertItemsEqualExceptCreatedAt(t, call.Item, want)
}

func TestPutTier_RejectsExisting(t *testing.T) {
	fake := &fakeDynamoWriteAPI{putErr: errors.New("ConditionalCheckFailedException: the conditional request failed")}
	err := PutTier(context.Background(), fake, "kmv-auth-electro", "kph-tier", "", 3600, 7200, 1)
	if err == nil {
		t.Fatalf("PutTier() = nil, want a conditional-check error")
	}
}

// --------------------------------------------------------------------------
// UpdateAccessCodeTier

func TestUpdateAccessCodeTier_SetsTierIdOnly(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	if err := UpdateAccessCodeTier(context.Background(), fake, "kmv-auth-electro", "greenhouse-guest", "no-access"); err != nil {
		t.Fatalf("UpdateAccessCodeTier() error: %v", err)
	}
	if len(fake.updateCalls) != 1 {
		t.Fatalf("UpdateItem called %d times, want 1", len(fake.updateCalls))
	}
	call := fake.updateCalls[0]

	if call.UpdateExpression == nil || *call.UpdateExpression != "SET tierId = :t" {
		t.Errorf("UpdateExpression = %v, want %q", call.UpdateExpression, "SET tierId = :t")
	}
	if call.ConditionExpression == nil || *call.ConditionExpression != "attribute_exists(pk)" {
		t.Errorf("ConditionExpression = %v, want %q", call.ConditionExpression, "attribute_exists(pk)")
	}
	if len(call.ExpressionAttributeValues) != 1 {
		t.Fatalf("ExpressionAttributeValues has %d entries, want 1 (only :t) — side-attributes must never appear in an edit's UpdateExpression", len(call.ExpressionAttributeValues))
	}
	tAV, ok := call.ExpressionAttributeValues[":t"].(*types.AttributeValueMemberS)
	if !ok || tAV.Value != "no-access" {
		t.Errorf(":t = %v, want %q", call.ExpressionAttributeValues[":t"], "no-access")
	}

	wantPK := electro.AccessCodePK("greenhouse-guest")
	pkAV, ok := call.Key["pk"].(*types.AttributeValueMemberS)
	if !ok || pkAV.Value != wantPK {
		t.Errorf("Key[pk] = %v, want %q", call.Key["pk"], wantPK)
	}
}

func TestUpdateAccessCodeTier_RejectsInvalidInputBeforeAnyCall(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	if err := UpdateAccessCodeTier(context.Background(), fake, "kmv-auth-electro", "", "no-access"); err == nil {
		t.Fatalf("UpdateAccessCodeTier(blank code) = nil, want an error")
	}
	if err := UpdateAccessCodeTier(context.Background(), fake, "kmv-auth-electro", "greenhouse-guest", ""); err == nil {
		t.Fatalf("UpdateAccessCodeTier(blank tierId) = nil, want an error")
	}
	if len(fake.updateCalls) != 0 {
		t.Fatalf("UpdateItem called %d times on invalid input, want 0", len(fake.updateCalls))
	}
}

// --------------------------------------------------------------------------
// UpdateTierLimits

func TestUpdateTierLimits_SetsThreeLimitsOnly(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	if err := UpdateTierLimits(context.Background(), fake, "kmv-auth-electro", "recruiting-30", 1200, 3600, 2); err != nil {
		t.Fatalf("UpdateTierLimits() error: %v", err)
	}
	if len(fake.updateCalls) != 1 {
		t.Fatalf("UpdateItem called %d times, want 1", len(fake.updateCalls))
	}
	call := fake.updateCalls[0]

	wantExpr := "SET sessionMaxSeconds = :s, periodMaxSeconds = :p, maxConcurrent = :c"
	if call.UpdateExpression == nil || *call.UpdateExpression != wantExpr {
		t.Errorf("UpdateExpression = %v, want %q", call.UpdateExpression, wantExpr)
	}
	if call.ConditionExpression == nil || *call.ConditionExpression != "attribute_exists(pk)" {
		t.Errorf("ConditionExpression = %v, want %q", call.ConditionExpression, "attribute_exists(pk)")
	}
	if len(call.ExpressionAttributeValues) != 3 {
		t.Fatalf("ExpressionAttributeValues has %d entries, want 3 (:s, :p, :c only) — no other tier attribute must appear in an edit's UpdateExpression", len(call.ExpressionAttributeValues))
	}

	wantPK := electro.TierPK("recruiting-30")
	pkAV, ok := call.Key["pk"].(*types.AttributeValueMemberS)
	if !ok || pkAV.Value != wantPK {
		t.Errorf("Key[pk] = %v, want %q", call.Key["pk"], wantPK)
	}
	wantSK := electro.TierSK()
	skAV, ok := call.Key["sk"].(*types.AttributeValueMemberS)
	if !ok || skAV.Value != wantSK {
		t.Errorf("Key[sk] = %v, want %q", call.Key["sk"], wantSK)
	}

	sAV, ok := call.ExpressionAttributeValues[":s"].(*types.AttributeValueMemberN)
	if !ok || sAV.Value != "1200" {
		t.Errorf(":s = %v, want %q", call.ExpressionAttributeValues[":s"], "1200")
	}
	pAV, ok := call.ExpressionAttributeValues[":p"].(*types.AttributeValueMemberN)
	if !ok || pAV.Value != "3600" {
		t.Errorf(":p = %v, want %q", call.ExpressionAttributeValues[":p"], "3600")
	}
	cAV, ok := call.ExpressionAttributeValues[":c"].(*types.AttributeValueMemberN)
	if !ok || cAV.Value != "2" {
		t.Errorf(":c = %v, want %q", call.ExpressionAttributeValues[":c"], "2")
	}
}

func TestUpdateTierLimits_RejectsMissingTier(t *testing.T) {
	fake := &fakeDynamoWriteAPI{updateErr: errors.New("ConditionalCheckFailedException: the conditional request failed")}
	err := UpdateTierLimits(context.Background(), fake, "kmv-auth-electro", "no-such-tier", 1200, 3600, 2)
	if err == nil {
		t.Fatalf("UpdateTierLimits() = nil, want a conditional-check error for a tier that does not exist (it must not silently create one)")
	}
}

func TestUpdateTierLimits_NeverCallsPutItem(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	if err := UpdateTierLimits(context.Background(), fake, "kmv-auth-electro", "recruiting-30", 1200, 3600, 2); err != nil {
		t.Fatalf("UpdateTierLimits() error: %v", err)
	}
	if len(fake.putCalls) != 0 {
		t.Fatalf("PutItem called %d times, want 0 — editing an existing tier's limits must never go through PutTier's guarded PutItem (P-03-tier-update-not-put)", len(fake.putCalls))
	}
}

func TestUpdateTierLimits_RejectsInvalidInputBeforeAnyCall(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	if err := UpdateTierLimits(context.Background(), fake, "kmv-auth-electro", "", 1200, 3600, 2); err == nil {
		t.Fatalf("UpdateTierLimits(blank tierId) = nil, want an error")
	}
	if len(fake.updateCalls) != 0 {
		t.Fatalf("UpdateItem called %d times on invalid input, want 0", len(fake.updateCalls))
	}
}

// --------------------------------------------------------------------------
// SetPhoneMapping / RemovePhoneMapping

func TestSetPhoneMapping_Shape(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	normalized := "+14165551234"
	if err := SetPhoneMapping(context.Background(), fake, "kmv-auth-electro", "greenhouse-guest", normalized); err != nil {
		t.Fatalf("SetPhoneMapping() error: %v", err)
	}
	if len(fake.updateCalls) != 1 {
		t.Fatalf("UpdateItem called %d times, want 1", len(fake.updateCalls))
	}
	call := fake.updateCalls[0]
	if call.ConditionExpression == nil || *call.ConditionExpression != "attribute_exists(pk)" {
		t.Errorf("ConditionExpression = %v, want %q", call.ConditionExpression, "attribute_exists(pk)")
	}
	wantExpr := "SET phone = :phone, phoneEnabled = :t, gsi3pk = :g3pk, gsi3sk = :g3sk"
	if call.UpdateExpression == nil || *call.UpdateExpression != wantExpr {
		t.Errorf("UpdateExpression = %v, want %q", call.UpdateExpression, wantExpr)
	}
	wantGSI3PK := electro.AccessCodeGSI3PK(normalized)
	if av, ok := call.ExpressionAttributeValues[":g3pk"].(*types.AttributeValueMemberS); !ok || av.Value != wantGSI3PK {
		t.Errorf(":g3pk = %v, want %q", call.ExpressionAttributeValues[":g3pk"], wantGSI3PK)
	}
}

func TestRemovePhoneMapping_Shape(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	if err := RemovePhoneMapping(context.Background(), fake, "kmv-auth-electro", "greenhouse-guest"); err != nil {
		t.Fatalf("RemovePhoneMapping() error: %v", err)
	}
	if len(fake.updateCalls) != 1 {
		t.Fatalf("UpdateItem called %d times, want 1", len(fake.updateCalls))
	}
	call := fake.updateCalls[0]
	wantExpr := "REMOVE phone, phoneEnabled, gsi3pk, gsi3sk"
	if call.UpdateExpression == nil || *call.UpdateExpression != wantExpr {
		t.Errorf("UpdateExpression = %v, want %q", call.UpdateExpression, wantExpr)
	}
	if call.ConditionExpression == nil || *call.ConditionExpression != "attribute_exists(pk)" {
		t.Errorf("ConditionExpression = %v, want %q", call.ConditionExpression, "attribute_exists(pk)")
	}
}

// --------------------------------------------------------------------------
// DeleteAccessCode

func TestDeleteAccessCode_Shape(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	if err := DeleteAccessCode(context.Background(), fake, "kmv-auth-electro", "greenhouse-guest"); err != nil {
		t.Fatalf("DeleteAccessCode() error: %v", err)
	}
	if len(fake.deleteCalls) != 1 {
		t.Fatalf("DeleteItem called %d times, want 1", len(fake.deleteCalls))
	}
	call := fake.deleteCalls[0]
	if call.ConditionExpression == nil || *call.ConditionExpression != "attribute_exists(pk)" {
		t.Errorf("ConditionExpression = %v, want %q", call.ConditionExpression, "attribute_exists(pk)")
	}
	wantPK := electro.AccessCodePK("greenhouse-guest")
	pkAV, ok := call.Key["pk"].(*types.AttributeValueMemberS)
	if !ok || pkAV.Value != wantPK {
		t.Errorf("Key[pk] = %v, want %q", call.Key["pk"], wantPK)
	}
}

func TestDeleteAccessCode_RejectsInvalidInputBeforeAnyCall(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	if err := DeleteAccessCode(context.Background(), fake, "kmv-auth-electro", ""); err == nil {
		t.Fatalf("DeleteAccessCode(blank code) = nil, want an error")
	}
	if len(fake.deleteCalls) != 0 {
		t.Fatalf("DeleteItem called %d times on invalid input, want 0", len(fake.deleteCalls))
	}
}

// --------------------------------------------------------------------------
// helpers

// assertItemsEqualExceptCreatedAt compares two DynamoDB item maps
// key-by-key, skipping createdAt (time-based) — asserting instead that it is
// present and numeric.
func assertItemsEqualExceptCreatedAt(t *testing.T, got, want map[string]types.AttributeValue) {
	t.Helper()

	if _, ok := got["createdAt"]; !ok {
		t.Errorf("got item missing createdAt")
	} else if _, ok := got["createdAt"].(*types.AttributeValueMemberN); !ok {
		t.Errorf("got[createdAt] = %v, want a numeric (N) attribute", got["createdAt"])
	}

	for key, wantAV := range want {
		if key == "createdAt" {
			continue
		}
		gotAV, ok := got[key]
		if !ok {
			t.Errorf("got item missing key %q", key)
			continue
		}
		if !attributeValuesEqual(gotAV, wantAV) {
			t.Errorf("got[%q] = %#v, want %#v", key, gotAV, wantAV)
		}
	}
	for key := range got {
		if key == "createdAt" {
			continue
		}
		if _, ok := want[key]; !ok {
			t.Errorf("got item has unexpected extra key %q = %#v", key, got[key])
		}
	}
}

func attributeValuesEqual(a, b types.AttributeValue) bool {
	switch av := a.(type) {
	case *types.AttributeValueMemberS:
		bv, ok := b.(*types.AttributeValueMemberS)
		return ok && av.Value == bv.Value
	case *types.AttributeValueMemberN:
		bv, ok := b.(*types.AttributeValueMemberN)
		return ok && av.Value == bv.Value
	case *types.AttributeValueMemberBOOL:
		bv, ok := b.(*types.AttributeValueMemberBOOL)
		return ok && av.Value == bv.Value
	default:
		return false
	}
}
