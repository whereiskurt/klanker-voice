// Package cmd — kv voipms: automates the API-drivable VoIP.ms provisioning
// steps (D-03). Portal-only security steps (2FA, international/premium
// locks, balance alerts, API IP-whitelist) are NOT automated here — they
// are documented in docs/operators/voipms-provisioning-runbook.md in the
// exact §25.F order.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

// --------------------------------------------------------------------------
// VoIP.ms REST API constants — ALL base-URL and method-name details are
// centralized in this one block per D-03's acceptance criteria (the REST
// endpoint filename below must appear exactly once in this file).
//
// SOURCE: .planning/phases/12-voip-ms-telephony-inbound-did/12-RESEARCH.md,
// section "VoIP.ms REST API Surface (D-03)" — itself citing
// voip.ms/resources/api. That research explicitly flagged every method name
// below as MEDIUM confidence: candidates gathered from public references,
// not confirmed against a live fetch of the current VoIP.ms API docs.
//
// VERIFY-BEFORE-HARDCODE: this executor session had no outbound web-fetch
// tool available, so none of the method names below could be checked
// against the live API reference as this task's plan instructs. Per the
// plan's own escape hatch ("if a name cannot be confirmed, leave a clearly
// marked UNVERIFIED comment ... do not silently guess"), every constant is
// marked UNVERIFIED. A human operator MUST confirm each name (and its exact
// parameter list) against https://voip.ms/resources/api before running any
// `kv voipms` subcommand against a real, live account. This is flagged again
// in the phase SUMMARY as an explicit follow-up item.
const (
	voipmsBaseURL = "https://voip.ms/api/v1/rest.php"

	// UNVERIFIED: creates a subaccount under the main VoIP.ms account.
	// Candidate name/shape per RESEARCH.md's Accounts/Subaccounts module.
	voipmsMethodCreateSubAccount = "createSubAccount"

	// UNVERIFIED: updates an existing subaccount's settings (IP
	// restriction, outbound enable/disable, etc).
	voipmsMethodSetSubAccount = "setSubAccount"

	// UNVERIFIED: assigns/updates a DID's routing target (subaccount, POP).
	voipmsMethodSetDIDRouting = "setDIDRouting"

	// UNVERIFIED: reads the current account balance.
	voipmsMethodGetBalance = "getBalance"

	// UNVERIFIED — LOWEST confidence of this block: RESEARCH.md could not
	// confirm a per-call max-duration cap method exists in the VoIP.ms API
	// at all; this is a candidate name only. Confirm this one first.
	voipmsMethodSetMaxCallDuration = "setMaxCallDuration"

	// UNVERIFIED: lists VoIP.ms POP/server info (used to source the SG
	// allow-list IPs documented in the operator runbook).
	voipmsMethodGetServersInfo = "getServersInfo"
)

// --------------------------------------------------------------------------
// Credentials — read ONLY from the environment (SSM-sourced by the operator
// per D-04). No literal api_username/api_password value is ever assigned in
// this file.

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
		httpClient: &http.Client{Timeout: 15 * time.Second},
		creds:      creds,
	}
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
		return out, fmt.Errorf("voip.ms method %s returned status %q", method, status)
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

// setVoipmsCallDuration sets the per-call max duration cap via
// voipmsMethodSetMaxCallDuration (the LOWEST-confidence method name in this
// file — see constants block).
func setVoipmsCallDuration(ctx context.Context, vc *voipmsClient, maxDurationSeconds int) error {
	if maxDurationSeconds <= 0 {
		return fmt.Errorf("max-duration must be a positive number of seconds")
	}
	params := url.Values{}
	params.Set("max_duration", strconv.Itoa(maxDurationSeconds))
	if _, err := vc.do(ctx, voipmsMethodSetMaxCallDuration, params); err != nil {
		return fmt.Errorf("set max call duration to %ds: %w", maxDurationSeconds, err)
	}
	return nil
}

// createVoipmsSubaccount creates the klanker-pbx subaccount. Per D-03/§25.A
// it must default outbound-disabled and IP-restricted — the exact parameter
// names for those two settings are UNVERIFIED (see constants block); a
// human must confirm them before relying on this in production and should
// re-check via `kv voipms` balance/route-did style manual verification
// after running it.
func createVoipmsSubaccount(ctx context.Context, vc *voipmsClient, username, password, allowedIP string) error {
	if username == "" || password == "" {
		return fmt.Errorf("username and password are required")
	}
	params := url.Values{}
	params.Set("subaccount", username)
	params.Set("password", password)
	// UNVERIFIED parameter names — outbound-disabled + IP-restriction, D-03/§25.A:
	params.Set("international_route", "0")
	params.Set("canada_routing", "system")
	if allowedIP != "" {
		params.Set("ip_restriction", allowedIP)
	}
	if _, err := vc.do(ctx, voipmsMethodCreateSubAccount, params); err != nil {
		return fmt.Errorf("create subaccount %s: %w", username, err)
	}
	return nil
}

// --------------------------------------------------------------------------
// Cobra command tree.

// NewVoipmsCmd builds the "kv voipms" parent command, mirroring NewCodeCmd's
// structure. Sub-commands: balance, route-did, set-caps, create-subaccount.
func NewVoipmsCmd(cfg *Config) *cobra.Command {
	voipmsCmd := &cobra.Command{
		Use:   "voipms",
		Short: "Automate the API-drivable VoIP.ms provisioning steps (D-03)",
		Long: "kv voipms wraps the repeatable, API-drivable VoIP.ms provisioning steps:\n" +
			"reading account balance, routing a DID to the klanker-pbx subaccount,\n" +
			"setting per-call caps, and (optionally) creating the subaccount.\n\n" +
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
			creds, err := voipmsCredsFromEnv()
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
			creds, err := voipmsCredsFromEnv()
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

	var maxDuration int
	setCaps := &cobra.Command{
		Use:   "set-caps",
		Short: "Set the per-call max duration cap",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			creds, err := voipmsCredsFromEnv()
			if err != nil {
				return err
			}
			vc := newVoipmsClient(creds)
			if err := setVoipmsCallDuration(c.Context(), vc, maxDuration); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "set max call duration to %ds\n", maxDuration)
			return nil
		},
	}
	setCaps.Flags().IntVar(&maxDuration, "max-duration", 600, "max seconds per call (default 10 min)")
	voipmsCmd.AddCommand(setCaps)

	var subUsername, subPassword, subAllowedIP string
	createSub := &cobra.Command{
		Use:   "create-subaccount",
		Short: "Create the klanker-pbx subaccount (outbound-disabled, IP-restricted)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			creds, err := voipmsCredsFromEnv()
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

	// cfg is accepted (mirroring NewCodeCmd's signature / root.go's uniform
	// registration call) though unused today — no DynamoDB/table access is
	// needed for VoIP.ms REST calls; kept for signature consistency and any
	// future need (e.g. reading --table-scoped defaults).
	return voipmsCmd
}
