// Package electro reproduces the ElectroDB key templates from
// apps/auth/webapp/src/entities/{access-code,tier}.ts byte-for-byte, so
// items written by kv are readable by the webapp's ElectroDB entities and
// vice versa (03-auth-service-access-codes Pitfall 1 / T-03-10 / T-03-17).
//
// FINAL key templates (source of truth: 03-02-SUMMARY.md "Key Templates",
// verified against the live entity files):
//
//	AccessCode  primary pk: "code#${code}"    sk: "code#"
//	            gsi1    pk: "accesscodes#"    sk: "code#${code}"
//	            gsi2    pk: "bypass#${bypassToken}" sk: "bypass#"  (SPARSE, bypass /join)
//	            gsi3    pk: "phone#${phone}"  sk: "phone#"         (SPARSE, §23 caller-ID mint)
//	Tier        primary pk: "tier#${tierId}"  sk: "tier#"
//	            gsi1    pk: "tiers#"          sk: "tier#${tierId}"
//
// The AccessCode gsi2 (byBypassToken) index powers the bypass /join auto-login
// feature (2026-07-10-bypass-join-login-design). It is SPARSE: only codes with
// a bypassToken set carry gsi2pk/gsi2sk. `kv code bypass` SETs both with these
// exact templates; the webapp's resolveBypassToken queries this index.
//
// The AccessCode gsi3 (byPhone) index powers the §23 VoIP.ms caller-ID mint
// path (Phase 12 Plan 02/03). It is SPARSE, mirroring gsi2 exactly: only
// codes with a `phone` set carry gsi3pk/gsi3sk. `kv code phone --add` SETs
// both with these exact templates; the webapp's resolvePhoneToCode queries
// this index.
//
// `code` and `tierId` are normalized lowercase+trim identically to the
// webapp's `normalizeCode()` (access-code.ts) and the Tier entity's `set`
// transform, so mixed-case writes from either side never diverge.
package electro

import (
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ElectroDB bookkeeping markers (observed byte-for-byte on live
// kmv-auth-electro items via `aws dynamodb scan`): every ElectroDB-managed
// item carries __edb_e__ (entity name) and __edb_v__ (entity "version",
// ElectroDB's model.version — a string, not a semver).
const (
	EDBEntityAttr  = "__edb_e__"
	EDBVersionAttr = "__edb_v__"

	AccessCodeEntityName = "AccessCode"
	TierEntityName       = "Tier"
	EDBVersion           = "1"

	GSI1IndexName = "gsi1pk-gsi1sk-index"
	GSI2IndexName = "gsi2pk-gsi2sk-index"
	GSI3IndexName = "gsi3pk-gsi3sk-index"
)

// NormalizeCode lowercases+trims a raw code exactly like the webapp's
// AccessCode.code `set` transform / normalizeCode() helper.
func NormalizeCode(code string) string {
	return strings.ToLower(strings.TrimSpace(code))
}

// NormalizeTierID lowercases+trims a raw tier id exactly like the webapp's
// Tier.tierId `set` transform.
func NormalizeTierID(tierID string) string {
	return strings.ToLower(strings.TrimSpace(tierID))
}

// --- AccessCode key templates ---

// AccessCodePK builds the AccessCode primary partition key: "code#${code}".
func AccessCodePK(code string) string {
	return "code#" + NormalizeCode(code)
}

// AccessCodeSK is the AccessCode primary sort key: the constant "code#"
// (empty composite — ElectroDB templates with no composite attributes still
// emit the literal template string).
func AccessCodeSK() string {
	return "code#"
}

// AccessCodeGSI1PK is the AccessCode gsi1 partition key: the constant
// "accesscodes#" (every code lives in one partition for `kv code list`).
func AccessCodeGSI1PK() string {
	return "accesscodes#"
}

// AccessCodeGSI1SK builds the AccessCode gsi1 sort key: "code#${code}".
func AccessCodeGSI1SK(code string) string {
	return "code#" + NormalizeCode(code)
}

// AccessCodeGSI2PK builds the AccessCode gsi2 (byBypassToken) partition key:
// "bypass#${bypassToken}". The bypass token is a random base62 string and is
// NOT case-normalized (unlike code/tierId) — it is an opaque secret whose case
// is significant, matching the webapp entity's plain "bypass#${bypassToken}"
// template (no `set` transform on bypassToken).
func AccessCodeGSI2PK(bypassToken string) string {
	return "bypass#" + bypassToken
}

// AccessCodeGSI2SK is the AccessCode gsi2 sort key: the constant "bypass#"
// (empty composite in the ElectroDB template).
func AccessCodeGSI2SK() string {
	return "bypass#"
}

// AccessCodeGSI3PK builds the AccessCode gsi3 (byPhone) partition key:
// "phone#${phone}". The phone value is expected to already be normalized to
// canonical E.164 (digits + a single leading '+') by the caller — unlike
// code/tierId, no case transform is applied here: digits have no casing, and
// (mirroring the gsi2 bypassToken casing note) the webapp entity's byPhone
// index also declares casing:"none", so this key writer must not alter the
// input in any way.
func AccessCodeGSI3PK(phone string) string {
	return "phone#" + phone
}

// AccessCodeGSI3SK is the AccessCode gsi3 sort key: the constant "phone#"
// (empty composite in the ElectroDB template).
func AccessCodeGSI3SK() string {
	return "phone#"
}

// --- Tier key templates ---

// TierPK builds the Tier primary partition key: "tier#${tierId}".
func TierPK(tierID string) string {
	return "tier#" + NormalizeTierID(tierID)
}

// TierSK is the Tier primary sort key: the constant "tier#".
func TierSK() string {
	return "tier#"
}

// TierGSI1PK is the Tier gsi1 partition key: the constant "tiers#".
func TierGSI1PK() string {
	return "tiers#"
}

// TierGSI1SK builds the Tier gsi1 sort key: "tier#${tierId}".
func TierGSI1SK(tierID string) string {
	return "tier#" + NormalizeTierID(tierID)
}

// AccessCodeItem is a full ElectroDB-shaped AccessCode item, ready to
// PutItem against the kmv-auth-electro table. Optional attributes
// (group/expiresAt/maxRedemptions) are omitted entirely when unset, matching
// ElectroDB's own behavior of never writing an attribute with no value.
type AccessCodeItem struct {
	Code            string
	TierID          string
	Group           string // "" -> omitted
	ExpiresAt       *int64 // epoch ms; nil -> omitted (never expires)
	MaxRedemptions  *int64 // nil -> omitted (unlimited)
	RedemptionCount int64
	CreatedAt       int64 // epoch ms
}

// NewAccessCodeItem constructs an AccessCodeItem with normalization and
// createdAt/redemptionCount defaults applied, mirroring the ElectroDB
// AccessCode entity's `set`/`default` attribute behavior.
func NewAccessCodeItem(code, tierID, group string, expiresAt, maxRedemptions *int64) AccessCodeItem {
	return AccessCodeItem{
		Code:            NormalizeCode(code),
		TierID:          NormalizeTierID(tierID),
		Group:           group,
		ExpiresAt:       expiresAt,
		MaxRedemptions:  maxRedemptions,
		RedemptionCount: 0,
		CreatedAt:       time.Now().UnixMilli(),
	}
}

// Marshal builds the raw DynamoDB attribute-value map for a PutItem call,
// including the pk/sk/gsi1pk/gsi1sk key attributes and the ElectroDB
// entity/version bookkeeping markers.
func (i AccessCodeItem) Marshal() map[string]types.AttributeValue {
	item := map[string]types.AttributeValue{
		"pk":              &types.AttributeValueMemberS{Value: AccessCodePK(i.Code)},
		"sk":              &types.AttributeValueMemberS{Value: AccessCodeSK()},
		"gsi1pk":          &types.AttributeValueMemberS{Value: AccessCodeGSI1PK()},
		"gsi1sk":          &types.AttributeValueMemberS{Value: AccessCodeGSI1SK(i.Code)},
		EDBEntityAttr:     &types.AttributeValueMemberS{Value: AccessCodeEntityName},
		EDBVersionAttr:    &types.AttributeValueMemberS{Value: EDBVersion},
		"code":            &types.AttributeValueMemberS{Value: NormalizeCode(i.Code)},
		"tierId":          &types.AttributeValueMemberS{Value: NormalizeTierID(i.TierID)},
		"redemptionCount": &types.AttributeValueMemberN{Value: itoa(i.RedemptionCount)},
		"createdAt":       &types.AttributeValueMemberN{Value: itoa(i.CreatedAt)},
	}
	if i.Group != "" {
		item["group"] = &types.AttributeValueMemberS{Value: i.Group}
	}
	if i.ExpiresAt != nil {
		item["expiresAt"] = &types.AttributeValueMemberN{Value: itoa(*i.ExpiresAt)}
	}
	if i.MaxRedemptions != nil {
		item["maxRedemptions"] = &types.AttributeValueMemberN{Value: itoa(*i.MaxRedemptions)}
	}
	return item
}

// TierItem is a full ElectroDB-shaped Tier item, ready to PutItem against
// the kmv-auth-electro table.
type TierItem struct {
	TierID         string
	Group          string // "" -> omitted
	SessionMaxSecs int64
	PeriodMaxSecs  int64
	MaxConcurrent  int64
	CreatedAt      int64 // epoch ms
}

// NewTierItem constructs a TierItem with normalization and createdAt applied.
func NewTierItem(tierID, group string, sessionMaxSecs, periodMaxSecs, maxConcurrent int64) TierItem {
	return TierItem{
		TierID:         NormalizeTierID(tierID),
		Group:          group,
		SessionMaxSecs: sessionMaxSecs,
		PeriodMaxSecs:  periodMaxSecs,
		MaxConcurrent:  maxConcurrent,
		CreatedAt:      time.Now().UnixMilli(),
	}
}

// Marshal builds the raw DynamoDB attribute-value map for a PutItem call.
func (t TierItem) Marshal() map[string]types.AttributeValue {
	item := map[string]types.AttributeValue{
		"pk":                &types.AttributeValueMemberS{Value: TierPK(t.TierID)},
		"sk":                &types.AttributeValueMemberS{Value: TierSK()},
		"gsi1pk":            &types.AttributeValueMemberS{Value: TierGSI1PK()},
		"gsi1sk":            &types.AttributeValueMemberS{Value: TierGSI1SK(t.TierID)},
		EDBEntityAttr:       &types.AttributeValueMemberS{Value: TierEntityName},
		EDBVersionAttr:      &types.AttributeValueMemberS{Value: EDBVersion},
		"tierId":            &types.AttributeValueMemberS{Value: NormalizeTierID(t.TierID)},
		"sessionMaxSeconds": &types.AttributeValueMemberN{Value: itoa(t.SessionMaxSecs)},
		"periodMaxSeconds":  &types.AttributeValueMemberN{Value: itoa(t.PeriodMaxSecs)},
		"maxConcurrent":     &types.AttributeValueMemberN{Value: itoa(t.MaxConcurrent)},
		"createdAt":         &types.AttributeValueMemberN{Value: itoa(t.CreatedAt)},
	}
	if t.Group != "" {
		item["group"] = &types.AttributeValueMemberS{Value: t.Group}
	}
	return item
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
