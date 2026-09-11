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
	RepoRoot string
	Git      GitAPI
	GH       GHAPI
	ECS      ECSAPI
	Health   TargetHealthAPI
	Net      NetworkStateAPI
	Pages    PageStoreAPI

	// Cluster, Services and TargetGroups are the ECS posture as resolved
	// BEFORE the applies run. Going into a hibernate that is the live
	// posture and exactly what drain and verify need. Going into a wake it
	// is the HIBERNATED posture -- the ecs-service unit derives both its
	// `services` and `target_groups` outputs as comprehensions over
	// resources that do not exist yet, so both come back empty. Wake must
	// therefore re-resolve through ResolvePosture after its applies rather
	// than gate on these.
	Cluster      string
	Services     []string
	TargetGroups map[string]string

	// ResolvePosture re-reads the ECS cluster, service names and
	// target-group ARNs from terragrunt outputs. It exists as a seam
	// (rather than orchestration constructing a TerraformOutputReader
	// itself) so the wake health gate is provable against a fake, like
	// every other dependency here. Required for wake; unused by hibernate.
	ResolvePosture func(ctx context.Context) (cluster string, services []string, targetGroups map[string]string, err error)

	AssetBucket string
	NATEIP      string
	Now         func() time.Time
	In          io.Reader
	Out         io.Writer
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
// sequential apply phases with the page swap between them (R15), verify,
// and report.
//
// Every dependency below is assumed resolved by buildHibernateDeps, which
// fails loudly (returns an error) rather than leave any of Net, Pages, or
// AssetBucket unset -- so none of those are nil/empty-guarded here. A
// silent-skip guard on the page swap or the post-apply verify would give an
// operator a `kv hibernate` that reports success while never swapping the
// maintenance page in, or never checking the ALB/NAT/EIP actually went
// away -- exactly the silent-failure class this command exists to close.
//
// THE FLAG IS NOT THE OPERATION. The flag is committed at step 4, before
// any apply runs, so finding it already in the wanted state means only that
// some earlier invocation got that far -- NOT that the applies, the page
// swap, or the verification ever happened. So an already-flipped flag skips
// exactly the rewrite/confirm/commit/push block and nothing else: the
// remaining steps re-run. They are all safe to repeat -- terragrunt applies
// are idempotent, SwapToMaintenance refuses to re-copy over an existing
// backup, and verification is read-only. This is what makes the runbook's
// "re-running kv hibernate is safe" recovery actually recover something.
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
	alreadySet := current == opts.Want

	// D-12: --dry-run mutates nothing, and it is checked before the flag
	// rewrite below so there is no path on which it can write a byte.
	if opts.DryRun {
		return printHibernateDryRun(deps.Out, action, phases, deps, opts, alreadySet)
	}

	if alreadySet {
		fmt.Fprintf(deps.Out,
			"kv %s: flag already hibernated=%t (committed by an earlier run) -- "+
				"re-running the remaining steps.\n", action, current)
	} else {
		path := filepath.Join(deps.RepoRoot, SiteHCLRelPath)
		info, serr := os.Stat(path)
		if serr != nil {
			return fmt.Errorf("stat %s: %w", SiteHCLRelPath, serr)
		}
		original, rerr := os.ReadFile(path)
		if rerr != nil {
			return fmt.Errorf("read %s: %w", SiteHCLRelPath, rerr)
		}
		if _, werr := SetHibernatedFlagFile(deps.RepoRoot, opts.Want); werr != nil {
			return werr
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
		if cerr := deps.Git.CommitPaths(ctx, message, SiteHCLRelPath); cerr != nil {
			return cerr
		}
		if perr := deps.Git.Push(ctx, LifecycleBranch); perr != nil {
			return perr
		}
	}

	// Phase 1 of a hibernate destroys services outright rather than scaling
	// them, so drain first -- voice tasks hold task-scale-in protection
	// while a session is live.
	if opts.Want {
		if _, derr := DrainToZero(ctx, deps.ECS, deps.Cluster, deps.Services, deps.Out, opts.Drain); derr != nil {
			return derr
		}
	}

	// R15: the maintenance page goes up BETWEEN the phases, not after both.
	// Phase 1 drops CloudFront's /api/* and /health behaviours, so from the
	// moment it succeeds voice.klankermaker.ai would otherwise serve a
	// live-looking mic button against a stack that cannot answer it. The
	// invariant is: the maintenance page is up whenever the stack is not
	// serving. (Wake holds the same invariant from the other side -- it
	// restores the SPA only after the health gate below.)
	var afterPhase func(context.Context, int) error
	if opts.Want {
		afterPhase = func(ctx context.Context, idx int) error {
			if idx != 0 {
				return nil
			}
			fmt.Fprintln(deps.Out, "\nmaintenance page:")
			page, rerr := os.ReadFile(filepath.Join(deps.RepoRoot, MaintenancePageRelPath))
			if rerr != nil {
				return fmt.Errorf("read maintenance page: %w", rerr)
			}
			return SwapToMaintenance(ctx, deps.Pages, deps.AssetBucket, page, deps.Out)
		}
	}

	runIDs, err := RunApplyPhases(ctx, deps.GH, LifecycleBranch, phases, now, deps.Out, afterPhase)
	if err != nil {
		return err
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

	// Wake's health gate must run against the posture the applies just
	// CREATED. deps.Cluster/Services/TargetGroups were resolved while the
	// stack was still hibernated, where the ecs-service unit's `services`
	// and `target_groups` outputs are comprehensions over resources that do
	// not exist -- both empty. Gating on those would make WaitForServicesRunning
	// pass vacuously (or fail on an empty DescribeServices) and leave
	// VoiceAndAuthTargetGroups with nothing to select, so the spec §8
	// requirement that wake not report success until voice and auth report
	// healthy would not be delivered at all.
	if deps.ResolvePosture == nil {
		return fmt.Errorf("kv wake: no posture resolver wired -- the post-apply health gate " +
			"cannot run, and reporting success without it would be exactly the silent " +
			"success this command exists to prevent")
	}
	cluster, services, allTargetGroups, perr := deps.ResolvePosture(ctx)
	if perr != nil {
		return fmt.Errorf("re-resolve ecs posture after wake: %w", perr)
	}
	if len(services) == 0 {
		return fmt.Errorf("re-resolve ecs posture after wake: the ecs-service unit reports no " +
			"services -- the apply did not actually recreate them, so there is nothing to " +
			"health-check and this wake has NOT succeeded")
	}

	if _, werr := WaitForServicesRunning(ctx, deps.ECS, cluster, services, deps.Out,
		opts.Drain.Timeout, opts.Drain.PollInterval); werr != nil {
		return werr
	}
	targetGroups, terr := VoiceAndAuthTargetGroups(allTargetGroups)
	if terr != nil {
		return terr
	}
	if herr := WaitForTargetsHealthy(ctx, deps.Health, targetGroups, deps.Out,
		opts.Drain.Timeout, opts.Drain.PollInterval); herr != nil {
		return herr
	}

	// R15, the wake half: the real SPA goes back only once the targets
	// report healthy. Restoring it before the gate puts a working-looking
	// mic button in front of services that may never come up.
	fmt.Fprintln(deps.Out, "\nmaintenance page:")
	if rerr := RestoreSPA(ctx, deps.Pages, deps.AssetBucket, deps.Out); rerr != nil {
		return rerr
	}

	// The NAT EIP's post-apply value is re-read here purely for the report
	// below -- a stale VoIP.ms allowlist is exactly the failure that "does
	// not fail at wake, it fails weeks later on the first SMS relay or CTF
	// OTP call" (lifecycle_verify.go's package doc), so wake is the one
	// place left to make it visible. A read failure is a warning, never a
	// reason to fail an otherwise-successful wake.
	postEIP, netWarning := "", ""
	if state, nerr := deps.Net.NetworkState(ctx); nerr != nil {
		netWarning = fmt.Sprintf("warning: could not re-read the NAT EIP after wake to confirm the VoIP.ms allowlist is still valid: %v", nerr)
	} else {
		postEIP = state.NATEIPPublicIP
	}

	printWakeReport(deps.Out, runIDs, deps.NATEIP, postEIP, netWarning)
	return nil
}

// MaintenancePageRelPath is the repo-relative path to the committed page
// that shadows the SPA shell while hibernated.
const MaintenancePageRelPath = "apps/voice/client/public/maintenance.html"

// printHibernateDryRun reports exactly what would happen and mutates
// nothing (D-12).
//
// It deliberately does NOT promise a CloudFront invalidation: neither
// direction issues one (R14). index.html is written no-cache/no-store by
// both build-voice.yml and our own PutObject, CloudFront honours an
// origin's Cache-Control and revalidates rather than serving the stale
// shell, so the swap takes effect on the next request and an invalidation
// would buy nothing but a new AWS SDK dependency.
//
// alreadySet means the flag is already in the wanted state, so a real run
// would skip the rewrite/commit/push and go straight to the applies.
func printHibernateDryRun(w io.Writer, action string, phases []ApplyPhase, deps HibernateDeps, opts HibernateOptions, alreadySet bool) error {
	fmt.Fprintf(w, "kv %s --dry-run: no dispatch, no S3 write, no git write.\n\n", action)
	if alreadySet {
		fmt.Fprintf(w, "`hibernated` is ALREADY %t in %s, so nothing would be committed -- "+
			"the remaining steps would simply re-run:\n", opts.Want, SiteHCLRelPath)
	} else {
		fmt.Fprintf(w, "Would flip `hibernated` to %t in %s, commit to %s, then:\n", opts.Want, SiteHCLRelPath, LifecycleBranch)
	}
	for i, p := range phases {
		fmt.Fprintf(w, "  phase %d: dispatch %s with modules=%q (%s)\n", i+1, TerragruntApplyWorkflow, p.Modules, p.Name)
		fmt.Fprintf(w, "           then WATCH it to terminal success before phase %d\n", i+2)
		if opts.Want && i == 0 {
			fmt.Fprintf(w, "           then: copy s3://%s/%s -> %s and put the maintenance page\n", deps.AssetBucket, IndexKey, SPABackupKey)
		}
	}
	if opts.Want {
		fmt.Fprintf(w, "  then: verify the ALB and NAT Gateway are gone and the EIP (%s) is retained\n", deps.NATEIP)
	} else {
		fmt.Fprintln(w, "  then: re-resolve the ECS posture the applies created")
		fmt.Fprintln(w, "  then: wait for voice and auth target groups to report healthy")
		fmt.Fprintf(w, "  then: restore s3://%s/%s from %s\n", deps.AssetBucket, IndexKey, SPABackupKey)
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

// printWakeReport reports the wake completion, then names exactly one of
// five NAT EIP outcomes so a changed or lost address -- which nothing else
// about a successful wake would surface -- is visible right here, at the
// moment the operator is looking at the output. preEIP is the address
// buildHibernateDeps resolved before the applies ran (the retained address,
// while hibernated); postEIP is re-read after. netWarning, when non-empty,
// means the post-apply re-read itself failed -- reported as a warning, not
// a reason to treat the wake as unsuccessful.
func printWakeReport(w io.Writer, runIDs []string, preEIP, postEIP, netWarning string) {
	fmt.Fprintln(w, "\nkv wake: complete.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Cost posture restored: hibernated ~$14/mo -> running ~$190/mo.")
	fmt.Fprintln(w, "Every ECS service is running and the voice and auth ALB target groups report healthy.")
	fmt.Fprintln(w, "The real SPA shell is back at index.html.")
	fmt.Fprintln(w)
	switch {
	case netWarning != "":
		fmt.Fprintln(w, netWarning)
	case preEIP == "" && postEIP == "":
		fmt.Fprintln(w, "warning: no NAT EIP is allocated after this wake -- the VoIP.ms API allowlist")
		fmt.Fprintln(w, "will not resolve; the SMS relay and the CTF OTP endpoint will fail.")
	case preEIP != "" && postEIP == preEIP:
		fmt.Fprintf(w, "NAT EIP %s is unchanged -- the VoIP.ms API allowlist is still valid.\n", postEIP)
	case preEIP == "" && postEIP != "":
		fmt.Fprintf(w, "A new NAT EIP was allocated (%s) -- none was retained going into this wake.\n", postEIP)
		fmt.Fprintln(w, "Update the VoIP.ms API allowlist now, or the SMS relay and the CTF OTP")
		fmt.Fprintln(w, "endpoint will fail SILENTLY, not with an error.")
	case preEIP != "" && postEIP == "":
		// The retained address was there going in and is not there coming
		// out -- the EIP was released during the wake. Without this case it
		// falls to default and prints "NAT EIP CHANGED: 1.2.3.4 -> " with
		// nothing after the arrow, which reads as a formatting bug rather
		// than as the loss of the address the VoIP.ms allowlist names.
		fmt.Fprintf(w, "The retained NAT EIP %s is GONE after this wake -- no EIP is allocated now.\n", preEIP)
		fmt.Fprintln(w, "The VoIP.ms API allowlist still names an address this account no longer holds,")
		fmt.Fprintln(w, "so the SMS relay and the CTF OTP endpoint will fail SILENTLY, not with an error.")
	default: // preEIP != "" && postEIP != "" && postEIP != preEIP -- a
		// different address, both sides present. The empty-postEIP variant
		// is handled by the case above, so both %s here are non-empty.
		fmt.Fprintf(w, "NAT EIP CHANGED: %s -> %s. Update the VoIP.ms API allowlist now, or the\n", preEIP, postEIP)
		fmt.Fprintln(w, "SMS relay and the CTF OTP endpoint will fail SILENTLY, not with an error.")
	}
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

	reader := NewTerragruntOutputReader(root)
	resolvePosture := func(ctx context.Context) (string, []string, map[string]string, error) {
		return ResolveECSPosture(ctx, reader)
	}

	// Resolved here for hibernate's drain and verify, which both need the
	// pre-apply (live) posture. Wake re-resolves through ResolvePosture
	// after its applies instead -- see HibernateDeps.Cluster's doc comment.
	cluster, services, targetGroups, err := resolvePosture(ctx)
	if err != nil {
		return HibernateDeps{}, fmt.Errorf("resolve ecs posture: %w", err)
	}

	net := NewNetworkStateAPI(reader)
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
		RepoRoot:       root,
		Git:            NewExecGit(root),
		GH:             NewExecGH(root),
		ECS:            NewECSAPI(ecsClient),
		Health:         NewTargetHealthAPI(elbClient),
		Net:            net,
		Pages:          NewPageStoreAPI(s3Client),
		Cluster:        cluster,
		Services:       services,
		TargetGroups:   targetGroups,
		ResolvePosture: resolvePosture,
		AssetBucket:    assetBucket,
		NATEIP:         state.NATEIPPublicIP,
		Now:            time.Now,
		In:             c.InOrStdin(),
		Out:            c.OutOrStdout(),
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
