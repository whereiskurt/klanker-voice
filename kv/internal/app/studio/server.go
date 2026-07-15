package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ServerOptions carries everything the studio HTTP server needs: the
// injected DynamoDB read API + table (tests inject an in-memory fake — see
// dynamo_adapter.go's DynamoReadAPI — never a live table), the repo-file
// adapter, the static Meta fields (region/profile/table) stamped onto every
// assembled ConfigView, and the loopback port to bind.
//
// Plan 04 additions — all optional injection points so read-only tests keep
// working unchanged with a zero value: Writer is the DynamoDB write client
// (nil disables every /api/rules write handler with a 500, never a panic);
// DIDRouter/InboundDIDs are the VoIP.ms-backed capabilities cmd/studio.go
// injects (package studio never imports package cmd — 16-RESEARCH.md
// Pitfall 4); either may be nil, in which case the DID handlers degrade
// gracefully (mirrors cmd/telephony.go's readInboundDIDs philosophy).
type ServerOptions struct {
	Dynamo DynamoReadAPI
	Table  string
	Repo   RepoFiles
	Meta   Meta

	// Writer is the write client for /api/rules, /api/rules/{code},
	// DELETE /api/rules/{code}, and POST /api/rules/{code}/block. nil in
	// read-only tests/deployments — every write handler returns a
	// structured 500 rather than a nil-pointer panic.
	Writer DynamoWriteAPI

	// DIDRouter routes an already-owned VoIP.ms DID to the PBX subaccount
	// (DID-01 "add"). nil degrades gracefully: POST /api/dids still writes
	// dids.yaml metadata and the response notes routing must be done via
	// the CLI.
	DIDRouter DIDRouterAPI

	// InboundDIDs lists the live VoIP.ms inbound DIDs. nil (VoIP.ms creds
	// unavailable) degrades to an empty list + a status note, mirroring
	// cmd/telephony.go's readInboundDIDs — GET /api/dids and /api/config's
	// InboundDIDs field never error because this is nil or fails.
	InboundDIDs func(context.Context) ([]InboundDID, error)

	// SSM is the Phase 17 injection point for POST /api/secret/reveal and
	// POST /api/secret/rotate — cmd/studio.go supplies cfg.SSMClient(ctx)
	// (an *ssm.Client, which satisfies SSMSecretAPI structurally). nil in
	// read-only tests/deployments — both handlers return a structured 500
	// (errSSMNotConfigured) rather than a nil-pointer panic, mirroring
	// Writer's nil-safety above.
	SSM SSMSecretAPI

	// KnowledgeRebuild is the injection point for POST /api/knowledge/rebuild
	// (KNOW-03) — knowledge_rebuild.go's NewKnowledgeRebuildTrigger(root)
	// builds the production value; nil degrades to a structured 500
	// (errKnowledgeRebuildNotConfigured) rather than a nil-pointer panic,
	// mirroring Writer/SSM's nil-safety above.
	KnowledgeRebuild *KnowledgeRebuildTrigger

	// Port is the TCP port to bind on 127.0.0.1 — never a full address, so
	// it is structurally impossible for a caller to steer the listener onto
	// a non-loopback interface (T-15-01 / spec §7: no --host flag exists).
	// "0" asks the OS for an ephemeral port, which tests use to avoid
	// colliding on a fixed port.
	Port string
}

// SSMSecretAPI is the union of SSMRevealAPI and SSMRotateAPI (secret_reveal.go
// / secret_rotate.go) — the type ServerOptions.SSM is typed as, since both
// the reveal and rotate handlers reach through the same injected SSM
// client. This composition contains no decrypt/write token literally (it
// only names the two interfaces), keeping server.go clean of the tokens
// TestNoSecretWrites_Phase16Files scans for.
type SSMSecretAPI interface {
	SSMRevealAPI
	SSMRotateAPI
}

// blockTierID is the canonical zero-limit tier RULE-04 "block a number"
// points a code at. Not required to pre-exist (quota.py's read_tier fails
// any UNKNOWN tier id closed to the same 0/0/0 shape) — ensureBlockTier
// still PutTiers it once, ignoring a conditional-exists failure, purely for
// operator legibility in `kv tier list` (16-RESEARCH.md Code Examples).
const blockTierID = "no-access"

// Server is the studio console's local HTTP server: a loopback-bound
// net/http server wrapping the read-only /api/config and /api/health
// endpoints plus the embedded web console.
type Server struct {
	opts ServerOptions
}

// NewServer returns a Server ready to Listen/Serve. It performs no I/O.
func NewServer(opts ServerOptions) *Server {
	return &Server{opts: opts}
}

// Handler builds the studio console's http.Handler: "/" serves the embedded
// web console (Plan 15-03's web/ directory), "GET /api/health" reports
// liveness plus the operator's region/profile, and "GET /api/config"
// assembles + serves the live ConfigView — or, when a DynamoDB read fails,
// a 200 response carrying ConfigView.Error (spec §8: never a 500 with an
// empty body, never a blank page).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	webRoot, err := fs.Sub(WebFS, "web")
	if err != nil {
		// WebFS is a compile-time //go:embed of a directory that ships in
		// this same package (embed.go) — a Sub failure here means the
		// embed itself is broken, not an operator-facing runtime
		// condition, so it is not worth threading through as an error.
		panic("studio: web assets not embedded correctly: " + err.Error())
	}
	mux.Handle("/", http.FileServerFS(webRoot))

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"region":  s.opts.Meta.Region,
			"profile": s.opts.Meta.Profile,
		})
	})

	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		view := s.assembleConfig(r.Context())
		writeJSON(w, http.StatusOK, view)
	})

	s.registerRuleHandlers(mux)
	s.registerDIDHandlers(mux)
	s.registerSecretHandlers(mux)
	s.registerKnowledgeHandlers(mux)
	s.registerSOPHandlers(mux)

	return mux
}

// registerRuleHandlers wires RULE-01..05's write endpoints — create/edit/
// delete/block a rule, reorder the rules table, edit the gate mode, and edit
// a spoken-unlock keyword — onto mux. Every handler validates before it
// hits a store and returns a structured JSON APIError on failure, never a
// bare 500 with an empty body (this task's must_have truth).
func (s *Server) registerRuleHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/rules", func(w http.ResponseWriter, r *http.Request) {
		var req RuleWriteReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		if s.opts.Writer == nil {
			writeError(w, http.StatusInternalServerError, errWriterNotConfigured)
			return
		}
		if req.GateMode != "" && !GateModeAllowed(req.GateMode) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid gate mode %q: must be one of dtmf, passphrase, either", req.GateMode))
			return
		}

		if err := PutAccessCode(r.Context(), s.opts.Writer, s.opts.Table, req.Code, req.TierID, "", nil, nil); err != nil {
			writeError(w, classifyWriteError(err, http.StatusConflict), err)
			return
		}

		if req.Phone != "" {
			normalized, err := normalizeE164(req.Phone)
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			if err := SetPhoneMapping(r.Context(), s.opts.Writer, s.opts.Table, req.Code, normalized); err != nil {
				writeError(w, classifyWriteError(err, http.StatusConflict), err)
				return
			}
		}

		if req.GateMode != "" {
			if err := s.opts.Repo.WriteTelephonyGate(req.GateMode, true); err != nil {
				writeError(w, classifyRepoWriteError(err), err)
				return
			}
		}

		writeJSON(w, http.StatusCreated, map[string]any{"code": req.Code, "tierId": req.TierID})
	})

	mux.HandleFunc("PUT /api/rules/{code}", func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("code")
		var req RuleEditReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		if s.opts.Writer == nil {
			writeError(w, http.StatusInternalServerError, errWriterNotConfigured)
			return
		}
		if err := UpdateAccessCodeTier(r.Context(), s.opts.Writer, s.opts.Table, code, req.TierID); err != nil {
			writeError(w, classifyWriteError(err, http.StatusNotFound), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": code, "tierId": req.TierID})
	})

	mux.HandleFunc("DELETE /api/rules/{code}", func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("code")
		if s.opts.Writer == nil {
			writeError(w, http.StatusInternalServerError, errWriterNotConfigured)
			return
		}
		if err := DeleteAccessCode(r.Context(), s.opts.Writer, s.opts.Table, code); err != nil {
			writeError(w, classifyWriteError(err, http.StatusNotFound), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": code, "deleted": true})
	})

	mux.HandleFunc("POST /api/rules/{code}/block", func(w http.ResponseWriter, r *http.Request) {
		code := r.PathValue("code")
		if s.opts.Writer == nil {
			writeError(w, http.StatusInternalServerError, errWriterNotConfigured)
			return
		}
		if err := s.ensureBlockTier(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		// RULE-04: block routes through the surgical UpdateAccessCodeTier
		// to the zero-limit blockTierID — NEVER a PutItem (16-RESEARCH.md
		// Pitfall 1) — so an already phone-mapped/bypass-enabled code's
		// side attributes survive the block untouched.
		if err := UpdateAccessCodeTier(r.Context(), s.opts.Writer, s.opts.Table, code, blockTierID); err != nil {
			writeError(w, classifyWriteError(err, http.StatusNotFound), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"code": code, "tierId": blockTierID})
	})

	mux.HandleFunc("PUT /api/order", func(w http.ResponseWriter, r *http.Request) {
		var req OrderWriteReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		if err := s.opts.Repo.WriteRuleOrder(req.Order); err != nil {
			writeError(w, classifyRepoWriteError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"order": req.Order})
	})

	mux.HandleFunc("PUT /api/secret", func(w http.ResponseWriter, r *http.Request) {
		var req SecretWriteReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		// Validate-then-write (T-16-13): a bad gate_mode must never reach
		// telephony.toml, or the voice pipeline's config loader refuses to
		// boot on its next read (16-RESEARCH.md Pitfall 2). Repeated here
		// (WriteTelephonyGate re-guards too) as defense in depth.
		if req.GateMode != "" && !GateModeAllowed(req.GateMode) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid gate mode %q: must be one of dtmf, passphrase, either", req.GateMode))
			return
		}
		if err := s.opts.Repo.WriteTelephonyGate(req.GateMode, req.RequireGate); err != nil {
			writeError(w, classifyRepoWriteError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"gateMode": req.GateMode, "requireGate": req.RequireGate})
	})

	mux.HandleFunc("PUT /api/unlocks", func(w http.ResponseWriter, r *http.Request) {
		var req UnlockWriteReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		var op KeywordOp
		switch req.Op {
		case "", "add":
			op = KeywordAdd
		case "remove":
			op = KeywordRemove
		default:
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid op %q: must be \"add\" or \"remove\"", req.Op))
			return
		}
		if err := s.opts.Repo.WriteTopicMapKeyword(req.TopicID, req.Term, req.Weight, op); err != nil {
			writeError(w, classifyRepoWriteError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"topicId": req.TopicID, "term": req.Term, "op": req.Op})
	})
}

// registerDIDHandlers wires DID-01/02's endpoints onto mux: list the merged
// live-VoIP.ms + dids.yaml DID view, "add" a DID (route an already-owned
// number via the injected DIDRouter, if present, plus always write
// dids.yaml metadata — never number provisioning, 16-RESEARCH.md
// Pitfall 3), and edit an existing DID's metadata.
func (s *Server) registerDIDHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/dids", func(w http.ResponseWriter, r *http.Request) {
		dids, status := s.mergedInboundDIDs(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"dids": dids, "status": status})
	})

	mux.HandleFunc("POST /api/dids", func(w http.ResponseWriter, r *http.Request) {
		var req DIDWriteReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}

		routingNote := ""
		if s.opts.DIDRouter != nil {
			if err := s.opts.DIDRouter.RouteDID(r.Context(), req.Did); err != nil {
				writeError(w, http.StatusBadGateway, fmt.Errorf("route DID %s: %w", req.Did, err))
				return
			}
		} else {
			routingNote = fmt.Sprintf("DID not routed automatically (no VoIP.ms router configured) — run `kv voipms route-did %s` via the CLI, then retry", req.Did)
		}

		meta := DIDMeta{Did: req.Did, Label: req.Label, Region: req.Region, DefaultRule: req.DefaultRule, Greeting: req.Greeting}
		if err := s.opts.Repo.WriteDIDMeta(meta); err != nil {
			writeError(w, classifyRepoWriteError(err), err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"did": meta, "routingNote": routingNote})
	})

	mux.HandleFunc("PUT /api/dids/{did}", func(w http.ResponseWriter, r *http.Request) {
		did := r.PathValue("did")
		var req DIDWriteReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		meta := DIDMeta{Did: did, Label: req.Label, Region: req.Region, DefaultRule: req.DefaultRule, Greeting: req.Greeting}
		if err := s.opts.Repo.WriteDIDMeta(meta); err != nil {
			writeError(w, classifyRepoWriteError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"did": meta})
	})
}

// registerSecretHandlers wires SEC-01/SEC-02's two endpoints onto mux:
// reveal an allow-listed telephony gate secret's current value once, and
// rotate one to a new SecureString value. Both handlers are deliberately
// thin — they decode the request, nil-check s.opts.SSM, and delegate to
// RevealSecret/RotateSecret (secret_reveal.go/secret_rotate.go); neither
// handler contains a decrypt/write token itself (T-16-15's discipline,
// narrowed for Phase 17 — TestNoSecretWrites_Phase16Files still scans this
// file).
func (s *Server) registerSecretHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/secret/reveal", func(w http.ResponseWriter, r *http.Request) {
		var req SecretRevealReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		if s.opts.SSM == nil {
			writeError(w, http.StatusInternalServerError, errSSMNotConfigured)
			return
		}
		result, err := RevealSecret(r.Context(), s.opts.SSM, req.Name)
		if err != nil {
			// RevealSecret's only Go-error path is the allow-list rejection
			// (T-17-01) — every other outcome (not set, AWS error) is
			// classified into result.Status, not returned as err.
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, SecretRevealResp{
			Name:      result.Name,
			Status:    result.Status,
			Value:     result.Value,
			Ephemeral: true,
		})
	})

	mux.HandleFunc("POST /api/secret/rotate", func(w http.ResponseWriter, r *http.Request) {
		var req SecretRotateReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		if s.opts.SSM == nil {
			writeError(w, http.StatusInternalServerError, errSSMNotConfigured)
			return
		}
		if err := RotateSecret(r.Context(), s.opts.SSM, req.Name, req.NewValue); err != nil {
			writeError(w, classifySecretError(err), err)
			return
		}
		writeJSON(w, http.StatusOK, SecretRotateResp{Name: req.Name, Rotated: true})
	})
}

// registerKnowledgeHandlers wires KNOW-02/KNOW-03's two endpoints onto mux:
// append a validated public source to an existing manifest.yaml pack, and
// trigger the exact same offline refresh_knowledge.py subprocess `kv
// knowledge refresh` already shells — surfacing a read-only git diff summary
// and never auto-committing (D-09's human-review gate, unchanged). Both
// handlers are deliberately thin, delegating to
// RepoFiles.WriteManifestSource (repofile_writer.go) and
// KnowledgeRebuildTrigger.Rebuild (knowledge_rebuild.go).
func (s *Server) registerKnowledgeHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/manifest/sources", func(w http.ResponseWriter, r *http.Request) {
		var req ManifestSourceWriteReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		if err := s.opts.Repo.WriteManifestSource(req.TopicID, req.Path, req.Kind); err != nil {
			writeError(w, classifyRepoWriteError(err), err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"topicId": req.TopicID, "path": req.Path, "kind": req.Kind, "public": true,
		})
	})

	mux.HandleFunc("POST /api/knowledge/rebuild", func(w http.ResponseWriter, r *http.Request) {
		var req RebuildReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		if s.opts.KnowledgeRebuild == nil {
			writeError(w, http.StatusInternalServerError, errKnowledgeRebuildNotConfigured)
			return
		}
		result, err := s.opts.KnowledgeRebuild.Rebuild(r.Context(), req)
		if err != nil {
			if errors.Is(err, errKnowledgeRebuildAlreadyRunning) {
				writeError(w, http.StatusConflict, err)
				return
			}
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
}

// SOPNameReq is the shared request body shape for all three SOP-03/SOP-01/
// SOP-02 endpoints below — every one of them takes exactly {"name": "..."},
// the SOP's identifier (also its sops/<name>.yaml filename stem).
type SOPNameReq struct {
	Name string `json:"name"`
}

// validateSOPName rejects a name that could path-traverse out of sopsDir
// once joined into a filename (sop.go's WriteSOP/ReadSOP and sop_git.go's
// SaveSOP all trust name as-is — neither validates it, since Plan 01/04's
// scope was the SOP DTO/IO and git-commit primitives, not the HTTP surface).
// This request arrives directly from an untrusted HTTP body, so the REST
// layer is where that check belongs — reusing validateCodeCharset's
// control-character/length discipline, plus a path-separator/relative-
// segment rejection no other id-shaped field in this package needs (no
// other write handler's id ever becomes a raw filename component).
func validateSOPName(name string) error {
	if err := validateCodeCharset(name); err != nil {
		return fmt.Errorf("invalid sop name: %w", err)
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return fmt.Errorf("invalid sop name %q: must not contain a path separator or be a relative path segment", name)
	}
	return nil
}

// registerSOPHandlers wires SOP-01/02/03's three endpoints onto mux —
// Save-as-SOP, the read-only pending changeset, and the gated Deploy action
// the "Save & deploy" tab drives. Every handler follows the same idioms
// registerRuleHandlers/registerKnowledgeHandlers already use (writeError/
// writeJSON, s.assembleConfig for a FRESH live view, s.opts.Writer's
// nil-safety). Git operations (SaveSOP's commit, Deploy's config commit) use
// execCommandRunner{} directly — the real os/exec CommandRunner
// knowledge_rebuild.go already defines and defaults to — rather than a new
// ServerOptions injection point, since cmd/studio.go's wiring is out of this
// plan's scope and every call site already has everything it needs
// (s.opts.Repo.Root) to shell git for real.
func (s *Server) registerSOPHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sop/save", func(w http.ResponseWriter, r *http.Request) {
		var req SOPNameReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		if err := validateSOPName(req.Name); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		live := s.assembleConfig(r.Context())
		sha, err := SaveSOP(r.Context(), s.opts.Repo.Root, req.Name, live, execCommandRunner{})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": req.Name, "sha": sha})
	})

	mux.HandleFunc("POST /api/sop/changeset", func(w http.ResponseWriter, r *http.Request) {
		var req SOPNameReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		if err := validateSOPName(req.Name); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		doc, err := ReadSOP(s.opts.Repo.Root, req.Name)
		if err != nil {
			writeError(w, classifyRepoWriteError(err), err)
			return
		}

		// Assembled FRESH on every request (RESEARCH Open Q1's accepted
		// answer) — never cached, so a live edit made via the Phase 16/17
		// handlers between two "Save & deploy" tab opens is always
		// reflected in the very next changeset read.
		live := s.assembleConfig(r.Context())
		writeJSON(w, http.StatusOK, DiffChangeset(doc, live))
	})

	mux.HandleFunc("POST /api/sop/deploy", func(w http.ResponseWriter, r *http.Request) {
		var req SOPNameReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
			return
		}
		if err := validateSOPName(req.Name); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if s.opts.Writer == nil {
			writeError(w, http.StatusInternalServerError, errWriterNotConfigured)
			return
		}

		live := s.assembleConfig(r.Context())
		result := Deploy(r.Context(), req.Name, live, DeployDeps{
			Root:             s.opts.Repo.Root,
			Writer:           s.opts.Writer,
			Table:            s.opts.Table,
			Repo:             s.opts.Repo,
			Runner:           execCommandRunner{},
			KnowledgeRebuild: s.opts.KnowledgeRebuild,
		})

		switch {
		case len(result.ValidationErrors) > 0:
			// A validation-failure response is a structured 4xx listing the
			// ValidationErrors — Deploy itself never attempted apply/
			// commit/rebuild, so there is no partial write to report
			// (P-06-validate-first).
			writeJSON(w, http.StatusUnprocessableEntity, result)
		case result.Error != "" && result.FailedSurface == "":
			// The only failure mode with no FailedSurface set is ReadSOP
			// itself failing (a missing/malformed sops/<name>.yaml) — Deploy
			// returns before Apply is ever reached. Mirrors
			// classifyRepoWriteError's RepoFileError->500 convention used
			// throughout this package for a repo-file read failure.
			writeJSON(w, http.StatusInternalServerError, result)
		default:
			// Success, OR a partial failure after validation passed (Apply/
			// config-commit/knowledge-rebuild) — always 200, with the full
			// DeployResult body naming exactly what succeeded and what
			// failed (spec §8: never a bare 5xx with an empty body; a
			// partial failure is resumable, since Apply is idempotent by
			// changeset and re-running Deploy is always safe).
			writeJSON(w, http.StatusOK, result)
		}
	})
}

// classifySecretError maps a RotateSecret error into an HTTP status: the
// allow-list rejection (raised BEFORE any AWS call) maps to 400; any other
// failure (a genuine SSM DescribeParameters/PutParameter error, already
// classified to a short non-sensitive message by RotateSecret) maps to 502
// — the studio server successfully reached the point of calling AWS, but
// AWS itself failed.
func classifySecretError(err error) int {
	if errors.Is(err, errSecretNotAllowed) {
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}

// mergedInboundDIDs reads dids.yaml metadata (best-effort — a read failure
// degrades to no metadata rather than blocking the DID list) and merges it
// with the injected live VoIP.ms lister's output via MergeInboundDIDs.
// status is a short, non-sensitive, human-readable degradation note (never
// the underlying error — 16-RESEARCH.md T-16-14) — "" when the live list
// was read successfully.
func (s *Server) mergedInboundDIDs(ctx context.Context) ([]InboundDID, string) {
	meta, _ := s.opts.Repo.ReadDIDMeta()

	if s.opts.InboundDIDs == nil {
		return MergeInboundDIDs(nil, meta), "not configured — VoIP.ms credentials unavailable"
	}
	live, err := s.opts.InboundDIDs(ctx)
	if err != nil {
		return MergeInboundDIDs(nil, meta), "error — inbound DID list unavailable"
	}
	return MergeInboundDIDs(live, meta), ""
}

// ensureBlockTier PutTiers the canonical zero-limit blockTierID, ignoring a
// ConditionalCheckFailedException (the tier already exists — the common
// case after the first block). Any other PutTier failure is returned as-is.
// Purely for operator legibility (`kv tier list`) — the runtime does not
// require this row to exist (blockTierID's doc comment).
func (s *Server) ensureBlockTier(ctx context.Context) error {
	err := PutTier(ctx, s.opts.Writer, s.opts.Table, blockTierID, "", 0, 0, 0)
	if err != nil && !isConditionalCheckFailed(err) {
		return err
	}
	return nil
}

// errWriterNotConfigured is returned by every /api/rules write handler when
// ServerOptions.Writer is nil (a read-only server) — a structured 500
// instead of a nil-pointer panic.
var errWriterNotConfigured = errors.New("studio server has no DynamoDB write client configured")

// errSSMNotConfigured is returned by /api/secret/reveal and
// /api/secret/rotate when ServerOptions.SSM is nil — a structured 500
// instead of a nil-pointer panic, mirroring errWriterNotConfigured.
var errSSMNotConfigured = errors.New("studio server has no SSM client configured")

// errKnowledgeRebuildNotConfigured is returned by POST /api/knowledge/rebuild
// when ServerOptions.KnowledgeRebuild is nil — a structured 500 instead of a
// nil-pointer panic, mirroring errWriterNotConfigured/errSSMNotConfigured.
var errKnowledgeRebuildNotConfigured = errors.New("studio server has no knowledge rebuild trigger configured")

// writeError encodes a structured {"error": "..."} JSON body — every write
// handler's failure path, never a bare 500 with an empty body.
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, APIError{Error: err.Error()})
}

// isConditionalCheckFailed reports whether err is (or wraps) a DynamoDB
// ConditionalCheckFailedException, mirroring cmd/killswitch.go's helper of
// the same purpose (duplicated per package studio's no-cmd-import
// constraint). Also matches a plain error whose message contains the
// exception name, so unit tests can simulate the condition with a fake
// DynamoWriteAPI that returns errors.New(...) rather than the real typed
// AWS exception.
func isConditionalCheckFailed(err error) bool {
	var condErr *ddbtypes.ConditionalCheckFailedException
	if errors.As(err, &condErr) {
		return true
	}
	return err != nil && strings.Contains(err.Error(), "ConditionalCheckFailedException")
}

// classifyWriteError maps a dynamo_writer.go function's error into an HTTP
// status: a conditional-check failure (item already exists, for a "create";
// item is gone, for an "edit"/"delete"/"block") maps to conflictStatus
// (409 for create, 404 for edit/delete/block); a validation error raised
// BEFORE any store call (validateCodeCharset/validateTierID — returned
// unwrapped, see dynamo_writer.go) maps to 400; anything else (a genuine
// DynamoDB failure) maps to 500.
func classifyWriteError(err error, conflictStatus int) int {
	switch {
	case isConditionalCheckFailed(err):
		return conflictStatus
	case errors.Unwrap(err) == nil:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// classifyRepoWriteError maps a repofile_writer.go/studio_files.go
// function's error into an HTTP status: a *RepoFileError (file I/O failure,
// or a "not found" structural problem) maps to 500; anything else (input
// validation — invalid gate_mode/topic id/term/DID/tier id, or a "term not
// found" business-rule failure) maps to 400, since it was raised before any
// file write.
func classifyRepoWriteError(err error) int {
	var rfErr *RepoFileError
	if errors.As(err, &rfErr) {
		return http.StatusInternalServerError
	}
	return http.StatusBadRequest
}

// assembleConfig reads every store through the injected adapters and
// projects them into a ConfigView via AssembleConfig. A DynamoDB read
// failure (Query/Scan error from ReadCodes/ReadTiers/ReadPhoneMappings)
// short-circuits into AssembleConfig's ErrorBanner path (spec §8) — no
// partial view is ever returned. Repo-file reads are best-effort: v1's
// AssembleInput has no signal for a repo-read failure (only a DynamoDB
// store banner exists), so a missing/unreadable manifest, topic-map, or
// telephony.toml degrades to an empty section rather than blocking the
// whole view.
func (s *Server) assembleConfig(ctx context.Context) ConfigView {
	in := AssembleInput{
		Region:  s.opts.Meta.Region,
		Profile: s.opts.Meta.Profile,
		Table:   s.opts.Meta.Table,
		Root:    s.opts.Repo.Root,
	}

	if codes, err := ReadCodes(ctx, s.opts.Dynamo, s.opts.Table); err != nil {
		in.DynamoErr = err
	} else {
		in.Codes = codes
	}

	if in.DynamoErr == nil {
		if tiers, err := ReadTiers(ctx, s.opts.Dynamo, s.opts.Table); err != nil {
			in.DynamoErr = err
		} else {
			in.Tiers = tiers
		}
	}

	if in.DynamoErr == nil {
		if mappings, err := ReadPhoneMappings(ctx, s.opts.Dynamo, s.opts.Table); err != nil {
			in.DynamoErr = err
		} else {
			in.PhoneMappings = mappings
		}
	}

	if manifest, err := s.opts.Repo.ReadManifest(); err == nil {
		in.Manifest = manifest
	}
	if unlocks, err := s.opts.Repo.ReadTopicMap(); err == nil {
		in.Unlocks = unlocks
	}
	if gateMode, err := s.opts.Repo.ReadTelephonyGate(); err == nil {
		in.GateMode = gateMode
	}
	if order, err := s.opts.Repo.ReadRuleOrder(); err == nil {
		in.RuleOrder = order
	}
	dids, _ := s.mergedInboundDIDs(ctx)
	in.InboundDIDs = dids

	return AssembleConfig(ctx, in)
}

// writeJSON encodes v as the response body with the given status, setting
// the JSON content type first.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Listen opens the loopback listener on 127.0.0.1:<Port> — the one place in
// this package net.Listen is called, and it is hard-coded to the 127.0.0.1
// literal (T-15-01 / spec §7: local-only, never 0.0.0.0, no --host flag).
// Split out from ListenAndServe so tests can assert the bound address
// (typically with Port "0" for an OS-assigned ephemeral port) before
// serving.
func (s *Server) Listen() (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:"+s.opts.Port)
	if err != nil {
		return nil, fmt.Errorf("listen on 127.0.0.1:%s: %w", s.opts.Port, err)
	}
	return ln, nil
}

// Serve runs the studio console's HTTP server over an already-open listener
// (from Listen) until ctx is cancelled, at which point it shuts the server
// down gracefully (http.Server.Shutdown, 5s budget) and returns nil. A
// serve-time error other than the expected http.ErrServerClosed (raised by
// Shutdown itself) is returned as-is.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	httpSrv := &http.Server{Handler: s.Handler()}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpSrv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		<-errCh
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// ListenAndServe opens the loopback listener (Listen) and serves until ctx
// is cancelled (Serve) — the single call cmd/studio.go's RunE needs.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := s.Listen()
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}
