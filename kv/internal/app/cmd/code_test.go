package cmd

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-voice/kv/internal/app/electro"
)

// TestNormalizeE164 asserts the Go normalizer reproduces the auth-app
// helper's (apps/auth/webapp/src/lib/phone-normalization.ts) canonical
// output for every shared, non-blank input shape (12-RESEARCH.md Pitfall
// 3). Blank/no-digit input is the one documented divergence: the TS helper
// (a passive ElectroDB `set` transform) returns "" for those inputs, while
// this interactive-CLI helper returns an error instead — never a silently
// empty phone key.
func TestNormalizeE164(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "spaced/parenthesized/dashed", input: "+1 (416) 555-1234", want: "+14165551234"},
		{name: "dashed with leading 1", input: "1-416-555-1234", want: "+14165551234"},
		{name: "bare 10-digit local", input: "416-555-1234", want: "+14165551234"},
		{name: "already canonical (idempotent)", input: "+14165551234", want: "+14165551234"},
		{name: "blank", input: "", wantErr: true},
		{name: "whitespace-only", input: "   ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeE164(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeE164(%q) = %q, nil; want an error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeE164(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("normalizeE164(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestNormalizeE164_AuthAppParity documents — with an explicit assertion,
// not just a comment — that the Go canonical output equals the auth-app
// canonical output (apps/auth/webapp/src/lib/phone-normalization.ts) for
// every case both normalizers accept. A byPhone GSI lookup silently 404s
// forever the moment these two diverge (12-RESEARCH.md Pitfall 3), so this
// test hardcodes the auth-app helper's own outputs (transcribed from its
// source, not re-derived) as the parity oracle.
func TestNormalizeE164_AuthAppParity(t *testing.T) {
	// authAppNormalizeE164Cases mirrors normalizeE164() in
	// apps/auth/webapp/src/lib/phone-normalization.ts applied to the same
	// inputs — kept here as a literal table (not a call into TS) so this
	// test fails loudly if either side's behavior for these inputs ever
	// changes without the other being updated in lockstep.
	authAppCases := map[string]string{
		"+1 (416) 555-1234": "+14165551234",
		"1-416-555-1234":    "+14165551234",
		"416-555-1234":      "+14165551234",
		"+14165551234":      "+14165551234",
	}
	for input, authAppWant := range authAppCases {
		goGot, err := normalizeE164(input)
		if err != nil {
			t.Fatalf("normalizeE164(%q) unexpected error: %v", input, err)
		}
		if goGot != authAppWant {
			t.Errorf("normalization parity broken: Go normalizeE164(%q) = %q, auth-app normalizeE164(%q) = %q",
				input, goGot, input, authAppWant)
		}
	}
}

// TestAddPhoneMapping proves AddPhoneMapping's UpdateItem correctly sets
// phone/phoneEnabled and the exact gsi3 key values a phone-mapped code
// needs, and that RemovePhoneMapping strips all four attributes back out
// (dropping the code from the sparse byPhone index) — against a real
// dynamodb-local instance, mirroring roundtrip_test.go's pattern. Skips
// (not fails) if dynamodb-local is unreachable, so `go test ./...` stays
// green in sandboxes without a running container.
func TestAddPhoneMapping(t *testing.T) {
	client := testDynamoClient(t)
	ctx := context.Background()
	code := "phone-add-" + randomSuffix()

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

	normalized, err := normalizeE164("+1 (416) 555-1234")
	if err != nil {
		t.Fatalf("normalizeE164: %v", err)
	}
	if err := AddPhoneMapping(ctx, client, roundTripTable, code, normalized); err != nil {
		t.Fatalf("AddPhoneMapping: %v", err)
	}

	resp, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(roundTripTable),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
			"sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
		},
	})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if resp.Item == nil {
		t.Fatalf("GetItem found nothing for code %q after AddPhoneMapping", code)
	}

	wantGSI3PK := electro.AccessCodeGSI3PK(normalized)
	wantGSI3SK := electro.AccessCodeGSI3SK()
	if av, ok := resp.Item["phone"].(*types.AttributeValueMemberS); !ok || av.Value != normalized {
		t.Errorf("phone = %v, want %q", resp.Item["phone"], normalized)
	}
	if av, ok := resp.Item["phoneEnabled"].(*types.AttributeValueMemberBOOL); !ok || !av.Value {
		t.Errorf("phoneEnabled = %v, want true", resp.Item["phoneEnabled"])
	}
	if av, ok := resp.Item["gsi3pk"].(*types.AttributeValueMemberS); !ok || av.Value != wantGSI3PK {
		t.Errorf("gsi3pk = %v, want %q", resp.Item["gsi3pk"], wantGSI3PK)
	}
	if av, ok := resp.Item["gsi3sk"].(*types.AttributeValueMemberS); !ok || av.Value != wantGSI3SK {
		t.Errorf("gsi3sk = %v, want %q", resp.Item["gsi3sk"], wantGSI3SK)
	}
}

// TestRemovePhoneMapping proves RemovePhoneMapping strips phone,
// phoneEnabled, gsi3pk, and gsi3sk back off a code that had a phone mapping
// — dropping it out of the sparse byPhone index (Task 2 acceptance
// criteria). Skips if dynamodb-local is unreachable.
func TestRemovePhoneMapping(t *testing.T) {
	client := testDynamoClient(t)
	ctx := context.Background()
	code := "phone-remove-" + randomSuffix()

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

	normalized, err := normalizeE164("+14165551234")
	if err != nil {
		t.Fatalf("normalizeE164: %v", err)
	}
	if err := AddPhoneMapping(ctx, client, roundTripTable, code, normalized); err != nil {
		t.Fatalf("AddPhoneMapping: %v", err)
	}
	if err := RemovePhoneMapping(ctx, client, roundTripTable, code); err != nil {
		t.Fatalf("RemovePhoneMapping: %v", err)
	}

	resp, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(roundTripTable),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: electro.AccessCodePK(code)},
			"sk": &types.AttributeValueMemberS{Value: electro.AccessCodeSK()},
		},
	})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if resp.Item == nil {
		t.Fatalf("GetItem found nothing for code %q after RemovePhoneMapping", code)
	}
	for _, attr := range []string{"phone", "phoneEnabled", "gsi3pk", "gsi3sk"} {
		if _, present := resp.Item[attr]; present {
			t.Errorf("attribute %q still present after RemovePhoneMapping: %v", attr, resp.Item[attr])
		}
	}
}

// TestAddPhoneMapping_RequiresExistingCode proves the ConditionExpression
// attribute_exists(pk) mitigation (T-12-03-01: a phone mapping must not be
// writable onto a non-existent code) — mirrors EnableBypass's own
// condition. Skips if dynamodb-local is unreachable.
func TestAddPhoneMapping_RequiresExistingCode(t *testing.T) {
	client := testDynamoClient(t)
	ctx := context.Background()
	code := "phone-nonexistent-" + randomSuffix()

	normalized, err := normalizeE164("+14165551234")
	if err != nil {
		t.Fatalf("normalizeE164: %v", err)
	}
	if err := AddPhoneMapping(ctx, client, roundTripTable, code, normalized); err == nil {
		t.Fatalf("AddPhoneMapping on nonexistent code %q succeeded, want a ConditionalCheckFailed error", code)
	}
}
