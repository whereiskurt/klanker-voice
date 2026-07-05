package cmd

// RoundTrip proves the Pitfall-1 gate bidirectionally against a real
// dynamodb-local instance:
//
//  1. kv creates a code -> a raw GetItem built with the webapp's expected
//     pk/sk (electro.AccessCodePK/AccessCodeSK) returns the item.
//  2. A webapp-shaped item (PutItem with the same key strings + ElectroDB
//     bookkeeping markers, simulating what AccessCode.create() would write)
//     is found by kv's own ListAccessCodes (the gsi1 query `kv code list`
//     uses).
//
// Requires a local DynamoDB (dynamodb-local) reachable at
// KV_TEST_DYNAMODB_ENDPOINT (default http://localhost:8888) with the
// kmv-auth-electro table already created (pk/sk + gsi1pk-gsi1sk-index) —
// the same table 03-02's own tests provisioned. If the endpoint is
// unreachable, the test is skipped (not failed) so `go test ./...` stays
// green in sandboxes without a running container; keys_test.go's pure
// string-equality assertions remain the always-on gate.

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-voice/kv/internal/app/electro"
)

const roundTripTable = "kmv-auth-electro"

func testDynamoClient(t *testing.T) *dynamodb.Client {
	t.Helper()
	endpoint := "http://localhost:8888"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("local", "local", "")),
	)
	if err != nil {
		t.Skipf("skipping round-trip test: could not load aws config: %v", err)
	}
	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	// Probe reachability + table existence; skip (not fail) if unavailable
	// so this test doesn't block `go test ./...` in a sandbox with no
	// container running.
	if _, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(roundTripTable),
	}); err != nil {
		t.Skipf("skipping round-trip test: dynamodb-local table %q unreachable: %v", roundTripTable, err)
	}
	return client
}

// TestRoundTrip_KVWriteWebappRead: kv creates a code; a raw GetItem built
// with the exact key strings the webapp's AccessCode.get({code}) composes
// must return it.
func TestRoundTrip_KVWriteWebappRead(t *testing.T) {
	client := testDynamoClient(t)
	ctx := context.Background()
	code := "roundtrip-kv-write-" + randomSuffix()

	if err := CreateAccessCode(ctx, client, roundTripTable, code, "roundtrip-tier", "", nil, nil); err != nil {
		t.Fatalf("CreateAccessCode: %v", err)
	}
	t.Cleanup(func() {
		_, _ = client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: aws.String(roundTripTable),
			Key: map[string]types.AttributeValue{
				"pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
				"sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
			},
		})
	})

	// Simulate the webapp's AccessCode.get({code}).go() — a GetItem with
	// exactly the key strings its ElectroDB entity would compose.
	resp, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(roundTripTable),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
			"sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
		},
	})
	if err != nil {
		t.Fatalf("webapp-shaped GetItem: %v", err)
	}
	if resp.Item == nil {
		t.Fatalf("webapp-shaped GetItem found nothing for kv-written code %q — Pitfall 1 regression", code)
	}
	tierAV, ok := resp.Item["tierId"].(*types.AttributeValueMemberS)
	if !ok || tierAV.Value != "roundtrip-tier" {
		t.Errorf("tierId = %v, want %q", resp.Item["tierId"], "roundtrip-tier")
	}
	entityAV, ok := resp.Item[electro.EDBEntityAttr].(*types.AttributeValueMemberS)
	if !ok || entityAV.Value != electro.AccessCodeEntityName {
		t.Errorf("__edb_e__ = %v, want %q", resp.Item[electro.EDBEntityAttr], electro.AccessCodeEntityName)
	}
}

// TestRoundTrip_WebappWriteKVRead: a webapp-shaped PutItem (same pk/sk/gsi1
// key strings + ElectroDB bookkeeping markers AccessCode.create() would
// write) must be found by kv's own ListAccessCodes (the gsi1 query `kv code
// list` performs).
func TestRoundTrip_WebappWriteKVRead(t *testing.T) {
	client := testDynamoClient(t)
	ctx := context.Background()
	code := "roundtrip-webapp-write-" + randomSuffix()

	// Simulate a webapp-side AccessCode.create({code, tierId}) write: same
	// key composition, same bookkeeping markers, built independently of
	// electro.NewAccessCodeItem to prove the *template*, not just our own
	// helper, is what's being matched.
	webappItem := map[string]types.AttributeValue{
		"pk":                   &types.AttributeValueMemberS{Value: "code#" + code},
		"sk":                   &types.AttributeValueMemberS{Value: "code#"},
		"gsi1pk":               &types.AttributeValueMemberS{Value: "accesscodes#"},
		"gsi1sk":               &types.AttributeValueMemberS{Value: "code#" + code},
		electro.EDBEntityAttr:  &types.AttributeValueMemberS{Value: electro.AccessCodeEntityName},
		electro.EDBVersionAttr: &types.AttributeValueMemberS{Value: electro.EDBVersion},
		"code":                 &types.AttributeValueMemberS{Value: code},
		"tierId":               &types.AttributeValueMemberS{Value: "roundtrip-tier"},
		"redemptionCount":      &types.AttributeValueMemberN{Value: "0"},
		"createdAt":            &types.AttributeValueMemberN{Value: "1"},
	}
	if _, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(roundTripTable),
		Item:      webappItem,
	}); err != nil {
		t.Fatalf("webapp-shaped PutItem: %v", err)
	}
	t.Cleanup(func() {
		_, _ = client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: aws.String(roundTripTable),
			Key: map[string]types.AttributeValue{
				"pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
				"sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
			},
		})
	})

	records, err := ListAccessCodes(ctx, client, roundTripTable)
	if err != nil {
		t.Fatalf("ListAccessCodes: %v", err)
	}
	found := false
	for _, r := range records {
		if r.Code == code {
			found = true
			if r.TierID != "roundtrip-tier" {
				t.Errorf("found code %q but tierId = %q, want %q", code, r.TierID, "roundtrip-tier")
			}
			break
		}
	}
	if !found {
		t.Fatalf("kv code list did not find webapp-written code %q — Pitfall 1 regression", code)
	}
}

func randomSuffix() string {
	return time.Now().UTC().Format("150405.000000000")
}
