package studio

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-voice/kv/internal/app/electro"
)

// fakeDynamoReadAPI implements DynamoReadAPI over canned, call-ordered
// Query/Scan responses (or a configurable error) — no live AWS call is ever
// made. It records every request so tests can assert the exact
// index/partition-key an adapter used.
type fakeDynamoReadAPI struct {
	queryResponses []*dynamodb.QueryOutput
	queryErr       error
	queryCalls     []*dynamodb.QueryInput

	scanResponses []*dynamodb.ScanOutput
	scanErr       error
	scanCalls     []*dynamodb.ScanInput
}

func (f *fakeDynamoReadAPI) Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.queryCalls = append(f.queryCalls, params)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	idx := len(f.queryCalls) - 1
	if idx >= len(f.queryResponses) {
		return &dynamodb.QueryOutput{}, nil
	}
	return f.queryResponses[idx], nil
}

func (f *fakeDynamoReadAPI) Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	f.scanCalls = append(f.scanCalls, params)
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	idx := len(f.scanCalls) - 1
	if idx >= len(f.scanResponses) {
		return &dynamodb.ScanOutput{}, nil
	}
	return f.scanResponses[idx], nil
}

func mustMarshalItems(t *testing.T, items []map[string]any) []map[string]types.AttributeValue {
	t.Helper()
	out := make([]map[string]types.AttributeValue, 0, len(items))
	for _, m := range items {
		av, err := attributevalue.MarshalMap(m)
		if err != nil {
			t.Fatalf("marshal item: %v", err)
		}
		out = append(out, av)
	}
	return out
}

// --------------------------------------------------------------------------
// ReadCodes

func TestReadCodes_UsesElectroGSI1KeyTemplate(t *testing.T) {
	fake := &fakeDynamoReadAPI{
		queryResponses: []*dynamodb.QueryOutput{
			{Items: mustMarshalItems(t, []map[string]any{
				{"code": "defcon34", "tierId": "kph-tier", "phone": "+14165551234", "phoneEnabled": true},
				{"code": "greenhouse-guest", "tierId": "pstn-baseline-tier"},
			})},
		},
	}
	got, err := ReadCodes(context.Background(), fake, "kmv-auth-electro")
	if err != nil {
		t.Fatalf("ReadCodes() error: %v", err)
	}
	if len(fake.queryCalls) != 1 {
		t.Fatalf("Query called %d times, want 1", len(fake.queryCalls))
	}
	call := fake.queryCalls[0]
	if call.IndexName == nil || *call.IndexName != electro.GSI1IndexName {
		t.Errorf("IndexName = %v, want %q", call.IndexName, electro.GSI1IndexName)
	}
	pkAV, ok := call.ExpressionAttributeValues[":pk"].(*types.AttributeValueMemberS)
	if !ok || pkAV.Value != electro.AccessCodeGSI1PK() {
		t.Errorf(":pk = %+v, want %q", call.ExpressionAttributeValues[":pk"], electro.AccessCodeGSI1PK())
	}

	want := []CodeRecord{
		{Code: "defcon34", TierID: "kph-tier", Phone: "+14165551234", PhoneEnabled: true},
		{Code: "greenhouse-guest", TierID: "pstn-baseline-tier"},
	}
	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestReadCodes_Paginates(t *testing.T) {
	nextKey := map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "code#defcon34"}}
	fake := &fakeDynamoReadAPI{
		queryResponses: []*dynamodb.QueryOutput{
			{
				Items:            mustMarshalItems(t, []map[string]any{{"code": "a", "tierId": "t1"}}),
				LastEvaluatedKey: nextKey,
			},
			{
				Items: mustMarshalItems(t, []map[string]any{{"code": "b", "tierId": "t2"}}),
			},
		},
	}
	got, err := ReadCodes(context.Background(), fake, "kmv-auth-electro")
	if err != nil {
		t.Fatalf("ReadCodes() error: %v", err)
	}
	if len(fake.queryCalls) != 2 {
		t.Fatalf("Query called %d times, want 2 (pagination)", len(fake.queryCalls))
	}
	if fake.queryCalls[1].ExclusiveStartKey == nil {
		t.Error("second Query call did not carry ExclusiveStartKey from the first page's LastEvaluatedKey")
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 across both pages", len(got))
	}
}

func TestReadCodes_PropagatesQueryError(t *testing.T) {
	fake := &fakeDynamoReadAPI{queryErr: errors.New("access denied")}
	_, err := ReadCodes(context.Background(), fake, "kmv-auth-electro")
	if err == nil {
		t.Fatal("ReadCodes() error = nil, want a wrapped error")
	}
}

func TestReadCodes_EmptyIsNonNil(t *testing.T) {
	fake := &fakeDynamoReadAPI{queryResponses: []*dynamodb.QueryOutput{{Items: mustMarshalItems(t, nil)}}}
	got, err := ReadCodes(context.Background(), fake, "kmv-auth-electro")
	if err != nil {
		t.Fatalf("ReadCodes() error: %v", err)
	}
	if got == nil {
		t.Fatal("ReadCodes() returned nil, want a non-nil empty slice")
	}
}

// --------------------------------------------------------------------------
// ReadTiers

func TestReadTiers_UsesElectroGSI1KeyTemplate(t *testing.T) {
	fake := &fakeDynamoReadAPI{
		queryResponses: []*dynamodb.QueryOutput{
			{Items: mustMarshalItems(t, []map[string]any{
				{"tierId": "kph-tier", "sessionMaxSeconds": int64(600), "periodMaxSeconds": int64(3600), "maxConcurrent": int64(4)},
			})},
		},
	}
	got, err := ReadTiers(context.Background(), fake, "kmv-auth-electro")
	if err != nil {
		t.Fatalf("ReadTiers() error: %v", err)
	}
	call := fake.queryCalls[0]
	if call.IndexName == nil || *call.IndexName != electro.GSI1IndexName {
		t.Errorf("IndexName = %v, want %q", call.IndexName, electro.GSI1IndexName)
	}
	pkAV, ok := call.ExpressionAttributeValues[":pk"].(*types.AttributeValueMemberS)
	if !ok || pkAV.Value != electro.TierGSI1PK() {
		t.Errorf(":pk = %+v, want %q", call.ExpressionAttributeValues[":pk"], electro.TierGSI1PK())
	}
	want := TierRecord{TierID: "kph-tier", SessionMaxSeconds: 600, PeriodMaxSeconds: 3600, MaxConcurrent: 4}
	if len(got) != 1 || got[0] != want {
		t.Errorf("got = %+v, want [%+v]", got, want)
	}
}

// TestReadTiers_PropagatesQueryError mirrors
// TestReadCodes_PropagatesQueryError / TestReadPhoneMappings_PropagatesScanError
// — ReadTiers was the one of the three read adapters with no error-case
// test (19-03-PLAN.md Task 3's §9 gap audit): assembleConfig's
// AssembleInput.DynamoErr short-circuit (view.go) depends on EVERY read
// adapter returning a non-nil error on an AWS failure, not just two of the
// three.
func TestReadTiers_PropagatesQueryError(t *testing.T) {
	fake := &fakeDynamoReadAPI{queryErr: errors.New("access denied")}
	_, err := ReadTiers(context.Background(), fake, "kmv-auth-electro")
	if err == nil {
		t.Fatal("ReadTiers() error = nil, want a wrapped error")
	}
}

// --------------------------------------------------------------------------
// ReadPhoneMappings

func TestReadPhoneMappings_ScansWithFilterExpression(t *testing.T) {
	fake := &fakeDynamoReadAPI{
		scanResponses: []*dynamodb.ScanOutput{
			{Items: mustMarshalItems(t, []map[string]any{
				{"phone": "+14165551234", "code": "defcon34", "tierId": "kph-tier", "phoneEnabled": true},
			})},
		},
	}
	got, err := ReadPhoneMappings(context.Background(), fake, "kmv-auth-electro")
	if err != nil {
		t.Fatalf("ReadPhoneMappings() error: %v", err)
	}
	call := fake.scanCalls[0]
	if call.FilterExpression == nil || *call.FilterExpression != "attribute_exists(phone)" {
		t.Errorf("FilterExpression = %v, want attribute_exists(phone)", call.FilterExpression)
	}
	want := PhoneMappingRecord{Phone: "+14165551234", Code: "defcon34", TierID: "kph-tier", PhoneEnabled: true}
	if len(got) != 1 || got[0] != want {
		t.Errorf("got = %+v, want [%+v]", got, want)
	}
}

func TestReadPhoneMappings_PropagatesScanError(t *testing.T) {
	fake := &fakeDynamoReadAPI{scanErr: errors.New("throttled")}
	_, err := ReadPhoneMappings(context.Background(), fake, "kmv-auth-electro")
	if err == nil {
		t.Fatal("ReadPhoneMappings() error = nil, want a wrapped error")
	}
}
