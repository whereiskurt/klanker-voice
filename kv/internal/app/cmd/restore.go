// Package cmd — kv restore: the other half of the Phase-16 backup/restore
// artifact contract (D-01/D-03). Reads a backup zip written by `kv backup`
// (backup.go), resolves every write destination live from terraform outputs
// (never from the manifest — D-10), drops the row classes that must never
// come back (D-11), and writes the rest in batched, retried, idempotent
// PutRequests (D-12). See
// docs/superpowers/specs/2026-08-12-pause-backup-teardown-design.md §4.7 for
// the three restore hazards this file is designed against.
package cmd

import (
	"archive/zip"
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// --------------------------------------------------------------------------
// Ephemeral row classification (D-11: "concurrency leases, OIDC session
// state, and expired-TTL items" are filtered by default — restoring a stale
// concurrency lease wedges the quota gate, a bug this project has already
// debugged once).
//
// EphemeralReason names the specific row class IsEphemeralItem matched, so
// the restore report's per-table skip breakdown (D-12) is honest about what
// it dropped and why, not just a bare count.
type EphemeralReason string

const (
	// EphemeralReasonConcurrencyLease is a voice-usage UsageHeartbeat item —
	// the D-01 concurrency slot. Restoring one stale would wedge the quota
	// gate for a real session.
	EphemeralReasonConcurrencyLease EphemeralReason = "concurrency lease"
	// EphemeralReasonOIDCSessionState is an auth-electro OIDCModel item whose
	// modelName is "Session" or "Interaction" — short-lived oidc-provider
	// login-flow state.
	EphemeralReasonOIDCSessionState EphemeralReason = "oidc session/interaction state"
	// EphemeralReasonLoginIntent is an auth-electro LoginIntent item — the
	// ~15-minute email->tier bridge that must never outlive the login it was
	// minted for.
	EphemeralReasonLoginIntent EphemeralReason = "login intent"
	// EphemeralReasonAuthSession is an auth-authjs next-auth session row.
	EphemeralReasonAuthSession EphemeralReason = "next-auth session"
	// EphemeralReasonVerificationToken is an auth-authjs next-auth magic-link
	// verification-token row — single-use and short-lived by design.
	EphemeralReasonVerificationToken EphemeralReason = "next-auth verification token"
	// EphemeralReasonExpiredTTL is any item whose table's designated expiry
	// attribute already reads in the past, independent of key shape.
	EphemeralReasonExpiredTTL EphemeralReason = "expired ttl"
)

// --------------------------------------------------------------------------
// Per-table key-shape and TTL-attribute constants. Every value below is
// READ, not guessed, from the live entity/adapter source that writes each
// table's rows — a future entity rename must update BOTH the cited source
// and this block, or restore silently stops recognizing the row class it
// exists to filter:
//
//   - apps/auth/webapp/src/entities/usage.ts:55-190 — voice-usage's only
//     ephemeral shape, UsageHeartbeat: pk template "session#${userId}", sk
//     template "heartbeat#${sessionId}" (both explicit ElectroDB templates,
//     used verbatim). UsageDaily/UsageRollup/UsageControl (the other three
//     entities on this table, lines 94-190) share none of that shape and are
//     never matched. The table's live DynamoDB TTL attribute is "expiresAt"
//     (epoch SECONDS), confirmed against
//     infra/terraform/live/site/services/voice/service.hcl:29-30
//     (ttl_enabled=true, ttl_attribute_name="expiresAt") — the ONLY
//     Phase-16 table with a DynamoDB-native TTL actually enabled; neither
//     auth table below has ttl_enabled set in
//     infra/terraform/live/site/services/auth/service.hcl, so this
//     classifier's TTL sweep on those two tables is a defensive
//     application-level check only, not a live DynamoDB TTL mirror.
//   - apps/auth/webapp/src/entities/oidc-adapter.ts:10-45 — OIDCModel's
//     primary pk composite is ["modelName"] with NO explicit template, so
//     its written pk is ElectroDB's default label format
//     "$<service>#<label>_<lowercased value>". Verified by constructing the
//     real Entity against the installed electrodb dependency and calling
//     .put({modelName:"Session",...}).params() /
//     .put({modelName:"Interaction",...}).params() rather than guessing:
//     modelName="Session" -> pk "$oidc#modelname_session", modelName=
//     "Interaction" -> pk "$oidc#modelname_interaction".
//   - apps/auth/webapp/src/entities/login-intent.ts:20-100 — LoginIntent's
//     explicit pk/sk templates "loginintent#${email}" / "loginintent#", and
//     its two distinct expiry attributes on the SAME item: expiresAt in
//     epoch MILLISECONDS (app-level belt check, lines 74-79) and ttl in
//     epoch SECONDS (the DynamoDB-TTL-shaped attribute, watch-derived from
//     expiresAt, lines 80-89). "ttl" is not used as an attribute name by any
//     other auth-electro entity (verified: `grep -rl '"ttl"' entities/*.ts`
//     matches only login-intent.ts), so a bare "does this item have a
//     numeric ttl attribute" check can never collide with AccessCode's own
//     (unrelated, epoch-ms, business-logic) expiresAt field.
//   - apps/auth/webapp/src/config/auth.ts:45-60 — constructs
//     @auth/dynamodb-adapter with NO partitionKey/sortKey/indexPartitionKey/
//     indexSortKey override, so the adapter's own published defaults apply
//     verbatim (verified against the installed
//     node_modules/@auth/dynamodb-adapter/index.js): a user row is
//     pk="USER#<id>" sk="USER#<id>"; an account row is
//     pk="USER#<userId>" sk="ACCOUNT#<provider>#<providerAccountId>"; a
//     session row is pk="USER#<userId>" sk="SESSION#<sessionToken>"; a
//     verification-token row is pk="VT#<identifier>" sk="VT#<token>". The
//     adapter's format.to special-cases exactly one attribute name,
//     "expires", as `value.getTime() / 1000` — epoch SECONDS — which is
//     this table's designated TTL attribute.
const (
	usageHeartbeatPKPrefix = "session#"
	usageHeartbeatSKPrefix = "heartbeat#"

	oidcModelPKSession     = "$oidc#modelname_session"
	oidcModelPKInteraction = "$oidc#modelname_interaction"
	loginIntentPKPrefix    = "loginintent#"

	authjsUserPrefix      = "USER#"
	authjsSessionSKPrefix = "SESSION#"
	authjsVTPrefix        = "VT#"

	// loginIntentExpiresAtAttr is LoginIntent's app-level belt-and-suspenders
	// expiry field, epoch MILLISECONDS — distinct in unit from the generic
	// per-table ttlAttrByLogicalTable sweep below, which reads LoginIntent's
	// OTHER attribute ("ttl", epoch seconds). Checked only when the item's
	// key shape already matches LoginIntent, so it can never fire against
	// AccessCode's own "expiresAt" (also epoch ms, but a business-logic
	// expiry that Test 3 requires survive restore, not a row class D-11
	// filters).
	loginIntentExpiresAtAttr = "expiresAt"
)

// ttlAttrByLogicalTable names, per logical table, the ONE attribute this
// classifier treats as an expiry timestamp in epoch SECONDS for the
// "regardless of key shape" TTL sweep (D-11). Scoping to a single named
// attribute per table — rather than any attribute that happens to be called
// "expiresAt" — is deliberate: AccessCode (auth-electro) and other durable
// entities carry their own unrelated "expiresAt" business field that must
// survive restore (Test 3), and only LoginIntent's "ttl" attribute is unique
// to the row class this sweep exists to catch.
var ttlAttrByLogicalTable = map[string]string{
	"voice-usage":  "expiresAt",
	"auth-electro": "ttl",
	"auth-authjs":  "expires",
}

// stringAttr reads a string-typed attribute, returning "" if absent or of a
// different DynamoDB type.
func stringAttr(item map[string]types.AttributeValue, key string) string {
	if v, ok := item[key]; ok {
		if s, ok := v.(*types.AttributeValueMemberS); ok {
			return s.Value
		}
	}
	return ""
}

// numberAttr reads a number-typed attribute as an int64, returning
// ok=false if absent, of a different type, or not integer-parseable.
func numberAttr(item map[string]types.AttributeValue, key string) (int64, bool) {
	v, ok := item[key]
	if !ok {
		return 0, false
	}
	n, ok := v.(*types.AttributeValueMemberN)
	if !ok {
		return 0, false
	}
	i, err := strconv.ParseInt(n.Value, 10, 64)
	if err != nil {
		return 0, false
	}
	return i, true
}

// epochExpired reports whether item's attr attribute, interpreted as an
// epoch timestamp in unit (time.Second or time.Millisecond), is strictly
// before now. Returns false (not expired) when the attribute is absent or
// non-numeric — an item with no expiry signal is never dropped by this
// check.
func epochExpired(item map[string]types.AttributeValue, attr string, unit time.Duration, now time.Time) bool {
	n, ok := numberAttr(item, attr)
	if !ok {
		return false
	}
	var t time.Time
	switch unit {
	case time.Second:
		t = time.Unix(n, 0)
	case time.Millisecond:
		t = time.UnixMilli(n)
	default:
		return false
	}
	return t.Before(now)
}

// IsEphemeralItem classifies one item read from logicalTable (one of
// "voice-usage", "auth-electro", "auth-authjs" — ResolveTableNames' derived
// keys) as ephemeral or not, and — when ephemeral — names the specific
// reason (D-11). now is the restore-time clock, injected so tests are
// deterministic.
//
// Classification is pure and total: it never mutates item, never issues an
// AWS call, and is safe to call for counting purposes even when a caller
// has decided not to act on the result (RestoreOptions.SkipEphemeral=false
// still calls this for every item so the report's skip counts stay honest
// in both modes).
func IsEphemeralItem(logicalTable string, item map[string]types.AttributeValue, now time.Time) (bool, EphemeralReason) {
	pk := stringAttr(item, "pk")
	sk := stringAttr(item, "sk")

	switch logicalTable {
	case "voice-usage":
		if strings.HasPrefix(pk, usageHeartbeatPKPrefix) && strings.HasPrefix(sk, usageHeartbeatSKPrefix) {
			return true, EphemeralReasonConcurrencyLease
		}
	case "auth-electro":
		if pk == oidcModelPKSession || pk == oidcModelPKInteraction {
			return true, EphemeralReasonOIDCSessionState
		}
		if strings.HasPrefix(pk, loginIntentPKPrefix) {
			return true, EphemeralReasonLoginIntent
		}
	case "auth-authjs":
		if strings.HasPrefix(pk, authjsUserPrefix) && strings.HasPrefix(sk, authjsSessionSKPrefix) {
			return true, EphemeralReasonAuthSession
		}
		if strings.HasPrefix(pk, authjsVTPrefix) && strings.HasPrefix(sk, authjsVTPrefix) {
			return true, EphemeralReasonVerificationToken
		}
	}

	if ttlAttr, ok := ttlAttrByLogicalTable[logicalTable]; ok {
		if epochExpired(item, ttlAttr, time.Second, now) {
			return true, EphemeralReasonExpiredTTL
		}
	}
	// LoginIntent's second, epoch-millisecond expiry signal — scoped to the
	// login-intent key shape so it can never fire against AccessCode's own
	// epoch-ms "expiresAt" business field on the same table.
	if logicalTable == "auth-electro" && strings.HasPrefix(pk, loginIntentPKPrefix) {
		if epochExpired(item, loginIntentExpiresAtAttr, time.Millisecond, now) {
			return true, EphemeralReasonExpiredTTL
		}
	}

	return false, ""
}

// --------------------------------------------------------------------------
// Opening and reading the archive (T-16-03-01: a restore from an unverified
// artifact is not offered).

// OpenBackupArchive calls VerifyBackupArchive first and refuses to proceed
// on any digest, byte-count, row-count, missing-entry, or extra-entry
// mismatch, then reopens path for reading. There is no code path in this
// file that reads zip entries without this check running first.
func OpenBackupArchive(path string) (*zip.ReadCloser, BackupManifest, error) {
	manifest, err := VerifyBackupArchive(path)
	if err != nil {
		return nil, BackupManifest{}, fmt.Errorf("open backup archive %s: refused (failed verification): %w", path, err)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, BackupManifest{}, fmt.Errorf("open backup archive %s: %w", path, err)
	}
	return zr, manifest, nil
}

// ReadTableItems decodes the JSONL entry at entryPath (16-02's
// marshalItemJSONL encoding, one item per line) back into DynamoDB items,
// via the exact inverse (unmarshalItemJSONL) backup.go already defines.
func ReadTableItems(zr *zip.ReadCloser, entryPath string) ([]map[string]types.AttributeValue, error) {
	var f *zip.File
	for _, cand := range zr.File {
		if cand.Name == entryPath {
			f = cand
			break
		}
	}
	if f == nil {
		return nil, fmt.Errorf("read table items: entry %s not found in archive", entryPath)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("read table items: open entry %s: %w", entryPath, err)
	}
	defer rc.Close()

	var items []map[string]types.AttributeValue
	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		item, err := unmarshalItemJSONL(line)
		if err != nil {
			return nil, fmt.Errorf("read table items: entry %s: %w", entryPath, err)
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read table items: entry %s: %w", entryPath, err)
	}
	return items, nil
}

// --------------------------------------------------------------------------
// Restore orchestration types (D-30: narrow AWS seams; D-10: Manifest*
// fields are provenance ONLY).

// dynamoBatchWriteAPI is the narrow subset of *dynamodb.Client RestoreTable
// needs.
type dynamoBatchWriteAPI interface {
	BatchWriteItem(ctx context.Context, in *dynamodb.BatchWriteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
}

// s3PutAPI is the narrow subset of *s3.Client RestoreLedger needs.
type s3PutAPI interface {
	PutObject(ctx context.Context, in *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// RestoreDeps carries every AWS/data seam RunRestore needs. Targets is the
// live-resolved destination set (16-01's ResolveLiveTargets) — restore's
// ONLY legal source of table/bucket destination names (D-10). Now is the
// restore-time clock (defaults to time.Now), injected for deterministic
// ephemeral-TTL tests.
type RestoreDeps struct {
	Dynamo  dynamoBatchWriteAPI
	S3      s3PutAPI
	Targets LiveTargets
	Now     func() time.Time
}

// RestoreOptions controls what RunRestore restores and how. Tables/Ledger
// both default to true when neither is explicitly set by the caller (a bare
// `kv restore <zip>` restores everything — see NewRestoreCmd). SkipEphemeral
// defaults to true at the command layer (D-11: filtering is the default,
// the flag is how an operator opts out).
type RestoreOptions struct {
	Tables        bool
	Ledger        bool
	DryRun        bool
	SkipEphemeral bool
}

// RestoreReport summarizes one RunRestore call.
//
// Manifest* fields are PROVENANCE ONLY — what manifest.json recorded about
// where the backup came from at the time it was written. Resolved* fields
// are the ONLY write destinations this restore actually used (D-10). A
// caller must never treat ManifestTableNames/ManifestBucket as a place data
// was written; PrintRestoreReport prints both, distinctly labelled, so any
// divergence (e.g. a post-destroy ledger bucket's new random_id suffix) is
// visible rather than silently trusted.
type RestoreReport struct {
	ArchivePath        string
	ManifestGitSHA     string
	ManifestAccountID  string
	ManifestTableNames map[string]string
	ManifestBucket     string
	ResolvedTableNames map[string]string
	ResolvedBucket     string
	TableWrites        map[string]int64
	TableSkipped       map[string]map[EphemeralReason]int64
	LedgerObjects      int64
	LedgerBytes        int64
	DryRun             bool
}
