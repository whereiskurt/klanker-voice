package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// healthyFakeDynamo returns one access code, one tier, and no phone
// mappings — enough for /api/config to assemble a non-empty ConfigView
// without exercising every AssembleConfig field-mapping edge case (that is
// view_test.go's job).
func healthyFakeDynamo(t *testing.T) *fakeDynamoReadAPI {
	t.Helper()
	return &fakeDynamoReadAPI{
		queryResponses: []*dynamodb.QueryOutput{
			// ReadCodes
			{Items: mustMarshalItems(t, []map[string]any{
				{"code": "defcon34", "tierId": "kph-tier", "phone": "+14165551234", "phoneEnabled": true},
			})},
			// ReadTiers
			{Items: mustMarshalItems(t, []map[string]any{
				{"tierId": "kph-tier", "sessionMaxSeconds": int64(600), "periodMaxSeconds": int64(3600), "maxConcurrent": int64(4)},
			})},
		},
		scanResponses: []*dynamodb.ScanOutput{
			// ReadPhoneMappings
			{Items: mustMarshalItems(t, []map[string]any{
				{"phone": "+14165551234", "code": "defcon34", "tierId": "kph-tier", "phoneEnabled": true},
			})},
		},
	}
}

// healthyFakeDynamoTimes is healthyFakeDynamo's repeating-response variant:
// fakeDynamoReadAPI indexes its canned Query/Scan responses by cumulative
// call count for the fake's whole lifetime (dynamo_adapter_test.go), not
// per logical "request" — a test that triggers more than one full
// assembleConfig() pass against the SAME fake (e.g. Plan 18-06's
// save-then-changeset round trip) needs `times` repetitions queued up front
// or a later pass silently sees empty ReadCodes/ReadTiers/ReadPhoneMappings
// results.
func healthyFakeDynamoTimes(t *testing.T, times int) *fakeDynamoReadAPI {
	t.Helper()
	codes := mustMarshalItems(t, []map[string]any{
		{"code": "defcon34", "tierId": "kph-tier", "phone": "+14165551234", "phoneEnabled": true},
	})
	tiers := mustMarshalItems(t, []map[string]any{
		{"tierId": "kph-tier", "sessionMaxSeconds": int64(600), "periodMaxSeconds": int64(3600), "maxConcurrent": int64(4)},
	})
	phones := mustMarshalItems(t, []map[string]any{
		{"phone": "+14165551234", "code": "defcon34", "tierId": "kph-tier", "phoneEnabled": true},
	})
	f := &fakeDynamoReadAPI{}
	for i := 0; i < times; i++ {
		f.queryResponses = append(f.queryResponses,
			&dynamodb.QueryOutput{Items: codes},
			&dynamodb.QueryOutput{Items: tiers},
		)
		f.scanResponses = append(f.scanResponses, &dynamodb.ScanOutput{Items: phones})
	}
	return f
}

func testMeta() Meta {
	return Meta{Region: "us-east-1", Profile: "klanker-application", Table: "kmv-auth-electro"}
}

// emptyRepo points at a directory with none of the three repo config files
// — ReadManifest/ReadTopicMap/ReadTelephonyGate all return a not-found
// RepoFileError, which assembleConfig treats as an empty (not blocking)
// section per its documented best-effort contract.
func emptyRepo(t *testing.T) RepoFiles {
	t.Helper()
	return RepoFiles{Root: t.TempDir()}
}

// --------------------------------------------------------------------------
// Loopback-bind test (T-15-01 / STUD-03).

func TestListen_BindsLoopbackOnly(t *testing.T) {
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   emptyRepo(t),
		Meta:   testMeta(),
		Port:   "0",
	})

	ln, err := s.Listen()
	if err != nil {
		t.Fatalf("Listen() error: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("listener bound to %q, want a 127.0.0.1:<port> address", addr)
	}
	if strings.HasPrefix(addr, "0.0.0.0") {
		t.Errorf("listener bound to %q — MUST NOT bind 0.0.0.0", addr)
	}
}

// --------------------------------------------------------------------------
// /api/health

func TestHandler_Health(t *testing.T) {
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   emptyRepo(t),
		Meta:   testMeta(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status field = %q, want \"ok\"", body["status"])
	}
	if body["region"] != "us-east-1" || body["profile"] != "klanker-application" {
		t.Errorf("body = %+v, want region/profile from Meta", body)
	}
}

// --------------------------------------------------------------------------
// /api/config — happy path

func TestHandler_Config_HealthyDynamo(t *testing.T) {
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   emptyRepo(t),
		Meta:   testMeta(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var view ConfigView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if view.Error != nil {
		t.Fatalf("view.Error = %+v, want nil on a healthy DynamoDB read", view.Error)
	}
	if len(view.Rules) != 1 || view.Rules[0].ID != "defcon34" {
		t.Fatalf("view.Rules = %+v, want one rule for code defcon34", view.Rules)
	}
	if len(view.DIDs) != 1 || view.DIDs[0].Phone != "+14165551234" {
		t.Fatalf("view.DIDs = %+v, want one DID for +14165551234", view.DIDs)
	}
	if view.Meta.Region != "us-east-1" || view.Meta.Profile != "klanker-application" {
		t.Errorf("view.Meta = %+v, want region/profile from Meta", view.Meta)
	}
}

// --------------------------------------------------------------------------
// /api/config — DynamoDB error banner (spec §8 / STUD-03)

func TestHandler_Config_DynamoErrorReturnsBanner(t *testing.T) {
	s := NewServer(ServerOptions{
		Dynamo: &fakeDynamoReadAPI{queryErr: errors.New("access denied")},
		Table:  "kmv-auth-electro",
		Repo:   emptyRepo(t),
		Meta:   testMeta(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	// The DynamoDB error case is the whole point of spec §8: the browser
	// fetch MUST still succeed (never a 500 with an empty body) so the UI's
	// fetch(...).then(resp => resp.json()) path renders the banner.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (error surfaces as a body field, not an HTTP error)", rec.Code)
	}
	var view ConfigView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if view.Error == nil {
		t.Fatal("view.Error = nil, want a populated ErrorBanner")
	}
	if view.Error.Store != "dynamodb" {
		t.Errorf("view.Error.Store = %q, want \"dynamodb\"", view.Error.Store)
	}
	if view.Error.Region != "us-east-1" || view.Error.Profile != "klanker-application" {
		t.Errorf("view.Error = %+v, want region/profile named", view.Error)
	}
	if view.Rules == nil || view.DIDs == nil || view.Knowledge == nil || view.Secrets == nil {
		t.Errorf("view = %+v, want non-nil empty slices on the error path, never nil", view)
	}
}

// --------------------------------------------------------------------------
// GET / serves the embedded console

func TestHandler_Root_ServesEmbeddedIndex(t *testing.T) {
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   emptyRepo(t),
		Meta:   testMeta(),
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "kv studio") {
		t.Errorf("body does not look like the embedded index.html (missing \"kv studio\"): %s", rec.Body.String()[:min(200, rec.Body.Len())])
	}
}

// --------------------------------------------------------------------------
// Serve — graceful shutdown on ctx cancel

func TestServe_ShutsDownOnContextCancel(t *testing.T) {
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   emptyRepo(t),
		Meta:   testMeta(),
		Port:   "0",
	})

	ln, err := s.Listen()
	if err != nil {
		t.Fatalf("Listen() error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(ctx, ln)
	}()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve() error after cancel = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve() did not return after ctx cancel")
	}
}

// ============================================================================
// Plan 16-04: REST write endpoints (RULE-01..05, DID-01/02).

// writableRepo builds a temp repo root seeded with all four Plan-04 repo
// files this file's write-handler tests exercise: telephony.toml and
// topic-map.yaml (golden fixtures with real edge-case content, shared with
// repofile_writer_test.go), dids.yaml and rule-order.yaml (freshly seeded
// empty, mirroring the real repo's shipped state, shared with
// studio_files_test.go's seededDIDsFile/seededRuleOrderFile constants) — one
// consistent RepoFiles every write-handler test in this file can share.
func writableRepo(t *testing.T) RepoFiles {
	t.Helper()
	dir := t.TempDir()

	telephonyGolden, err := os.ReadFile("testdata/telephony.golden.toml")
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	topicMapGolden, err := os.ReadFile("testdata/topic-map.golden.yaml")
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}

	writeFile(t, dir, telephonyConfigPath, string(telephonyGolden))
	writeFile(t, dir, topicMapPath, string(topicMapGolden))
	writeFile(t, dir, studioDIDsPath, seededDIDsFile)
	writeFile(t, dir, ruleOrderPath, seededRuleOrderFile)

	return RepoFiles{Root: dir}
}

// doJSON issues an httptest request against s.Handler() with a JSON-encoded
// body (nil for no body) and decodes the JSON response into out (nil to
// skip decoding), returning the response status code.
func doJSON(t *testing.T, s *Server, method, path string, body any) (status int, raw []byte) {
	t.Helper()
	var reqBody *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reqBody = bytes.NewReader(b)
	} else {
		reqBody = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func decodeAPIError(t *testing.T, raw []byte) APIError {
	t.Helper()
	var apiErr APIError
	if err := json.Unmarshal(raw, &apiErr); err != nil {
		t.Fatalf("decode APIError: %v (body: %s)", err, raw)
	}
	return apiErr
}

// --------------------------------------------------------------------------
// POST /api/rules (RULE-02 create)

func TestServer_RuleCreate_Success(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: fake,
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/rules", RuleWriteReq{Code: "new-code", TierID: "kph-tier"})
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", status, raw)
	}
	if len(fake.putCalls) != 1 {
		t.Fatalf("PutItem called %d times, want 1 (PutAccessCode)", len(fake.putCalls))
	}
	if len(fake.updateCalls) != 0 {
		t.Errorf("UpdateItem called %d times, want 0 (no phone given)", len(fake.updateCalls))
	}
}

func TestServer_RuleCreate_WithPhone_SetsMapping(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: fake,
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/rules", RuleWriteReq{Code: "new-code", TierID: "kph-tier", Phone: "416-555-0100"})
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", status, raw)
	}
	if len(fake.putCalls) != 1 {
		t.Fatalf("PutItem called %d times, want 1", len(fake.putCalls))
	}
	if len(fake.updateCalls) != 1 {
		t.Fatalf("UpdateItem called %d times, want 1 (SetPhoneMapping)", len(fake.updateCalls))
	}
}

func TestServer_RuleCreate_Conflict(t *testing.T) {
	fake := &fakeDynamoWriteAPI{putErr: errors.New("ConditionalCheckFailedException: the conditional request failed")}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: fake,
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/rules", RuleWriteReq{Code: "defcon34", TierID: "kph-tier"})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", status, raw)
	}
	if decodeAPIError(t, raw).Error == "" {
		t.Error("APIError.Error is empty, want a structured message")
	}
}

func TestServer_RuleCreate_InvalidGateModeWritesNothing(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: fake,
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
	})

	status, _ := doJSON(t, s, http.MethodPost, "/api/rules", RuleWriteReq{Code: "new-code", TierID: "kph-tier", GateMode: "none"})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an invalid gate mode", status)
	}
	if len(fake.putCalls) != 0 {
		t.Errorf("PutItem called %d times on invalid gate mode, want 0", len(fake.putCalls))
	}
}

func TestServer_RuleCreate_NoWriterConfigured(t *testing.T) {
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/rules", RuleWriteReq{Code: "new-code", TierID: "kph-tier"})
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when no Writer is configured (body: %s)", status, raw)
	}
}

// TestServer_RuleCreate_TransportErrorReturnsBannerNotBlank exercises the
// OTHER branch of classifyWriteError — a genuine AWS/transport failure that
// is NOT a ConditionalCheckFailedException (every other write-error test in
// this file simulates only the conditional-check/conflict shape). PutItem
// wraps this in "put access code %q: %w" (dynamo_writer.go), so
// errors.Unwrap(err) != nil and classifyWriteError falls through to its
// default 500 branch — proving an unreachable-DynamoDB-style failure still
// reaches the client as a structured, non-empty JSON body (spec §8's "never
// a blank screen, never a stack dump" — 19-CONTEXT.md's resilience
// decision), not a bare 500 with an empty body.
func TestServer_RuleCreate_TransportErrorReturnsBannerNotBlank(t *testing.T) {
	fake := &fakeDynamoWriteAPI{putErr: errors.New("operation error DynamoDB: PutItem, https response error StatusCode: 0, RequestError: send request failed")}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: fake,
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/rules", RuleWriteReq{Code: "new-code", TierID: "kph-tier"})
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for a non-conditional-check AWS failure (body: %s)", status, raw)
	}
	if len(raw) == 0 {
		t.Fatal("response body is empty, want a structured JSON error banner")
	}
	apiErr := decodeAPIError(t, raw)
	if apiErr.Error == "" {
		t.Error("APIError.Error is empty, want a non-blank message the UI can render as a banner")
	}
}

// --------------------------------------------------------------------------
// PUT /api/rules/{code} (RULE-02 edit)

func TestServer_RuleEdit_Success(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: fake,
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPut, "/api/rules/defcon34", RuleEditReq{TierID: "pstn-baseline-tier"})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}
	if len(fake.updateCalls) != 1 {
		t.Fatalf("UpdateItem called %d times, want 1", len(fake.updateCalls))
	}
	call := fake.updateCalls[0]
	if call.UpdateExpression == nil || *call.UpdateExpression != "SET tierId = :t" {
		t.Errorf("UpdateExpression = %v, want the surgical tierId-only SET (never a PutItem)", call.UpdateExpression)
	}
	if len(fake.putCalls) != 0 {
		t.Errorf("PutItem called %d times on an edit, want 0 (Pitfall 1: edits must never PutItem)", len(fake.putCalls))
	}
}

func TestServer_RuleEdit_NotFound(t *testing.T) {
	fake := &fakeDynamoWriteAPI{updateErr: errors.New("ConditionalCheckFailedException: the conditional request failed")}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: fake,
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
	})

	status, _ := doJSON(t, s, http.MethodPut, "/api/rules/ghost-code", RuleEditReq{TierID: "kph-tier"})
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an edit against a non-existent code", status)
	}
}

// --------------------------------------------------------------------------
// DELETE /api/rules/{code} (RULE-02 delete)

func TestServer_RuleDelete_Success(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: fake,
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodDelete, "/api/rules/defcon34", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}
	if len(fake.deleteCalls) != 1 {
		t.Fatalf("DeleteItem called %d times, want 1", len(fake.deleteCalls))
	}
}

func TestServer_RuleDelete_NotFound(t *testing.T) {
	fake := &fakeDynamoWriteAPI{deleteErr: errors.New("ConditionalCheckFailedException: the conditional request failed")}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: fake,
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
	})

	status, _ := doJSON(t, s, http.MethodDelete, "/api/rules/ghost-code", nil)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a delete against a non-existent code", status)
	}
}

// --------------------------------------------------------------------------
// POST /api/rules/{code}/block (RULE-04)

func TestServer_Block_RoutesThroughUpdateAccessCodeTier(t *testing.T) {
	fake := &fakeDynamoWriteAPI{}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: fake,
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/rules/defcon34/block", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}
	if len(fake.putCalls) != 1 {
		t.Fatalf("PutItem called %d times, want 1 (ensureBlockTier's legibility-only PutTier)", len(fake.putCalls))
	}
	if len(fake.updateCalls) != 1 {
		t.Fatalf("UpdateItem called %d times, want 1 (UpdateAccessCodeTier)", len(fake.updateCalls))
	}
	call := fake.updateCalls[0]
	tAV, ok := call.ExpressionAttributeValues[":t"].(*ddbtypes.AttributeValueMemberS)
	if !ok || tAV.Value != blockTierID {
		t.Errorf(":t = %v, want %q", call.ExpressionAttributeValues[":t"], blockTierID)
	}
}

func TestServer_Block_ToleratesTierAlreadyExisting(t *testing.T) {
	// ensureBlockTier's PutTier is a legibility-only convenience call — a
	// ConditionalCheckFailedException (the no-access tier already exists,
	// the common case after the first block) must be swallowed, not
	// surfaced as a failure.
	fake := &fakeDynamoWriteAPI{putErr: errors.New("ConditionalCheckFailedException: the conditional request failed")}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: fake,
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/rules/defcon34/block", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when the block tier already exists (body: %s)", status, raw)
	}
	if len(fake.updateCalls) != 1 {
		t.Fatalf("UpdateItem called %d times, want 1", len(fake.updateCalls))
	}
}

func TestServer_Block_NotFound(t *testing.T) {
	fake := &fakeDynamoWriteAPI{updateErr: errors.New("ConditionalCheckFailedException: the conditional request failed")}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: fake,
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
	})

	status, _ := doJSON(t, s, http.MethodPost, "/api/rules/ghost-code/block", nil)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for blocking a non-existent code", status)
	}
}

// --------------------------------------------------------------------------
// PUT /api/order (RULE-03)

func TestServer_Order_Success(t *testing.T) {
	repo := writableRepo(t)
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPut, "/api/order", OrderWriteReq{Order: []string{"defcon34", "greenhouse-code"}})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}
	got, err := repo.ReadRuleOrder()
	if err != nil {
		t.Fatalf("ReadRuleOrder() error: %v", err)
	}
	want := []string{"defcon34", "greenhouse-code"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ReadRuleOrder() = %v, want %v", got, want)
	}
}

func TestServer_Order_InvalidCodeIDRejected(t *testing.T) {
	repo := writableRepo(t)
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
	})

	status, _ := doJSON(t, s, http.MethodPut, "/api/order", OrderWriteReq{Order: []string{"bad\x00code"}})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a control-character code id", status)
	}
}

// --------------------------------------------------------------------------
// PUT /api/secret (RULE-02 SECRET field: gate mode / require_gate)

func TestServer_Secret_WritesGateMode(t *testing.T) {
	repo := writableRepo(t)
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPut, "/api/secret", SecretWriteReq{GateMode: "dtmf", RequireGate: true})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}
	got, err := repo.ReadTelephonyGate()
	if err != nil {
		t.Fatalf("ReadTelephonyGate() error: %v", err)
	}
	if got != "dtmf" {
		t.Errorf("ReadTelephonyGate() = %q, want %q", got, "dtmf")
	}
}

func TestServer_Secret_RejectsInvalidGateModeWritesNothing(t *testing.T) {
	repo := writableRepo(t)
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
	})

	before, err := repo.ReadTelephonyGate()
	if err != nil {
		t.Fatalf("ReadTelephonyGate() error: %v", err)
	}

	status, _ := doJSON(t, s, http.MethodPut, "/api/secret", SecretWriteReq{GateMode: "none", RequireGate: true})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for gate_mode=\"none\" (16-RESEARCH.md Pitfall 2)", status)
	}

	after, err := repo.ReadTelephonyGate()
	if err != nil {
		t.Fatalf("ReadTelephonyGate() error: %v", err)
	}
	if after != before {
		t.Errorf("ReadTelephonyGate() changed from %q to %q on a rejected write, want unchanged", before, after)
	}
}

// --------------------------------------------------------------------------
// PUT /api/unlocks (spoken-unlock keyword edits)

func TestServer_Unlocks_AddsKeyword(t *testing.T) {
	repo := writableRepo(t)
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPut, "/api/unlocks", UnlockWriteReq{TopicID: "tiogo", Term: "tenable io", Weight: 2, Op: "add"})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}
	unlocks, err := repo.ReadTopicMap()
	if err != nil {
		t.Fatalf("ReadTopicMap() error: %v", err)
	}
	found := false
	for _, u := range unlocks {
		if u.Phrase == "tenable io" {
			found = true
		}
	}
	if !found {
		t.Errorf("ReadTopicMap() = %+v, want it to include the newly added \"tenable io\" term", unlocks)
	}
}

func TestServer_Unlocks_InvalidOpRejected(t *testing.T) {
	repo := writableRepo(t)
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
	})

	status, _ := doJSON(t, s, http.MethodPut, "/api/unlocks", UnlockWriteReq{TopicID: "tiogo", Term: "x", Op: "frobnicate"})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unrecognized op", status)
	}
}

// --------------------------------------------------------------------------
// GET/POST/PUT /api/dids (DID-01/02)

// fakeDIDRouter implements DIDRouterAPI over an in-memory recorder.
type fakeDIDRouter struct {
	calls []string
	err   error
}

func (f *fakeDIDRouter) RouteDID(ctx context.Context, did string) error {
	f.calls = append(f.calls, did)
	return f.err
}

func TestServer_DID_List_MergesLiveAndMetadata(t *testing.T) {
	repo := writableRepo(t)
	if err := repo.WriteDIDMeta(DIDMeta{Did: "+16135550100", DefaultRule: "kph-tier-code", Greeting: "hello there"}); err != nil {
		t.Fatalf("seed WriteDIDMeta() error: %v", err)
	}

	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
		InboundDIDs: func(ctx context.Context) ([]InboundDID, error) {
			return []InboundDID{{Did: "+16135550100", Routing: "account:klanker-pbx"}}, nil
		},
	})

	status, raw := doJSON(t, s, http.MethodGet, "/api/dids", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}
	var body struct {
		DIDs   []InboundDID `json:"dids"`
		Status string       `json:"status"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "" {
		t.Errorf("Status = %q, want empty on a successful merge", body.Status)
	}
	if len(body.DIDs) != 1 {
		t.Fatalf("len(DIDs) = %d, want 1", len(body.DIDs))
	}
	if body.DIDs[0].DefaultRule != "kph-tier-code" || body.DIDs[0].Greeting != "hello there" {
		t.Errorf("DIDs[0] = %+v, want the merged metadata fields present", body.DIDs[0])
	}
	if body.DIDs[0].Routing != "account:klanker-pbx" {
		t.Errorf("DIDs[0].Routing = %q, want the live routing value preserved", body.DIDs[0].Routing)
	}
}

func TestServer_DID_List_DegradesWhenNoLister(t *testing.T) {
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
		// InboundDIDs left nil — VoIP.ms creds unavailable.
	})

	status, raw := doJSON(t, s, http.MethodGet, "/api/dids", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 even when no lister is configured (body: %s)", status, raw)
	}
	var body struct {
		DIDs   []InboundDID `json:"dids"`
		Status string       `json:"status"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status == "" {
		t.Error("Status is empty, want a degradation note when no lister is configured")
	}
	if body.DIDs == nil {
		t.Error("DIDs is nil, want a non-nil (possibly empty) slice")
	}
}

func TestServer_DID_Add_RoutesThenWritesMetadata(t *testing.T) {
	repo := writableRepo(t)
	router := &fakeDIDRouter{}
	s := NewServer(ServerOptions{
		Dynamo:    healthyFakeDynamo(t),
		Table:     "kmv-auth-electro",
		Repo:      repo,
		Meta:      testMeta(),
		DIDRouter: router,
	})

	req := DIDWriteReq{Did: "+16135550199", Label: "Ottawa 2nd line", DefaultRule: "kph-tier-code", Greeting: "hi"}
	status, raw := doJSON(t, s, http.MethodPost, "/api/dids", req)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", status, raw)
	}
	if len(router.calls) != 1 || router.calls[0] != "+16135550199" {
		t.Fatalf("DIDRouter.RouteDID calls = %v, want exactly [\"+16135550199\"]", router.calls)
	}
	metas, err := repo.ReadDIDMeta()
	if err != nil {
		t.Fatalf("ReadDIDMeta() error: %v", err)
	}
	found := false
	for _, m := range metas {
		if m.Did == "+16135550199" && m.DefaultRule == "kph-tier-code" {
			found = true
		}
	}
	if !found {
		t.Errorf("ReadDIDMeta() = %+v, want the newly added DID's metadata present", metas)
	}
}

func TestServer_DID_Add_NilRouterStillWritesMetadataWithNote(t *testing.T) {
	repo := writableRepo(t)
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
		// DIDRouter left nil.
	})

	req := DIDWriteReq{Did: "+16135550199", DefaultRule: "kph-tier-code"}
	status, raw := doJSON(t, s, http.MethodPost, "/api/dids", req)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201 even with no router configured (body: %s)", status, raw)
	}
	var body struct {
		RoutingNote string `json:"routingNote"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.RoutingNote == "" {
		t.Error("routingNote is empty, want a note explaining routing must be done via the CLI")
	}
	metas, err := repo.ReadDIDMeta()
	if err != nil {
		t.Fatalf("ReadDIDMeta() error: %v", err)
	}
	found := false
	for _, m := range metas {
		if m.Did == "+16135550199" {
			found = true
		}
	}
	if !found {
		t.Error("metadata was not written despite a nil DIDRouter")
	}
}

func TestServer_DID_Add_RouterErrorSkipsMetadataWrite(t *testing.T) {
	repo := writableRepo(t)
	router := &fakeDIDRouter{err: errors.New("voip.ms method setDIDRouting returned status \"failure\"")}
	s := NewServer(ServerOptions{
		Dynamo:    healthyFakeDynamo(t),
		Table:     "kmv-auth-electro",
		Repo:      repo,
		Meta:      testMeta(),
		DIDRouter: router,
	})

	status, _ := doJSON(t, s, http.MethodPost, "/api/dids", DIDWriteReq{Did: "+16135550199"})
	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 when the injected router fails", status)
	}
	metas, err := repo.ReadDIDMeta()
	if err != nil {
		t.Fatalf("ReadDIDMeta() error: %v", err)
	}
	for _, m := range metas {
		if m.Did == "+16135550199" {
			t.Error("metadata was written despite the router failing — add should not partially succeed")
		}
	}
}

func TestServer_DID_Edit_UpsertsMetadataOnly(t *testing.T) {
	repo := writableRepo(t)
	router := &fakeDIDRouter{}
	s := NewServer(ServerOptions{
		Dynamo:    healthyFakeDynamo(t),
		Table:     "kmv-auth-electro",
		Repo:      repo,
		Meta:      testMeta(),
		DIDRouter: router,
	})

	req := DIDWriteReq{DefaultRule: "greenhouse-code", Greeting: "updated greeting"}
	status, raw := doJSON(t, s, http.MethodPut, "/api/dids/+16135550100", req)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}
	if len(router.calls) != 0 {
		t.Errorf("DIDRouter.RouteDID called %d times on an edit, want 0 (edit never routes)", len(router.calls))
	}
	metas, err := repo.ReadDIDMeta()
	if err != nil {
		t.Fatalf("ReadDIDMeta() error: %v", err)
	}
	found := false
	for _, m := range metas {
		if m.Did == "+16135550100" && m.DefaultRule == "greenhouse-code" && m.Greeting == "updated greeting" {
			found = true
		}
	}
	if !found {
		t.Errorf("ReadDIDMeta() = %+v, want +16135550100's metadata updated in place", metas)
	}
}

// --------------------------------------------------------------------------
// GET /api/config — Plan 04's order-aware + DID-merged + compilesTo
// additions, exercised through the full HTTP path (view_test.go covers
// AssembleConfig directly).

func TestHandler_Config_IncludesCompilesToAndInboundDIDs(t *testing.T) {
	repo := writableRepo(t)
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
		InboundDIDs: func(ctx context.Context) ([]InboundDID, error) {
			return []InboundDID{{Did: "+16135550100"}}, nil
		},
	})

	status, raw := doJSON(t, s, http.MethodGet, "/api/config", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}
	var view ConfigView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(view.CompilesTo) == 0 {
		t.Error("view.CompilesTo is empty, want RULE-05's per-field store metadata")
	}
	if len(view.InboundDIDs) != 1 || view.InboundDIDs[0].Did != "+16135550100" {
		t.Errorf("view.InboundDIDs = %+v, want the injected live DID", view.InboundDIDs)
	}
}

// ============================================================================
// Plan 16-04 Task 3: no-secret-value write guard + loopback-bind
// re-assertion (V6 gate).

// noSecretWriteScanFiles are every Phase-16/17 studio source file this
// task's gate covers: the two Phase-16 write-primitive files, the
// studio-owned-file reader/writer, this package's HTTP handler file
// (server.go, which this plan extends with the new secret reveal/rotate
// endpoints), and secret_adapter.go (Phase 17 narrows, not deletes, the
// "names-only, zero SSM calls" prohibition — secret_adapter.go must stay
// token-free even though secret_reveal.go/secret_rotate.go, deliberately
// NOT in this list, now hold the package's only decrypt/write calls).
var noSecretWriteScanFiles = []string{
	"dynamo_writer.go",
	"repofile_writer.go",
	"studio_files.go",
	"server.go",
	"secret_adapter.go",
}

// noSecretWriteBannedTokens are literal Go identifiers that would indicate a
// secret VALUE read/write/decrypt call reached one of noSecretWriteScanFiles
// — the same convention secret_adapter.go's package doc comment documents
// (its own grep gate), expressed here as a table-driven Go test per this
// task's action text.
var noSecretWriteBannedTokens = []string{
	"PutParameter",
	"WithDecryption",
	"kms:Decrypt",
	"ssm.Decrypt",
	"DecryptParameter",
}

// TestNoSecretWrites_Phase16Files asserts none of noSecretWriteScanFiles
// reference an SSM PutParameter / decrypt-capable / KMS-decrypt token —
// this phase writes zero secret VALUES (T-16-15). Comment-only lines are
// filtered out so a doc comment explaining what NOT to do (like this test's
// own doc comments) never self-trips the gate.
func TestNoSecretWrites_Phase16Files(t *testing.T) {
	for _, filename := range noSecretWriteScanFiles {
		src, err := os.ReadFile(filename)
		if err != nil {
			t.Fatalf("read %s: %v", filename, err)
		}
		lineNo := 0
		for line := range strings.SplitSeq(string(src), "\n") {
			lineNo++
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for _, token := range noSecretWriteBannedTokens {
				if strings.Contains(line, token) {
					t.Errorf("%s:%d contains banned token %q (possible secret-value read/write): %s", filename, lineNo, token, trimmed)
				}
			}
		}
	}
}

// ============================================================================
// Plan 17-01 Task 3: two-new-file token-discipline + assemble-stays-
// decryption-free regression gates (T-17-01/T-17-04).

// TestSecretFiles_TokenDiscipline is the mirrored, opposite-direction
// companion to TestNoSecretWrites_Phase16Files: secret_reveal.go must
// contain exactly one actual decrypt-capable call site ("WithDecryption:",
// the SecureString-decrypt field assignment) and secret_rotate.go exactly
// one actual write call site (".PutParameter(", the invocation itself —
// distinct from the SSMRotateAPI interface's PutParameter method
// declaration and the ssm.PutParameterInput/Output type names, both of
// which legitimately mention the word "PutParameter" without being a call).
// Comment-only lines are filtered out, matching
// TestNoSecretWrites_Phase16Files' convention. Also re-confirms (redundant
// with, but explicit per 17-01-PLAN.md Task 3's action text) that neither
// token appears anywhere in server.go/secret_adapter.go.
func TestSecretFiles_TokenDiscipline(t *testing.T) {
	countNonCommentOccurrences := func(t *testing.T, filename, token string) int {
		t.Helper()
		src, err := os.ReadFile(filename)
		if err != nil {
			t.Fatalf("read %s: %v", filename, err)
		}
		count := 0
		for line := range strings.SplitSeq(string(src), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			count += strings.Count(line, token)
		}
		return count
	}

	if got := countNonCommentOccurrences(t, "secret_reveal.go", "WithDecryption:"); got != 1 {
		t.Errorf("secret_reveal.go contains %d non-comment \"WithDecryption:\" call sites, want exactly 1", got)
	}
	if got := countNonCommentOccurrences(t, "secret_rotate.go", ".PutParameter("); got != 1 {
		t.Errorf("secret_rotate.go contains %d non-comment \".PutParameter(\" call sites, want exactly 1", got)
	}

	for _, filename := range []string{"server.go", "secret_adapter.go"} {
		if got := countNonCommentOccurrences(t, filename, "WithDecryption"); got != 0 {
			t.Errorf("%s contains %d non-comment \"WithDecryption\" mentions, want 0", filename, got)
		}
		if got := countNonCommentOccurrences(t, filename, "PutParameter"); got != 0 {
			t.Errorf("%s contains %d non-comment \"PutParameter\" mentions, want 0", filename, got)
		}
	}
}

// fakeSSMSecretAPI implements SSMSecretAPI recording every call it receives
// — used both to prove GET /api/config never reaches SSM
// (TestAssembleConfig_NoDecryption) and to exercise the reveal/rotate REST
// routes end to end (TestHandler_SecretReveal*/TestHandler_SecretRotate*)
// without wiring a real SSM client. getValue configures GetParameter's
// decrypted value; putErr, when set, makes PutParameter fail.
type fakeSSMSecretAPI struct {
	getCalls      int
	describeCalls int
	putCalls      int
	getValue      string
	putErr        error
}

func (f *fakeSSMSecretAPI) GetParameter(ctx context.Context, params *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	f.getCalls++
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: &f.getValue}}, nil
}

func (f *fakeSSMSecretAPI) DescribeParameters(ctx context.Context, params *ssm.DescribeParametersInput, optFns ...func(*ssm.Options)) (*ssm.DescribeParametersOutput, error) {
	f.describeCalls++
	return &ssm.DescribeParametersOutput{}, nil
}

func (f *fakeSSMSecretAPI) PutParameter(ctx context.Context, params *ssm.PutParameterInput, optFns ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	f.putCalls++
	if f.putErr != nil {
		return nil, f.putErr
	}
	return &ssm.PutParameterOutput{}, nil
}

// TestAssembleConfig_NoDecryption asserts GET /api/config makes zero SSM
// calls of any kind (GetParameter, DescribeParameters, PutParameter) even
// when ServerOptions.SSM is populated with a live-looking client — the
// Phase-15 "assemble path does zero decryption" prohibition, narrowed by
// Phase 17 to explicitly cover the new SSM injection point rather than
// deleted by it (T-17-04).
func TestAssembleConfig_NoDecryption(t *testing.T) {
	fakeSSM := &fakeSSMSecretAPI{}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   emptyRepo(t),
		Meta:   testMeta(),
		SSM:    fakeSSM,
	})

	status, raw := doJSON(t, s, http.MethodGet, "/api/config", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}

	if fakeSSM.getCalls != 0 {
		t.Errorf("fakeSSM.getCalls = %d, want 0 — /api/config must never call GetParameter", fakeSSM.getCalls)
	}
	if fakeSSM.describeCalls != 0 {
		t.Errorf("fakeSSM.describeCalls = %d, want 0 — /api/config must never call DescribeParameters", fakeSSM.describeCalls)
	}
	if fakeSSM.putCalls != 0 {
		t.Errorf("fakeSSM.putCalls = %d, want 0 — /api/config must never call PutParameter", fakeSSM.putCalls)
	}
}

// TestHandler_SecretReveal_ReachableOverREST confirms POST /api/secret/reveal
// is wired onto the studio mux end to end: an allow-listed name decrypts via
// the injected fake SSM client and the value comes back in the JSON body
// exactly once.
func TestHandler_SecretReveal_ReachableOverREST(t *testing.T) {
	fakeSSM := &fakeSSMSecretAPI{getValue: "sentinel-revealed-value"}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   emptyRepo(t),
		Meta:   testMeta(),
		SSM:    fakeSSM,
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/secret/reveal", SecretRevealReq{Name: telephonyAccessPinParam})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}
	var resp SecretRevealResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp.Value != "sentinel-revealed-value" || !resp.Ephemeral {
		t.Errorf("resp = %+v, want Value=sentinel-revealed-value Ephemeral=true", resp)
	}
	if fakeSSM.getCalls != 1 {
		t.Errorf("fakeSSM.getCalls = %d, want 1", fakeSSM.getCalls)
	}
}

// TestHandler_SecretReveal_RejectsNonAllowlistedName confirms the allow-list
// check happens at the REST layer too (via RevealSecret) — a 400, zero AWS
// calls.
func TestHandler_SecretReveal_RejectsNonAllowlistedName(t *testing.T) {
	fakeSSM := &fakeSSMSecretAPI{}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   emptyRepo(t),
		Meta:   testMeta(),
		SSM:    fakeSSM,
	})

	status, _ := doJSON(t, s, http.MethodPost, "/api/secret/reveal", SecretRevealReq{Name: "/kmv/secrets/use1/jwt/signing_key"})
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if fakeSSM.getCalls != 0 {
		t.Errorf("fakeSSM.getCalls = %d, want 0", fakeSSM.getCalls)
	}
}

// TestHandler_SecretReveal_NilSSM_Returns500NotPanic confirms nil-safety —
// mirrors errWriterNotConfigured's pattern for the write handlers.
func TestHandler_SecretReveal_NilSSM_Returns500NotPanic(t *testing.T) {
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   emptyRepo(t),
		Meta:   testMeta(),
	})

	status, _ := doJSON(t, s, http.MethodPost, "/api/secret/reveal", SecretRevealReq{Name: telephonyAccessPinParam})
	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", status)
	}
}

// TestHandler_SecretRotate_ReachableOverREST confirms POST /api/secret/rotate
// is wired onto the studio mux end to end and never echoes the new value
// back in the response body.
func TestHandler_SecretRotate_ReachableOverREST(t *testing.T) {
	fakeSSM := &fakeSSMSecretAPI{}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   emptyRepo(t),
		Meta:   testMeta(),
		SSM:    fakeSSM,
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/secret/rotate", SecretRotateReq{Name: telephonyPassphraseWordsParam, NewValue: "brand-new-words"})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}
	var resp SecretRotateResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !resp.Rotated || strings.Contains(string(raw), "brand-new-words") {
		t.Errorf("body = %s, want {rotated:true} and NEVER the new value echoed back", raw)
	}
	if fakeSSM.putCalls != 1 {
		t.Errorf("fakeSSM.putCalls = %d, want 1", fakeSSM.putCalls)
	}
}

// TestHandler_SecretRotate_RejectsNonAllowlistedName mirrors the reveal
// handler's allow-list rejection test.
func TestHandler_SecretRotate_RejectsNonAllowlistedName(t *testing.T) {
	fakeSSM := &fakeSSMSecretAPI{}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   emptyRepo(t),
		Meta:   testMeta(),
		SSM:    fakeSSM,
	})

	status, _ := doJSON(t, s, http.MethodPost, "/api/secret/rotate", SecretRotateReq{Name: "/kmv/secrets/use1/oidc/cookie_key", NewValue: "x"})
	if status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
	if fakeSSM.putCalls != 0 {
		t.Errorf("fakeSSM.putCalls = %d, want 0", fakeSSM.putCalls)
	}
}

// TestHandler_SecretRotate_NilSSM_Returns500NotPanic mirrors
// TestHandler_SecretReveal_NilSSM_Returns500NotPanic for the rotate
// endpoint — the reveal handler's nil-guard already had coverage; the
// rotate handler's identical guard (server.go's registerSecretHandlers) did
// not, until now (19-03-PLAN.md Task 2b).
func TestHandler_SecretRotate_NilSSM_Returns500NotPanic(t *testing.T) {
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   emptyRepo(t),
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/secret/rotate", SecretRotateReq{Name: telephonyAccessPinParam, NewValue: "x"})
	if status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (body: %s)", status, raw)
	}
	if decodeAPIError(t, raw).Error == "" {
		t.Error("APIError.Error is empty, want a structured \"not configured\" message")
	}
}

// --------------------------------------------------------------------------
// POST /api/knowledge/rebuild (KNOW-03) — nil-guard REST coverage.
//
// knowledge_rebuild_test.go already covers KnowledgeRebuildTrigger.Rebuild
// exhaustively at the unit level (single-flight, D-09 never-commits,
// stderr-surfacing, etc.) but no test in this file previously drove the
// actual POST /api/knowledge/rebuild HTTP route at all — server.go's own
// nil-guard (errKnowledgeRebuildNotConfigured, mirroring
// errWriterNotConfigured/errSSMNotConfigured) had zero REST-level coverage
// (19-03-PLAN.md Task 2b).

// TestHandler_KnowledgeRebuild_NilTrigger_Returns500NotPanic confirms a
// server with no KnowledgeRebuild configured (a read-only deployment, or
// cmd/studio.go's wiring simply absent) returns a structured 500 rather
// than dereferencing the nil *KnowledgeRebuildTrigger.
func TestHandler_KnowledgeRebuild_NilTrigger_Returns500NotPanic(t *testing.T) {
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   emptyRepo(t),
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/knowledge/rebuild", RebuildReq{})
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body: %s)", status, raw)
	}
	if decodeAPIError(t, raw).Error == "" {
		t.Error("APIError.Error is empty, want a structured \"not configured\" message")
	}
}

// TestHandler_KnowledgeRebuild_ReachableOverREST confirms the route is
// actually wired end to end (mux.HandleFunc("POST /api/knowledge/rebuild",
// ...)) using an in-memory CommandRunner fake — no real `uv`/git subprocess
// — proving a configured trigger reaches Rebuild and its RebuildResult comes
// back as the response body.
func TestHandler_KnowledgeRebuild_ReachableOverREST(t *testing.T) {
	root := t.TempDir()
	trig := &KnowledgeRebuildTrigger{Runner: &deployFakeRunner{}, Root: root}
	s := NewServer(ServerOptions{
		Dynamo:           healthyFakeDynamo(t),
		Table:            "kmv-auth-electro",
		Repo:             emptyRepo(t),
		Meta:             testMeta(),
		KnowledgeRebuild: trig,
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/knowledge/rebuild", RebuildReq{})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}
	var result RebuildResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !result.Success {
		t.Errorf("result.Success = false, want true (deployFakeRunner returns a zero-value, err-free CommandResult)")
	}
}

// TestLoopbackBind_StillHoldsAfterPhase16Handlers re-asserts T-15-01/T-16-11
// after this plan's new mux handlers are registered: Listen() still binds
// 127.0.0.1 only, and no second net.Listen call site exists in the package
// (loopback is the entire access-control model — no handler in this plan
// opens its own listener/bind).
// ============================================================================
// Plan 18-06: POST /api/sop/save, /api/sop/changeset, /api/sop/deploy
// (SOP-01/02/03).

// gitInitStudioRepo git-inits repo.Root for real (execCommandRunner{} shells
// real git under the SOP handlers — server.go's registerSOPHandlers doc
// comment) — skips the test if git isn't on PATH, mirroring
// TestSaveSOP_ScopedCommit's own skip guard.
func gitInitStudioRepo(t *testing.T, repo RepoFiles) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	runGit(t, repo.Root, "init", "-q")
	runGit(t, repo.Root, "config", "user.email", "test@example.com")
	runGit(t, repo.Root, "config", "user.name", "test")
}

// --------------------------------------------------------------------------
// POST /api/sop/save

func TestServer_SOPSave_Success(t *testing.T) {
	repo := writableRepo(t)
	gitInitStudioRepo(t, repo)
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/sop/save", SOPNameReq{Name: "conference-2026"})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}
	var body struct {
		Name string `json:"name"`
		Sha  string `json:"sha"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Name != "conference-2026" {
		t.Errorf("body.Name = %q, want %q", body.Name, "conference-2026")
	}
	if body.Sha == "" {
		t.Error("body.Sha is empty, want a commit sha")
	}

	if _, err := ReadSOP(repo.Root, "conference-2026"); err != nil {
		t.Errorf("ReadSOP() after save error: %v", err)
	}
}

func TestServer_SOPSave_RejectsPathTraversalName(t *testing.T) {
	repo := writableRepo(t)
	gitInitStudioRepo(t, repo)
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
	})

	status, _ := doJSON(t, s, http.MethodPost, "/api/sop/save", SOPNameReq{Name: "../../etc/passwd"})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a path-traversal name", status)
	}
	if _, err := os.Stat(filepath.Join(repo.Root, "..", "..", "etc", "passwd.yaml")); err == nil {
		t.Fatal("a file was written outside the repo root — path traversal succeeded")
	}
}

// TestServer_SOPSave_RefusesOnPreStagedConflict drives POST /api/sop/save
// against a REAL git repo (execCommandRunner{}) that already has an
// UNRELATED file staged (`git add`ed but not committed) — the "operator has
// an in-progress `git add` outside the console" scenario T-19-06 names.
// gitCommitScoped's post-add staged-subset assertion (sop_git.go) must
// refuse the whole save rather than sweeping that pre-existing staged
// content into the SOP commit — mirroring
// TestGitCommitScoped_RefusesUnexpectedStage's fake-runner-level proof, but
// exercised end to end through the actual REST handler + a real git
// process (19-03-PLAN.md Task 2d / must_have "never auto-forced").
func TestServer_SOPSave_RefusesOnPreStagedConflict(t *testing.T) {
	repo := writableRepo(t)
	gitInitStudioRepo(t, repo)

	conflictPath := filepath.Join(repo.Root, "conflict.txt")
	if err := os.WriteFile(conflictPath, []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write conflict seed file: %v", err)
	}
	runGit(t, repo.Root, "add", "conflict.txt")
	runGit(t, repo.Root, "commit", "-q", "-m", "seed")

	// Dirty conflict.txt and STAGE it (not commit) — a pre-existing staged
	// change unrelated to this Save-as-SOP call.
	if err := os.WriteFile(conflictPath, []byte("staged in-progress edit\n"), 0o644); err != nil {
		t.Fatalf("dirty conflict file: %v", err)
	}
	runGit(t, repo.Root, "add", "conflict.txt")

	beforeLog := runGitOutput(t, repo.Root, "log", "--oneline")

	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/sop/save", SOPNameReq{Name: "conference-2026"})
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — a pre-staged unrelated file must refuse the commit, never auto-force it through (body: %s)", status, raw)
	}
	if decodeAPIError(t, raw).Error == "" {
		t.Error("APIError.Error is empty, want a structured refusal message")
	}

	// No new commit landed — the refusal must be a true no-op on git
	// history, not a partial/forced commit.
	afterLog := runGitOutput(t, repo.Root, "log", "--oneline")
	if afterLog != beforeLog {
		t.Errorf("git log changed after a refused save: before=%q after=%q", beforeLog, afterLog)
	}

	// The conflicting file's CONTENT is never touched by the refusal path —
	// only git's index bookkeeping is reset, never a working-tree write.
	content, err := os.ReadFile(conflictPath)
	if err != nil {
		t.Fatalf("read conflict.txt after refusal: %v", err)
	}
	if string(content) != "staged in-progress edit\n" {
		t.Errorf("conflict.txt content = %q, want the operator's edit left untouched", content)
	}
}

// --------------------------------------------------------------------------
// POST /api/sop/changeset

func TestServer_SOPChangeset_ComparesAgainstFreshLive(t *testing.T) {
	repo := writableRepo(t)
	gitInitStudioRepo(t, repo)
	// Two full assembleConfig passes happen in this test (one inside the
	// save handler, one inside the changeset handler) — healthyFakeDynamo's
	// fakeDynamoReadAPI indexes its canned responses by CUMULATIVE call
	// count across the whole fake's lifetime (dynamo_adapter_test.go), so a
	// single-response fake would starve the second assembleConfig call.
	// healthyFakeDynamoTimes(t, 2) supplies enough responses for both.
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamoTimes(t, 2),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
	})

	// Save-as-SOP first, capturing the CURRENT live view (one rule,
	// defcon34, from healthyFakeDynamo).
	status, _ := doJSON(t, s, http.MethodPost, "/api/sop/save", SOPNameReq{Name: "baseline"})
	if status != http.StatusOK {
		t.Fatalf("save status = %d, want 200", status)
	}

	// An unchanged SOP vs the SAME live view yields an empty changeset.
	status, raw := doJSON(t, s, http.MethodPost, "/api/sop/changeset", SOPNameReq{Name: "baseline"})
	if status != http.StatusOK {
		t.Fatalf("changeset status = %d, want 200 (body: %s)", status, raw)
	}
	var changeset []ChangesetEntry
	if err := json.Unmarshal(raw, &changeset); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(changeset) != 0 {
		t.Errorf("changeset = %+v, want empty (SOP was just saved from this exact live view)", changeset)
	}
}

func TestServer_SOPChangeset_UnknownNameReturns500(t *testing.T) {
	repo := writableRepo(t)
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/sop/changeset", SOPNameReq{Name: "does-not-exist"})
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for an unknown SOP name (body: %s)", status, raw)
	}
}

// --------------------------------------------------------------------------
// POST /api/sop/deploy

func TestServer_SOPDeploy_ValidationFailureReturns422NoWrite(t *testing.T) {
	repo := writableRepo(t)
	gitInitStudioRepo(t, repo)
	fake := &fakeDynamoWriteAPI{}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: fake,
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
	})

	doc := SOPDoc{
		Name: "broken",
		Rules: []SOPRule{
			{Code: "orphan-rule", Who: WhoSpec{Type: "any"}, TierID: "missing-tier"},
		},
	}
	if err := WriteSOP(repo.Root, "broken", doc); err != nil {
		t.Fatalf("WriteSOP() error: %v", err)
	}

	status, raw := doJSON(t, s, http.MethodPost, "/api/sop/deploy", SOPNameReq{Name: "broken"})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", status, raw)
	}
	var result DeployResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(result.ValidationErrors) == 0 {
		t.Fatal("result.ValidationErrors is empty, want the orphan-tier failure")
	}
	if len(fake.putCalls) != 0 || len(fake.updateCalls) != 0 || len(fake.deleteCalls) != 0 {
		t.Errorf("fake writer recorded a call, want none — a validation failure must write nothing")
	}
}

func TestServer_SOPDeploy_Success(t *testing.T) {
	repo := writableRepo(t)
	gitInitStudioRepo(t, repo)
	fake := &fakeDynamoWriteAPI{}
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: fake,
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
	})

	// "new-tier" is deliberately distinct from healthyFakeDynamo's seeded
	// "kph-tier" (same id+limits would make diffTiers see it as unchanged,
	// producing a "changed"/no-op-if-identical entry instead of "added" —
	// this test wants a clean, unambiguous PutTier+PutAccessCode pair).
	doc := SOPDoc{
		Name: "conference-2026",
		Rules: []SOPRule{
			{Code: "new-guest", TierID: "new-tier", Who: WhoSpec{Type: "any"}},
		},
		Tiers: []SOPTier{
			{TierID: "new-tier", SessionMaxSeconds: 600, PeriodMaxSeconds: 3600, MaxConcurrent: 4},
		},
	}
	if err := WriteSOP(repo.Root, "conference-2026", doc); err != nil {
		t.Fatalf("WriteSOP() error: %v", err)
	}

	status, raw := doJSON(t, s, http.MethodPost, "/api/sop/deploy", SOPNameReq{Name: "conference-2026"})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, raw)
	}
	var result DeployResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("result.Error = %q, want empty (failedSurface=%q)", result.Error, result.FailedSurface)
	}
	if len(result.Applied) == 0 {
		t.Error("result.Applied is empty, want the tier+rule \"added\" entries")
	}
	if len(fake.putCalls) != 2 {
		t.Errorf("PutItem called %d times, want 2 (PutTier + PutAccessCode)", len(fake.putCalls))
	}
}

func TestServer_SOPDeploy_NoWriterConfigured(t *testing.T) {
	repo := writableRepo(t)
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Table:  "kmv-auth-electro",
		Repo:   repo,
		Meta:   testMeta(),
	})

	status, raw := doJSON(t, s, http.MethodPost, "/api/sop/deploy", SOPNameReq{Name: "whatever"})
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when no Writer is configured (body: %s)", status, raw)
	}
}

func TestLoopbackBind_StillHoldsAfterPhase16Handlers(t *testing.T) {
	s := NewServer(ServerOptions{
		Dynamo: healthyFakeDynamo(t),
		Writer: &fakeDynamoWriteAPI{},
		Table:  "kmv-auth-electro",
		Repo:   writableRepo(t),
		Meta:   testMeta(),
		Port:   "0",
	})

	ln, err := s.Listen()
	if err != nil {
		t.Fatalf("Listen() error: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Errorf("listener bound to %q, want a 127.0.0.1:<port> address", addr)
	}

	for _, filename := range []string{"server.go"} {
		src, err := os.ReadFile(filename)
		if err != nil {
			t.Fatalf("read %s: %v", filename, err)
		}
		count := strings.Count(string(src), "net.Listen(")
		if count != 1 {
			t.Errorf("%s contains %d net.Listen(...) call site(s), want exactly 1 (the existing Listen() method)", filename, count)
		}
	}
}
