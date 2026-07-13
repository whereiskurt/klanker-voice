package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	smithy "github.com/aws/smithy-go"
	"github.com/spf13/cobra"
)

// --------------------------------------------------------------------------
// Fakes.

// fakeTelephonyScanClient implements telephonyScanAPI over a canned set of
// items (or a configurable error) — no live AWS call ever made.
type fakeTelephonyScanClient struct {
	items []map[string]any
	err   error
}

func (f *fakeTelephonyScanClient) Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	items := make([]map[string]types.AttributeValue, 0, len(f.items))
	for _, m := range f.items {
		av, err := attributevalue.MarshalMap(m)
		if err != nil {
			return nil, err
		}
		items = append(items, av)
	}
	return &dynamodb.ScanOutput{Items: items}, nil
}

// fakeSSMGetParameterClient implements ssmGetParameterAPI with
// per-parameter-name canned responses/errors, and records which parameter
// names were actually requested (so tests can assert GetParameter was never
// called).
type fakeSSMGetParameterClient struct {
	values  map[string]string
	errs    map[string]error
	calledN []string
}

func (f *fakeSSMGetParameterClient) GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	name := aws.ToString(params.Name)
	f.calledN = append(f.calledN, name)
	if err, ok := f.errs[name]; ok {
		return nil, err
	}
	val, ok := f.values[name]
	if !ok {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{
			Name:  aws.String(name),
			Value: aws.String(val),
		},
	}, nil
}

// --------------------------------------------------------------------------
// ListPhoneMappings / DID rows.

func TestListPhoneMappings_RendersRows(t *testing.T) {
	fake := &fakeTelephonyScanClient{
		items: []map[string]any{
			{"phone": "+14165551234", "code": "defcon34", "tierId": "kph-tier", "phoneEnabled": true},
			{"phone": "+16135551234", "code": "otheruser", "tierId": "pstn-baseline-tier", "phoneEnabled": false},
		},
	}
	got, err := ListPhoneMappings(context.Background(), fake, "kmv-auth-electro")
	if err != nil {
		t.Fatalf("ListPhoneMappings() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	want := []PhoneMappingRecord{
		{Phone: "+14165551234", Code: "defcon34", TierID: "kph-tier", PhoneEnabled: true},
		{Phone: "+16135551234", Code: "otheruser", TierID: "pstn-baseline-tier", PhoneEnabled: false},
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestListPhoneMappings_Empty(t *testing.T) {
	fake := &fakeTelephonyScanClient{items: []map[string]any{}}
	got, err := ListPhoneMappings(context.Background(), fake, "kmv-auth-electro")
	if err != nil {
		t.Fatalf("ListPhoneMappings() error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0", len(got))
	}
	if got == nil {
		t.Fatal("ListPhoneMappings() returned nil, want a non-nil empty slice")
	}

	// The empty case should render the table header only.
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := printTelephony(cmd, TelephonyListReport{DIDs: got}, false); err != nil {
		t.Fatalf("printTelephony() error: %v", err)
	}
	if !strings.Contains(buf.String(), "PHONE") || !strings.Contains(buf.String(), "CODE") {
		t.Errorf("output missing table header: %q", buf.String())
	}
}

// --------------------------------------------------------------------------
// Secrets: redacted by default, revealed with --show-secrets, SSM errors
// handled gracefully.

func TestReadTelephonySecrets_HiddenByDefault(t *testing.T) {
	fake := &fakeSSMGetParameterClient{
		values: map[string]string{
			telephonyAccessPinParam:       "1234",
			telephonyPassphraseWordsParam: "correct horse battery",
		},
	}
	report := readTelephonySecrets(context.Background(), fake, false)
	if len(fake.calledN) != 0 {
		t.Fatalf("GetParameter called %d times with show=false, want 0 calls", len(fake.calledN))
	}
	for _, e := range report.Entries {
		if e.Value != "" {
			t.Errorf("entry %q has a non-empty Value with show=false: %q", e.Name, e.Value)
		}
		if !strings.Contains(e.Status, "hidden") {
			t.Errorf("entry %q status = %q, want it to mention hidden", e.Name, e.Status)
		}
	}

	// The rendered default text output must not contain a secret value either.
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := printTelephony(cmd, TelephonyListReport{Secrets: report}, false); err != nil {
		t.Fatalf("printTelephony() error: %v", err)
	}
	if strings.Contains(buf.String(), "1234") || strings.Contains(buf.String(), "correct horse battery") {
		t.Errorf("default text output leaked a secret value: %q", buf.String())
	}
}

func TestReadTelephonySecrets_ShownWithShowSecrets(t *testing.T) {
	fake := &fakeSSMGetParameterClient{
		values: map[string]string{
			telephonyAccessPinParam:       "1234",
			telephonyPassphraseWordsParam: "correct horse battery",
		},
	}
	report := readTelephonySecrets(context.Background(), fake, true)
	if len(fake.calledN) != 2 {
		t.Fatalf("GetParameter called %d times with show=true, want 2 calls", len(fake.calledN))
	}
	got := map[string]SecretEntry{}
	for _, e := range report.Entries {
		got[e.Name] = e
	}
	if e := got[telephonyAccessPinParam]; e.Status != "set" || e.Value != "1234" {
		t.Errorf("access pin entry = %+v, want Status=set Value=1234", e)
	}
	if e := got[telephonyPassphraseWordsParam]; e.Status != "set" || e.Value != "correct horse battery" {
		t.Errorf("passphrase words entry = %+v, want Status=set Value=%q", e, "correct horse battery")
	}
}

func TestReadTelephonySecrets_SSMErrorsHandledGracefully(t *testing.T) {
	fake := &fakeSSMGetParameterClient{
		values: map[string]string{},
		errs: map[string]error{
			telephonyAccessPinParam:       &ssmtypes.ParameterNotFound{},
			telephonyPassphraseWordsParam: &smithy.GenericAPIError{Code: "AccessDenied", Message: "not authorized"},
		},
	}
	report := readTelephonySecrets(context.Background(), fake, true)
	got := map[string]SecretEntry{}
	for _, e := range report.Entries {
		got[e.Name] = e
	}
	if e := got[telephonyAccessPinParam]; e.Status != "not set" || e.Value != "" {
		t.Errorf("not-found entry = %+v, want Status=\"not set\" Value=\"\"", e)
	}
	if e := got[telephonyPassphraseWordsParam]; !strings.Contains(e.Status, "error") || e.Value != "" {
		t.Errorf("access-denied entry = %+v, want Status containing \"error\" and Value=\"\"", e)
	}
}

// --------------------------------------------------------------------------
// --json shape.

func TestPrintTelephony_JSONShape_HidesSecretsByDefault(t *testing.T) {
	const secretValue = "s3cr3t-pin-value"
	fake := &fakeSSMGetParameterClient{
		values: map[string]string{telephonyAccessPinParam: secretValue},
	}
	secrets := readTelephonySecrets(context.Background(), fake, false)
	report := TelephonyListReport{
		DIDs:       []PhoneMappingRecord{{Phone: "+14165551234", Code: "defcon34", TierID: "kph-tier", PhoneEnabled: true}},
		Secrets:    secrets,
		GateConfig: GateConfigReport{Found: false},
	}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := printTelephony(cmd, report, true); err != nil {
		t.Fatalf("printTelephony() error: %v", err)
	}

	var decoded TelephonyListReport
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if strings.Contains(buf.String(), secretValue) {
		t.Errorf("JSON output with show=false contains a secret value: %s", buf.String())
	}
	if strings.Contains(buf.String(), `"value"`) {
		t.Errorf("JSON output with show=false contains a \"value\" field at all (want omitempty to drop it): %s", buf.String())
	}
}

func TestPrintTelephony_JSONShape_ShowsSecretsWhenRevealed(t *testing.T) {
	fake := &fakeSSMGetParameterClient{
		values: map[string]string{telephonyAccessPinParam: "1234", telephonyPassphraseWordsParam: "correct horse battery"},
	}
	secrets := readTelephonySecrets(context.Background(), fake, true)
	report := TelephonyListReport{Secrets: secrets, GateConfig: GateConfigReport{Found: false}}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := printTelephony(cmd, report, true); err != nil {
		t.Fatalf("printTelephony() error: %v", err)
	}

	var decoded TelephonyListReport
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "1234") {
		t.Errorf("JSON output with show=true is missing the revealed secret value: %s", buf.String())
	}
}

// --------------------------------------------------------------------------
// Gate config best-effort scan.

func TestParseGateConfig_ParsesFourFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telephony.toml")
	content := `label = "KPH(telephony-harness)"

[stt]
provider = "deepgram-nova3"

[telephony]                         # some inline comment
enabled = true
provider = "voipms"
gate_mode = "either"                 # "dtmf" | "passphrase" | "either"
require_gate = true
gate_window_seconds = 10             # fail-closed goodbye
unlock_tier_id = "kph-tier"          # tier granted on unlock

[quota]
heartbeat_renew_interval = 15
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	report, err := parseGateConfig(path)
	if err != nil {
		t.Fatalf("parseGateConfig() error: %v", err)
	}
	if !report.Found {
		t.Fatal("report.Found = false, want true")
	}
	if report.GateMode != "either" {
		t.Errorf("GateMode = %q, want %q", report.GateMode, "either")
	}
	if report.RequireGate != "true" {
		t.Errorf("RequireGate = %q, want %q", report.RequireGate, "true")
	}
	if report.GateWindowSeconds != "10" {
		t.Errorf("GateWindowSeconds = %q, want %q", report.GateWindowSeconds, "10")
	}
	if report.UnlockTierID != "kph-tier" {
		t.Errorf("UnlockTierID = %q, want %q", report.UnlockTierID, "kph-tier")
	}
}

func TestParseGateConfig_MissingFileIsNotAnError(t *testing.T) {
	report, err := parseGateConfig(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("parseGateConfig() on a missing file returned an error, want nil: %v", err)
	}
	if report.Found {
		t.Error("report.Found = true for a missing file, want false")
	}
}

// --------------------------------------------------------------------------
// Command tree wiring.

func TestTelephonyCmdHelpListsFlags(t *testing.T) {
	cfg := &Config{}
	telephonyCmd := NewTelephonyCmd(cfg)
	var list *cobra.Command
	for _, sub := range telephonyCmd.Commands() {
		if sub.Name() == "list" {
			list = sub
		}
	}
	if list == nil {
		t.Fatal("kv telephony is missing the list sub-command")
	}
	for _, flag := range []string{"show-secrets", "json", "config"} {
		if list.Flags().Lookup(flag) == nil {
			t.Errorf("kv telephony list is missing expected flag --%s", flag)
		}
	}
}

func TestTelephonyRootRegistersCmd(t *testing.T) {
	root := NewRootCmd()
	found := false
	for _, sub := range root.Commands() {
		if sub.Name() == "telephony" {
			found = true
		}
	}
	if !found {
		t.Fatal("kv root command tree is missing the telephony sub-command")
	}
}

// --------------------------------------------------------------------------
// readInboundDIDs — graceful degradation (no creds / lister error / success).

func TestReadInboundDIDs_CredsAbsentNotesEnvVars(t *testing.T) {
	report := readInboundDIDs(context.Background(), false, nil)
	if len(report.Records) != 0 {
		t.Errorf("Records = %+v, want empty", report.Records)
	}
	if !strings.Contains(report.Status, "VOIPMS_API_USERNAME") || !strings.Contains(report.Status, "VOIPMS_API_PASSWORD") {
		t.Errorf("Status = %q, want it to mention VOIPMS_API_USERNAME/VOIPMS_API_PASSWORD", report.Status)
	}
}

func TestReadInboundDIDs_ListerErrorYieldsSafeShortNote(t *testing.T) {
	const sentinelPassword = "s3cr3t-api-password-should-never-leak"
	listerErr := errors.New("call voip.ms method getDIDsInfo: Get \"https://voip.ms/api/v1/rest.php?api_password=" + sentinelPassword + "&method=getDIDsInfo\": connection refused")
	lister := func(ctx context.Context) ([]InboundDIDRecord, error) {
		return nil, listerErr
	}
	report := readInboundDIDs(context.Background(), true, lister)
	if len(report.Records) != 0 {
		t.Errorf("Records = %+v, want empty", report.Records)
	}
	if report.Status == "" {
		t.Fatal("Status is empty, want a short safe note")
	}
	if strings.Contains(report.Status, sentinelPassword) {
		t.Errorf("Status leaked the password sentinel: %q", report.Status)
	}
	if strings.Contains(report.Status, "voip.ms/api") || strings.Contains(report.Status, "rest.php") {
		t.Errorf("Status leaked the raw request URL: %q", report.Status)
	}
	if len(report.Status) > 120 {
		t.Errorf("Status is %d chars, want a short note (<=120 chars): %q", len(report.Status), report.Status)
	}
}

func TestReadInboundDIDs_ListerSuccessCarriesRecords(t *testing.T) {
	want := []InboundDIDRecord{
		{DID: "14165551234", Description: "Main line", Routing: "sip:klanker-pbx", POP: "Toronto"},
	}
	lister := func(ctx context.Context) ([]InboundDIDRecord, error) {
		return want, nil
	}
	report := readInboundDIDs(context.Background(), true, lister)
	if report.Status != "" {
		t.Errorf("Status = %q, want empty on success", report.Status)
	}
	if len(report.Records) != 1 || report.Records[0] != want[0] {
		t.Errorf("Records = %+v, want %+v", report.Records, want)
	}
}

// --------------------------------------------------------------------------
// printTelephony — Inbound DIDs section renders first, with graceful
// degradation and --json inclusion.

func TestPrintTelephony_InboundDIDsRendersBeforeMintMappings(t *testing.T) {
	report := TelephonyListReport{
		InboundDIDs: InboundDIDReport{
			Records: []InboundDIDRecord{
				{DID: "14165551234", Description: "Main line", Routing: "sip:klanker-pbx", POP: "Toronto"},
			},
		},
		DIDs: []PhoneMappingRecord{
			{Phone: "+14165551234", Code: "defcon34", TierID: "kph-tier", PhoneEnabled: true},
		},
	}
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := printTelephony(cmd, report, false); err != nil {
		t.Fatalf("printTelephony() error: %v", err)
	}
	out := buf.String()
	inboundIdx := strings.Index(out, "Inbound DIDs")
	mintIdx := strings.Index(out, "Caller-ID mint mappings")
	if inboundIdx < 0 {
		t.Fatalf("output missing 'Inbound DIDs' section: %q", out)
	}
	if mintIdx < 0 {
		t.Fatalf("output missing 'Caller-ID mint mappings' section: %q", out)
	}
	if inboundIdx >= mintIdx {
		t.Errorf("'Inbound DIDs' (idx %d) does not appear before 'Caller-ID mint mappings' (idx %d): %q", inboundIdx, mintIdx, out)
	}
	for _, want := range []string{"DID", "DESCRIPTION", "ROUTING", "POP", "14165551234", "Main line"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q: %q", want, out)
		}
	}
}

func TestPrintTelephony_InboundDIDsStatusNoteWhenAbsent(t *testing.T) {
	report := TelephonyListReport{
		InboundDIDs: InboundDIDReport{Status: "not configured — set VOIPMS_API_USERNAME/PASSWORD to list inbound DIDs"},
	}
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := printTelephony(cmd, report, false); err != nil {
		t.Fatalf("printTelephony() error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "not configured") {
		t.Errorf("output missing the not-configured status note: %q", out)
	}
}

func TestPrintTelephony_JSONIncludesInboundDIDs(t *testing.T) {
	report := TelephonyListReport{
		InboundDIDs: InboundDIDReport{
			Records: []InboundDIDRecord{
				{DID: "14165551234", Description: "Main line", Routing: "sip:klanker-pbx", POP: "Toronto"},
			},
		},
		GateConfig: GateConfigReport{Found: false},
	}
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := printTelephony(cmd, report, true); err != nil {
		t.Fatalf("printTelephony() error: %v", err)
	}
	var decoded TelephonyListReport
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}
	if len(decoded.InboundDIDs.Records) != 1 || decoded.InboundDIDs.Records[0].DID != "14165551234" {
		t.Errorf("decoded InboundDIDs.Records = %+v, want the seeded DID record", decoded.InboundDIDs.Records)
	}
	if !strings.Contains(buf.String(), "14165551234") {
		t.Errorf("JSON output missing the inbound DID: %s", buf.String())
	}
}
