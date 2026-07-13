// Package cmd — kv telephony: a single operator table covering the inbound-
// telephony surface (§23/§24 VoIP.ms + Asterisk gate): DID caller-ID -> access
// code mappings (DynamoDB), the DTMF PIN + gate passphrase words (SSM
// SecureString, redacted by default), and the [telephony] gate config
// (best-effort read of apps/voice/configs/telephony.toml).
package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
)

// --------------------------------------------------------------------------
// DID caller-ID -> access-code mappings (DynamoDB base-table scan).

// PhoneMappingRecord is the read-side shape of a phone-mapped AccessCode
// item — one row per DID caller-ID mapping.
type PhoneMappingRecord struct {
	Phone        string `json:"phone" dynamodbav:"phone"`
	Code         string `json:"code" dynamodbav:"code"`
	TierID       string `json:"tierId" dynamodbav:"tierId"`
	PhoneEnabled bool   `json:"phoneEnabled" dynamodbav:"phoneEnabled"`
}

// telephonyScanAPI is the narrow subset of *dynamodb.Client this file needs,
// so tests can inject an in-memory fake instead of a real DynamoDB
// connection.
type telephonyScanAPI interface {
	Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// ListPhoneMappings lists every phone-mapped access code.
//
// Read-path decision: the gsi3/byPhone index (electro.GSI3IndexName) is
// partitioned PER-PHONE ("phone#${phone}", see electro/keys.go) — there is
// no single partition to Query for "all phone-mapped codes", and a sparse
// index's attribute projection does not guarantee tierId is present on every
// item either. So this lists via a base-table Scan with
// FilterExpression: attribute_exists(phone), paginating over
// LastEvaluatedKey exactly like ListAccessCodes' loop. This is the
// documented acceptable filtered-scan choice for what is, in practice, a
// small operator table.
func ListPhoneMappings(ctx context.Context, api telephonyScanAPI, table string) ([]PhoneMappingRecord, error) {
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

// --------------------------------------------------------------------------
// Gate secrets (SSM SecureString, redacted by default).

// telephonyAccessPinParam / telephonyPassphraseWordsParam are the two §24
// gate secret parameter names (see apps/voice/configs/telephony.toml's
// documented env/SSM secret list: TELEPHONY_ACCESS_PIN,
// TELEPHONY_PASSPHRASE_WORDS).
const (
	telephonyAccessPinParam       = "/kmv/secrets/use1/telephony/access_pin"
	telephonyPassphraseWordsParam = "/kmv/secrets/use1/telephony/passphrase_words"
)

// ssmGetParameterAPI is the narrow subset of *ssm.Client this file needs, so
// tests can inject an in-memory fake instead of a real SSM connection.
type ssmGetParameterAPI interface {
	GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// SecretEntry is one gate-secret's redaction status for the report.
type SecretEntry struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// Value carries the secret only when Status=="set" AND --show-secrets was
	// passed; omitempty keeps it out of the JSON encoding entirely otherwise
	// (T-dfu-02).
	Value string `json:"value,omitempty"`
}

// SecretsReport is the §24 gate-secrets section of the telephony report.
type SecretsReport struct {
	Entries []SecretEntry `json:"entries"`
}

// readTelephonySecrets reads the two §24 gate secrets from SSM. When show is
// false it does NOT call SSM at all (T-dfu-01) — every entry is
// Status="hidden — use --show-secrets" with no value. When show is true it
// calls GetParameter WithDecryption for each parameter name and classifies
// the result: found -> Status "set" + Value; ParameterNotFound -> Status
// "not set", no value; any other error (e.g. AccessDenied) -> Status "error"
// + a short note, Value empty. An SSM failure is NEVER returned as a
// top-level error — the command must keep rendering the DID + gate-config
// sections even if the operator lacks SSM permissions (T-dfu-03).
func readTelephonySecrets(ctx context.Context, api ssmGetParameterAPI, show bool) SecretsReport {
	names := []string{telephonyAccessPinParam, telephonyPassphraseWordsParam}
	report := SecretsReport{Entries: make([]SecretEntry, 0, len(names))}
	for _, name := range names {
		if !show {
			report.Entries = append(report.Entries, SecretEntry{
				Name:   name,
				Status: "hidden — use --show-secrets",
			})
			continue
		}
		out, err := api.GetParameter(ctx, &ssm.GetParameterInput{
			Name:           aws.String(name),
			WithDecryption: aws.Bool(true),
		})
		if err != nil {
			var notFound *ssmtypes.ParameterNotFound
			if errors.As(err, &notFound) {
				report.Entries = append(report.Entries, SecretEntry{
					Name:   name,
					Status: "not set",
				})
				continue
			}
			report.Entries = append(report.Entries, SecretEntry{
				Name:   name,
				Status: fmt.Sprintf("error — %s", shortSSMErrorNote(err)),
			})
			continue
		}
		value := ""
		if out.Parameter != nil && out.Parameter.Value != nil {
			value = *out.Parameter.Value
		}
		report.Entries = append(report.Entries, SecretEntry{
			Name:   name,
			Status: "set",
			Value:  value,
		})
	}
	return report
}

// shortSSMErrorNote derives a short, non-sensitive note from an SSM error
// (e.g. AccessDenied) without ever including request internals.
func shortSSMErrorNote(err error) string {
	var apiErr interface{ ErrorCode() string }
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return "unavailable"
}

// --------------------------------------------------------------------------
// Gate config (best-effort minimal [telephony] TOML block scan — NO new
// TOML dependency; kv has no TOML parser).

// GateConfigReport is the parsed [telephony] gate-config section of the
// report. Found=false means the config file was not found; every field then
// shows "config not found" in text rendering.
type GateConfigReport struct {
	Found             bool   `json:"found"`
	GateMode          string `json:"gateMode,omitempty"`
	RequireGate       string `json:"requireGate,omitempty"`
	GateWindowSeconds string `json:"gateWindowSeconds,omitempty"`
	UnlockTierID      string `json:"unlockTierId,omitempty"`
}

// parseGateConfig opens path and does a minimal line scan of its [telephony]
// block, deliberately scoped to four scalar keys — NOT a full TOML parse.
// It finds the "[telephony]" header line, reads subsequent lines until the
// next "[section]" header (or EOF), and for each "key = value" line in that
// span extracts gate_mode, require_gate, gate_window_seconds, and
// unlock_tier_id — stripping surrounding quotes and any trailing
// " #"-prefixed inline comment. A missing file yields
// GateConfigReport{Found: false}, not an error.
func parseGateConfig(path string) (GateConfigReport, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return GateConfigReport{Found: false}, nil
		}
		return GateConfigReport{}, fmt.Errorf("open gate config %q: %w", path, err)
	}
	defer f.Close()
	return scanTelephonyBlock(f), nil
}

// scanTelephonyBlock implements parseGateConfig's minimal line scan over an
// already-open reader — split out so tests can exercise it directly against
// a strings.Reader if desired, though parseGateConfig(tempfile) is the
// primary test path.
func scanTelephonyBlock(r io.Reader) GateConfigReport {
	report := GateConfigReport{Found: false}
	inBlock := false
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if line == "[telephony]" || strings.HasPrefix(line, "[telephony]") {
				inBlock = true
				report.Found = true
				continue
			}
			if inBlock {
				// a new [section] header ends the telephony block.
				break
			}
			continue
		}
		if !inBlock {
			continue
		}
		key, value, ok := parseTOMLScalarLine(line)
		if !ok {
			continue
		}
		switch key {
		case "gate_mode":
			report.GateMode = value
		case "require_gate":
			report.RequireGate = value
		case "gate_window_seconds":
			report.GateWindowSeconds = value
		case "unlock_tier_id":
			report.UnlockTierID = value
		}
	}
	// Best-effort: a read error mid-scan returns whatever was parsed so far —
	// the gate-config section is advisory and never fatal to `telephony list`.
	_ = scanner.Err()
	return report
}

// parseTOMLScalarLine splits a "key = value  # comment" line, stripping
// surrounding quotes from value and any trailing " #"-prefixed inline
// comment. Returns ok=false for lines that aren't a recognizable key=value
// pair (e.g. a bare "[section]" already handled by the caller, or a
// comment-only line).
func parseTOMLScalarLine(line string) (key, value string, ok bool) {
	if strings.HasPrefix(line, "#") {
		return "", "", false
	}
	rawKey, rawValue, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(rawKey)
	value = strings.TrimSpace(rawValue)
	if commentIdx := strings.Index(value, " #"); commentIdx >= 0 {
		value = strings.TrimSpace(value[:commentIdx])
	}
	value = strings.Trim(value, `"`)
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

// --------------------------------------------------------------------------
// Inbound DIDs (§25 VoIP.ms getDIDsInfo — the numbers the public calls).
//
// Distinct from the DID caller-ID -> access-code mint mappings above
// (PhoneMappingRecord, keyed by the CALLER's number): this section answers
// "what numbers do people actually dial to reach the agent?" — sourced live
// from VoIP.ms, since those DIDs are wildcard-routed to the Asterisk edge
// and are not enumerated anywhere in repo config.

// InboundDIDReport is the Inbound DIDs section of the telephony report.
// Status carries a short, human-readable degradation note (not configured /
// API error) — Records is empty whenever Status is non-empty.
type InboundDIDReport struct {
	Records []InboundDIDRecord `json:"records"`
	Status  string             `json:"status,omitempty"`
}

// readInboundDIDs mirrors readTelephonySecrets' total-degradation
// philosophy: a missing-creds or VoIP.ms API failure NEVER returns a
// top-level error — the DynamoDB/SSM/gate-config sections must always
// render regardless of VoIP.ms's availability (T-k0k-03). When credsOK is
// false, lister is never invoked and Status explains which env vars to set.
// When the lister errors, Status is derived via shortVoipmsErrorNote — never
// the raw error, which could carry the request URL / api_password.
func readInboundDIDs(ctx context.Context, credsOK bool, lister func(context.Context) ([]InboundDIDRecord, error)) InboundDIDReport {
	if !credsOK {
		return InboundDIDReport{
			Status: "not configured — set VOIPMS_API_USERNAME/VOIPMS_API_PASSWORD to list inbound DIDs",
		}
	}
	records, err := lister(ctx)
	if err != nil {
		return InboundDIDReport{
			Status: fmt.Sprintf("error — %s", shortVoipmsErrorNote(err)),
		}
	}
	return InboundDIDReport{Records: records}
}

// shortVoipmsErrorNote derives a short, non-sensitive note from a
// ListInboundDIDs error without ever interpolating the raw request URL or
// api_password. When the error is a *voipmsStatusError, the VoIP.ms response
// enum (status/error code — never a credential) is surfaced with an
// actionable hint so the operator can self-diagnose (e.g. ip_not_enabled ->
// whitelist this IP). For any other error (network/parse) the note stays
// generic: voipmsClient.do() already unwraps *url.Error before wrapping (so
// the URL — which carries api_password in its query string — never reaches
// the error chain), and we never echo a raw transport error on top of that.
func shortVoipmsErrorNote(err error) string {
	if err == nil {
		return "unavailable"
	}
	var statusErr *voipmsStatusError
	if errors.As(err, &statusErr) {
		code := statusErr.Code()
		// Defensive length bound: VoIP.ms codes are short enums; anything
		// unexpectedly long falls back to the generic note rather than risk
		// echoing an oversized/unknown payload.
		if code != "" && len(code) <= 40 {
			if hint := voipmsStatusHint(code); hint != "" {
				return fmt.Sprintf("VoIP.ms rejected the call: %s (%s)", code, hint)
			}
			return fmt.Sprintf("VoIP.ms rejected the call: %s", code)
		}
	}
	return "VoIP.ms API call failed (network or response error)"
}

// voipmsStatusHint maps the common VoIP.ms error codes to a one-line operator
// hint. Unknown codes return "" — the caller then surfaces the bare code.
func voipmsStatusHint(code string) string {
	switch code {
	case "ip_not_enabled":
		return "whitelist this IP in the VoIP.ms API panel"
	case "invalid_credentials":
		return "check the VoIP.ms API creds (env or SSM)"
	case "missing_credentials":
		return "set VOIPMS_API_USERNAME/VOIPMS_API_PASSWORD or the SSM params"
	case "no_did":
		return "no DIDs provisioned on this account"
	default:
		return ""
	}
}

// --------------------------------------------------------------------------
// Assembled report + rendering.

// TelephonyListReport is the full `kv telephony list` output shape.
type TelephonyListReport struct {
	InboundDIDs InboundDIDReport     `json:"inboundDids"`
	DIDs        []PhoneMappingRecord `json:"dids"`
	Secrets     SecretsReport        `json:"secrets"`
	GateConfig  GateConfigReport     `json:"gateConfig"`
}

// defaultTelephonyConfigPath is the default --config path, relative to the
// kv binary's working directory (the repo root in normal operator usage).
const defaultTelephonyConfigPath = "apps/voice/configs/telephony.toml"

// SSMClient builds an aws-sdk-go-v2 SSM client from the Config via the
// shared loadAWS helper (root.go), mirroring DynamoClient. Region defaults
// to "us-east-1" (the /kmv/secrets/use1/* params live in region use1) and
// profile defaults to klanker-application — both overridable via
// --region/--profile or their env vars — no EndpointURL override needed
// (SSM has no local dev substitute like dynamodb-local).
func (c *Config) SSMClient(ctx context.Context) (*ssm.Client, error) {
	cfg, err := c.loadAWS(ctx)
	if err != nil {
		return nil, err
	}
	return ssm.NewFromConfig(cfg), nil
}

// NewTelephonyCmd builds the "kv telephony" parent command with a single
// "list" subcommand — the operator's single at-a-glance view of the inbound
// telephony surface.
func NewTelephonyCmd(cfg *Config) *cobra.Command {
	telephonyCmd := &cobra.Command{
		Use:   "telephony",
		Short: "Operator overview of the inbound-telephony surface (DIDs, gate secrets, gate config)",
		Long: "kv telephony gives an operator a single at-a-glance view of the\n" +
			"inbound-telephony configuration without hand-querying DynamoDB, SSM,\n" +
			"and the pipeline TOML separately: DID caller-ID -> access-code\n" +
			"mappings, the §24 gate secrets (redacted by default), and the\n" +
			"[telephony] gate config.",
	}

	var (
		showSecrets bool
		asJSON      bool
		configPath  string
	)
	list := &cobra.Command{
		Use:   "list",
		Short: "List DID mappings, gate-secret status, and gate config",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			client, err := cfg.DynamoClient(c.Context())
			if err != nil {
				return err
			}
			dids, err := ListPhoneMappings(c.Context(), client, cfg.Table)
			if err != nil {
				return err
			}

			var inboundDIDs InboundDIDReport
			if creds, err := cfg.resolveVoipmsCreds(c.Context()); err != nil {
				inboundDIDs = readInboundDIDs(c.Context(), false, nil)
			} else {
				vc := newVoipmsClient(creds)
				inboundDIDs = readInboundDIDs(c.Context(), true, func(ctx context.Context) ([]InboundDIDRecord, error) {
					return ListInboundDIDs(ctx, vc)
				})
			}

			var secrets SecretsReport
			if showSecrets {
				ssmClient, err := cfg.SSMClient(c.Context())
				if err != nil {
					return err
				}
				secrets = readTelephonySecrets(c.Context(), ssmClient, true)
			} else {
				secrets = readTelephonySecrets(c.Context(), nil, false)
			}

			gateConfig, err := parseGateConfig(configPath)
			if err != nil {
				return err
			}

			report := TelephonyListReport{
				InboundDIDs: inboundDIDs,
				DIDs:        dids,
				Secrets:     secrets,
				GateConfig:  gateConfig,
			}
			return printTelephony(c, report, asJSON)
		},
	}
	list.Flags().BoolVar(&showSecrets, "show-secrets", false, "read + display the decrypted gate secrets from SSM (default: hidden)")
	list.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	list.Flags().StringVar(&configPath, "config", defaultTelephonyConfigPath, "path to the telephony pipeline TOML config (for the [telephony] gate-config section)")
	telephonyCmd.AddCommand(list)

	return telephonyCmd
}

// printTelephony renders the report, mirroring printAccessCodes/printTiers.
func printTelephony(c *cobra.Command, report TelephonyListReport, asJSON bool) error {
	out := c.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Fprintln(out, "Inbound DIDs (numbers the public calls):")
	if report.InboundDIDs.Status != "" {
		fmt.Fprintf(out, "  %s\n", report.InboundDIDs.Status)
	} else {
		didW := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(didW, "DID\tDESCRIPTION\tROUTING\tPOP")
		for _, r := range report.InboundDIDs.Records {
			fmt.Fprintf(didW, "%s\t%s\t%s\t%s\n", r.DID, r.Description, r.Routing, r.POP)
		}
		if err := didW.Flush(); err != nil {
			return err
		}
	}

	fmt.Fprintln(out, "\nCaller-ID mint mappings (auto-identity by caller ID):")
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "PHONE\tCODE\tTIER\tENABLED")
	for _, r := range report.DIDs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\n", r.Phone, r.Code, r.TierID, r.PhoneEnabled)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(out, "\nGate secrets:")
	for _, s := range report.Secrets.Entries {
		if s.Value != "" {
			fmt.Fprintf(out, "  %s: %s (%s)\n", s.Name, s.Status, s.Value)
		} else {
			fmt.Fprintf(out, "  %s: %s\n", s.Name, s.Status)
		}
	}

	fmt.Fprintln(out, "\nGate config:")
	if !report.GateConfig.Found {
		fmt.Fprintln(out, "  config not found")
		return nil
	}
	printGateField(out, "gate_mode", report.GateConfig.GateMode)
	printGateField(out, "require_gate", report.GateConfig.RequireGate)
	printGateField(out, "gate_window_seconds", report.GateConfig.GateWindowSeconds)
	printGateField(out, "unlock_tier_id", report.GateConfig.UnlockTierID)
	return nil
}

func printGateField(out io.Writer, name, value string) {
	if value == "" {
		value = "config not found"
	}
	fmt.Fprintf(out, "  %s: %s\n", name, value)
}
