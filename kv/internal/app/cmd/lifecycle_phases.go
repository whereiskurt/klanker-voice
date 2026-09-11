// Package cmd -- the two-phase sequential apply engine behind kv hibernate
// and kv wake (2026-09-10 spec §4).
//
// Terragrunt applies dependencies FIRST: network before ecs-service. That
// is right for creation and exactly backwards for removal -- a single apply
// would try to delete the ALB while ecs-service still holds
// aws_lb_listener_rule resources bound to its listener, and fail partway,
// leaving the stack half torn down.
//
// So removal is two applies, and they must be STRICTLY sequential.
// terragrunt-apply.yml declares
//
//	concurrency: { group: "${{ github.workflow }}-${{ github.ref }}",
//	               cancel-in-progress: true }
//
// and both phases dispatch against the same ref, so they land in the SAME
// concurrency group. Dispatching phase 2 while phase 1 is still running
// CANCELS PHASE 1 MID-APPLY -- with a half-completed destroy inside it.
// That is why RunApplyPhases watches each run to a terminal state before
// dispatching the next, and why no flag (including --yes) may relax it.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ApplyPhase is one dispatch of terragrunt-apply.yml: a human-readable
// name for the report, and the exact `modules` workflow input.
type ApplyPhase struct {
	Name    string
	Modules string
}

// HibernatePhases is the removal order. Phase 1 removes everything that
// references the ALB (the services with their target groups and listener
// rules, and CloudFront's ALB origin); only then can phase 2 delete the
// ALB and the NAT Gateway.
var HibernatePhases = []ApplyPhase{
	{Name: "services and CloudFront origin", Modules: "ecs-service,cloudfront"},
	{Name: "ALB and NAT Gateway", Modules: "network"},
}

// WakePhases is the creation order -- the reverse of HibernatePhases, and
// terragrunt's own natural dependency order, so wake carries no equivalent
// ordering hazard.
var WakePhases = []ApplyPhase{
	{Name: "ALB and NAT Gateway", Modules: "network"},
	{Name: "services and CloudFront origin", Modules: "ecs-service,cloudfront"},
}

// ErrEmptyModuleList is returned when a phase carries no modules.
// terragrunt-apply.yml documents its `modules` input as "empty = all", so
// an empty string is not a harmless default -- it applies EVERY unit,
// including `email`. See ErrForbiddenModule.
var ErrEmptyModuleList = errors.New("phase has an empty module list, which terragrunt-apply.yml treats as apply-all")

// ErrForbiddenModule is returned when a phase names a unit that must never
// be applied by these commands.
var ErrForbiddenModule = errors.New("phase names a forbidden module")

// forbiddenModules are units these commands must never apply. `email` owns
// aws_ses_active_receipt_rule_set, and SES allows exactly ONE active
// receipt rule set per account per region -- applying it steals the slot
// and silently kills klanker-maker's inbound mail. See
// docs/operators/ses-active-rule-set-fix.md; the underlying defect is
// unfixed, so this guard is load-bearing.
//
// NOTE ON MATCHING: keys are BARE unit names, and ValidatePhases compares
// them against the comma-separated `modules` string verbatim. That is
// sufficient today because every phase list in this package is a package
// constant naming bare units (HibernatePhases, WakePhases). It is NOT
// sufficient in general: terragrunt-apply.yml also accepts
// slash-qualified paths (e.g. "global/email", "region/us-east-1/email"),
// and "global/email" would sail straight past this map. So if a
// user-facing `--modules` flag is ever added, it must normalise each
// entry to its last path segment before this check -- otherwise the guard
// silently stops protecting another project's inbound mail.
var forbiddenModules = map[string]bool{"email": true}

// ValidatePhases checks every phase before any dispatch happens, so a bad
// list fails before it can touch the account.
func ValidatePhases(phases []ApplyPhase) error {
	if len(phases) == 0 {
		return fmt.Errorf("%w: no phases", ErrEmptyModuleList)
	}
	for _, p := range phases {
		if strings.TrimSpace(p.Modules) == "" {
			return fmt.Errorf("%w: phase %q", ErrEmptyModuleList, p.Name)
		}
		for _, m := range strings.Split(p.Modules, ",") {
			if forbiddenModules[strings.TrimSpace(m)] {
				return fmt.Errorf("%w: phase %q names %q", ErrForbiddenModule, p.Name, strings.TrimSpace(m))
			}
		}
	}
	return nil
}

// RunApplyPhases dispatches each phase in order and watches it to a
// terminal state before dispatching the next, returning the run id of every
// completed phase. It returns on the first failing phase without
// dispatching any later one (D-07).
//
// afterPhase, when non-nil, runs after each phase reaches terminal success
// and before the next phase is dispatched, receiving that phase's zero-based
// index. It is the hook `kv hibernate` swaps the maintenance page in through
// (R15): the page must be up from the moment phase 1 drops CloudFront's
// /api/* behaviour, not only once phase 2 has also succeeded. An error from
// afterPhase aborts the whole run and no later phase is dispatched -- the
// same bar a failed phase clears, for the same reason: it is safer to leave
// the ALB standing and re-run than to tear it down behind a page swap that
// did not happen.
func RunApplyPhases(ctx context.Context, gh GHAPI, ref string, phases []ApplyPhase, now func() time.Time, w io.Writer, afterPhase func(context.Context, int) error) ([]string, error) {
	if err := ValidatePhases(phases); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}

	runIDs := make([]string, 0, len(phases))
	for i, p := range phases {
		fmt.Fprintf(w, "\nphase %d/%d: %s (modules=%s)\n", i+1, len(phases), p.Name, p.Modules)

		dispatchedAt := now()
		if err := gh.DispatchWorkflow(ctx, TerragruntApplyWorkflow, ref, map[string]string{"modules": p.Modules}); err != nil {
			return runIDs, fmt.Errorf("phase %d (%s) dispatch: %w", i+1, p.Modules, err)
		}
		runID, err := gh.LatestRunID(ctx, TerragruntApplyWorkflow, ref, dispatchedAt)
		if err != nil {
			return runIDs, fmt.Errorf("phase %d (%s) resolve run id: %w", i+1, p.Modules, err)
		}

		// The sequential gate. Nothing below dispatches until this returns.
		if err := gh.WatchRun(ctx, runID, w); err != nil {
			return runIDs, fmt.Errorf("phase %d (%s) run %s did not complete successfully: %w -- "+
				"later phases were NOT dispatched; the stack is part-way through and "+
				"`kv pause status` will show what actually applied", i+1, p.Modules, runID, err)
		}
		runIDs = append(runIDs, runID)

		if afterPhase != nil {
			if err := afterPhase(ctx, i); err != nil {
				return runIDs, fmt.Errorf("after phase %d (%s): %w -- "+
					"later phases were NOT dispatched", i+1, p.Modules, err)
			}
		}
	}
	return runIDs, nil
}
