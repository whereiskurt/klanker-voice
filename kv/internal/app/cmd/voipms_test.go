package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// newTestVoipmsClient builds a voipmsClient pointed at an httptest.Server so
// no test ever makes a live network call to voip.ms.
func newTestVoipmsClient(t *testing.T, handler http.HandlerFunc) (*voipmsClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	vc := &voipmsClient{
		baseURL:    srv.URL,
		httpClient: srv.Client(),
		creds:      voipmsCreds{Username: "test-user", Password: "test-pass"},
	}
	return vc, srv
}

// TestVoipmsRestRequestShape asserts vc.do() builds a request against the
// injected base URL with method/api_username/api_password query params, and
// parses a success envelope without making any live network call.
func TestVoipmsRestRequestShape(t *testing.T) {
	var gotQuery url.Values
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	out, err := vc.do(t.Context(), "someMethod", nil)
	if err != nil {
		t.Fatalf("do() error: %v", err)
	}
	if status, _ := out["status"].(string); status != "success" {
		t.Errorf("status = %v, want success", out["status"])
	}
	if got := gotQuery.Get("method"); got != "someMethod" {
		t.Errorf("method param = %q, want %q", got, "someMethod")
	}
	if got := gotQuery.Get("api_username"); got != "test-user" {
		t.Errorf("api_username param = %q, want %q", got, "test-user")
	}
	if got := gotQuery.Get("api_password"); got != "test-pass" {
		t.Errorf("api_password param = %q, want %q", got, "test-pass")
	}
}

// TestVoipmsRestRequestShape_FailureStatus asserts a non-"success" envelope
// surfaces as a Go error rather than being silently treated as OK.
func TestVoipmsRestRequestShape_FailureStatus(t *testing.T) {
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"failure","error":"invalid_credentials"}`))
	})

	if _, err := vc.do(t.Context(), "someMethod", nil); err == nil {
		t.Fatal("do() with status=failure returned nil error, want an error")
	}
}

// TestVoipmsGetBalanceBuildsRequest asserts getVoipmsBalance calls the
// centralized getBalance method constant and parses the balance field.
func TestVoipmsGetBalanceBuildsRequest(t *testing.T) {
	var gotMethod string
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.URL.Query().Get("method")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","balance":{"current_balance":"12.34"}}`))
	})

	bal, err := getVoipmsBalance(t.Context(), vc)
	if err != nil {
		t.Fatalf("getVoipmsBalance() error: %v", err)
	}
	if gotMethod != voipmsMethodGetBalance {
		t.Errorf("method = %q, want %q", gotMethod, voipmsMethodGetBalance)
	}
	if bal != "12.34" {
		t.Errorf("balance = %q, want %q", bal, "12.34")
	}
}

// TestVoipmsGetBalanceFallsBackToRawJSON asserts an unexpected response
// shape doesn't panic — it degrades to the raw JSON envelope, since the
// balance response shape is UNVERIFIED against a live fetch.
func TestVoipmsGetBalanceFallsBackToRawJSON(t *testing.T) {
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","unexpected_shape":true}`))
	})

	bal, err := getVoipmsBalance(t.Context(), vc)
	if err != nil {
		t.Fatalf("getVoipmsBalance() error: %v", err)
	}
	if !strings.Contains(bal, "unexpected_shape") {
		t.Errorf("fallback balance = %q, want it to contain the raw envelope", bal)
	}
}

// TestVoipmsRouteDidBuildsRequest asserts routeVoipmsDidToPbx calls the
// centralized setDIDRouting method constant with did + routing params, and
// defaults the subaccount to klanker-pbx when unset.
func TestVoipmsRouteDidBuildsRequest(t *testing.T) {
	var gotQuery url.Values
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	if err := routeVoipmsDidToPbx(t.Context(), vc, "14165551234", ""); err != nil {
		t.Fatalf("routeVoipmsDidToPbx() error: %v", err)
	}
	if got := gotQuery.Get("method"); got != voipmsMethodSetDIDRouting {
		t.Errorf("method = %q, want %q", got, voipmsMethodSetDIDRouting)
	}
	if got := gotQuery.Get("did"); got != "14165551234" {
		t.Errorf("did param = %q, want %q", got, "14165551234")
	}
	if got := gotQuery.Get("routing"); !strings.Contains(got, "klanker-pbx") {
		t.Errorf("routing param = %q, want it to reference klanker-pbx", got)
	}
}

// TestVoipmsRouteDidRejectsBlankDid asserts a blank DID never reaches the
// network layer.
func TestVoipmsRouteDidRejectsBlankDid(t *testing.T) {
	called := false
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	if err := routeVoipmsDidToPbx(t.Context(), vc, "", ""); err == nil {
		t.Fatal("routeVoipmsDidToPbx(\"\") returned nil error, want an error")
	}
	if called {
		t.Fatal("routeVoipmsDidToPbx(\"\") made an HTTP call, want it to reject before the network")
	}
}

// TestVoipmsSetCapsExplainsNoAPI asserts `kv voipms set-caps` fails loudly
// with the verified-absence error (the VoIP.ms API has no per-call
// max-duration method) instead of calling a phantom API method or
// pretending a cap was applied.
func TestVoipmsSetCapsExplainsNoAPI(t *testing.T) {
	voipmsCmd := NewVoipmsCmd(&Config{})
	for _, sub := range voipmsCmd.Commands() {
		if sub.Name() != "set-caps" {
			continue
		}
		err := sub.RunE(sub, nil)
		if err == nil {
			t.Fatal("set-caps returned nil error, want the no-cap-API explanation error")
		}
		if !strings.Contains(err.Error(), "no per-call max-duration method") {
			t.Errorf("set-caps error = %q, want it to explain the API has no per-call max-duration method", err)
		}
		if !strings.Contains(err.Error(), "voipms-provisioning-runbook.md") {
			t.Errorf("set-caps error = %q, want it to point at the provisioning runbook", err)
		}
		return
	}
	t.Fatal("set-caps sub-command not found")
}

// TestVoipmsCreateSubaccountBuildsRequest asserts createVoipmsSubaccount
// calls the centralized createSubAccount method constant and includes the
// outbound-disable / IP-restriction params.
func TestVoipmsCreateSubaccountBuildsRequest(t *testing.T) {
	var gotQuery url.Values
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	if err := createVoipmsSubaccount(t.Context(), vc, "klanker-pbx", "strong-pass", "203.0.113.5"); err != nil {
		t.Fatalf("createVoipmsSubaccount() error: %v", err)
	}
	if got := gotQuery.Get("method"); got != voipmsMethodCreateSubAccount {
		t.Errorf("method = %q, want %q", got, voipmsMethodCreateSubAccount)
	}
	if got := gotQuery.Get("username"); got != "klanker-pbx" {
		t.Errorf("username param = %q, want %q", got, "klanker-pbx")
	}
	if got := gotQuery.Get("ip_restriction"); got != "203.0.113.5" {
		t.Errorf("ip_restriction param = %q, want %q", got, "203.0.113.5")
	}
	if got := gotQuery.Get("enable_ip_restriction"); got != "1" {
		t.Errorf("enable_ip_restriction param = %q, want %q", got, "1")
	}
	if got := gotQuery.Get("lock_international"); got != "1" {
		t.Errorf("lock_international param = %q, want %q (outbound locked)", got, "1")
	}
	if got := gotQuery.Get("international_route"); got != "0" {
		t.Errorf("international_route param = %q, want %q (outbound disabled)", got, "0")
	}
}

// TestVoipmsCredsFromEnv asserts credentials come ONLY from the environment
// (VOIPMS_API_USERNAME / VOIPMS_API_PASSWORD) — never a CLI flag — and that
// a clear error surfaces when they're unset.
func TestVoipmsCredsFromEnv(t *testing.T) {
	t.Setenv("VOIPMS_API_USERNAME", "")
	t.Setenv("VOIPMS_API_PASSWORD", "")
	if _, err := voipmsCredsFromEnv(); err == nil {
		t.Fatal("voipmsCredsFromEnv() with unset env returned nil error, want an error")
	}

	t.Setenv("VOIPMS_API_USERNAME", "opuser")
	t.Setenv("VOIPMS_API_PASSWORD", "oppass")
	creds, err := voipmsCredsFromEnv()
	if err != nil {
		t.Fatalf("voipmsCredsFromEnv() error: %v", err)
	}
	if creds.Username != "opuser" || creds.Password != "oppass" {
		t.Errorf("creds = %+v, want Username=opuser Password=oppass", creds)
	}
}

// TestVoipmsCmdHelpListsSubcommands asserts `kv voipms` registers the
// balance, route-did, set-caps, and create-subaccount sub-commands.
func TestVoipmsCmdHelpListsSubcommands(t *testing.T) {
	cfg := &Config{}
	voipmsCmd := NewVoipmsCmd(cfg)

	want := map[string]bool{
		"balance":           false,
		"route-did":         false,
		"set-caps":          false,
		"create-subaccount": false,
	}
	for _, sub := range voipmsCmd.Commands() {
		name := sub.Name()
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("kv voipms is missing expected sub-command %q", name)
		}
	}
	if voipmsCmd.Use != "voipms" {
		t.Errorf("Use = %q, want %q", voipmsCmd.Use, "voipms")
	}
}

// TestVoipmsMethodNamesCentralized proves the acceptance criteria
// mechanically: the base URL ("rest.php") appears exactly once in
// voipms.go, in the centralized constants block, and no
// api_password/api_username literal VALUE is assigned anywhere in the file
// (credentials are os.Getenv-only).
func TestVoipmsMethodNamesCentralized(t *testing.T) {
	src, err := os.ReadFile("voipms.go")
	if err != nil {
		t.Fatalf("read voipms.go: %v", err)
	}
	content := string(src)

	if n := strings.Count(content, "rest.php"); n != 1 {
		t.Errorf("rest.php appears %d times in voipms.go, want exactly 1 (centralized base URL)", n)
	}

	// Every VoIP.ms method-name constant must be declared via the
	// voipmsMethod* naming convention in one place.
	for _, name := range []string{
		"voipmsMethodCreateSubAccount",
		"voipmsMethodSetSubAccount",
		"voipmsMethodSetDIDRouting",
		"voipmsMethodGetBalance",
		"voipmsMethodGetServersInfo",
	} {
		if !strings.Contains(content, name) {
			t.Errorf("voipms.go missing expected centralized constant %q", name)
		}
	}

	// No literal credential VALUE assignment (e.g. apiPassword := "abc123"
	// or api_username = "kurt@example.com") — creds must come from
	// os.Getenv only. This regex looks for an assignment of a quoted
	// string literal to an identifier containing api_username/api_password
	// (case-insensitive, underscore or camelCase).
	credAssign := regexp.MustCompile(`(?i)(api[_]?username|api[_]?password)\s*[:=]=?\s*"[^"$]`)
	if credAssign.MatchString(content) {
		t.Error("voipms.go appears to assign a literal credential value — credentials must come from os.Getenv only")
	}
	if !strings.Contains(content, `os.Getenv("VOIPMS_API_USERNAME")`) {
		t.Error("voipms.go does not read VOIPMS_API_USERNAME via os.Getenv")
	}
	if !strings.Contains(content, `os.Getenv("VOIPMS_API_PASSWORD")`) {
		t.Error("voipms.go does not read VOIPMS_API_PASSWORD via os.Getenv")
	}
}

// TestVoipmsRootRegistersCmd asserts NewRootCmd wires kv voipms into the
// command tree (root.go's registration line).
func TestVoipmsRootRegistersCmd(t *testing.T) {
	root := NewRootCmd()
	found := false
	for _, sub := range root.Commands() {
		if sub.Name() == "voipms" {
			found = true
		}
	}
	if !found {
		t.Fatal("kv root command tree is missing the voipms sub-command")
	}
}
