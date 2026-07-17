// Package cmd — kv voipms: automates the API-drivable VoIP.ms provisioning
// steps (D-03). Portal-only security steps (2FA, international/premium
// locks, balance alerts, API IP-whitelist) are NOT automated here — they
// are documented in docs/operators/voipms-provisioning-runbook.md in the
// exact §25.F order.
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
)

// --------------------------------------------------------------------------
// VoIP.ms REST API constants — ALL base-URL and method-name details are
// centralized in this one block per D-03's acceptance criteria (the REST
// endpoint filename below must appear exactly once in this file).
//
// SOURCE: .planning/phases/12-voip-ms-telephony-inbound-did/12-RESEARCH.md,
// section "VoIP.ms REST API Surface (D-03)" — itself citing
// voip.ms/resources/api.
//
// VERIFIED 2026-07-12 against the VoIP.ms API method registry (via an
// auto-generated client of the live API method list): every constant below
// exists under exactly this name. The one candidate that does NOT exist —
// setMaxCallDuration — was removed; the VoIP.ms API exposes no per-call
// max-duration method at all (see the `set-caps` sub-command, which now
// explains where that cap is actually enforced).
const (
	voipmsBaseURL = "https://voip.ms/api/v1/rest.php"

	// VERIFIED: creates a subaccount under the main VoIP.ms account.
	// The subaccount name parameter is `username` (NOT `subaccount`);
	// IP restriction is `enable_ip_restriction` (0/1) + `ip_restriction`.
	voipmsMethodCreateSubAccount = "createSubAccount"

	// VERIFIED: updates an existing subaccount's settings (IP
	// restriction, international lock, etc). Takes `id` for the
	// subaccount, plus the same setting params as createSubAccount.
	voipmsMethodSetSubAccount = "setSubAccount"

	// VERIFIED: assigns/updates a DID's routing target. Params: `did`,
	// `routing` (e.g. "account:klanker-pbx").
	voipmsMethodSetDIDRouting = "setDIDRouting"

	// VERIFIED: reads the current account balance.
	voipmsMethodGetBalance = "getBalance"

	// VERIFIED: lists VoIP.ms POP/server info (used to source the SG
	// allow-list IPs documented in the operator runbook).
	voipmsMethodGetServersInfo = "getServersInfo"

	// VERIFIED: lists the DIDs provisioned on the account — the actual
	// numbers the public dials to reach the agent (§25 inbound surface).
	// Params: none required for a full-account listing.
	voipmsMethodGetDIDsInfo = "getDIDsInfo"

	// VERIFIED: writes a DID's settings. setDIDInfo is FULL-REPLACE, not a
	// partial update — every accepted field it doesn't receive is reset to
	// its default, so callers must always re-send a full getDIDsInfo
	// snapshot (see setVoipmsDIDPrefix). Param names mirror getDIDsInfo's
	// response field names 1:1.
	voipmsMethodSetDIDInfo = "setDIDInfo"
)

// --------------------------------------------------------------------------
// Credentials — env-first, SSM fallback (D-04). Never a CLI flag (would leak
// into shell history / process listings), never logged. No literal
// api_username/api_password value is ever assigned in this file: the
// process-env read stays os.Getenv-only, and the SSM fallback reads the two
// parameters below by name — their decrypted values are never interpolated
// into any log or error string.

// voipmsCreds carries the VoIP.ms REST API credentials.
type voipmsCreds struct {
	Username string
	Password string
}

// voipmsCredsFromEnv reads VOIPMS_API_USERNAME / VOIPMS_API_PASSWORD from
// the process environment. It never accepts these as CLI flags (would leak
// into shell history / process listings) and never logs their values.
func voipmsCredsFromEnv() (voipmsCreds, error) {
	u := os.Getenv("VOIPMS_API_USERNAME")
	p := os.Getenv("VOIPMS_API_PASSWORD")
	if u == "" || p == "" {
		return voipmsCreds{}, fmt.Errorf(
			"VOIPMS_API_USERNAME and VOIPMS_API_PASSWORD must be set in the environment " +
				"(SSM-sourced — see docs/operators/voipms-provisioning-runbook.md)")
	}
	return voipmsCreds{Username: u, Password: p}, nil
}

// voipmsUsernameSSMPath / voipmsPasswordSSMPath are the SSM SecureString
// parameter paths for the VoIP.ms API credentials (see
// docs/operators/voipms-provisioning-runbook.md lines 168-169), used as the
// fallback source when the VOIPMS_API_* env vars are unset.
const (
	voipmsUsernameSSMPath = "/kmv/secrets/use1/voipms/api_username"
	voipmsPasswordSSMPath = "/kmv/secrets/use1/voipms/api_password"
)

// voipmsCredsFromSSM reads the VoIP.ms API credentials from SSM
// (WithDecryption) via the narrow ssmGetParameterAPI seam (telephony.go),
// so tests can inject a fake and no live AWS call is ever required. Any
// GetParameter failure is reported without ever interpolating the
// credential values themselves — only a short, non-sensitive note derived
// via shortSSMErrorNote.
func voipmsCredsFromSSM(ctx context.Context, api ssmGetParameterAPI) (voipmsCreds, error) {
	uOut, err := api.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(voipmsUsernameSSMPath),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return voipmsCreds{}, fmt.Errorf("read VoIP.ms credentials from SSM: %s", shortSSMErrorNote(err))
	}
	pOut, err := api.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(voipmsPasswordSSMPath),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return voipmsCreds{}, fmt.Errorf("read VoIP.ms credentials from SSM: %s", shortSSMErrorNote(err))
	}
	creds := voipmsCreds{}
	if uOut.Parameter != nil && uOut.Parameter.Value != nil {
		creds.Username = *uOut.Parameter.Value
	}
	if pOut.Parameter != nil && pOut.Parameter.Value != nil {
		creds.Password = *pOut.Parameter.Value
	}
	return creds, nil
}

// resolveVoipmsCreds resolves the VoIP.ms API credentials env-first, then
// falling back to SSM: VOIPMS_API_USERNAME/VOIPMS_API_PASSWORD win when
// both are set (env override/testing path, ssmFactory is never invoked);
// otherwise ssmFactory builds an SSM client and the credentials are read
// from the well-known SecureString paths. Any failure surfaces ONE clear,
// leak-free operator-facing error naming both remediation options.
func resolveVoipmsCreds(ctx context.Context, ssmFactory func(context.Context) (ssmGetParameterAPI, error)) (voipmsCreds, error) {
	if creds, err := voipmsCredsFromEnv(); err == nil {
		return creds, nil
	}
	api, err := ssmFactory(ctx)
	if err != nil {
		return voipmsCreds{}, fmt.Errorf(
			"could not resolve VoIP.ms API credentials: set VOIPMS_API_USERNAME/VOIPMS_API_PASSWORD, "+
				"or ensure your AWS profile can read /kmv/secrets/use1/voipms/* (%s)", shortSSMErrorNote(err))
	}
	creds, err := voipmsCredsFromSSM(ctx, api)
	if err != nil {
		// voipmsCredsFromSSM's error is already a leak-free message (via
		// shortSSMErrorNote internally) — reuse it directly rather than
		// re-deriving via shortSSMErrorNote(err), which would only see a
		// plain *errors.errorString here and fall back to "unavailable",
		// discarding the more specific note.
		return voipmsCreds{}, fmt.Errorf(
			"could not resolve VoIP.ms API credentials: set VOIPMS_API_USERNAME/VOIPMS_API_PASSWORD, "+
				"or ensure your AWS profile can read /kmv/secrets/use1/voipms/* (%s)", err)
	}
	return creds, nil
}

// resolveVoipmsCreds is a thin Config method delegating to the package-level
// resolveVoipmsCreds, wiring the ssmFactory to c.SSMClient — *ssm.Client
// satisfies ssmGetParameterAPI.
func (c *Config) resolveVoipmsCreds(ctx context.Context) (voipmsCreds, error) {
	return resolveVoipmsCreds(ctx, func(ctx context.Context) (ssmGetParameterAPI, error) {
		return c.SSMClient(ctx)
	})
}

// --------------------------------------------------------------------------
// Thin REST client — baseURL and httpClient are both overridable so tests
// can point at an httptest.Server and assert request shape without any live
// network call.

// voipmsClient wraps HTTP access to the VoIP.ms REST API.
type voipmsClient struct {
	baseURL    string
	httpClient *http.Client
	creds      voipmsCreds
}

// newVoipmsClient builds a client against the real VoIP.ms endpoint.
func newVoipmsClient(creds voipmsCreds) *voipmsClient {
	return &voipmsClient{
		baseURL:    voipmsBaseURL,
		// 45s: createSubAccount/orderDID are observed to take >15s server-side
		// behind VoIP.ms's Cloudflare front; a timeout here does NOT mean the
		// operation failed server-side — verify with a read call before retrying.
		httpClient: &http.Client{Timeout: 45 * time.Second},
		creds:      creds,
	}
}

// voipmsStatusError is returned by do() when the VoIP.ms envelope reports a
// non-success result. It carries ONLY the safe response enums — the "status"
// field and (when present) the granular "error" code (e.g. "ip_not_enabled",
// "invalid_credentials", "no_did"). It deliberately never captures the request
// URL or any credential (those live in the query string; see do()'s *url.Error
// unwrap), so the reason can be surfaced to an operator without leaking secrets.
type voipmsStatusError struct {
	Method string
	Status string // out["status"] — e.g. "failure" or a bare error code
	Reason string // out["error"] — the granular code, when the envelope carries one
}

// Code returns the most specific safe reason: the granular "error" code when
// present, otherwise the "status" field.
func (e *voipmsStatusError) Code() string {
	if e.Reason != "" {
		return e.Reason
	}
	return e.Status
}

func (e *voipmsStatusError) Error() string {
	return fmt.Sprintf("voip.ms method %s returned status %q (error %q)", e.Method, e.Status, e.Reason)
}

// do calls a VoIP.ms REST method with the given extra params, appending the
// auth params and parsing the {"status": "success"|"failure", ...} JSON
// envelope common to every VoIP.ms REST API method.
func (vc *voipmsClient) do(ctx context.Context, method string, params url.Values) (map[string]any, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("api_username", vc.creds.Username)
	params.Set("api_password", vc.creds.Password)
	params.Set("method", method)

	reqURL := vc.baseURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build voip.ms request for method %s: %w", method, err)
	}
	resp, err := vc.httpClient.Do(req)
	if err != nil {
		// NEVER wrap the raw transport error: *url.Error stringifies the full
		// request URL, which carries api_password (and any password param) in
		// the query string — Go's error chain would leak them into logs and
		// terminal output (D-04). Unwrap to the inner cause first.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, fmt.Errorf("call voip.ms method %s: %w", method, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read voip.ms response for method %s: %w", method, err)
	}

	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("parse voip.ms JSON response for method %s: %w", method, err)
	}

	status, _ := out["status"].(string)
	if status != "success" {
		reason, _ := out["error"].(string)
		return out, &voipmsStatusError{Method: method, Status: status, Reason: reason}
	}
	return out, nil
}

// --------------------------------------------------------------------------
// Provisioning operations (D-03 automation scope).

// getVoipmsBalance reads the account balance via voipmsMethodGetBalance.
// The exact response shape is UNVERIFIED (see constants block); this
// defensively falls back to the raw JSON envelope if the expected
// balance.current_balance field isn't present, rather than panicking.
func getVoipmsBalance(ctx context.Context, vc *voipmsClient) (string, error) {
	out, err := vc.do(ctx, voipmsMethodGetBalance, nil)
	if err != nil {
		return "", err
	}
	if bal, ok := out["balance"].(map[string]any); ok {
		if cur, ok := bal["current_balance"].(string); ok {
			return cur, nil
		}
	}
	raw, _ := json.Marshal(out)
	return string(raw), nil
}

// routeVoipmsDidToPbx routes did to the klanker-pbx subaccount (or an
// explicitly named subaccount) via voipmsMethodSetDIDRouting.
func routeVoipmsDidToPbx(ctx context.Context, vc *voipmsClient, did, subaccountUsername string) error {
	if did == "" {
		return fmt.Errorf("did must not be blank")
	}
	if subaccountUsername == "" {
		subaccountUsername = "klanker-pbx"
	}
	params := url.Values{}
	params.Set("did", did)
	params.Set("routing", fmt.Sprintf("account:%s", subaccountUsername))
	if _, err := vc.do(ctx, voipmsMethodSetDIDRouting, params); err != nil {
		return fmt.Errorf("route DID %s to subaccount %s: %w", did, subaccountUsername, err)
	}
	return nil
}

// errNoCapAPI explains the verified absence of a per-call duration cap in
// the VoIP.ms API. The cap the design wants (D-03 §25) is enforced in two
// real places instead: the Asterisk/controller call timer (app-side, the
// authoritative per-call bound) and the portal-only balance protections
// (balance alerts / auto-suspend) in the §25.F runbook.
var errNoCapAPI = fmt.Errorf(
	"the VoIP.ms API has no per-call max-duration method (verified 2026-07-12) — " +
		"per-call caps are enforced by the Asterisk/controller call timer, and " +
		"account burn is bounded by the portal-only balance protections; see " +
		"docs/operators/voipms-provisioning-runbook.md")

// createVoipmsSubaccount creates the klanker-pbx subaccount. Per D-03/§25.A
// it defaults outbound-locked (lock_international=1, international_route=0)
// and IP-restricted (enable_ip_restriction=1 + ip_restriction) — parameter
// names verified against the createSubAccount signature. Operators should
// still confirm the resulting portal state after running it (§25.F step 5).
func createVoipmsSubaccount(ctx context.Context, vc *voipmsClient, username, password, allowedIP string) error {
	if username == "" || password == "" {
		return fmt.Errorf("username and password are required")
	}
	params := url.Values{}
	params.Set("username", username)
	params.Set("password", password)
	// Required-by-API device parameters (verified against createSubAccount's
	// signature; values confirmed live via the API's own validation errors).
	// protocol=1 (SIP), auth_type=1 (user/password), device_type=2
	// (Asterisk/IP-PBX), ulaw-only to match the Phase-12 trunk posture.
	params.Set("protocol", "1")
	params.Set("auth_type", "1")
	params.Set("device_type", "2")
	params.Set("dtmf_mode", "auto")
	params.Set("music_on_hold", "default")
	params.Set("nat", "yes")
	params.Set("allowed_codecs", "ulaw")
	// Outbound-disabled + international lock, D-03/§25.A (verified params).
	// international_route: 1=Value route (a required enum; 0 is rejected as
	// "missing"). Irrelevant in practice — lock_international=1 blocks all
	// international termination regardless of route choice.
	params.Set("lock_international", "1")
	params.Set("international_route", "1")
	if allowedIP != "" {
		params.Set("enable_ip_restriction", "1")
		params.Set("ip_restriction", allowedIP)
	}
	if _, err := vc.do(ctx, voipmsMethodCreateSubAccount, params); err != nil {
		return fmt.Errorf("create subaccount %s: %w", username, err)
	}
	return nil
}

// InboundDIDRecord is one VoIP.ms-provisioned inbound DID — the actual
// number the public dials to reach the agent (distinct from the caller-ID
// mint mappings in telephony.go's PhoneMappingRecord, which are keyed by the
// CALLER's number, not the DID being called).
type InboundDIDRecord struct {
	DID         string `json:"did"`
	Description string `json:"description"`
	Routing     string `json:"routing"`
	POP         string `json:"pop"`
}

// ListInboundDIDs lists the account's provisioned inbound DIDs via
// voipmsMethodGetDIDsInfo. The response shape is defensively parsed: VoIP.ms
// returns "dids" as a JSON array when there are 0 or 2+ DIDs, but collapses
// it to a single bare object when there is exactly one (a documented
// VoIP.ms API quirk) — both shapes are handled. Any other/missing shape
// degrades to a non-nil empty slice rather than erroring or panicking.
func ListInboundDIDs(ctx context.Context, vc *voipmsClient) ([]InboundDIDRecord, error) {
	out, err := vc.do(ctx, voipmsMethodGetDIDsInfo, nil)
	if err != nil {
		return nil, err
	}
	records := []InboundDIDRecord{}
	switch dids := out["dids"].(type) {
	case []any:
		for _, item := range dids {
			if m, ok := item.(map[string]any); ok {
				records = append(records, didRecordFromMap(m))
			}
		}
	case map[string]any:
		records = append(records, didRecordFromMap(dids))
	}
	return records, nil
}

// didRecordFromMap reads one DID record's fields from a decoded JSON map.
// VoIP.ms returns strings for these fields, but fmt.Sprint is used as a
// defensive fallback rather than assuming the type.
func didRecordFromMap(m map[string]any) InboundDIDRecord {
	field := func(key string) string {
		v, ok := m[key]
		if !ok || v == nil {
			return ""
		}
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprint(v)
	}
	return InboundDIDRecord{
		DID:         field("did"),
		Description: field("description"),
		Routing:     field("routing"),
		POP:         field("pop"),
	}
}

// --------------------------------------------------------------------------
// Caller-ID prefix tooling (Part B, docs/superpowers/specs/
// 2026-07-17-per-did-gate-policy-and-cid-tooling.md). Automates the
// hand-run setDIDInfo dance for enrolling a DID's callerid_prefix, baking
// in the live-proven gotchas: full-snapshot preserve (setDIDInfo is
// full-replace), forced cnam=0, a verify readback, and bounded retry
// against the flaky VoIP.ms Cloudflare front.

// voipmsStringField reads a string field from a decoded VoIP.ms JSON map,
// using the same defensive coercion didRecordFromMap uses (string, else
// fmt.Sprint) — shared by the DID-snapshot helpers below.
func voipmsStringField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// voipmsDoWithRetry calls vc.do up to 3 times, retrying ONLY transport-layer
// failures — VoIP.ms's Cloudflare front intermittently 522s from some
// egress (T-pcy-03). A clean *voipmsStatusError (a real API-level rejection
// like no_did) is returned immediately, never retried: hammering a
// legitimate rejection wastes calls and could mask a real problem as
// flakiness. Never logs params/URL/creds — do()'s own *url.Error unwrap
// already guards that, and this wrapper adds no logging of its own.
func voipmsDoWithRetry(ctx context.Context, vc *voipmsClient, method string, params url.Values) (map[string]any, error) {
	const maxAttempts = 3
	backoffs := []time.Duration{500 * time.Millisecond, 1 * time.Second}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		out, err := vc.do(ctx, method, params)
		if err == nil {
			return out, nil
		}
		var statusErr *voipmsStatusError
		if errors.As(err, &statusErr) {
			// A real API-level rejection — do not retry.
			return out, err
		}
		lastErr = err
		if attempt < maxAttempts-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoffs[attempt]):
			}
		}
	}
	return nil, lastErr
}

// getVoipmsDIDInfo returns the FULL raw snapshot map for a single DID via
// voipmsMethodGetDIDsInfo (scoped with a `did` param), defensively handling
// both the array and single-bare-object `dids` shapes exactly like
// ListInboundDIDs. The full map is returned — not InboundDIDRecord —
// because setVoipmsDIDPrefix needs every field to safely re-send the
// full-replace setDIDInfo call.
func getVoipmsDIDInfo(ctx context.Context, vc *voipmsClient, did string) (map[string]any, error) {
	if did == "" {
		return nil, fmt.Errorf("did must not be blank")
	}
	params := url.Values{}
	params.Set("did", did)
	out, err := voipmsDoWithRetry(ctx, vc, voipmsMethodGetDIDsInfo, params)
	if err != nil {
		return nil, err
	}
	switch dids := out["dids"].(type) {
	case []any:
		for _, item := range dids {
			if m, ok := item.(map[string]any); ok {
				if voipmsStringField(m, "did") == did {
					return m, nil
				}
			}
		}
	case map[string]any:
		if voipmsStringField(dids, "did") == did {
			return dids, nil
		}
	}
	return nil, fmt.Errorf("DID %s not found", did)
}

// voipmsDIDPreserveFields lists the getDIDsInfo snapshot fields forwarded
// verbatim to setDIDInfo. setDIDInfo is FULL-REPLACE: any accepted field
// omitted here is silently wiped server-side — this is the live-proven trap
// the design spec's Part B exists to guard against.
var voipmsDIDPreserveFields = []string{
	"did",
	"routing",
	"pop",
	"dialtime",
	"billing_type",
	"description",
	"note",
	"failover_busy",
	"failover_unreachable",
	"failover_noanswer",
	"voicemail",
	"canada_routing",
}

// setVoipmsDIDPrefix sets (or clears, when prefix == "") the caller-ID name
// prefix on did via a full-replace setDIDInfo call:
//  1. snapshot the DID via getVoipmsDIDInfo.
//  2. forward every voipmsDIDPreserveFields field present+non-empty in the
//     snapshot, so no other live DID setting (routing/pop/dialtime/
//     billing_type/failover_*/...) is silently wiped.
//  3. FORCE cnam=0 (overriding any snapshot cnam=1) — cnam=1 makes VoIP.ms
//     overwrite the caller-ID NAME via CNAM lookup, so the prefix never
//     rides through (the live-proven silent-failure guard, DID 3283).
//  4. set callerid_prefix=prefix (may be "" for the clear path) and call
//     setDIDInfo.
//  5. readback via getVoipmsDIDInfo and verify routing was preserved AND
//     callerid_prefix landed — this catches a cnam-clobbered prefix even
//     when the API itself reported success.
func setVoipmsDIDPrefix(ctx context.Context, vc *voipmsClient, did, prefix string) error {
	snapshot, err := getVoipmsDIDInfo(ctx, vc, did)
	if err != nil {
		return fmt.Errorf("snapshot DID %s before setting caller-ID prefix: %w", did, err)
	}

	params := url.Values{}
	for _, key := range voipmsDIDPreserveFields {
		if v := voipmsStringField(snapshot, key); v != "" {
			params.Set(key, v)
		}
	}
	// FORCE cnam=0 (overriding any snapshot cnam) — cnam=1 makes VoIP.ms
	// overwrite the caller-ID NAME via CNAM lookup so the prefix set below
	// never rides through (live-proven silent failure on DID 3283).
	params.Set("cnam", "0")
	// prefix may be "" — that's the clear-cid-prefix path.
	params.Set("callerid_prefix", prefix)

	if _, err := voipmsDoWithRetry(ctx, vc, voipmsMethodSetDIDInfo, params); err != nil {
		return fmt.Errorf("set caller-ID prefix on DID %s: %w", did, err)
	}

	readback, err := getVoipmsDIDInfo(ctx, vc, did)
	if err != nil {
		return fmt.Errorf("verify DID %s after setting caller-ID prefix: %w", did, err)
	}
	wantRouting := voipmsStringField(snapshot, "routing")
	gotRouting := voipmsStringField(readback, "routing")
	if wantRouting == "" || gotRouting != wantRouting {
		return fmt.Errorf(
			"DID %s readback shows routing %q, want preserved routing %q — refusing to trust the caller-ID prefix change",
			did, gotRouting, wantRouting)
	}
	if got := voipmsStringField(readback, "callerid_prefix"); got != prefix {
		return fmt.Errorf("DID %s readback shows caller-ID prefix %q, want %q", did, got, prefix)
	}
	return nil
}

// --------------------------------------------------------------------------
// Cobra command tree.

// NewVoipmsCmd builds the "kv voipms" parent command, mirroring NewCodeCmd's
// structure. Sub-commands: balance, route-did, set-caps, create-subaccount,
// set-cid-prefix, clear-cid-prefix.
func NewVoipmsCmd(cfg *Config) *cobra.Command {
	voipmsCmd := &cobra.Command{
		Use:   "voipms",
		Short: "Automate the API-drivable VoIP.ms provisioning steps (D-03)",
		Long: "kv voipms wraps the repeatable, API-drivable VoIP.ms provisioning steps:\n" +
			"reading account balance, routing a DID to the klanker-pbx subaccount,\n" +
			"and (optionally) creating the subaccount. Per-call caps are NOT\n" +
			"API-drivable (see `kv voipms set-caps` for where they are enforced).\n\n" +
			"Portal-only security steps (2FA, international/premium lock, balance\n" +
			"alerts, API IP-whitelist) are NOT automated here — see\n" +
			"docs/operators/voipms-provisioning-runbook.md for the full §25.F order.\n\n" +
			"Credentials (VOIPMS_API_USERNAME / VOIPMS_API_PASSWORD) are read from the\n" +
			"environment only (SSM-sourced per D-04) — never a flag, never printed.",
	}

	balance := &cobra.Command{
		Use:   "balance",
		Short: "Read the current VoIP.ms account balance",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			creds, err := cfg.resolveVoipmsCreds(c.Context())
			if err != nil {
				return err
			}
			vc := newVoipmsClient(creds)
			bal, err := getVoipmsBalance(c.Context(), vc)
			if err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "VoIP.ms balance: %s\n", bal)
			return nil
		},
	}
	voipmsCmd.AddCommand(balance)

	var routeSubaccount string
	routeDid := &cobra.Command{
		Use:   "route-did <did>",
		Short: "Route a DID to the klanker-pbx subaccount",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			creds, err := cfg.resolveVoipmsCreds(c.Context())
			if err != nil {
				return err
			}
			vc := newVoipmsClient(creds)
			if err := routeVoipmsDidToPbx(c.Context(), vc, args[0], routeSubaccount); err != nil {
				return err
			}
			sub := routeSubaccount
			if sub == "" {
				sub = "klanker-pbx"
			}
			fmt.Fprintf(c.OutOrStdout(), "routed DID %s to subaccount %s\n", args[0], sub)
			return nil
		},
	}
	routeDid.Flags().StringVar(&routeSubaccount, "subaccount", "klanker-pbx", "VoIP.ms subaccount username to route the DID to")
	voipmsCmd.AddCommand(routeDid)

	setCaps := &cobra.Command{
		Use:   "set-caps",
		Short: "Explain where per-call caps are enforced (no VoIP.ms API method exists)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			// Fail loudly rather than pretend a cap was applied: the
			// VoIP.ms API has no per-call max-duration method (verified
			// 2026-07-12), so a "success" here would mislead operators.
			return errNoCapAPI
		},
	}
	voipmsCmd.AddCommand(setCaps)

	var subUsername, subPassword, subAllowedIP string
	createSub := &cobra.Command{
		Use:   "create-subaccount",
		Short: "Create the klanker-pbx subaccount (outbound-disabled, IP-restricted)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			creds, err := cfg.resolveVoipmsCreds(c.Context())
			if err != nil {
				return err
			}
			if subPassword == "" {
				return fmt.Errorf("--password is required (a strong unique SIP password)")
			}
			vc := newVoipmsClient(creds)
			if err := createVoipmsSubaccount(c.Context(), vc, subUsername, subPassword, subAllowedIP); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "created subaccount %s (outbound disabled)\n", subUsername)
			return nil
		},
	}
	createSub.Flags().StringVar(&subUsername, "username", "klanker-pbx", "subaccount username to create")
	createSub.Flags().StringVar(&subPassword, "password", "", "subaccount SIP password (required; generate a strong unique value)")
	createSub.Flags().StringVar(&subAllowedIP, "allowed-ip", "", "IP to restrict the subaccount to (edge egress IP)")
	voipmsCmd.AddCommand(createSub)

	setCidPrefix := &cobra.Command{
		Use:   "set-cid-prefix <did> <tag>",
		Short: "Set the caller-ID name prefix on a DID (full-snapshot preserve, cnam forced 0)",
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			creds, err := cfg.resolveVoipmsCreds(c.Context())
			if err != nil {
				return err
			}
			vc := newVoipmsClient(creds)
			if err := setVoipmsDIDPrefix(c.Context(), vc, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "set caller-ID prefix %q on DID %s (cnam forced 0, routing preserved)\n", args[1], args[0])
			return nil
		},
	}
	voipmsCmd.AddCommand(setCidPrefix)

	clearCidPrefix := &cobra.Command{
		Use:   "clear-cid-prefix <did>",
		Short: "Clear the caller-ID name prefix on a DID (full-snapshot preserve, cnam forced 0)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			creds, err := cfg.resolveVoipmsCreds(c.Context())
			if err != nil {
				return err
			}
			vc := newVoipmsClient(creds)
			if err := setVoipmsDIDPrefix(c.Context(), vc, args[0], ""); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "cleared caller-ID prefix on DID %s\n", args[0])
			return nil
		},
	}
	voipmsCmd.AddCommand(clearCidPrefix)

	// cfg is accepted (mirroring NewCodeCmd's signature / root.go's uniform
	// registration call) though unused today — no DynamoDB/table access is
	// needed for VoIP.ms REST calls; kept for signature consistency and any
	// future need (e.g. reading --table-scoped defaults).
	return voipmsCmd
}
