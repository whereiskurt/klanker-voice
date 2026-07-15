// Package studio's Deploy orchestrator (SOP-03): the one gated action the
// "Save & deploy" tab's Deploy button drives, composing every prior plan's
// primitive in a strict, D-09-preserving order:
//
//	ReadSOP -> Validate -> DiffChangeset -> Apply (DynamoDB, idempotent) ->
//	write the "gate"/"order" changeset entries directly (Apply's
//	deliberate scope boundary — sop_apply.go's package doc comment) ->
//	gitCommitScoped the changed config surfaces, by EXACT path, BEFORE ->
//	KnowledgeRebuildTrigger.Rebuild, ONLY if a knowledge/pack source
//	changed.
//
// Deploy refuses the whole action on ANY Validate failure — no apply, no
// commit, no rebuild is ever attempted (P-06-validate-first). It never
// stages a directory glob and never touches the knowledge-generated subtree
// (apps/voice/knowledge/topics, /chunks) — only the exact config path
// constants already defined elsewhere in this package
// (P-06-commit-before-rebuild / P-06-never-commits-generated-packs). It
// never runs `git push`/`gh pr` — commits land LOCALLY only, on the current
// branch (P-06-no-remote-push); the caller reports "committed — ready to
// push/PR" (a separate, explicit human step, matching gsd-pr-branch's
// pattern).
package studio

import (
	"context"
	"fmt"
	"path/filepath"
)

// DeployDeps carries every dependency Deploy needs: ApplyDeps' two write
// surfaces (DynamoDB + repo files), the git CommandRunner + repo Root
// gitCommitScoped needs (sop_git.go), and the optional knowledge-rebuild
// trigger (nil degrades to "never refresh", mirroring
// ServerOptions.KnowledgeRebuild's nil-safety in server.go).
type DeployDeps struct {
	Root   string
	Writer DynamoWriteAPI
	Table  string
	Repo   RepoFiles
	Runner CommandRunner

	KnowledgeRebuild *KnowledgeRebuildTrigger
}

// DeployResult reports what Deploy actually did — SEQUENCED best-effort
// reporting, not a real cross-store transaction (Go + DynamoDB + local git +
// a subprocess have no shared transaction primitive, 18-RESEARCH.md Wave
// F). A partial failure leaves whatever succeeded already-applied
// (Applied/CommitSha below stay populated) and names the step that failed
// (FailedSurface/Error) — because Apply is idempotent by changeset and
// gitCommitScoped is safe to retry, re-running Deploy after fixing the
// failure is always safe (T-18-19).
type DeployResult struct {
	Name string `json:"name"`

	// ValidationErrors is non-empty ONLY when Validate refused the deploy —
	// in that case every field below is zero-valued: no changeset was even
	// computed, and no store/git/rebuild call was ever attempted
	// (P-06-validate-first).
	ValidationErrors []ValidationError `json:"validationErrors,omitempty"`

	// Changeset is the []ChangesetEntry Apply was driven off (SOP-02's
	// review data), echoed back so the caller can render exactly what this
	// Deploy call acted on — computed fresh against the live ConfigView the
	// caller supplied, never cached.
	Changeset []ChangesetEntry `json:"changeset,omitempty"`

	// Applied / Skipped mirror ApplyResult (sop_apply.go): Applied is every
	// changeset entry Apply successfully wrote; Skipped is every "removed"
	// entry (Apply never deletes — additive + update-only, T-18-07).
	Applied []ChangesetEntry `json:"applied,omitempty"`
	Skipped []ChangesetEntry `json:"skipped,omitempty"`

	// CommitSha / CommittedPaths report the config-commit step. CommitSha
	// stays empty when no git-tracked config surface changed (a no-op
	// re-deploy of an already-applied SOP never touches git — there is
	// nothing to stage). CommittedPaths is the EXACT pathspec list passed to
	// gitCommitScoped — never a directory, never the knowledge subtree.
	CommitSha      string   `json:"commitSha,omitempty"`
	CommittedPaths []string `json:"committedPaths,omitempty"`

	// RefreshTriggered / RefreshResult report the conditional knowledge
	// rebuild step: triggered ONLY when the changeset's own "knowledge"
	// surface shows an added pack or a changed source list, and only when a
	// KnowledgeRebuildTrigger is configured.
	RefreshTriggered bool           `json:"refreshTriggered"`
	RefreshResult    *RebuildResult `json:"refreshResult,omitempty"`

	// FailedSurface / Error name the step that stopped Deploy short of
	// completing every step above — "" / "" on full success. FailedSurface
	// is one of a changeset Surface value ("rule"/"tier"/"did"/"unlock"/
	// "knowledge"/"gate"/"order"), or "config-commit" / "knowledge-rebuild"
	// for a failure in one of those two non-changeset-keyed steps.
	FailedSurface string `json:"failedSurface,omitempty"`
	Error         string `json:"error,omitempty"`
}

// Deploy runs the full SOP-03 sequence against the named SOP
// (root/apps/voice/configs/studio/sops/<name>.yaml). live is the caller's
// FRESHLY assembled ConfigView (server.go's s.assembleConfig(ctx)) — Deploy
// itself has no DynamoDB read/repo-file-read capability of its own; it is
// always the caller's job to assemble the live side fresh, per request,
// exactly as the changeset endpoint does (never cached, RESEARCH Open Q1).
func Deploy(ctx context.Context, name string, live ConfigView, deps DeployDeps) DeployResult {
	result := DeployResult{Name: name}

	doc, err := ReadSOP(deps.Root, name)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	// Validate-first (P-06-validate-first / T-18-18): ANY validation
	// failure refuses the whole action here, before DiffChangeset is even
	// computed — no apply, no commit, no rebuild.
	if errs := Validate(doc); len(errs) > 0 {
		result.ValidationErrors = errs
		return result
	}

	changeset := DiffChangeset(doc, live)
	result.Changeset = changeset

	applyResult, err := Apply(ctx, doc, changeset, ApplyDeps{Writer: deps.Writer, Table: deps.Table, Repo: deps.Repo})
	result.Applied = applyResult.Applied
	result.Skipped = applyResult.Skipped
	if err != nil {
		// Apply returns on the FIRST write error — every entry before it in
		// changeset order is already accounted for in Applied+Skipped, so
		// that count is exactly the index of the entry that failed (Apply's
		// own iteration order, sop_apply.go).
		if idx := len(applyResult.Applied) + len(applyResult.Skipped); idx < len(changeset) {
			result.FailedSurface = changeset[idx].Surface
		}
		result.Error = err.Error()
		return result
	}

	// "gate" (telephony.toml) and "order" (rule-order.yaml) are
	// deliberately OUT of Apply's scope (sop_apply.go's package doc
	// comment's "Scope note") — both are whole-value repo-file overwrites,
	// not per-item added/changed routing, so Deploy drives them directly
	// off the same changeset here, before the config commit.
	if err := deployGateSurface(doc, changeset, deps.Repo); err != nil {
		result.FailedSurface = "gate"
		result.Error = err.Error()
		return result
	}
	if err := deployOrderSurface(doc, changeset, deps.Repo); err != nil {
		result.FailedSurface = "order"
		result.Error = err.Error()
		return result
	}

	// Step 4: commit the changed config surfaces, by EXACT path, BEFORE any
	// rebuild (RESEARCH Pitfall 3 / P-06-commit-before-rebuild). paths is
	// nil when nothing in a git-tracked config surface changed (only
	// DynamoDB-only surfaces — rule/tier — were in this changeset), in
	// which case there is nothing to stage and Deploy skips the commit step
	// entirely rather than asking git to commit zero staged changes.
	paths := deployPathspec(name, changeset)
	if len(paths) > 0 {
		sha, err := gitCommitScoped(ctx, deps.Runner, deps.Root, fmt.Sprintf("sop: deploy %s", name), paths)
		if err != nil {
			result.FailedSurface = "config-commit"
			result.Error = err.Error()
			return result
		}
		result.CommitSha = sha
		result.CommittedPaths = paths
	}

	// Step 5: refresh knowledge ONLY if a pack source changed — AFTER the
	// config commit above, never before (P-06-commit-before-rebuild). The
	// rebuild itself never git-adds/commits anything (knowledge_rebuild.go's
	// D-09 discipline, unchanged) — it only surfaces a read-only diff for a
	// human to review.
	if knowledgeSourceChanged(changeset) && deps.KnowledgeRebuild != nil {
		result.RefreshTriggered = true
		rr, err := deps.KnowledgeRebuild.Rebuild(ctx, RebuildReq{})
		if err != nil {
			result.FailedSurface = "knowledge-rebuild"
			result.Error = err.Error()
			return result
		}
		result.RefreshResult = &rr
	}

	return result
}

// deploySurfaceChanged reports whether changeset carries any NON-"removed"
// entry for surface — the signal that surface's backing repo file (if any)
// actually received a write and therefore belongs in the config-commit
// pathspec. A "removed"-only surface never drove a write (Apply/Deploy
// never deletes), so its file is left out of the pathspec entirely.
func deploySurfaceChanged(changeset []ChangesetEntry, surface string) bool {
	for _, e := range changeset {
		if e.Surface == surface && e.Kind != "removed" {
			return true
		}
	}
	return false
}

// deployPathspec builds the EXACT config-commit pathspec (never a directory
// glob, never the knowledge subtree as a directory — P-06-commit-before-
// rebuild): the SOP's own file plus each config path constant whose surface
// actually changed. Returns nil when no git-tracked config surface changed
// at all (a rule/tier-only changeset, or an empty one) — Deploy's caller
// treats a nil/empty result as "skip the commit step", since there would be
// nothing staged to commit.
func deployPathspec(name string, changeset []ChangesetEntry) []string {
	var configPaths []string
	if deploySurfaceChanged(changeset, "knowledge") {
		configPaths = append(configPaths, manifestPath)
	}
	if deploySurfaceChanged(changeset, "unlock") {
		configPaths = append(configPaths, topicMapPath)
	}
	if deploySurfaceChanged(changeset, "gate") {
		configPaths = append(configPaths, telephonyConfigPath)
	}
	if deploySurfaceChanged(changeset, "did") {
		configPaths = append(configPaths, studioDIDsPath)
	}
	if deploySurfaceChanged(changeset, "order") {
		configPaths = append(configPaths, ruleOrderPath)
	}
	if len(configPaths) == 0 {
		return nil
	}

	sopPath := filepath.ToSlash(filepath.Join(sopsDir, name+".yaml"))
	return append([]string{sopPath}, configPaths...)
}

// knowledgeSourceChanged reports whether changeset's "knowledge" surface
// shows a whole new pack (Kind "added") or a changed source list (Kind
// "changed", Field "sources") — the trigger condition for step 5's
// conditional rebuild (RESEARCH: "if diffKnowledge produced any
// 'added'/'changed' pack-source entry, trigger the knowledge rebuild"). A
// knowledge entry changed on any OTHER field (spokenName/pack) never
// touches the actual source material, so it does not warrant a rebuild.
func knowledgeSourceChanged(changeset []ChangesetEntry) bool {
	for _, e := range changeset {
		if e.Surface != "knowledge" {
			continue
		}
		if e.Kind == "added" {
			return true
		}
		if e.Kind == "changed" && e.Field == "sources" {
			return true
		}
	}
	return false
}

// deployGateSurface writes doc's target gate config to telephony.toml when
// the changeset's "gate" surface shows a change — the ONE call covers both
// possible field-level entries (mode/ref) diffGate may have produced, since
// doc.Gate already carries the full target value for both. requireGate is
// inferred from doc.Gate.Ref being set (sop_validate.go's
// checkGateRequireConsistency uses the same signal — a SOP has no separate
// require_gate boolean field, sop.go's SOPDoc doc comment).
func deployGateSurface(doc SOPDoc, changeset []ChangesetEntry, repo RepoFiles) error {
	for _, e := range changeset {
		if e.Surface != "gate" || e.Kind != "changed" {
			continue
		}
		requireGate := doc.Gate.Ref != ""
		if err := repo.WriteTelephonyGate(doc.Gate.Mode, requireGate); err != nil {
			return err
		}
		return nil
	}
	return nil
}

// deployOrderSurface writes doc's target rule-authoring order to
// rule-order.yaml when the changeset's "order" surface shows a change — a
// full replace (WriteRuleOrder's own contract), matching diffOrder's single
// whole-list "changed" entry shape.
func deployOrderSurface(doc SOPDoc, changeset []ChangesetEntry, repo RepoFiles) error {
	for _, e := range changeset {
		if e.Surface != "order" || e.Kind != "changed" {
			continue
		}
		return repo.WriteRuleOrder(doc.Order)
	}
	return nil
}
