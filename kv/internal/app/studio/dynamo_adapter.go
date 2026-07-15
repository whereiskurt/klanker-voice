package studio

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/whereiskurt/klanker-voice/kv/internal/app/electro"
)

// DynamoReadAPI is the narrow subset of *dynamodb.Client this package needs
// — Query (for the GSI1 code/tier partitions) and Scan (for the
// phone-mapping base-table scan) — so unit tests inject an in-memory fake
// instead of a live table, mirroring cmd/telephony.go's telephonyScanAPI
// pattern.
type DynamoReadAPI interface {
	Query(ctx context.Context, params *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// CodeRecord is the read-side shape of an AccessCode item — the subset of
// attributes the studio console needs, including the optional phone/
// phoneEnabled attributes AddPhoneMapping sets on the same primary item.
type CodeRecord struct {
	Code         string `dynamodbav:"code"`
	TierID       string `dynamodbav:"tierId"`
	Phone        string `dynamodbav:"phone"`
	PhoneEnabled bool   `dynamodbav:"phoneEnabled"`
}

// TierRecord is the read-side shape of a Tier item.
type TierRecord struct {
	TierID            string `dynamodbav:"tierId"`
	SessionMaxSeconds int64  `dynamodbav:"sessionMaxSeconds"`
	PeriodMaxSeconds  int64  `dynamodbav:"periodMaxSeconds"`
	MaxConcurrent     int64  `dynamodbav:"maxConcurrent"`
}

// PhoneMappingRecord is the read-side shape of a phone-mapped AccessCode
// item — one row per DID caller-ID mapping (mirrors
// cmd/telephony.go's PhoneMappingRecord).
type PhoneMappingRecord struct {
	Phone        string `dynamodbav:"phone"`
	Code         string `dynamodbav:"code"`
	TierID       string `dynamodbav:"tierId"`
	PhoneEnabled bool   `dynamodbav:"phoneEnabled"`
}

// ReadCodes queries the gsi1pk-gsi1sk-index "accesscodes#" partition — the
// same GSI/partition cmd.ListAccessCodes reads — paginating over
// LastEvaluatedKey.
func ReadCodes(ctx context.Context, api DynamoReadAPI, table string) ([]CodeRecord, error) {
	out := []CodeRecord{}
	var lastKey map[string]types.AttributeValue
	for {
		resp, err := api.Query(ctx, &dynamodb.QueryInput{
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
		var page []CodeRecord
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

// ReadTiers queries the gsi1pk-gsi1sk-index "tiers#" partition — the same
// GSI/partition cmd.ListTiers reads — paginating over LastEvaluatedKey.
func ReadTiers(ctx context.Context, api DynamoReadAPI, table string) ([]TierRecord, error) {
	out := []TierRecord{}
	var lastKey map[string]types.AttributeValue
	for {
		resp, err := api.Query(ctx, &dynamodb.QueryInput{
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

// ReadPhoneMappings lists every phone-mapped access code via a base-table
// Scan with FilterExpression attribute_exists(phone), paginating over
// LastEvaluatedKey — the same documented acceptable filtered-scan choice
// cmd.ListPhoneMappings uses (there is no single GSI3 partition to Query for
// "all phone-mapped codes"; see cmd/telephony.go's ListPhoneMappings doc
// comment).
func ReadPhoneMappings(ctx context.Context, api DynamoReadAPI, table string) ([]PhoneMappingRecord, error) {
	out := []PhoneMappingRecord{}
	var lastKey map[string]types.AttributeValue
	for {
		resp, err := api.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(table),
			FilterExpression:  aws.String("attribute_exists(phone)"),
			ExclusiveStartKey: lastKey,
		})
		if err != nil {
			return nil, fmt.Errorf("scan phone mappings: %w", err)
		}
		var page []PhoneMappingRecord
		if err := attributevalue.UnmarshalListOfMaps(resp.Items, &page); err != nil {
			return nil, fmt.Errorf("unmarshal phone mappings: %w", err)
		}
		out = append(out, page...)
		if resp.LastEvaluatedKey == nil {
			break
		}
		lastKey = resp.LastEvaluatedKey
	}
	return out, nil
}
