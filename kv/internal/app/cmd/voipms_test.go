package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	smithy "github.com/aws/smithy-go"
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
	if got := gotQuery.Get("international_route"); got != "1" {
		t.Errorf("international_route param = %q, want %q (value route, locked)", got, "1")
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

// --------------------------------------------------------------------------
// ListInboundDIDs (getDIDsInfo) — the inbound-DID inventory helper.

// TestListInboundDIDs_ArrayShape asserts the canned array envelope shape
// returns one InboundDIDRecord per entry with fields populated.
func TestListInboundDIDs_ArrayShape(t *testing.T) {
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","dids":[
			{"did":"14165551234","description":"Toronto main line","routing":"sip:klanker-pbx","pop":"Toronto"},
			{"did":"16135555678","description":"Ottawa overflow","routing":"sip:klanker-pbx","pop":"Toronto"}
		]}`))
	})

	got, err := ListInboundDIDs(t.Context(), vc)
	if err != nil {
		t.Fatalf("ListInboundDIDs() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	want := []InboundDIDRecord{
		{DID: "14165551234", Description: "Toronto main line", Routing: "sip:klanker-pbx", POP: "Toronto"},
		{DID: "16135555678", Description: "Ottawa overflow", Routing: "sip:klanker-pbx", POP: "Toronto"},
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

// TestListInboundDIDs_SingleObjectShape asserts the VoIP.ms single-DID quirk
// (dids is a bare object, not an array) yields exactly one record — not
// zero, not a panic.
func TestListInboundDIDs_SingleObjectShape(t *testing.T) {
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","dids":{"did":"14165551234","description":"Only DID","routing":"sip:klanker-pbx","pop":"Toronto"}}`))
	})

	got, err := ListInboundDIDs(t.Context(), vc)
	if err != nil {
		t.Fatalf("ListInboundDIDs() error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	want := InboundDIDRecord{DID: "14165551234", Description: "Only DID", Routing: "sip:klanker-pbx", POP: "Toronto"}
	if got[0] != want {
		t.Errorf("got[0] = %+v, want %+v", got[0], want)
	}
}

// TestListInboundDIDs_RequestShape asserts ListInboundDIDs sends
// method=getDIDsInfo to the injected base URL (mirroring
// TestVoipmsGetBalanceBuildsRequest).
func TestListInboundDIDs_RequestShape(t *testing.T) {
	var gotMethod string
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.URL.Query().Get("method")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","dids":[]}`))
	})

	if _, err := ListInboundDIDs(t.Context(), vc); err != nil {
		t.Fatalf("ListInboundDIDs() error: %v", err)
	}
	if gotMethod != voipmsMethodGetDIDsInfo {
		t.Errorf("method = %q, want %q", gotMethod, voipmsMethodGetDIDsInfo)
	}
	if voipmsMethodGetDIDsInfo != "getDIDsInfo" {
		t.Errorf("voipmsMethodGetDIDsInfo = %q, want %q", voipmsMethodGetDIDsInfo, "getDIDsInfo")
	}
}

// TestListInboundDIDs_MissingDidsKeyIsEmptyNotPanic asserts a missing/oddly
// typed "dids" key degrades to a non-nil empty slice, no error, no panic.
func TestListInboundDIDs_MissingDidsKeyIsEmptyNotPanic(t *testing.T) {
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	got, err := ListInboundDIDs(t.Context(), vc)
	if err != nil {
		t.Fatalf("ListInboundDIDs() error: %v", err)
	}
	if got == nil {
		t.Fatal("ListInboundDIDs() returned nil, want a non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0", len(got))
	}
}

// TestListInboundDIDs_OddlyTypedDidsKeyIsEmptyNotPanic asserts a "dids" key
// of an unexpected type (e.g. a bare string) also degrades safely.
func TestListInboundDIDs_OddlyTypedDidsKeyIsEmptyNotPanic(t *testing.T) {
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","dids":"no DIDs found"}`))
	})

	got, err := ListInboundDIDs(t.Context(), vc)
	if err != nil {
		t.Fatalf("ListInboundDIDs() error: %v", err)
	}
	if got == nil {
		t.Fatal("ListInboundDIDs() returned nil, want a non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0", len(got))
	}
}

// TestListInboundDIDs_FailureStatusIsError asserts a status=failure envelope
// surfaces as a Go error (inherited from do()).
func TestListInboundDIDs_FailureStatusIsError(t *testing.T) {
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"failure","error":"invalid_credentials"}`))
	})

	if _, err := ListInboundDIDs(t.Context(), vc); err == nil {
		t.Fatal("ListInboundDIDs() with status=failure returned nil error, want an error")
	}
}

// --------------------------------------------------------------------------
// Caller-ID prefix tooling (Part B) — setVoipmsDIDPrefix / set-cid-prefix /
// clear-cid-prefix.

// newFakeCidPrefixServer builds a fake VoIP.ms server serving getDIDsInfo
// (snapshot + readback) and setDIDInfo for a single canned DID, branching on
// the `method` query param. It tracks the last setDIDInfo callerid_prefix
// and cnam it received and reflects them back on subsequent getDIDsInfo
// responses (closure state), so setVoipmsDIDPrefix's readback verification
// sees the value it just set. It also returns pointers so the test can
// inspect the captured setDIDInfo query and the getDIDsInfo call count.
func newFakeCidPrefixServer(t *testing.T) (vc *voipmsClient, setDIDInfoQuery *url.Values, getDIDsInfoCalls *int) {
	t.Helper()
	setDIDInfoQuery = &url.Values{}
	getDIDsInfoCalls = new(int)

	// Initial snapshot state: cnam=1 (so forcing to 0 is observable) and a
	// distinctive routing/pop/dialtime/billing_type so preservation is
	// observable. callerid_prefix starts empty and is updated by a
	// setDIDInfo call, closure-captured across requests.
	lastPrefix := ""
	lastCnam := "1"

	vc, _ = newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Query().Get("method")
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case voipmsMethodGetDIDsInfo:
			*getDIDsInfoCalls++
			_, _ = w.Write([]byte(`{"status":"success","dids":{` +
				`"did":"7254043234",` +
				`"description":"Las Vegas",` +
				`"routing":"account:557010_klanker-pbx",` +
				`"pop":"5",` +
				`"dialtime":"60",` +
				`"cnam":"` + lastCnam + `",` +
				`"billing_type":"1",` +
				`"callerid_prefix":"` + lastPrefix + `"}}`))
		case voipmsMethodSetDIDInfo:
			q := r.URL.Query()
			*setDIDInfoQuery = q
			lastPrefix = q.Get("callerid_prefix")
			lastCnam = q.Get("cnam")
			_, _ = w.Write([]byte(`{"status":"success"}`))
		default:
			t.Fatalf("unexpected method %q hit the fake CID-prefix server", method)
		}
	})
	return vc, setDIDInfoQuery, getDIDsInfoCalls
}

// TestVoipmsSetCidPrefix_AssemblesFullSnapshotForcesCnam0 asserts
// setVoipmsDIDPrefix forwards the snapshot preserve fields, forces cnam=0
// even though the snapshot cnam was "1", sets callerid_prefix to the
// requested tag, and runs the readback (>=2 getDIDsInfo calls).
func TestVoipmsSetCidPrefix_AssemblesFullSnapshotForcesCnam0(t *testing.T) {
	vc, setDIDInfoQuery, getDIDsInfoCalls := newFakeCidPrefixServer(t)

	if err := setVoipmsDIDPrefix(t.Context(), vc, "7254043234", "KVD3234"); err != nil {
		t.Fatalf("setVoipmsDIDPrefix() error: %v", err)
	}

	q := *setDIDInfoQuery
	if got := q.Get("method"); got != voipmsMethodSetDIDInfo {
		t.Errorf("setDIDInfo method = %q, want %q", got, voipmsMethodSetDIDInfo)
	}
	if got := q.Get("did"); got != "7254043234" {
		t.Errorf("did param = %q, want %q", got, "7254043234")
	}
	if got := q.Get("routing"); got != "account:557010_klanker-pbx" {
		t.Errorf("routing param = %q, want preserved snapshot value %q", got, "account:557010_klanker-pbx")
	}
	if got := q.Get("pop"); got != "5" {
		t.Errorf("pop param = %q, want preserved snapshot value %q", got, "5")
	}
	if got := q.Get("dialtime"); got != "60" {
		t.Errorf("dialtime param = %q, want preserved snapshot value %q", got, "60")
	}
	if got := q.Get("billing_type"); got != "1" {
		t.Errorf("billing_type param = %q, want preserved snapshot value %q", got, "1")
	}
	if got := q.Get("cnam"); got != "0" {
		t.Errorf("cnam param = %q, want forced %q (snapshot cnam was \"1\")", got, "0")
	}
	if got := q.Get("callerid_prefix"); got != "KVD3234" {
		t.Errorf("callerid_prefix param = %q, want %q", got, "KVD3234")
	}
	if *getDIDsInfoCalls < 2 {
		t.Errorf("getDIDsInfo calls = %d, want >= 2 (snapshot + readback)", *getDIDsInfoCalls)
	}
}

// TestVoipmsClearCidPrefix_EmptiesPrefixPreservesRest asserts
// setVoipmsDIDPrefix(..., "") (the clear-cid-prefix path) sends an empty
// but present callerid_prefix, forces cnam=0, and still preserves
// routing/pop.
func TestVoipmsClearCidPrefix_EmptiesPrefixPreservesRest(t *testing.T) {
	vc, setDIDInfoQuery, _ := newFakeCidPrefixServer(t)

	if err := setVoipmsDIDPrefix(t.Context(), vc, "7254043234", ""); err != nil {
		t.Fatalf("setVoipmsDIDPrefix() error: %v", err)
	}

	q := *setDIDInfoQuery
	if _, present := q["callerid_prefix"]; !present {
		t.Fatal("callerid_prefix param is absent, want it present and empty")
	}
	if got := q.Get("callerid_prefix"); got != "" {
		t.Errorf("callerid_prefix param = %q, want empty", got)
	}
	if got := q.Get("cnam"); got != "0" {
		t.Errorf("cnam param = %q, want forced %q", got, "0")
	}
	if got := q.Get("routing"); got != "account:557010_klanker-pbx" {
		t.Errorf("routing param = %q, want preserved snapshot value %q", got, "account:557010_klanker-pbx")
	}
	if got := q.Get("pop"); got != "5" {
		t.Errorf("pop param = %q, want preserved snapshot value %q", got, "5")
	}
}

// TestGetVoipmsDIDInfo_RejectsBlankDid asserts a blank DID never reaches the
// network layer (mirrors TestVoipmsRouteDidRejectsBlankDid).
func TestGetVoipmsDIDInfo_RejectsBlankDid(t *testing.T) {
	called := false
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	if _, err := getVoipmsDIDInfo(t.Context(), vc, ""); err == nil {
		t.Fatal("getVoipmsDIDInfo(\"\") returned nil error, want an error")
	}
	if called {
		t.Fatal("getVoipmsDIDInfo(\"\") made an HTTP call, want it to reject before the network")
	}
}

// TestGetVoipmsDIDInfo_NotFound asserts a DID absent from the response
// yields a clear not-found error, not a nil map / silent success.
func TestGetVoipmsDIDInfo_NotFound(t *testing.T) {
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","dids":{"did":"14165551234","routing":"sip:klanker-pbx"}}`))
	})
	if _, err := getVoipmsDIDInfo(t.Context(), vc, "7254043234"); err == nil {
		t.Fatal("getVoipmsDIDInfo() for a DID absent from the response returned nil error, want an error")
	}
}

// TestVoipmsDoWithRetry_RetriesTransportFailureThenSucceeds asserts the
// bounded retry wrapper retries a transport-layer failure and succeeds once
// the server recovers, without exceeding the N=3 attempt bound.
func TestVoipmsDoWithRetry_RetriesTransportFailureThenSucceeds(t *testing.T) {
	attempts := 0
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			// Simulate a transient Cloudflare-522-shaped failure: close the
			// connection without a response rather than returning any body.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("ResponseWriter does not support hijacking")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	out, err := voipmsDoWithRetry(t.Context(), vc, "someMethod", url.Values{})
	if err != nil {
		t.Fatalf("voipmsDoWithRetry() error: %v", err)
	}
	if status, _ := out["status"].(string); status != "success" {
		t.Errorf("status = %v, want success", out["status"])
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2 (one transient failure then success)", attempts)
	}
}

// TestVoipmsDoWithRetry_DoesNotRetryStatusError asserts a clean
// *voipmsStatusError (a real API rejection) is returned immediately —
// never retried.
func TestVoipmsDoWithRetry_DoesNotRetryStatusError(t *testing.T) {
	attempts := 0
	vc, _ := newTestVoipmsClient(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"failure","error":"no_did"}`))
	})

	_, err := voipmsDoWithRetry(t.Context(), vc, "someMethod", url.Values{})
	if err == nil {
		t.Fatal("voipmsDoWithRetry() with status=failure returned nil error, want an error")
	}
	var statusErr *voipmsStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("voipmsDoWithRetry() error = %v (%T), want a *voipmsStatusError", err, err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (a clean API rejection must not be retried)", attempts)
	}
}

// TestVoipmsCidPrefixSubcommandsRegistered asserts `kv voipms` registers
// set-cid-prefix and clear-cid-prefix alongside the existing sub-commands.
func TestVoipmsCidPrefixSubcommandsRegistered(t *testing.T) {
	cfg := &Config{}
	voipmsCmd := NewVoipmsCmd(cfg)

	want := map[string]bool{
		"set-cid-prefix":   false,
		"clear-cid-prefix": false,
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
}

// --------------------------------------------------------------------------
// resolveVoipmsCreds — env-first, SSM-fallback credential resolution (D-04).

// TestResolveVoipmsCreds_EnvWins asserts that when both VOIPMS_API_USERNAME
// and VOIPMS_API_PASSWORD are set, resolveVoipmsCreds returns them directly
// and never invokes the SSM factory.
func TestResolveVoipmsCreds_EnvWins(t *testing.T) {
	t.Setenv("VOIPMS_API_USERNAME", "envuser")
	t.Setenv("VOIPMS_API_PASSWORD", "envpass")

	factoryCalled := false
	ssmFactory := func(ctx context.Context) (ssmGetParameterAPI, error) {
		factoryCalled = true
		t.Fatal("ssmFactory should never be invoked when env creds are set")
		return nil, nil
	}

	creds, err := resolveVoipmsCreds(t.Context(), ssmFactory)
	if err != nil {
		t.Fatalf("resolveVoipmsCreds() error: %v", err)
	}
	if creds.Username != "envuser" || creds.Password != "envpass" {
		t.Errorf("creds = %+v, want Username=envuser Password=envpass", creds)
	}
	if factoryCalled {
		t.Error("ssmFactory was called, want env creds to short-circuit SSM")
	}
}

// TestResolveVoipmsCreds_SSMFallbackSuccess asserts that with the env vars
// unset, resolveVoipmsCreds falls back to SSM and returns the canned
// parameter values from the fake.
func TestResolveVoipmsCreds_SSMFallbackSuccess(t *testing.T) {
	t.Setenv("VOIPMS_API_USERNAME", "")
	t.Setenv("VOIPMS_API_PASSWORD", "")

	fake := &fakeSSMGetParameterClient{
		values: map[string]string{
			voipmsUsernameSSMPath: "ssmuser",
			voipmsPasswordSSMPath: "ssmpass",
		},
	}
	ssmFactory := func(ctx context.Context) (ssmGetParameterAPI, error) {
		return fake, nil
	}

	creds, err := resolveVoipmsCreds(t.Context(), ssmFactory)
	if err != nil {
		t.Fatalf("resolveVoipmsCreds() error: %v", err)
	}
	if creds.Username != "ssmuser" || creds.Password != "ssmpass" {
		t.Errorf("creds = %+v, want Username=ssmuser Password=ssmpass", creds)
	}
}

// TestResolveVoipmsCreds_SSMFallbackError asserts that when the env vars are
// unset and SSM errors, resolveVoipmsCreds returns an error that leaks
// neither the canned api_password value nor any raw SSM param value
// (leak guard, T-l0v-01).
func TestResolveVoipmsCreds_SSMFallbackError(t *testing.T) {
	t.Setenv("VOIPMS_API_USERNAME", "")
	t.Setenv("VOIPMS_API_PASSWORD", "")

	fake := &fakeSSMGetParameterClient{
		values: map[string]string{
			voipmsPasswordSSMPath: "super-secret-password",
		},
		errs: map[string]error{
			voipmsUsernameSSMPath: &smithy.GenericAPIError{Code: "AccessDenied", Message: "not authorized"},
		},
	}
	ssmFactory := func(ctx context.Context) (ssmGetParameterAPI, error) {
		return fake, nil
	}

	_, err := resolveVoipmsCreds(t.Context(), ssmFactory)
	if err == nil {
		t.Fatal("resolveVoipmsCreds() with SSM error returned nil error, want an error")
	}
	msg := err.Error()
	if strings.Contains(msg, "super-secret-password") {
		t.Errorf("resolveVoipmsCreds() error leaked the canned api_password value: %q", msg)
	}
	if strings.Contains(msg, voipmsPasswordSSMPath) || strings.Contains(msg, voipmsUsernameSSMPath) {
		t.Errorf("resolveVoipmsCreds() error leaked a raw SSM param path/value: %q", msg)
	}
}
