// Package cmd -- kv hibernate / kv wake, the third lifecycle tier
// (2026-09-10 spec §8). Sits below kv pause: where pause scales services to
// zero and leaves ~$60/mo of NAT Gateway and ALB running against no
// traffic, hibernate destroys them and lands at ~$14/mo, retaining the NAT
// EIP, DNS, certs and every durable store so wake needs no restore.
//
// Like kv pause, this command must never read, write, or reference the
// kill-switch (killswitch.go) -- they are orthogonal controls at different
// layers, and coupling them would produce a surprise at wake.
//
// Nothing here may automate a DID release: 725-404-8283 and the toll-free
// 855-916-INFO cannot be bought back once released.
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spf13/cobra"
)

// cfAssetsBucketParam is the SSM parameter holding the CloudFront/S3 asset
// bucket name for the voice site, published by the cloudfront-assets
// terraform module (infra/terraform/modules/cloudfront-assets/v1.0.0/ssm.tf)
// as /${site.label}/cloudfront-assets/${region.label}/${domain}/bucket_name.
// kv has no SITE_LABEL handling anywhere else, so this is hardcoded with
// the "kmv" site label -- the same idiom as ssmInventoryPathPrefix
// (backup.go) and the /kmv/secrets/... gate-secret params (telephony.go).
// MUST stay in step with .github/workflows/build-voice.yml, which reads
// this exact parameter as
// "/${SITE_LABEL}/cloudfront-assets/use1/voice/bucket_name".
const cfAssetsBucketParam = "/kmv/cloudfront-assets/use1/voice/bucket_name"

// HibernateDeps carries everything RunHibernateFlip needs, injected so
// orchestration never constructs an AWS/git/gh client itself -- every
// dependency is a narrow interface a test can fake.
type HibernateDeps struct {
	RepoRoot     string
	Git          GitAPI
	GH           GHAPI
	ECS          ECSAPI
	Health       TargetHealthAPI
	Net          NetworkStateAPI
	Pages        PageStoreAPI
	Cluster      string
	Services     []string
	TargetGroups map[string]string
	AssetBucket  string
	NATEIP       string
	Now          func() time.Time
	In           io.Reader
	Out          io.Writer
}

// HibernateOptions parameterizes RunHibernateFlip for both hibernate
// (Want=true) and wake (Want=false).
type HibernateOptions struct {
	Want   bool
	Yes    bool
	Reason string
	DryRun bool
	Drain  DrainOptions
}

// Commit messages for the two directions.
const (
	HibernateCommitMessage = "ops(infra): hibernate (destroy NAT, ALB, ECS services)"
	WakeCommitMessage      = "ops(infra): wake (restore NAT, ALB, ECS services)"
)

// phasesFor returns the apply order for a direction: removal order when
// hibernating, terragrunt's natural creation order when waking.
func phasesFor(want bool) []ApplyPhase {
	if want {
		return HibernatePhases
	}
	return WakePhases
}

// RunHibernateFlip implements the spec §8 command flow in order: preflight,
// idempotent read, rewrite-and-confirm, commit and push, drain, the two
// sequential apply phases, the page swap, verify, and report.
//
// Every dependency below is assumed resolved by buildHibernateDeps, which
// fails loudly (returns an error) rather than leave any of Net, Pages, or
// AssetBucket unset -- so none of those are nil/empty-guarded here. A
// silent-skip guard on the page swap or the post-apply verify would give an
// operator a `kv hibernate` that reports success while never swapping the
// maintenance page in, or never checking the ALB/NAT/EIP actually went
// away -- exactly the silent-failure class this command exists to close.
func RunHibernateFlip(ctx context.Context, deps HibernateDeps, opts HibernateOptions) error {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	phases := phasesFor(opts.Want)

	// D-08: validate before anything at all, so a bad list cannot reach the
	// account even via an early failure path.
	if err := ValidatePhases(phases); err != nil {
		return err
	}

	if err := PreflightLifecycle(ctx, deps.Git, deps.GH.AuthStatus, LifecycleBranch); err != nil {
		return err
	}

	current, err := ReadHibernatedFlagFile(deps.RepoRoot)
	if err != nil {
		return err
	}
	action := "wake"
	if opts.Want {
		action = "hibernate"
	}
	if current == opts.Want {
		fmt.Fprintf(deps.Out, "kv %s: no-op, already hibernated=%t\n", action, current)
		return nil
	}

	if opts.DryRun {
		return printHibernateDryRun(deps.Out, action, phases, deps, opts)
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
	if _, err := SetHibernatedFlagFile(deps.RepoRoot, opts.Want); err != nil {
		return err
	}
	restoreOriginal := func() error { return os.WriteFile(path, original, info.Mode()) }

	if !opts.Yes {
		diff, derr := deps.Git.Diff(ctx, SiteHCLRelPath)
		if derr != nil {
			_ = restoreOriginal()
			return derr
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

	message := WakeCommitMessage
	if opts.Want {
		message = HibernateCommitMessage
	}
	if opts.Reason != "" {
		message = message + "\n\n" + opts.Reason
	}
	if err := deps.Git.CommitPaths(ctx, message, SiteHCLRelPath); err != nil {
		return err
	}
	if err := deps.Git.Push(ctx, LifecycleBranch); err != nil {
		return err
	}

	// Phase 1 of a hibernate destroys services outright rather than scaling
	// them, so drain first -- voice tasks hold task-scale-in protection
	// while a session is live.
	if opts.Want {
		if _, derr := DrainToZero(ctx, deps.ECS, deps.Cluster, deps.Services, deps.Out, opts.Drain); derr != nil {
			return derr
		}
	}

	runIDs, err := RunApplyPhases(ctx, deps.GH, LifecycleBranch, phases, now, deps.Out)
	if err != nil {
		return err
	}

	fmt.Fprintln(deps.Out, "\nmaintenance page:")
	if opts.Want {
		page, rerr := os.ReadFile(filepath.Join(deps.RepoRoot, MaintenancePageRelPath))
		if rerr != nil {
			return fmt.Errorf("read maintenance page: %w", rerr)
		}
		if serr := SwapToMaintenance(ctx, deps.Pages, deps.AssetBucket, page, deps.Out); serr != nil {
			return serr
		}
	} else if rerr := RestoreSPA(ctx, deps.Pages, deps.AssetBucket, deps.Out); rerr != nil {
		return rerr
	}

	if opts.Want {
		v, verr := VerifyHibernated(ctx, deps.ECS, deps.Net, deps.Cluster, deps.Services, deps.NATEIP)
		if verr != nil {
			return verr
		}
		if serr := v.Err(); serr != nil {
			return serr
		}
		printHibernateReport(deps.Out, runIDs, deps.NATEIP)
		return nil
	}

	if _, werr := WaitForServicesRunning(ctx, deps.ECS, deps.Cluster, deps.Services, deps.Out,
		opts.Drain.Timeout, opts.Drain.PollInterval); werr != nil {
		return werr
	}
	targetGroups, terr := VoiceAndAuthTargetGroups(deps.TargetGroups)
	if terr != nil {
		return terr
	}
	if herr := WaitForTargetsHealthy(ctx, deps.Health, targetGroups, deps.Out,
		opts.Drain.Timeout, opts.Drain.PollInterval); herr != nil {
		return herr
	}
	printWakeReport(deps.Out, runIDs)
	return nil
}

// MaintenancePageRelPath is the repo-relative path to the committed page
// that shadows the SPA shell while hibernated.
const MaintenancePageRelPath = "apps/voice/client/public/maintenance.html"

// printHibernateDryRun reports exactly what would happen and mutates
// nothing (D-12).
func printHibernateDryRun(w io.Writer, action string, phases []ApplyPhase, deps HibernateDeps, opts HibernateOptions) error {
	fmt.Fprintf(w, "kv %s --dry-run: no dispatch, no S3 write, no invalidation.\n\n", action)
	fmt.Fprintf(w, "Would flip `hibernated` to %t in %s, commit to %s, then:\n", opts.Want, SiteHCLRelPath, LifecycleBranch)
	for i, p := range phases {
		fmt.Fprintf(w, "  phase %d: dispatch %s with modules=%q (%s)\n", i+1, TerragruntApplyWorkflow, p.Modules, p.Name)
		fmt.Fprintf(w, "           then WATCH it to terminal success before phase %d\n", i+2)
	}
	if opts.Want {
		fmt.Fprintf(w, "  then: copy s3://%s/%s -> %s and put the maintenance page\n", deps.AssetBucket, IndexKey, SPABackupKey)
		fmt.Fprintf(w, "  then: verify the ALB and NAT Gateway are gone and the EIP (%s) is retained\n", deps.NATEIP)
	} else {
		fmt.Fprintf(w, "  then: restore s3://%s/%s from %s\n", deps.AssetBucket, IndexKey, SPABackupKey)
		fmt.Fprintln(w, "  then: wait for voice and auth target groups to report healthy")
	}
	return nil
}

func printHibernateReport(w io.Writer, runIDs []string, eip string) {
	fmt.Fprintln(w, "\nkv hibernate: complete.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Cost posture: paused ~$60/mo -> hibernated ~$14/mo")
	fmt.Fprintln(w, "  (Fargate $0; NAT Gateway $0; ALB $0; retained EIP ~$3.60; misc floor ~$10.)")
	if eip != "" {
		fmt.Fprintf(w, "\nNAT EIP %s is retained (unattached) -- the VoIP.ms allowlist stays valid.\n", eip)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "While hibernated:")
	fmt.Fprintln(w, "  - voice.klankermaker.ai serves the maintenance page and still unfurls.")
	fmt.Fprintln(w, "  - auth.klankermaker.ai does not resolve to a healthy origin at all.")
	fmt.Fprintln(w, "  - the DIDs stay provisioned and BILLED by VoIP.ms; callers get a fast busy.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Manual follow-ups this command cannot do:")
	fmt.Fprintln(w, "  - The ElevenLabs subscription (Creator, $22/mo as of 2026-09-10) survives")
	fmt.Fprintln(w, "    hibernate, pause and destroy alike -- still ~1.5x the hibernated AWS bill.")
	fmt.Fprintln(w, "    It is kept deliberately, for the cloned voice; this line is a reminder,")
	fmt.Fprintln(w, "    not a recommendation to cancel.")
	fmt.Fprintln(w, "  - Releasing a DID is irreversible and stays outside this tool.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "The kill-switch was not touched -- it is a separate, application-layer")
	fmt.Fprintln(w, "mechanism (kv killswitch) and remains in whatever state it was already in.")
	if len(runIDs) > 0 {
		fmt.Fprintf(w, "\nApply runs: %v\n", runIDs)
	}
}

func printWakeReport(w io.Writer, runIDs []string) {
	fmt.Fprintln(w, "\nkv wake: complete.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Cost posture restored: hibernated ~$14/mo -> running ~$190/mo.")
	fmt.Fprintln(w, "Every ECS service is running and the voice and auth ALB target groups report healthy.")
	fmt.Fprintln(w, "The real SPA shell is back at index.html.")
	if len(runIDs) > 0 {
		fmt.Fprintf(w, "\nApply runs: %v\n", runIDs)
	}
}

// NewHibernateCmd builds "kv hibernate".
func NewHibernateCmd(cfg *Config) *cobra.Command {
	var yes, dryRun bool
	var reason string

	cmd := &cobra.Command{
		Use:   "hibernate",
		Short: "Destroy the ECS services, NAT Gateway and ALB (~$60/mo -> ~$14/mo)",
		Long: "kv hibernate flips the git-tracked `hibernated` boolean in\n" +
			"infra/terraform/live/site/site.hcl, commits and pushes it to main, then\n" +
			"dispatches TWO STRICTLY SEQUENTIAL terragrunt-apply runs: first\n" +
			"ecs-service,cloudfront (removing everything that references the ALB), then\n" +
			"network (deleting the ALB and NAT Gateway). The second is never dispatched\n" +
			"until the first has completed successfully -- they share the workflow's\n" +
			"cancel-in-progress concurrency group, so an eager dispatch would cancel a\n" +
			"half-completed destroy.\n\n" +
			"The NAT EIP, DNS, certs, DynamoDB, the S3 ledger, the cf-assets bucket and\n" +
			"ECR all survive, so `kv wake` needs no data restore. It never touches the\n" +
			"kill-switch and never releases a DID.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			deps, err := buildHibernateDeps(ctx, cfg, c, true)
			if err != nil {
				return err
			}
			return RunHibernateFlip(ctx, deps, HibernateOptions{
				Want: true, Yes: yes, Reason: reason, DryRun: dryRun,
				Drain: DrainOptions{Correct: true},
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the diff-and-confirm prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report the plan and mutate nothing")
	cmd.Flags().StringVar(&reason, "reason", "", "operator note recorded as a trailing line in the commit body")
	return cmd
}

// NewWakeCmd builds "kv wake".
func NewWakeCmd(cfg *Config) *cobra.Command {
	var yes, dryRun bool

	cmd := &cobra.Command{
		Use:   "wake",
		Short: "Rebuild the NAT Gateway, ALB and ECS services from a hibernated stack",
		Long: "kv wake mirrors kv hibernate, running the same two units in the reverse\n" +
			"order (network first, then ecs-service,cloudfront) -- which is terragrunt's\n" +
			"natural dependency order, so wake carries no ordering hazard. It restores\n" +
			"the real SPA shell over the maintenance page and does not report success\n" +
			"until the voice and auth ALB target groups report healthy.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := c.Context()
			deps, err := buildHibernateDeps(ctx, cfg, c, false)
			if err != nil {
				return err
			}
			return RunHibernateFlip(ctx, deps, HibernateOptions{Want: false, Yes: yes, DryRun: dryRun})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the diff-and-confirm prompt")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report the plan and mutate nothing")
	return cmd
}

// buildHibernateDeps resolves a repo root, wires the production seams, and
// resolves live resource identifiers via terragrunt outputs and SSM -- the
// same approach buildLifecycleDeps uses for kv pause, extended with the S3
// page store and the network unit's post-apply identity (NetworkStateAPI,
// Task 7).
//
// want is true for `kv hibernate`, false for `kv wake`. Two identifiers
// must resolve or this returns an error rather than a HibernateDeps with a
// silently-empty field (see RunHibernateFlip's doc comment):
//
//   - AssetBucket, for both directions -- a wake that cannot resolve it
//     would leave the maintenance page up forever while reporting success.
//   - NATEIP, for hibernate only -- going into a hibernate the EIP must
//     already be allocated (it is retained independently of the NAT
//     Gateway, spec §3), so an empty read here means something is already
//     wrong and hibernating on top of it would compound the problem. At
//     wake the EIP's presence isn't a precondition of the flow itself (the
//     post-apply verify that depends on it only runs for hibernate), so an
//     empty read is passed through rather than blocking the restore.
func buildHibernateDeps(ctx context.Context, cfg *Config, c *cobra.Command, want bool) (HibernateDeps, error) {
	root, err := repoRoot()
	if err != nil {
		return HibernateDeps{}, err
	}
	ecsClient, err := cfg.ECSClient(ctx)
	if err != nil {
		return HibernateDeps{}, err
	}
	elbClient, err := cfg.ELBv2Client(ctx)
	if err != nil {
		return HibernateDeps{}, err
	}
	s3Client, err := cfg.S3Client(ctx)
	if err != nil {
		return HibernateDeps{}, err
	}
	ssmClient, err := cfg.SSMClient(ctx)
	if err != nil {
		return HibernateDeps{}, err
	}

	cluster, services, targetGroups, err := ResolveECSPosture(ctx, NewTerragruntOutputReader(root))
	if err != nil {
		return HibernateDeps{}, fmt.Errorf("resolve ecs posture: %w", err)
	}

	net := NewNetworkStateAPI(NewTerragruntOutputReader(root))
	state, err := net.NetworkState(ctx)
	if err != nil {
		return HibernateDeps{}, fmt.Errorf("resolve network state: %w", err)
	}
	if want && state.NATEIPPublicIP == "" {
		return HibernateDeps{}, fmt.Errorf(
			"resolve NAT EIP: the network unit's nat_eip_public_ip output is empty -- " +
				"cannot hibernate without an already-retained EIP (see nat_gateway.retain_eip in site.hcl)")
	}

	assetBucket, err := resolveAssetBucket(ctx, ssmClient, cfAssetsBucketParam)
	if err != nil {
		return HibernateDeps{}, err
	}

	return HibernateDeps{
		RepoRoot:     root,
		Git:          NewExecGit(root),
		GH:           NewExecGH(root),
		ECS:          NewECSAPI(ecsClient),
		Health:       NewTargetHealthAPI(elbClient),
		Net:          net,
		Pages:        NewPageStoreAPI(s3Client),
		Cluster:      cluster,
		Services:     services,
		TargetGroups: targetGroups,
		AssetBucket:  assetBucket,
		NATEIP:       state.NATEIPPublicIP,
		Now:          time.Now,
		In:           c.InOrStdin(),
		Out:          c.OutOrStdout(),
	}, nil
}

// resolveAssetBucket reads the CloudFront/S3 asset bucket name from SSM
// (a plain String parameter, no decryption needed). Any failure --
// ParameterNotFound, AccessDenied, or an empty value -- is returned as an
// error rather than an empty string: see buildHibernateDeps's doc comment
// for why a silent-empty AssetBucket must not survive to RunHibernateFlip.
func resolveAssetBucket(ctx context.Context, api ssmGetParameterAPI, name string) (string, error) {
	out, err := api.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name)})
	if err != nil {
		return "", fmt.Errorf("resolve cloudfront assets bucket (%s): %w", name, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil || *out.Parameter.Value == "" {
		return "", fmt.Errorf("resolve cloudfront assets bucket (%s): empty value", name)
	}
	return *out.Parameter.Value, nil
}
