// Package cmd — kv pause / kv resume: the operator-facing lifecycle
// commands that assemble the pieces built across 16-05 (the paused-flag
// mechanism, lifecycle_pauseflag.go), 16-06 (the git preflight seam and gh
// workflow dispatch/tracking, lifecycle_git.go/lifecycle_gh.go), and 16-07
// (the ECS drain-to-zero hazard closure and the ALB resume health gate,
// lifecycle_ecs.go/lifecycle_alb.go) into the spec §5.3 command flow. See
// docs/superpowers/specs/2026-08-12-pause-backup-teardown-design.md §5 for
// the full design and docs/ops/pause-resume.md for the operator runbook.
//
// This command must never read, write, or reference the kill-switch
// mechanism defined in killswitch.go -- they are orthogonal controls at
// different layers (D-24), and PrintPauseReport says so explicitly in every
// report.
package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// LifecycleDeps carries everything RunLifecycleFlip needs to run the §5.3
// flow, injected so orchestration never constructs an AWS/git/gh client
// itself (D-30) -- every dependency is a narrow interface a test can fake.
type LifecycleDeps struct {
	RepoRoot     string
	Git          GitAPI
	GH           GHAPI
	ECS          ECSAPI
	Health       TargetHealthAPI
	Cluster      string
	Services     []string
	TargetGroups map[string]string
	Now          func() time.Time
	In           io.Reader
	Out          io.Writer
}

// LifecycleFlipOptions parameterizes RunLifecycleFlip for both pause
// (Want=true) and resume (Want=false, spec §5.3: "kv resume mirrors it").
type LifecycleFlipOptions struct {
	Want           bool
	Yes            bool
	Reason         string
	RequiredBranch string
	Drain          DrainOptions
}

// ErrLifecycleCancelled is returned when the operator declines the
// show-the-diff-and-confirm prompt, or when a non-interactive stdin offers
// nothing to read without --yes -- neither is ever treated as an implied
// yes (D-18).
var ErrLifecycleCancelled = errors.New("pause/resume cancelled: operator did not confirm")

// pauseSuccessLine / resumeSuccessLine are the literal completion markers
// PrintPauseReport emits -- RunLifecycleFlip must never write either before
// every prior step (including, for resume, the D-22 health wait) has
// returned nil.
const (
	pauseSuccessLine  = "kv pause: complete."
	resumeSuccessLine = "kv resume: complete."
)

// RunLifecycleFlip implements the spec §5.3 command flow in order:
// preflight, an idempotent read (already in the wanted state is a clean
// no-op), rewrite-and-confirm with a shown diff (skippable with
// opts.Yes), commit and push to opts.RequiredBranch, dispatch the
// terragrunt-apply workflow and stream it to completion, verify (drain to
// zero when pausing; wait for services running then target-group health
// when resuming), and finally report. It returns non-nil on the first
// failing step and never emits a success line before every step has
// succeeded.
func RunLifecycleFlip(ctx context.Context, deps LifecycleDeps, opts LifecycleFlipOptions) error {
	requiredBranch := opts.RequiredBranch
	if requiredBranch == "" {
		requiredBranch = LifecycleBranch
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}

	if err := PreflightLifecycle(ctx, deps.Git, deps.GH.AuthStatus, requiredBranch); err != nil {
		return err
	}

	current, err := ReadPausedFlagFile(deps.RepoRoot)
	if err != nil {
		return err
	}
	if current == opts.Want {
		action := "pause"
		if !opts.Want {
			action = "resume"
		}
		fmt.Fprintf(deps.Out, "kv %s: no-op, already paused=%t\n", action, current)
		return nil
	}

	path := filepath.Join(deps.RepoRoot, SiteHCLRelPath)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", SiteHCLRelPath, err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", SiteHCLRelPath, err)
	}

	if _, err := SetPausedFlagFile(deps.RepoRoot, opts.Want); err != nil {
		return err
	}
	restoreOriginal := func() error {
		return os.WriteFile(path, original, info.Mode())
	}

	if !opts.Yes {
		diff, err := deps.Git.Diff(ctx, SiteHCLRelPath)
		if err != nil {
			_ = restoreOriginal()
			return err
		}
		fmt.Fprintln(deps.Out, diff)
		fmt.Fprint(deps.Out, "Proceed? [y/N] ")

		if !confirmAffirmative(deps.In) {
			if rerr := restoreOriginal(); rerr != nil {
				return fmt.Errorf("restore %s after cancellation: %w", SiteHCLRelPath, rerr)
			}
			return ErrLifecycleCancelled
		}
	}

	message := PauseCommitMessage
	if !opts.Want {
		message = ResumeCommitMessage
	}
	if opts.Reason != "" {
		message = message + "\n\n" + opts.Reason
	}
	if err := deps.Git.CommitPaths(ctx, message, SiteHCLRelPath); err != nil {
		return err
	}
	if err := deps.Git.Push(ctx, requiredBranch); err != nil {
		return err
	}

	dispatchedAt := now()
	if err := deps.GH.DispatchWorkflow(ctx, TerragruntApplyWorkflow, requiredBranch, map[string]string{"modules": EcsServiceApplyModules}); err != nil {
		return err
	}
	runID, err := deps.GH.LatestRunID(ctx, TerragruntApplyWorkflow, requiredBranch, dispatchedAt)
	if err != nil {
		return err
	}
	if err := deps.GH.WatchRun(ctx, runID, deps.Out); err != nil {
		return fmt.Errorf("ci run %s did not complete successfully: %w", runID, err)
	}

	report := PauseReport{Paused: opts.Want, RunID: runID}
	start := now()

	if opts.Want {
		drainReport, derr := DrainToZero(ctx, deps.ECS, deps.Cluster, deps.Services, deps.Out, opts.Drain)
		report.Drain = drainReport
		report.Corrected = drainReport.Corrected
		report.Elapsed = now().Sub(start)
		if derr != nil {
			return derr
		}
	} else {
		if _, werr := WaitForServicesRunning(ctx, deps.ECS, deps.Cluster, deps.Services, deps.Out, opts.Drain.Timeout, opts.Drain.PollInterval); werr != nil {
			return werr
		}
		targetGroups, terr := VoiceAndAuthTargetGroups(deps.TargetGroups)
		if terr != nil {
			return terr
		}
		// D-22: no success line may be emitted before this returns nil.
		if herr := WaitForTargetsHealthy(ctx, deps.Health, targetGroups, deps.Out, opts.Drain.Timeout, opts.Drain.PollInterval); herr != nil {
			return herr
		}
		report.Elapsed = now().Sub(start)
	}

	PrintPauseReport(deps.Out, report)
	return nil
}

// confirmAffirmative reads a single line from r and reports whether it is
// an affirmative answer (y/yes, case-insensitive, surrounding whitespace
// trimmed). Anything else -- an explicit "no", garbage, or EOF from an
// empty/non-interactive stdin with nothing to read -- is a refusal, never
// an implied yes (D-18).
func confirmAffirmative(r io.Reader) bool {
	if r == nil {
		return false
	}
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}

// --------------------------------------------------------------------------
// PauseReport / PrintPauseReport.

// PauseReport summarizes one RunLifecycleFlip run for the operator-facing
// completion report (D-25).
type PauseReport struct {
	Paused    bool
	Drain     DrainReport
	Elapsed   time.Duration
	RunID     string
	Corrected []string
}

// PrintPauseReport writes the operator-facing completion report for a
// finished pause or resume. For a pause, it states the resulting cost
// posture, names ElevenLabs Pro ($99/mo) as the largest remaining line item
// during a long pause and that pausing/downgrading it is a manual
// vendor-console step, documents paused behaviour (the voice page still
// loads and only the mic tap 503s; the auth host is ALB-only and 503s; the
// DIDs stay provisioned and callers get a fast busy), states the kill-switch
// was not touched and remains a separate mechanism, and states that a
// mid-pause CI deploy re-applies desired_count=0 and leaves the stack paused
// because the config is the guard (D-23). When r.Corrected is non-empty, it
// names each corrected service and states that the Application Auto Scaling
// ordering correction fired (D-20). For a resume, it states the restored
// cost posture and that the voice/auth target groups reported healthy.
func PrintPauseReport(w io.Writer, r PauseReport) {
	if r.Paused {
		printPausedReport(w, r)
		return
	}
	printResumedReport(w, r)
}

func printPausedReport(w io.Writer, r PauseReport) {
	fmt.Fprintln(w, pauseSuccessLine)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Cost posture: running ~$190/mo -> paused ~$60/mo (Fargate now $0/mo; NAT+ALB+WAF+misc ~$60/mo continues).")
	fmt.Fprintln(w, "ElevenLabs Pro ($99/mo) is unaffected by this command and becomes the largest remaining")
	fmt.Fprintln(w, "line item during a long pause; pausing or downgrading it is a manual step in the")
	fmt.Fprintln(w, "ElevenLabs console -- not something this command can do.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "While paused:")
	fmt.Fprintln(w, "  - voice.klankermaker.ai still loads from CloudFront/S3; only the mic tap fails (/api/offer -> 503).")
	fmt.Fprintln(w, "  - auth.klankermaker.ai is ALB-only and returns 503.")
	fmt.Fprintln(w, "  - the DIDs stay provisioned and billed by VoIP.ms; SIP registration drops and callers get a fast busy.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "The kill-switch was not touched -- it is a separate, application-layer mechanism")
	fmt.Fprintln(w, "(kv killswitch) and remains in whatever state it was already in.")
	fmt.Fprintln(w, "A CI deploy landing while paused re-applies desired_count=0 from site.hcl and")
	fmt.Fprintln(w, "leaves the stack paused -- the config is the guard, so this is safe by construction.")
	if len(r.Corrected) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Application Auto Scaling ordering correction fired for: %s\n", strings.Join(r.Corrected, ", "))
		fmt.Fprintln(w, "(kv corrected desired_count back to 0 itself; terraform already recorded 0, so no drift was introduced.)")
	}
	if r.RunID != "" {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Apply run: %s (verified in %s)\n", r.RunID, r.Elapsed.Round(time.Second))
	}
}

func printResumedReport(w io.Writer, r PauseReport) {
	fmt.Fprintln(w, resumeSuccessLine)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Cost posture restored: paused ~$60/mo -> running ~$190/mo.")
	fmt.Fprintln(w, "Every ECS service is running and the voice and auth ALB target groups report healthy.")
	if r.RunID != "" {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Apply run: %s (verified in %s)\n", r.RunID, r.Elapsed.Round(time.Second))
	}
}

// --------------------------------------------------------------------------
// Cobra commands.

// buildLifecycleDeps resolves a repo root, wires the production GitAPI/
// GHAPI/ECSAPI/TargetHealthAPI implementations, and resolves the live ECS
// cluster/service/target-group names via ResolveECSPosture (D-30).
func buildLifecycleDeps(ctx context.Context, cfg *Config, c *cobra.Command) (LifecycleDeps, error) {
	root, err := repoRoot()
	if err != nil {
		return LifecycleDeps{}, err
	}
	ecsClient, err := cfg.ECSClient(ctx)
	if err != nil {
		return LifecycleDeps{}, err
	}
	elbClient, err := cfg.ELBv2Client(ctx)
	if err != nil {
		return LifecycleDeps{}, err
	}

	cluster, services, targetGroups, err := ResolveECSPosture(ctx, NewTerragruntOutputReader(root))
	if err != nil {
		return LifecycleDeps{}, fmt.Errorf("resolve ecs posture: %w", err)
	}

	return LifecycleDeps{
		RepoRoot:     root,
		Git:          NewExecGit(root),
		GH:           NewExecGH(root),
		ECS:          NewECSAPI(ecsClient),
		Health:       NewTargetHealthAPI(elbClient),
		Cluster:      cluster,
		Services:     services,
		TargetGroups: targetGroups,
		Now:          time.Now,
		In:           c.InOrStdin(),
		Out:          c.OutOrStdout(),
	}, nil
}

// NewPauseCmd builds the "kv pause" command (D-01/D-03/D-15): scales every
// ECS service to zero via the git-tracked `paused` flag in site.hcl,
// dispatches the existing terragrunt-apply workflow, and verifies with the
// D-20 drain-to-zero hazard closure. A "status" subcommand reports the flag
// value alongside each service's live desired/running counts without ever
// committing, pushing, or dispatching anything.
func NewPauseCmd(cfg *Config) *cobra.Command {
	var yes bool
	var reason string

	pauseCmd := &cobra.Command{
		Use:   "pause",
		Short: "Scale every ECS service to zero (~$190/mo -> ~$60/mo, D-15..D-25)",
		Long: "kv pause flips the git-tracked `paused` boolean in\n" +
			"infra/terraform/live/site/site.hcl, commits and pushes it to main, dispatches\n" +
			"the terragrunt-apply workflow (gated by the terraform-apply environment's\n" +
			"required-reviewer rule), and verifies every service reaches desired=running=0,\n" +
			"correcting the Application Auto Scaling apply-ordering hazard itself if needed\n" +
			"(D-20). It never reads, writes, or references the kill-switch (D-24) -- see\n" +
			"`kv killswitch` for that separate, instant control.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			deps, err := buildLifecycleDeps(ctx, cfg, c)
			if err != nil {
				return err
			}
			opts := LifecycleFlipOptions{
				Want:           true,
				Yes:            yes,
				Reason:         reason,
				RequiredBranch: LifecycleBranch,
				Drain:          DrainOptions{Correct: true},
			}
			return RunLifecycleFlip(ctx, deps, opts)
		},
	}
	pauseCmd.Flags().BoolVar(&yes, "yes", false, "skip the diff-and-confirm prompt")
	pauseCmd.Flags().StringVar(&reason, "reason", "", "operator note recorded as a trailing line in the commit body")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show the paused flag and each service's live desired/running counts",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			deps, err := buildLifecycleDeps(ctx, cfg, c)
			if err != nil {
				return err
			}
			paused, err := ReadPausedFlagFile(deps.RepoRoot)
			if err != nil {
				return err
			}
			postures, err := deps.ECS.DescribeServices(ctx, deps.Cluster, deps.Services)
			if err != nil {
				return err
			}
			return printPauseStatus(c.OutOrStdout(), paused, postures)
		},
	}
	pauseCmd.AddCommand(status)

	return pauseCmd
}

// printPauseStatus prints the paused flag value and each service's live
// desired/running counts side by side, so a flag-versus-reality divergence
// (e.g. a half-applied pause) is directly observable.
func printPauseStatus(w io.Writer, paused bool, postures []ServicePosture) error {
	fmt.Fprintf(w, "paused flag (site.hcl): %t\n\n", paused)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SERVICE\tDESIRED\tRUNNING")
	for _, p := range postures {
		fmt.Fprintf(tw, "%s\t%d\t%d\n", p.Name, p.Desired, p.Running)
	}
	return tw.Flush()
}

// NewResumeCmd builds the "kv resume" command (D-22): mirrors kv pause but
// flips the flag back to false and additionally waits for the voice and
// auth ALB target groups to report healthy before reporting success -- a
// clean apply that never reaches healthy is exactly the failure worth
// catching.
func NewResumeCmd(cfg *Config) *cobra.Command {
	var yes bool

	resumeCmd := &cobra.Command{
		Use:   "resume",
		Short: "Scale every ECS service back up and wait for voice/auth targets to report healthy (D-22)",
		Long: "kv resume mirrors kv pause: it flips `paused` back to false in\n" +
			"site.hcl, commits, pushes, and dispatches the terragrunt-apply workflow, then\n" +
			"waits for every service to reach its running count AND for the voice and auth\n" +
			"ALB target groups to report healthy before printing a success line. It never\n" +
			"reports success on a clean apply alone -- an apply that never reaches healthy\n" +
			"is exactly the failure this gate exists to catch (D-22).",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			deps, err := buildLifecycleDeps(ctx, cfg, c)
			if err != nil {
				return err
			}
			opts := LifecycleFlipOptions{
				Want:           false,
				Yes:            yes,
				RequiredBranch: LifecycleBranch,
			}
			return RunLifecycleFlip(ctx, deps, opts)
		},
	}
	resumeCmd.Flags().BoolVar(&yes, "yes", false, "skip the diff-and-confirm prompt")

	return resumeCmd
}
