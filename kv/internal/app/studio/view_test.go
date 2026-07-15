package studio

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func goldenInput() AssembleInput {
	return AssembleInput{
		Region:  "us-east-1",
		Profile: "klanker-application",
		Table:   "kmv-auth-electro",
		Codes: []CodeRecord{
			{Code: "defcon34", TierID: "kph-tier", Phone: "+14165551234", PhoneEnabled: true},
			{Code: "walkup-guest", TierID: "pstn-baseline-tier"},
		},
		Tiers: []TierRecord{
			{TierID: "kph-tier", SessionMaxSeconds: 600, PeriodMaxSeconds: 3600, MaxConcurrent: 4},
			{TierID: "pstn-baseline-tier", SessionMaxSeconds: 180, PeriodMaxSeconds: 1800, MaxConcurrent: 1},
		},
		PhoneMappings: []PhoneMappingRecord{
			{Phone: "+14165551234", Code: "defcon34", TierID: "kph-tier", PhoneEnabled: true},
		},
		Manifest: []KnowledgePack{
			{ID: "klanker-maker", SpokenName: "klanker-maker", Pack: "klanker-maker.md", Talkable: true},
		},
		Unlocks: []Unlock{
			{Phrase: "klanker maker", Add: []string{"klanker-maker"}},
		},
		GateMode: "either",
	}
}

func TestAssembleConfig_HappyPath(t *testing.T) {
	view := AssembleConfig(context.Background(), goldenInput())

	if view.Error != nil {
		t.Fatalf("view.Error = %+v, want nil", view.Error)
	}
	if view.Meta.Generator != "kv studio" {
		t.Errorf("Meta.Generator = %q, want %q", view.Meta.Generator, "kv studio")
	}
	if view.Meta.Region != "us-east-1" || view.Meta.Profile != "klanker-application" || view.Meta.Table != "kmv-auth-electro" {
		t.Errorf("Meta = %+v, want the golden input's region/profile/table", view.Meta)
	}
	if view.Meta.ImportedAtMs <= 0 {
		t.Error("Meta.ImportedAtMs is not set")
	}

	if len(view.Rules) != 2 {
		t.Fatalf("len(view.Rules) = %d, want 2", len(view.Rules))
	}

	known := view.Rules[0]
	if known.ID != "defcon34" {
		t.Errorf("Rules[0].ID = %q, want %q", known.ID, "defcon34")
	}
	if known.Who.Type != "known" || len(known.Who.Numbers) != 1 || known.Who.Numbers[0] != "+14165551234" {
		t.Errorf("Rules[0].Who = %+v, want known/+14165551234", known.Who)
	}
	if known.Grant != (GrantSpec{Minutes: 10, PeriodMin: 60, Concurrency: 4, TierID: "kph-tier"}) {
		t.Errorf("Rules[0].Grant = %+v, want the joined kph-tier grant (600s->10min, 3600s->60min)", known.Grant)
	}
	if known.Secret.Mode != "either" || known.Secret.Ref != "/kmv/secrets/use1/telephony/access_pin" {
		t.Errorf("Rules[0].Secret = %+v, want mode=either ref=access_pin", known.Secret)
	}
	if len(known.Unlocks) != 1 || known.Unlocks[0].Phrase != "klanker maker" {
		t.Errorf("Rules[0].Unlocks = %+v, want the golden unlock", known.Unlocks)
	}
	if len(known.Knowledge) != 1 || known.Knowledge[0] != "klanker-maker" {
		t.Errorf("Rules[0].Knowledge = %+v, want [klanker-maker]", known.Knowledge)
	}
	if known.Persona != "concierge" {
		t.Errorf("Rules[0].Persona = %q, want %q", known.Persona, "concierge")
	}

	anyRule := view.Rules[1]
	if anyRule.Who.Type != "any" || len(anyRule.Who.Numbers) != 0 {
		t.Errorf("Rules[1].Who = %+v, want type=any with no numbers (no phone mapping)", anyRule.Who)
	}
	if anyRule.Grant != (GrantSpec{Minutes: 3, PeriodMin: 30, Concurrency: 1, TierID: "pstn-baseline-tier"}) {
		t.Errorf("Rules[1].Grant = %+v, want the joined pstn-baseline-tier grant", anyRule.Grant)
	}

	if len(view.DIDs) != 1 {
		t.Fatalf("len(view.DIDs) = %d, want 1", len(view.DIDs))
	}
	if view.DIDs[0] != (DID{Phone: "+14165551234", Code: "defcon34", TierID: "kph-tier", Enabled: true}) {
		t.Errorf("view.DIDs[0] = %+v, want the golden DID", view.DIDs[0])
	}

	if len(view.Knowledge) != 1 || view.Knowledge[0].UsedByRules != 2 {
		t.Errorf("view.Knowledge = %+v, want UsedByRules=2 (every rule reaches every pack, v1)", view.Knowledge)
	}

	if len(view.Secrets) != 3 {
		t.Fatalf("len(view.Secrets) = %d, want 3", len(view.Secrets))
	}
	for _, s := range view.Secrets {
		if s.Mode != "either" || s.Store != "ssm" {
			t.Errorf("secret %+v, want mode=either store=ssm", s)
		}
	}
}

func TestAssembleConfig_DynamoErrorReturnsBannerNotPartialView(t *testing.T) {
	in := goldenInput()
	in.DynamoErr = errors.New("connect: connection refused")

	view := AssembleConfig(context.Background(), in)

	if view.Error == nil {
		t.Fatal("view.Error = nil, want an ErrorBanner")
	}
	if view.Error.Store != "dynamodb" {
		t.Errorf("view.Error.Store = %q, want %q", view.Error.Store, "dynamodb")
	}
	if view.Error.Region != "us-east-1" || view.Error.Profile != "klanker-application" {
		t.Errorf("view.Error region/profile = %q/%q, want us-east-1/klanker-application", view.Error.Region, view.Error.Profile)
	}
	if !strings.Contains(view.Error.Message, "us-east-1") || !strings.Contains(view.Error.Message, "klanker-application") {
		t.Errorf("view.Error.Message = %q, want it to name the region and profile (spec §8)", view.Error.Message)
	}

	if view.Rules == nil || len(view.Rules) != 0 {
		t.Errorf("view.Rules = %+v, want a non-nil empty slice", view.Rules)
	}
	if view.DIDs == nil || len(view.DIDs) != 0 {
		t.Errorf("view.DIDs = %+v, want a non-nil empty slice", view.DIDs)
	}
	if view.Knowledge == nil || len(view.Knowledge) != 0 {
		t.Errorf("view.Knowledge = %+v, want a non-nil empty slice", view.Knowledge)
	}
	if view.Secrets == nil || len(view.Secrets) != 0 {
		t.Errorf("view.Secrets = %+v, want a non-nil empty slice", view.Secrets)
	}

	// JSON must encode [] for Rules/DIDs, never null (a blank-looking view).
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("json.Marshal(view) error: %v", err)
	}
	for _, want := range []string{`"rules":[]`, `"dids":[]`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("JSON output missing %q: %s", want, raw)
		}
	}
	if strings.Contains(string(raw), `"rules":null`) || strings.Contains(string(raw), `"dids":null`) {
		t.Errorf("JSON output renders rules/dids as null: %s", raw)
	}
}

func TestAssembleConfig_NoPhoneMappingProducesAnyWho(t *testing.T) {
	in := AssembleInput{
		Region:  "us-east-1",
		Profile: "klanker-application",
		Table:   "kmv-auth-electro",
		Codes: []CodeRecord{
			{Code: "walkup-guest", TierID: "pstn-baseline-tier"},
		},
		Tiers: []TierRecord{
			{TierID: "pstn-baseline-tier", SessionMaxSeconds: 180, PeriodMaxSeconds: 1800, MaxConcurrent: 1},
		},
		GateMode: "passphrase",
	}
	view := AssembleConfig(context.Background(), in)
	if len(view.Rules) != 1 {
		t.Fatalf("len(view.Rules) = %d, want 1", len(view.Rules))
	}
	if view.Rules[0].Who.Type != "any" {
		t.Errorf("Who.Type = %q, want %q", view.Rules[0].Who.Type, "any")
	}
	if len(view.Rules[0].Who.Numbers) != 0 {
		t.Errorf("Who.Numbers = %+v, want empty", view.Rules[0].Who.Numbers)
	}
}

func TestAssembleConfig_JSONRoundTripsWithPinnedTags(t *testing.T) {
	view := AssembleConfig(context.Background(), goldenInput())
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("json.Marshal(view) error: %v", err)
	}
	for _, tag := range []string{
		`"who"`, `"numbers"`, `"periodMin"`, `"tierId"`, `"usedByRules"`,
		`"importedAtMs"`, `"spokenName"`, `"phrase"`,
	} {
		if !strings.Contains(string(raw), tag) {
			t.Errorf("JSON output missing pinned tag %s: %s", tag, raw)
		}
	}

	var decoded ConfigView
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal round-trip error: %v", err)
	}
	if len(decoded.Rules) != len(view.Rules) {
		t.Errorf("round-tripped Rules len = %d, want %d", len(decoded.Rules), len(view.Rules))
	}
}

// --------------------------------------------------------------------------
// Plan 16-04: rule-order application, InboundDID merge, compilesTo.

// TestAssembleConfig_AppliesRuleOrder asserts RULE-03's presentation order:
// codes named in RuleOrder come first, in that exact sequence; any code NOT
// named falls back to the original (DynamoDB read) order, appended after.
func TestAssembleConfig_AppliesRuleOrder(t *testing.T) {
	in := goldenInput() // Codes: [defcon34, walkup-guest], in that read order
	in.RuleOrder = []string{"walkup-guest", "defcon34"}

	view := AssembleConfig(context.Background(), in)

	if len(view.Rules) != 2 {
		t.Fatalf("len(view.Rules) = %d, want 2", len(view.Rules))
	}
	if view.Rules[0].ID != "walkup-guest" || view.Rules[1].ID != "defcon34" {
		t.Errorf("view.Rules order = [%s, %s], want [walkup-guest, defcon34] per RuleOrder", view.Rules[0].ID, view.Rules[1].ID)
	}
}

// TestAssembleConfig_RuleOrderIgnoresUnknownAndAppendsMissing asserts a
// stray id in RuleOrder that doesn't match any code is silently ignored,
// and a code NOT named in RuleOrder is appended afterward rather than
// dropped.
func TestAssembleConfig_RuleOrderIgnoresUnknownAndAppendsMissing(t *testing.T) {
	in := goldenInput()
	in.RuleOrder = []string{"no-such-code", "walkup-guest"}

	view := AssembleConfig(context.Background(), in)

	if len(view.Rules) != 2 {
		t.Fatalf("len(view.Rules) = %d, want 2 (unknown order entries dropped, nothing lost)", len(view.Rules))
	}
	if view.Rules[0].ID != "walkup-guest" {
		t.Errorf("view.Rules[0].ID = %q, want %q (the one RuleOrder entry that matched)", view.Rules[0].ID, "walkup-guest")
	}
	if view.Rules[1].ID != "defcon34" {
		t.Errorf("view.Rules[1].ID = %q, want %q (unordered code appended after)", view.Rules[1].ID, "defcon34")
	}
}

// TestAssembleConfig_EmptyRuleOrderIsANoOp asserts an empty/nil RuleOrder
// leaves the DynamoDB read order untouched (the seeded/no-file state).
func TestAssembleConfig_EmptyRuleOrderIsANoOp(t *testing.T) {
	in := goldenInput()
	view := AssembleConfig(context.Background(), in)
	if view.Rules[0].ID != "defcon34" || view.Rules[1].ID != "walkup-guest" {
		t.Errorf("view.Rules order = [%s, %s], want the original DynamoDB read order unchanged", view.Rules[0].ID, view.Rules[1].ID)
	}
}

// TestAssembleConfig_CarriesInboundDIDsThrough asserts AssembleConfig passes
// AssembleInput.InboundDIDs through into the ConfigView unchanged (the
// actual live+metadata merge is server.go's job — see
// TestMergeInboundDIDs_* below for the merge logic itself, and
// server_test.go's TestServer_DID_List_MergesLiveAndMetadata for the full
// HTTP path).
func TestAssembleConfig_CarriesInboundDIDsThrough(t *testing.T) {
	in := goldenInput()
	in.InboundDIDs = []InboundDID{{Did: "+16135550100", DefaultRule: "kph-tier-code"}}

	view := AssembleConfig(context.Background(), in)

	if len(view.InboundDIDs) != 1 || view.InboundDIDs[0].Did != "+16135550100" {
		t.Errorf("view.InboundDIDs = %+v, want the input's InboundDIDs carried through", view.InboundDIDs)
	}
}

// TestAssembleConfig_NilInboundDIDsIsNonNilEmpty asserts the "never nil"
// contract holds for InboundDIDs too, matching Rules/DIDs/Knowledge/Secrets.
func TestAssembleConfig_NilInboundDIDsIsNonNilEmpty(t *testing.T) {
	view := AssembleConfig(context.Background(), goldenInput())
	if view.InboundDIDs == nil {
		t.Error("view.InboundDIDs is nil, want a non-nil (possibly empty) slice")
	}
}

// --------------------------------------------------------------------------
// MergeInboundDIDs (DID-01/02)

func TestMergeInboundDIDs_AttachesMetadataToLiveEntry(t *testing.T) {
	live := []InboundDID{{Did: "+16135550100", Routing: "account:klanker-pbx"}}
	meta := []DIDMeta{{Did: "+16135550100", Label: "Ottawa main", DefaultRule: "kph-tier-code", Greeting: "hi"}}

	merged := MergeInboundDIDs(live, meta)

	if len(merged) != 1 {
		t.Fatalf("len(merged) = %d, want 1", len(merged))
	}
	got := merged[0]
	if got.Routing != "account:klanker-pbx" {
		t.Errorf("merged[0].Routing = %q, want the live value preserved", got.Routing)
	}
	if got.DefaultRule != "kph-tier-code" || got.Greeting != "hi" {
		t.Errorf("merged[0] = %+v, want the metadata's DefaultRule/Greeting attached", got)
	}
	if got.Label != "Ottawa main" {
		t.Errorf("merged[0].Label = %q, want the metadata's label filled in (live had none)", got.Label)
	}
}

func TestMergeInboundDIDs_MetadataOnlyRowIncluded(t *testing.T) {
	// A metadata row for a DID VoIP.ms didn't report this session (e.g.
	// creds absent, or the row was authored ahead of routing) must still
	// surface — never silently dropped.
	meta := []DIDMeta{{Did: "+13475550199", DefaultRule: "greenhouse-code"}}

	merged := MergeInboundDIDs(nil, meta)

	if len(merged) != 1 || merged[0].Did != "+13475550199" {
		t.Fatalf("merged = %+v, want the metadata-only row included", merged)
	}
}

func TestMergeInboundDIDs_LiveEntryWithNoMetadataUnchanged(t *testing.T) {
	live := []InboundDID{{Did: "+19995550100", Routing: "account:klanker-pbx", Label: "from voip.ms"}}

	merged := MergeInboundDIDs(live, nil)

	if len(merged) != 1 {
		t.Fatalf("len(merged) = %d, want 1", len(merged))
	}
	if merged[0].Label != "from voip.ms" || merged[0].DefaultRule != "" {
		t.Errorf("merged[0] = %+v, want the live entry unchanged when no metadata matches", merged[0])
	}
}

// --------------------------------------------------------------------------
// Plan 17-02 Task 1: KnowledgePack.TokenEstimate (KNOW-01)

// TestAssembleConfig_KnowledgeTokenEstimate asserts a pack whose on-disk
// topics/{Pack} file is N bytes reports TokenEstimate ~= N/4, read from
// AssembleInput.Root/knowledgePacksDir/Pack.
func TestAssembleConfig_KnowledgeTokenEstimate(t *testing.T) {
	root := t.TempDir()
	packsDir := filepath.Join(root, knowledgePacksDir)
	if err := os.MkdirAll(packsDir, 0o755); err != nil {
		t.Fatalf("mkdir packs dir: %v", err)
	}
	content := strings.Repeat("token ", 100) // 600 bytes
	if err := os.WriteFile(filepath.Join(packsDir, "klanker-maker.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write pack file: %v", err)
	}

	in := goldenInput()
	in.Root = root

	view := AssembleConfig(context.Background(), in)

	if len(view.Knowledge) != 1 {
		t.Fatalf("len(view.Knowledge) = %d, want 1", len(view.Knowledge))
	}
	want := len(content) / 4
	if got := view.Knowledge[0].TokenEstimate; got != want {
		t.Errorf("view.Knowledge[0].TokenEstimate = %d, want %d (len(content)/4)", got, want)
	}
}

// TestAssembleConfig_KnowledgeTokenEstimateMissingFileIsZero asserts a pack
// whose on-disk file does not exist degrades to TokenEstimate 0 — never an
// error, never a panic (a read-only view must never fail on a not-yet-built
// pack).
func TestAssembleConfig_KnowledgeTokenEstimateMissingFileIsZero(t *testing.T) {
	in := goldenInput()
	in.Root = t.TempDir() // no pack files written under this root

	view := AssembleConfig(context.Background(), in)

	if len(view.Knowledge) != 1 {
		t.Fatalf("len(view.Knowledge) = %d, want 1", len(view.Knowledge))
	}
	if got := view.Knowledge[0].TokenEstimate; got != 0 {
		t.Errorf("view.Knowledge[0].TokenEstimate = %d, want 0 for a missing pack file", got)
	}
}

// TestAssembleConfig_KnowledgeUsedByRulesUnchangedByTokenEstimate asserts
// adding TokenEstimate did not disturb UsedByRules' existing v1 "every rule
// reaches every pack" semantics (len(rules)).
func TestAssembleConfig_KnowledgeUsedByRulesUnchangedByTokenEstimate(t *testing.T) {
	in := goldenInput()
	in.Root = t.TempDir()

	view := AssembleConfig(context.Background(), in)

	if len(view.Knowledge) != 1 || view.Knowledge[0].UsedByRules != len(view.Rules) {
		t.Errorf("view.Knowledge[0].UsedByRules = %d, want %d (len(rules), unchanged)", view.Knowledge[0].UsedByRules, len(view.Rules))
	}
}

// --------------------------------------------------------------------------
// compilesToMap (RULE-05)

// TestCompilesTo_NamesRealStores asserts the compiles-to panel's field->
// store map is populated and every value names one of the real backing
// stores this phase actually writes to — never a placeholder/guess.
func TestCompilesTo_NamesRealStores(t *testing.T) {
	m := compilesToMap()
	if len(m) == 0 {
		t.Fatal("compilesToMap() is empty, want RULE-05's field->store entries")
	}

	wantKeys := []string{
		"rule.who", "rule.grant", "rule.secret.mode", "rule.secret.requireGate",
		"rule.secret.ref", "rule.unlocks", "rule.knowledge", "rule.order",
		"did.defaultRule", "did.greeting", "did.routing",
	}
	for _, k := range wantKeys {
		v, ok := m[k]
		if !ok || v == "" {
			t.Errorf("compilesToMap()[%q] = %q, want a non-empty real-store label", k, v)
		}
	}

	knownStores := []string{"DynamoDB", "TOML", "YAML", "SSM", "VoIP.ms"}
	for field, store := range m {
		matched := false
		for _, known := range knownStores {
			if strings.HasPrefix(store, known) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("compilesToMap()[%q] = %q, does not start with a known real store label (%v)", field, store, knownStores)
		}
	}
}

// TestCompilesTo_PresentOnConfigResponse asserts /api/config's ConfigView
// carries CompilesTo (also exercised end-to-end in
// server_test.go's TestHandler_Config_IncludesCompilesToAndInboundDIDs).
func TestCompilesTo_PresentOnConfigResponse(t *testing.T) {
	view := AssembleConfig(context.Background(), goldenInput())
	if len(view.CompilesTo) == 0 {
		t.Error("view.CompilesTo is empty, want RULE-05's per-field store metadata on every assembled view")
	}
}
