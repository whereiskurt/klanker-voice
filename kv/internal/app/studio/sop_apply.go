// Package studio's Apply/reconcile orchestrator (SOP-03): Apply makes the
// live stores match a SOPDoc, driven ENTIRELY off a precomputed
// []ChangesetEntry (sop_diff.go's DiffChangeset output) — NEVER off doc's
// own Rules/Tiers/Dids/Knowledge/Unlocks lists directly. This is what makes
// three properties hold by construction, not by convention:
//
//   - Idempotent (T-18-10): a changeset computed against a SOP that's
//     already been applied is empty (DiffChangeset's own guarantee), so a
//     second Apply for that (now-empty) changeset issues zero writes.
//   - Additive + in-place-update ONLY, never deleting (T-18-07 /
//     P-03-no-auto-delete): Kind "removed" is always skipped — Apply never
//     calls DeleteAccessCode or RemovePhoneMapping anywhere in this file.
//   - Correct create-vs-update routing (T-18-08 / P-03-tier-update-not-put):
//     Kind "added" drives the create-only writer for that surface
//     (PutTier/PutAccessCode+SetPhoneMapping/WriteDIDMeta/WriteTopicMapKeyword);
//     Kind "changed" drives the in-place UPDATE writer instead
//     (UpdateTierLimits/UpdateAccessCodeTier/WriteDIDMeta/WriteTopicMapKeyword
//     — the latter two are already idempotent upserts, so they serve both
//     kinds; UpdateTierLimits/UpdateAccessCodeTier are TIER/RULE-specific
//     because PutTier/PutAccessCode are guarded attribute_not_exists(pk)
//     and would fail loudly on an existing item).
//
// Every DynamoDB/repo-file write below reuses a Phase-16/17/18-Plan-03
// writer verbatim (18-RESEARCH.md "Don't Hand-Roll") — this file adds zero
// new store-interaction logic, only changeset-driven routing.
//
// Scope note: "gate" (telephony.toml) and "order" (rule-order.yaml) are
// single-entity, full-replace repo-file surfaces with existing writers
// (WriteTelephonyGate/WriteRuleOrder) that this plan deliberately does not
// wire into Apply — the reuse list this plan's PLAN.md enumerates is
// PutAccessCode/PutTier/UpdateAccessCodeTier/UpdateTierLimits/SetPhoneMapping/
// WriteDIDMeta/WriteTopicMapKeyword/WriteManifestSource only. Deploy's
// orchestration (a later plan) is expected to write those two directly from
// the changeset's "gate"/"order" entries, since they're whole-value
// overwrites, not per-item added/changed routing. Apply no-ops on those two
// surfaces here rather than guessing at that orchestration.
package studio

import (
	"context"
	"fmt"
)

// ApplyDeps carries the two write surfaces Apply needs — the DynamoDB write
// client + table (dynamo_writer.go) and the repo-file writer
// (repofile_writer.go/studio_files.go) — mirroring how server.go's
// ServerOptions injects the same two surfaces into the REST handlers
// (18-RESEARCH.md Project Structure).
type ApplyDeps struct {
	Writer DynamoWriteAPI
	Table  string
	Repo   RepoFiles
}

// ApplyResult reports what Apply actually did, per changeset entry —
// Deploy's eventual step-list rendering. This is sequenced best-effort
// reporting, not a real cross-store transaction (Go + DynamoDB + local repo
// files have no shared transaction primitive, 18-RESEARCH.md Wave F): a
// partial failure leaves whatever Applied already succeeded already-applied,
// which the idempotent design makes safe to re-run.
type ApplyResult struct {
	Applied []ChangesetEntry // entries Apply successfully wrote
	Skipped []ChangesetEntry // "removed" entries — always skipped, never deleted
}

// Apply reconciles the live stores to match doc, driven strictly off
// changeset (sop_diff.go's DiffChangeset(doc, live) output). It returns on
// the first write error — whatever already succeeded in this call stays
// applied (ApplyResult.Applied), and because Apply is idempotent by
// changeset, re-running Apply with a freshly recomputed changeset after
// fixing the failure is always safe.
func Apply(ctx context.Context, doc SOPDoc, changeset []ChangesetEntry, deps ApplyDeps) (ApplyResult, error) {
	var result ApplyResult

	tiersByID := make(map[string]SOPTier, len(doc.Tiers))
	for _, t := range doc.Tiers {
		tiersByID[t.TierID] = t
	}
	rulesByCode := make(map[string]SOPRule, len(doc.Rules))
	for _, r := range doc.Rules {
		rulesByCode[r.Code] = r
	}
	didsByID := make(map[string]DIDMeta, len(doc.Dids))
	for _, d := range doc.Dids {
		didsByID[d.Did] = d
	}
	packsByID := make(map[string]SOPPack, len(doc.Knowledge))
	for _, p := range doc.Knowledge {
		packsByID[p.ID] = p
	}
	unlocksByPhrase := make(map[string]Unlock, len(doc.Unlocks))
	for _, u := range doc.Unlocks {
		unlocksByPhrase[u.Phrase] = u
	}

	for _, entry := range changeset {
		if entry.Kind == "removed" {
			// Additive + update-only: Apply NEVER deletes
			// (P-03-no-auto-delete). A rule/tier/did/pack present live but
			// absent from the SOP is left alone — deletion stays a manual
			// console action.
			result.Skipped = append(result.Skipped, entry)
			continue
		}

		var err error
		switch entry.Surface {
		case "tier":
			err = applyTierEntry(ctx, deps, entry, tiersByID)
		case "rule":
			err = applyRuleEntry(ctx, deps, entry, rulesByCode)
		case "did":
			err = applyDidEntry(ctx, deps, entry, didsByID)
		case "knowledge":
			err = applyKnowledgeEntry(ctx, deps, entry, packsByID)
		case "unlock":
			err = applyUnlockEntry(ctx, deps, entry, unlocksByPhrase)
		case "gate", "order":
			// Deliberately out of this plan's scope — see package doc
			// comment's "Scope note".
		default:
			err = fmt.Errorf("apply: unknown changeset surface %q", entry.Surface)
		}
		if err != nil {
			return result, fmt.Errorf("apply %s %q (%s): %w", entry.Surface, entry.Key, entry.Kind, err)
		}
		result.Applied = append(result.Applied, entry)
	}

	return result, nil
}

// applyTierEntry routes a "tier" surface changeset entry: Kind "added" ->
// PutTier (guarded create-only); Kind "changed" -> UpdateTierLimits (guarded
// in-place edit) — NEVER PutTier for "changed" (P-03-tier-update-not-put).
// diffTiers emits one entry per changed FIELD (sessionMaxSeconds/
// periodMaxSeconds/maxConcurrent independently), so a tier with all three
// limits changed drives UpdateTierLimits up to three times in the same
// Apply call — each call writes the SAME correct target values (tiersByID
// always holds the SOP's full target row, not just the one changed field),
// so the redundancy is harmless and still idempotent, just not
// write-minimal.
func applyTierEntry(ctx context.Context, deps ApplyDeps, entry ChangesetEntry, tiersByID map[string]SOPTier) error {
	t, ok := tiersByID[entry.Key]
	if !ok {
		return fmt.Errorf("tier %q not found in SOP tiers", entry.Key)
	}
	switch entry.Kind {
	case "added":
		return PutTier(ctx, deps.Writer, deps.Table, t.TierID, "", t.SessionMaxSeconds, t.PeriodMaxSeconds, t.MaxConcurrent)
	case "changed":
		return UpdateTierLimits(ctx, deps.Writer, deps.Table, t.TierID, t.SessionMaxSeconds, t.PeriodMaxSeconds, t.MaxConcurrent)
	default:
		return nil
	}
}

// applyRuleEntry routes a "rule" surface changeset entry: Kind "added" ->
// PutAccessCode (guarded create-only), plus SetPhoneMapping when the SOP
// rule carries a number; Kind "changed" branches on Field — "tierId" ->
// UpdateAccessCodeTier (guarded in-place edit, NEVER PutAccessCode);
// "who.numbers" -> SetPhoneMapping when the SOP now has a number, or a
// documented no-op when the SOP has REMOVED the number (Apply never calls
// RemovePhoneMapping — that is this surface's instance of
// P-03-no-auto-delete); "who.type" -> no-op (who.type is derived from phone
// presence by AssembleConfig, not an independently writable field — a
// who.numbers entry, if any, is what actually drives this).
func applyRuleEntry(ctx context.Context, deps ApplyDeps, entry ChangesetEntry, rulesByCode map[string]SOPRule) error {
	r, ok := rulesByCode[entry.Key]
	if !ok {
		return fmt.Errorf("rule %q not found in SOP rules", entry.Key)
	}
	switch entry.Kind {
	case "added":
		if err := PutAccessCode(ctx, deps.Writer, deps.Table, r.Code, r.TierID, "", nil, nil); err != nil {
			return err
		}
		if len(r.Who.Numbers) == 0 {
			return nil
		}
		normalized, err := normalizeE164(r.Who.Numbers[0])
		if err != nil {
			return fmt.Errorf("rule %q: %w", r.Code, err)
		}
		return SetPhoneMapping(ctx, deps.Writer, deps.Table, r.Code, normalized)
	case "changed":
		switch entry.Field {
		case "tierId":
			return UpdateAccessCodeTier(ctx, deps.Writer, deps.Table, r.Code, r.TierID)
		case "who.numbers":
			if len(r.Who.Numbers) == 0 {
				// The SOP has removed the rule's number — Apply never
				// calls RemovePhoneMapping (additive + update-only); the
				// live mapping is left alone.
				return nil
			}
			normalized, err := normalizeE164(r.Who.Numbers[0])
			if err != nil {
				return fmt.Errorf("rule %q: %w", r.Code, err)
			}
			return SetPhoneMapping(ctx, deps.Writer, deps.Table, r.Code, normalized)
		default:
			// "who.type" and any other rule field diffTiers/diffRules may
			// grow to report have no independent writer (see doc comment) —
			// no-op, not an error.
			return nil
		}
	default:
		return nil
	}
}

// applyDidEntry routes a "did" surface changeset entry through
// RepoFiles.WriteDIDMeta — already a keyed upsert (studio_files.go's doc
// comment: "an existing row's fields are replaced in place; a new Did is
// appended"), so BOTH "added" and "changed" drive the exact same call.
func applyDidEntry(ctx context.Context, deps ApplyDeps, entry ChangesetEntry, didsByID map[string]DIDMeta) error {
	_ = ctx // WriteDIDMeta takes no context — kept for signature symmetry with the other apply* helpers.
	d, ok := didsByID[entry.Key]
	if !ok {
		return fmt.Errorf("did %q not found in SOP dids", entry.Key)
	}
	return deps.Repo.WriteDIDMeta(d)
}

// applyKnowledgeEntry routes a "knowledge" surface changeset entry.
// WriteManifestSource is append-only with no dedup (repofile_writer.go's
// doc comment) and only upserts a source under an ALREADY-EXISTING
// manifest.yaml topic — so:
//
//   - Kind "added" (a whole new pack/topic id, absent from live entirely)
//     has no existing topic to append a source into, and no writer exists
//     in this plan's reuse set to create a brand-new topic from scratch —
//     no-op (out of this plan's scope, matching 18-RESEARCH.md's Don't
//     Hand-Roll table).
//   - Kind "changed" with Field "sources" is the ONLY path that calls
//     WriteManifestSource, and even then ONLY for source paths present in
//     the SOP's target Sources but NOT in entry.From's live Sources
//     (P-03-knowledge-added-only) — never the SOP's full source list
//     unconditionally. This is what makes a second Apply of an
//     already-applied SOP write ZERO manifest sources: the second
//     DiffChangeset call sees identical Sources on both sides, so no
//     "sources"-field entry is even produced (T-18-09).
func applyKnowledgeEntry(ctx context.Context, deps ApplyDeps, entry ChangesetEntry, packsByID map[string]SOPPack) error {
	_ = ctx // WriteManifestSource takes no context — kept for signature symmetry.
	if entry.Kind != "changed" || entry.Field != "sources" {
		return nil
	}
	p, ok := packsByID[entry.Key]
	if !ok {
		return fmt.Errorf("knowledge pack %q not found in SOP knowledge", entry.Key)
	}

	liveSources, _ := entry.From.([]KnowledgeSource)
	livePaths := make(map[string]bool, len(liveSources))
	for _, s := range liveSources {
		livePaths[s.Path] = true
	}

	for _, s := range p.Sources {
		if livePaths[s.Path] {
			// Already present live — WriteManifestSource is append-only,
			// re-adding it would duplicate the `- path:` line
			// (P-03-knowledge-added-only).
			continue
		}
		if err := deps.Repo.WriteManifestSource(p.ID, s.Path, s.Kind); err != nil {
			return fmt.Errorf("knowledge pack %q source %q: %w", p.ID, s.Path, err)
		}
	}
	return nil
}

// applyUnlockEntry routes an "unlock" surface changeset entry through
// RepoFiles.WriteTopicMapKeyword(KeywordAdd) — already an idempotent
// add-or-replace-in-place upsert (repofile_writer.go's doc comment), so
// BOTH "added" and "changed" drive the same call. Unlock carries no weight
// field (ReadTopicMap's parseYAMLKeywords intentionally drops it — the
// store-shaped SOPDoc mirrors that same omission, sop.go's doc comment), so
// Apply always writes weight 0 (no weight line), matching the SOP's own
// fidelity rather than inventing a value the SOP never captured. u.Add[0]
// is the term's topic id — ReadTopicMap only ever populates exactly one
// entry per Unlock row.
func applyUnlockEntry(ctx context.Context, deps ApplyDeps, entry ChangesetEntry, unlocksByPhrase map[string]Unlock) error {
	_ = ctx // WriteTopicMapKeyword takes no context — kept for signature symmetry.
	u, ok := unlocksByPhrase[entry.Key]
	if !ok {
		return fmt.Errorf("unlock %q not found in SOP unlocks", entry.Key)
	}
	if len(u.Add) == 0 {
		// No topic to attach the term to — nothing writable.
		return nil
	}
	return deps.Repo.WriteTopicMapKeyword(u.Add[0], u.Phrase, 0, KeywordAdd)
}
