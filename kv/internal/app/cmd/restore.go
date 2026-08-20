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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithy "github.com/aws/smithy-go"
	"github.com/spf13/cobra"
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

// --------------------------------------------------------------------------
// Batched, retried, idempotent writes (D-12: a full-item PutRequest is
// inherently idempotent — re-submitting the exact same item never
// duplicates or errors, which is what makes a re-run over a
// partially-completed restore converge instead of diverging).

const (
	// restoreBatchSize is DynamoDB's BatchWriteItem hard limit — 25 requests
	// per call.
	restoreBatchSize = 25
	// restoreMaxAttempts bounds how many times a batch (or its
	// UnprocessedItems remainder) is retried before RestoreTable gives up
	// and returns an error.
	restoreMaxAttempts = 6
	// restoreMaxBackoff caps the exponential backoff delay between retries.
	restoreMaxBackoff = 30 * time.Second
	// restoreBackoffBase is the first retry's base delay, before jitter.
	restoreBackoffBase = 250 * time.Millisecond
)

// isRetryableError classifies a BatchWriteItem/PutObject error as
// retryable: DynamoDB throttling (ProvisionedThroughputExceededException,
// ThrottlingException, RequestLimitExceeded) or any 5xx server-fault smithy
// API error. Classification is by errors.As against the SDK's own typed
// errors, never by string-matching an error message.
func isRetryableError(err error) bool {
	var provisionedThroughput *types.ProvisionedThroughputExceededException
	if errors.As(err, &provisionedThroughput) {
		return true
	}
	var throttling *types.ThrottlingException
	if errors.As(err, &throttling) {
		return true
	}
	var requestLimit *types.RequestLimitExceeded
	if errors.As(err, &requestLimit) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorFault() == smithy.FaultServer {
		return true
	}
	return false
}

// restoreBackoffDuration returns the delay before retry attempt N
// (0-indexed), exponential with full jitter, capped at restoreMaxBackoff.
func restoreBackoffDuration(attempt int) time.Duration {
	d := restoreBackoffBase << uint(attempt)
	if d <= 0 || d > restoreMaxBackoff {
		d = restoreMaxBackoff
	}
	return time.Duration(rand.Int63n(int64(d)))
}

// restoreSleep waits for restoreBackoffDuration(attempt) or ctx
// cancellation, whichever comes first.
func restoreSleep(ctx context.Context, attempt int) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(restoreBackoffDuration(attempt)):
		return nil
	}
}

// restoreBatchWithRetry issues one BatchWriteItem for batch against table,
// re-submitting only UnprocessedItems and retrying retryable failures with
// backoff, up to restoreMaxAttempts. Returns the number of items actually
// written (succeeded, not merely attempted).
func restoreBatchWithRetry(ctx context.Context, api dynamoBatchWriteAPI, table string, batch []map[string]types.AttributeValue) (int64, error) {
	pending := make([]types.WriteRequest, 0, len(batch))
	for _, item := range batch {
		item := item
		pending = append(pending, types.WriteRequest{PutRequest: &types.PutRequest{Item: item}})
	}

	var written int64
	for attempt := 0; attempt < restoreMaxAttempts && len(pending) > 0; attempt++ {
		resp, err := api.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
			RequestItems: map[string][]types.WriteRequest{table: pending},
		})
		if err != nil {
			if isRetryableError(err) && attempt < restoreMaxAttempts-1 {
				if sleepErr := restoreSleep(ctx, attempt); sleepErr != nil {
					return written, sleepErr
				}
				continue
			}
			return written, fmt.Errorf("batch write table %s: %w", table, err)
		}

		unprocessed := resp.UnprocessedItems[table]
		written += int64(len(pending) - len(unprocessed))
		pending = unprocessed
		if len(pending) > 0 && attempt < restoreMaxAttempts-1 {
			if sleepErr := restoreSleep(ctx, attempt); sleepErr != nil {
				return written, sleepErr
			}
		}
	}
	if len(pending) > 0 {
		return written, fmt.Errorf("batch write table %s: %d item(s) still unprocessed after %d attempts", table, len(pending), restoreMaxAttempts)
	}
	return written, nil
}

// RestoreTable filters items through IsEphemeralItem (when
// opts.SkipEphemeral) and writes the survivors to table in batches of
// restoreBatchSize, retrying with backoff (D-12). Per-reason skip counts are
// accumulated regardless of opts.SkipEphemeral, so the report stays honest
// about what a classifier hit even when an operator chose to keep those
// rows. When opts.DryRun is true, no BatchWriteItem call is issued at all —
// the returned written count is the number of items that WOULD have been
// written.
func RestoreTable(ctx context.Context, api dynamoBatchWriteAPI, table string, items []map[string]types.AttributeValue, opts RestoreOptions, now func() time.Time, logical string) (written int64, skipped map[EphemeralReason]int64, err error) {
	if now == nil {
		now = time.Now
	}
	skipped = map[EphemeralReason]int64{}

	toWrite := make([]map[string]types.AttributeValue, 0, len(items))
	for _, item := range items {
		ephemeral, reason := IsEphemeralItem(logical, item, now())
		if ephemeral {
			skipped[reason]++
			if opts.SkipEphemeral {
				continue
			}
		}
		toWrite = append(toWrite, item)
	}

	if opts.DryRun {
		return int64(len(toWrite)), skipped, nil
	}

	for i := 0; i < len(toWrite); i += restoreBatchSize {
		end := i + restoreBatchSize
		if end > len(toWrite) {
			end = len(toWrite)
		}
		n, err := restoreBatchWithRetry(ctx, api, table, toWrite[i:end])
		written += n
		if err != nil {
			return written, skipped, err
		}
	}
	return written, skipped, nil
}

// RestoreLedger streams every "ledger/"-prefixed zip entry to PutObject,
// reconstructing the original S3 key by stripping prefix. Same dry-run rule
// as RestoreTable: with opts.DryRun, objects/bytes are still counted but no
// PutObject call is issued.
func RestoreLedger(ctx context.Context, api s3PutAPI, zr *zip.ReadCloser, prefix, bucket string, opts RestoreOptions) (objects int64, bytesTotal int64, err error) {
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, prefix) {
			continue
		}
		key := strings.TrimPrefix(f.Name, prefix)
		if key == "" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return objects, bytesTotal, fmt.Errorf("restore ledger: open entry %s: %w", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		closeErr := rc.Close()
		if err != nil {
			return objects, bytesTotal, fmt.Errorf("restore ledger: read entry %s: %w", f.Name, err)
		}
		if closeErr != nil {
			return objects, bytesTotal, fmt.Errorf("restore ledger: close entry %s: %w", f.Name, closeErr)
		}
		objects++
		bytesTotal += int64(len(data))
		if opts.DryRun {
			continue
		}
		if _, err := api.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body:   bytes.NewReader(data),
		}); err != nil {
			return objects, bytesTotal, fmt.Errorf("restore ledger: put object %s: %w", key, err)
		}
	}
	return objects, bytesTotal, nil
}

// restoreLedgerPrefix is the zip-internal prefix (D-04's layout) every
// ledger object lives under.
const restoreLedgerPrefix = "ledger/"

// RunRestore is the full restore orchestration: open+verify the archive
// (OpenBackupArchive), resolve every destination from deps.Targets (D-10,
// never from the manifest), validate every needed destination exists BEFORE
// issuing a single write (D-13 — a missing destination refuses loudly
// rather than partially writing), then restore tables and/or the ledger per
// opts. Tables and Ledger both default to true when neither is set, so a
// bare `kv restore <zip>` restores everything.
func RunRestore(ctx context.Context, deps RestoreDeps, archivePath string, opts RestoreOptions) (RestoreReport, error) {
	if !opts.Tables && !opts.Ledger {
		opts.Tables = true
		opts.Ledger = true
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}

	zr, manifest, err := OpenBackupArchive(archivePath)
	if err != nil {
		return RestoreReport{}, err
	}
	defer zr.Close()

	report := RestoreReport{
		ArchivePath:        archivePath,
		ManifestGitSHA:     manifest.GitSHA,
		ManifestAccountID:  manifest.AWSAccountID,
		ManifestTableNames: map[string]string{},
		ManifestBucket:     manifest.Ledger.BucketName,
		ResolvedTableNames: deps.Targets.TableNames,
		ResolvedBucket:     deps.Targets.LedgerBucket,
		TableWrites:        map[string]int64{},
		TableSkipped:       map[string]map[EphemeralReason]int64{},
		DryRun:             opts.DryRun,
	}
	for _, tbl := range manifest.Tables {
		report.ManifestTableNames[tbl.Logical] = tbl.TableName
	}

	// Validate every needed destination BEFORE any write (D-13): a missing
	// table or bucket must never be discovered partway through a restore
	// that already wrote some rows.
	if opts.Tables {
		for _, tbl := range manifest.Tables {
			if _, ok := deps.Targets.TableNames[tbl.Logical]; !ok {
				return report, fmt.Errorf("restore: no live destination for table %q — run `terragrunt apply` to create it (config (git) -> terragrunt apply -> kv restore is the required ordering; kv restore creates no infrastructure, D-13)", tbl.Logical)
			}
		}
	}
	if opts.Ledger && deps.Targets.LedgerBucket == "" {
		return report, fmt.Errorf("restore: no live destination for the ledger bucket — run `terragrunt apply` to create it (config (git) -> terragrunt apply -> kv restore is the required ordering; kv restore creates no infrastructure, D-13)")
	}

	if opts.Tables {
		for _, tbl := range manifest.Tables {
			dest := deps.Targets.TableNames[tbl.Logical]
			items, err := ReadTableItems(zr, tbl.Path)
			if err != nil {
				return report, err
			}
			written, skipped, err := RestoreTable(ctx, deps.Dynamo, dest, items, opts, now, tbl.Logical)
			report.TableWrites[tbl.Logical] = written
			report.TableSkipped[tbl.Logical] = skipped
			if err != nil {
				return report, err
			}
		}
	}

	if opts.Ledger {
		objects, bytesTotal, err := RestoreLedger(ctx, deps.S3, zr, restoreLedgerPrefix, deps.Targets.LedgerBucket, opts)
		report.LedgerObjects = objects
		report.LedgerBytes = bytesTotal
		if err != nil {
			return report, err
		}
	}

	return report, nil
}

// --------------------------------------------------------------------------
// The `kv restore` command (D-03) and its report printer.

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedInt64Keys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedReasonKeys(m map[EphemeralReason]int64) []EphemeralReason {
	keys := make([]EphemeralReason, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// PrintRestoreReport writes a human-readable summary of r to w, in the style
// of the existing kv status printers (text/tabwriter). It prints, in order:
// a leading DRY RUN banner when r.DryRun; the archive path; a provenance
// block (manifest.json's recorded git SHA, account id, table names, and
// bucket — labelled PROVENANCE, never a write destination); a destinations
// block (the live-resolved table names and bucket that were actually
// written — labelled DESTINATIONS, D-10); per-table written/skipped counts
// with skip reasons broken out; and the ledger object count and byte total.
func PrintRestoreReport(w io.Writer, r RestoreReport) {
	if r.DryRun {
		fmt.Fprintln(w, "=== DRY RUN — no writes were issued ===")
	}
	fmt.Fprintf(w, "archive: %s\n", r.ArchivePath)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "PROVENANCE (from manifest.json — audit only, NEVER a write destination, D-10):")
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "  git sha:\t%s\n", r.ManifestGitSHA)
	fmt.Fprintf(tw, "  aws account id:\t%s\n", r.ManifestAccountID)
	for _, logical := range sortedStringKeys(r.ManifestTableNames) {
		fmt.Fprintf(tw, "  table %s:\t%s\n", logical, r.ManifestTableNames[logical])
	}
	fmt.Fprintf(tw, "  ledger bucket:\t%s\n", r.ManifestBucket)
	tw.Flush()

	fmt.Fprintln(w)
	fmt.Fprintln(w, "DESTINATIONS (live-resolved from terraform outputs — where this restore actually wrote, D-10):")
	tw = tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, logical := range sortedStringKeys(r.ResolvedTableNames) {
		fmt.Fprintf(tw, "  table %s:\t%s\n", logical, r.ResolvedTableNames[logical])
	}
	fmt.Fprintf(tw, "  ledger bucket:\t%s\n", r.ResolvedBucket)
	tw.Flush()

	fmt.Fprintln(w)
	fmt.Fprintln(w, "TABLES:")
	tw = tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, logical := range sortedInt64Keys(r.TableWrites) {
		fmt.Fprintf(tw, "  %s\twritten=%d\n", logical, r.TableWrites[logical])
		for _, reason := range sortedReasonKeys(r.TableSkipped[logical]) {
			fmt.Fprintf(tw, "    skipped (%s)\t%d\n", reason, r.TableSkipped[logical][reason])
		}
	}
	tw.Flush()

	fmt.Fprintln(w)
	fmt.Fprintf(w, "LEDGER: %d object(s), %s\n", r.LedgerObjects, humanBytes(r.LedgerBytes))
}

// NewRestoreCmd builds the "kv restore <zip>" command (D-01/D-03): the other
// half of the backup/restore artifact contract. Destinations are resolved
// live from terraform outputs every time (D-10) — this command never trusts
// a name recorded in the archive's own manifest.json.
func NewRestoreCmd(cfg *Config) *cobra.Command {
	var (
		tables        bool
		ledger        bool
		dryRun        bool
		skipEphemeral bool
	)

	restoreCmd := &cobra.Command{
		Use:   "restore <zip>",
		Short: "Restore a kv backup archive into the live stack (D-03)",
		Long: "kv restore reads a backup zip written by `kv backup`, verifies it\n" +
			"(refusing to read an unverified archive), resolves every write\n" +
			"destination live from current terraform outputs (never from the\n" +
			"archive's own manifest.json — bucket names carry a new random_id\n" +
			"suffix after a recreate, D-10), and writes the DynamoDB tables and/or\n" +
			"the S3 ledger back in batched, retried, idempotent writes (D-12).\n" +
			"Ephemeral rows — concurrency leases, OIDC session/interaction state,\n" +
			"login intents, next-auth sessions and verification tokens, and any\n" +
			"item whose TTL attribute has already expired — are filtered by\n" +
			"default (--skip-ephemeral, D-11): restoring a stale concurrency\n" +
			"lease would wedge the quota gate. This command assumes the target\n" +
			"tables and bucket already exist; it creates no infrastructure\n" +
			"(D-13) — the required ordering is: config (git), then\n" +
			"`terragrunt apply`, then `kv restore`.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			archivePath := args[0]

			root, err := repoRoot()
			if err != nil {
				return err
			}
			targets, err := ResolveLiveTargets(ctx, NewTerragruntOutputReader(root))
			if err != nil {
				return fmt.Errorf("resolve live targets: %w", err)
			}

			dynamoClient, err := cfg.DynamoClient(ctx)
			if err != nil {
				return err
			}
			s3Client, err := cfg.S3Client(ctx)
			if err != nil {
				return err
			}

			deps := RestoreDeps{
				Dynamo:  dynamoClient,
				S3:      s3Client,
				Targets: targets,
				Now:     time.Now,
			}
			opts := RestoreOptions{
				Tables:        tables,
				Ledger:        ledger,
				DryRun:        dryRun,
				SkipEphemeral: skipEphemeral,
			}
			report, err := RunRestore(ctx, deps, archivePath, opts)
			if err != nil {
				return err
			}
			PrintRestoreReport(c.OutOrStdout(), report)
			return nil
		},
	}

	restoreCmd.Flags().BoolVar(&tables, "tables", false, "restore the DynamoDB tables (default: both tables and ledger, when neither --tables nor --ledger is given)")
	restoreCmd.Flags().BoolVar(&ledger, "ledger", false, "restore the S3 ledger (default: both tables and ledger, when neither --tables nor --ledger is given)")
	restoreCmd.Flags().BoolVar(&dryRun, "dry-run", false, "report per-table write/skip counts and the ledger object count without issuing a single write (D-12)")
	restoreCmd.Flags().BoolVar(&skipEphemeral, "skip-ephemeral", true, "filter ephemeral rows — concurrency leases, session state, expired TTL items — on by default (D-11)")

	return restoreCmd
}
