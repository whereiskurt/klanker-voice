package studio

import "context"

// ConfigView is the unified, read-only projection of a live klanker-voice
// configuration — the JSON contract this package assembles from DynamoDB,
// repo config files, and SSM secret names. PINNED shape (spec §5): Phase
// 15-02's server marshals this exact type, Phase 15-03's web UI renders it,
// and Phase 18's SOP snapshot serializes it.
type ConfigView struct {
	Meta      Meta            `json:"meta"`
	Rules     []Rule          `json:"rules"`
	DIDs      []DID           `json:"dids"`
	Knowledge []KnowledgePack `json:"knowledge"`
	Secrets   []SecretRef     `json:"secrets"`
	Error     *ErrorBanner    `json:"error,omitempty"`

	// InboundDIDs is the Plan-04 addition (DID-01/02): the live VoIP.ms
	// inbound-DID list (the numbers the public dials) merged with the
	// studio-owned dids.yaml default-rule/greeting metadata. Distinct from
	// DIDs above (the §23 caller-ID mint mapping, keyed by the CALLER's
	// number). Always a non-nil (possibly empty) slice.
	InboundDIDs []InboundDID `json:"inboundDids"`

	// CompilesTo is RULE-05's per-field backing-store metadata — see
	// compilesToMap() in view.go for the exact field->store enumeration.
	// Always non-nil.
	CompilesTo map[string]string `json:"compilesTo"`
}

// Meta carries the provenance of an assembled ConfigView: which AWS
// region/profile/table it was read from and when.
type Meta struct {
	Region       string `json:"region"`
	Profile      string `json:"profile"`
	Table        string `json:"table"`
	ImportedAtMs int64  `json:"importedAtMs"`
	Generator    string `json:"generator"` // always "kv studio"
}

// Rule is the unified operator concept — "a call from THIS number using THIS
// passphrase gets THIS much time and THIS knowledge" — assembled from the
// AccessCode (DynamoDB), the telephony gate config, and the knowledge/router
// repo files. It is a view object, not a new store (spec §5).
type Rule struct {
	ID        string     `json:"id"`
	Who       WhoSpec    `json:"who"`
	Secret    SecretSpec `json:"secret"`
	Unlocks   []Unlock   `json:"unlocks"`
	Grant     GrantSpec  `json:"grant"`
	Knowledge []string   `json:"knowledge"`
	Persona   string     `json:"persona"`
}

// WhoSpec identifies which caller a rule matches: a known number, any caller
// (no phone mapping), or a blocked number.
type WhoSpec struct {
	Type    string   `json:"type"` // known | any | block
	Numbers []string `json:"numbers"`
}

// SecretSpec names the gate secret a rule requires to unlock, as a
// reference — never a value. Mode mirrors telephony.toml's [telephony]
// gate_mode, which the voice pipeline's config loader accepts ONLY as
// passphrase|dtmf|either (telephony/config.py's ALLOWED_GATE_MODES) — there
// is no "none"/off gate_mode value. "No secret required" is a SEPARATE
// boolean field, telephony.toml's require_gate=false; it is never expressed
// as a fourth gate_mode value (16-RESEARCH.md Pitfall 2 — see
// repofile_writer.go's WriteTelephonyGate).
type SecretSpec struct {
	Mode string `json:"mode"` // passphrase | dtmf | either (no "none" — see require_gate)
	Ref  string `json:"ref"`  // SSM param name
}

// Unlock is one spoken-phrase trigger from the router's topic-map that adds
// knowledge packs to the active conversation.
type Unlock struct {
	Phrase string   `json:"phrase"`
	Add    []string `json:"add"` // knowledge pack ids
}

// GrantSpec is the time/concurrency grant a rule confers, joined from the
// DynamoDB Tier row named by TierID.
type GrantSpec struct {
	Minutes     int64  `json:"minutes"`
	PeriodMin   int64  `json:"periodMin"`
	Concurrency int64  `json:"concurrency"`
	TierID      string `json:"tierId"`
}

// DID is one owned inbound-caller-ID -> access-code mapping (distinct from
// the public dialed-in DIDs; this is the §23 caller-ID mint mapping).
type DID struct {
	Phone   string `json:"phone"`
	Code    string `json:"code"`
	TierID  string `json:"tierId"`
	Enabled bool   `json:"enabled"`
}

// KnowledgePack is a read-only display of one manifest.yaml topic: what the
// router can reach and how many rules can reach it (v1: every rule can reach
// every pack — see AssembleConfig).
type KnowledgePack struct {
	ID          string            `json:"id"`
	SpokenName  string            `json:"spokenName"`
	Pack        string            `json:"pack"`
	Kind        string            `json:"kind"` // reserved: no single top-level kind in manifest.yaml today; unset in v1
	Sources     []KnowledgeSource `json:"sources"`
	UsedByRules int               `json:"usedByRules"`
	Talkable    bool              `json:"talkable"`

	// TokenEstimate is KNOW-01's cheap len(bytes)/4 estimate over the pack's
	// already-on-disk markdown file (knowledgePacksDir/Pack) — never a live
	// Anthropic token-count API call (17-RESEARCH.md Pattern 1 / Assumption
	// A3). 0 when the pack file is missing/unreadable (a not-yet-built
	// pack) — never an error, never a panic. See AssembleConfig's
	// packTokenEstimate.
	TokenEstimate int `json:"tokenEstimate"`
}

// KnowledgeSource is one corpus-provenance entry for a knowledge pack.
type KnowledgeSource struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Public bool   `json:"public"`
}

// SecretRef is a name-only reference to an SSM SecureString param — never a
// value. The secret adapter that produces these MUST NOT call SSM.
type SecretRef struct {
	Name  string `json:"name"`
	Store string `json:"store"` // always "ssm" in v1
	Mode  string `json:"mode"`
}

// ErrorBanner is the structured "can't reach a store" banner the console
// renders instead of a blank/partial view (spec §8).
type ErrorBanner struct {
	Store   string `json:"store"` // dynamodb | ssm
	Region  string `json:"region"`
	Profile string `json:"profile"`
	Message string `json:"message"`
}

// --------------------------------------------------------------------------
// Plan 04: REST write DTOs + the injected VoIP.ms DID router/lister
// interfaces. package studio must not import package cmd (16-RESEARCH.md
// Pitfall 4) — cmd/studio.go builds the concrete DIDRouterAPI/InboundDIDs
// implementations (closing over VoIP.ms helpers) and injects them via
// ServerOptions.

// APIError is the structured JSON error body every write handler returns on
// failure — never a bare 500 with an empty body (spec §8's philosophy,
// extended to the write path).
type APIError struct {
	Error string `json:"error"`
}

// RuleWriteReq is the POST /api/rules request body: create a brand-new rule
// (AccessCode), with an optional phone (the caller-ID WHO match) and an
// optional initial gate mode. GateMode, when set, writes the shared
// [telephony].gate_mode (v1 has exactly one gate config for every rule — see
// SecretSpec's doc comment) via WriteTelephonyGate(gateMode, true).
type RuleWriteReq struct {
	Code     string `json:"code"`
	TierID   string `json:"tierId"`
	Phone    string `json:"phone,omitempty"`
	GateMode string `json:"gateMode,omitempty"`
}

// RuleEditReq is the PUT /api/rules/{code} request body: repoint an existing
// rule's grant at a different tier via the surgical UpdateAccessCodeTier —
// never a PutItem (16-RESEARCH.md Pitfall 1).
type RuleEditReq struct {
	TierID string `json:"tierId"`
}

// OrderWriteReq is the PUT /api/order request body — the full replacement
// rule-authoring display order (RULE-03; presentation only, see
// studio_files.go's WriteRuleOrder doc comment).
type OrderWriteReq struct {
	Order []string `json:"order"`
}

// SecretWriteReq is the PUT /api/secret request body — the gate MODE
// selection (RULE-02's SECRET field) and whether a gate is required at all.
// "No secret required" is expressed as RequireGate:false with GateMode left
// empty (untouched) — there is deliberately no "none" gate_mode value
// (16-RESEARCH.md Pitfall 2).
type SecretWriteReq struct {
	GateMode    string `json:"gateMode,omitempty"`
	RequireGate bool   `json:"requireGate"`
}

// UnlockWriteReq is the PUT /api/unlocks request body — one topic-map
// keyword add/replace/remove (Op: "add" adds or replaces the weight of an
// existing term; "add" is also the default when Op is omitted; "remove"
// deletes it).
type UnlockWriteReq struct {
	TopicID string `json:"topicId"`
	Term    string `json:"term"`
	Weight  int    `json:"weight,omitempty"`
	Op      string `json:"op,omitempty"` // "add" (default) | "remove"
}

// DIDWriteReq is the POST /api/dids / PUT /api/dids/{did} request body — the
// studio-owned dids.yaml metadata fields (DID-02). Did is read from the
// request body on POST and from the path on PUT.
type DIDWriteReq struct {
	Did         string `json:"did,omitempty"`
	Label       string `json:"label,omitempty"`
	Region      string `json:"region,omitempty"`
	DefaultRule string `json:"defaultRule,omitempty"`
	Greeting    string `json:"greeting,omitempty"`
}

// ManifestSourceWriteReq is the POST /api/manifest/sources request body
// (KNOW-02): append a validated source to an existing manifest.yaml topic's
// sources list. There is deliberately no Public field — WriteManifestSource
// always hardcodes public:true (17-RESEARCH.md locked decision: a source
// can never be added as non-public from this console).
type ManifestSourceWriteReq struct {
	TopicID string `json:"topicId"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
}

// RebuildReq is the POST /api/knowledge/rebuild request body (KNOW-03): only
// the two operator-toggleable flags `kv knowledge refresh` itself exposes
// (SkipDistill/DryRun). There is deliberately no Manifest/OutDir field — the
// rebuild handler hardcodes both paths and never accepts a request-supplied
// path (T-17-06). The default (zero value) is the FULL distill pass
// (17-RESEARCH.md Pitfall 6).
type RebuildReq struct {
	SkipDistill bool `json:"skipDistill,omitempty"`
	DryRun      bool `json:"dryRun,omitempty"`
}

// RebuildResult is POST /api/knowledge/rebuild's response body: the
// subprocess outcome plus a read-only git status/diff summary of what
// changed under apps/voice/knowledge — never a commit (D-09 stays a human
// step). Stderr is the subprocess's captured stderr VERBATIM on a non-zero
// exit (17-RESEARCH.md Pitfall 5) — never collapsed to a generic "exit
// status 1".
type RebuildResult struct {
	Success      bool     `json:"success"`
	ExitCode     int      `json:"exitCode"`
	Stdout       string   `json:"stdout"`
	Stderr       string   `json:"stderr"`
	ChangedFiles []string `json:"changedFiles"`
	DiffStat     string   `json:"diffStat,omitempty"`
	Summary      string   `json:"summary"`
}

// InboundDID is the merged view of one inbound DID (DID-01/02): live VoIP.ms
// account state (Did/Routing, and Label/Region when VoIP.ms supplies them)
// merged with the studio-owned dids.yaml metadata (DefaultRule/Greeting, and
// a fallback Label/Region when VoIP.ms doesn't supply one). Distinct from
// the DID type above (the §23 caller-ID mint mapping).
type InboundDID struct {
	Did         string `json:"did"`
	Label       string `json:"label,omitempty"`
	Region      string `json:"region,omitempty"`
	Routing     string `json:"routing,omitempty"`
	DefaultRule string `json:"defaultRule,omitempty"`
	Greeting    string `json:"greeting,omitempty"`
}

// DIDRouterAPI is the injection point for routing an already-owned VoIP.ms
// DID to the PBX subaccount (DID-01 "add" — never number provisioning, see
// 16-RESEARCH.md Pitfall 3). cmd/studio.go supplies an implementation
// closing over routeVoipmsDidToPbx; a nil DIDRouter degrades gracefully —
// POST /api/dids still writes dids.yaml metadata and the response notes
// routing must be done via the CLI (mirrors readInboundDIDs' "never block
// the rest of the console" philosophy).
type DIDRouterAPI interface {
	RouteDID(ctx context.Context, did string) error
}
