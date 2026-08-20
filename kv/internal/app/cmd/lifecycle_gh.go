package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GHAPI is the narrow seam kv pause / kv resume dispatch the existing
// terragrunt-apply.yml workflow through (D-30). Every argv this file
// assembles is provable offline against a recording fake commandRunner
// (D-31) -- no method here ever reads, constructs, or logs a credential;
// the gh binary owns its own credential storage end to end.
type GHAPI interface {
	// AuthStatus reports whether gh currently holds a valid session.
	AuthStatus(ctx context.Context) error
	// DispatchWorkflow runs `gh workflow run` for workflow at ref with one
	// -f field flag per inputs entry.
	DispatchWorkflow(ctx context.Context, workflow, ref string, inputs map[string]string) error
	// LatestRunID resolves the newest run of workflow on ref created at or
	// after notBefore, polling with a bounded backoff since dispatch and
	// run-list visibility are not atomic.
	LatestRunID(ctx context.Context, workflow, ref string, notBefore time.Time) (string, error)
	// WatchRun polls runID to a terminal conclusion, writing a
	// human-readable progress line to w on every status change. A run
	// waiting on the terraform-apply environment's required-reviewer gate
	// is reported as an ordinary waiting state, never as an error or a
	// timeout (D-19/D-23, T-16-06-03).
	WatchRun(ctx context.Context, runID string, w io.Writer) error
}

// TerragruntApplyWorkflow is the workflow file kv pause / kv resume
// dispatch (.github/workflows/terragrunt-apply.yml) -- that file is never
// created or modified by this project (D-23); it is dispatched exactly as
// it already exists.
const TerragruntApplyWorkflow = "terragrunt-apply.yml"

// EcsServiceApplyModules is the `modules` workflow_dispatch input value
// that resolves to region/us-east-1/ecs-service under
// infra/terraform/live/site (terragrunt-apply.yml's bare-name resolution,
// see its "Terragrunt Apply" step).
const EcsServiceApplyModules = "ecs-service"

// Bounded attempts to resolve the just-dispatched run id. `gh workflow
// run` and `gh run list` are not atomic -- the freshly dispatched run is
// not always visible on the very next list call -- so LatestRunID polls a
// short, bounded number of times rather than looping forever (mitigates
// T-16-06-06, denial of service via an unbounded poll).
const (
	latestRunIDMaxAttempts = 5
	latestRunIDBackoff     = 500 * time.Millisecond
	latestRunListLimit     = 20
)

// ErrLatestRunNotFound is returned when no run created at or after the
// dispatch timestamp appears within latestRunIDMaxAttempts polls -- named
// so the operator understands the dispatch may not have registered yet,
// rather than seeing a bare decode or not-found error.
var ErrLatestRunNotFound = errors.New("no matching workflow run found after dispatch")

// Run status/conclusion values `gh run view`/`gh run list --json` report,
// the subset WatchRun and LatestRunID care about. "waiting" is the status
// GitHub Actions reports while a run is held on an environment's
// required-reviewer gate (terraform-apply) -- WatchRun treats it as a
// first-class, non-error waiting state (D-19).
const (
	runStatusCompleted   = "completed"
	runStatusWaiting     = "waiting"
	runConclusionSuccess = "success"
)

// defaultWatchPollInterval is WatchRun's production poll cadence.
const defaultWatchPollInterval = 5 * time.Second

// commandRunner is the injectable seam every gh invocation in this file
// runs through. execGH's zero value is never used directly -- NewExecGH
// wires run to an exec.CommandContext-backed closure -- but every test in
// lifecycle_gh_test.go injects a recording fake instead, so every argv
// assertion runs offline and never against a real gh binary.
type commandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// execGH is the production GHAPI.
type execGH struct {
	root              string
	run               commandRunner
	sleep             func(time.Duration)
	watchPollInterval time.Duration
}

// NewExecGH builds a GHAPI rooted at root (see repoRoot() in knowledge.go),
// shelling out to gh with Dir set to root via an exec.CommandContext
// closure, following knowledge.go's stdio-wiring convention (stdout/stderr
// captured separately so a failure's error names the command without ever
// echoing the process environment).
func NewExecGH(root string) GHAPI {
	g := &execGH{
		root:              root,
		sleep:             time.Sleep,
		watchPollInterval: defaultWatchPollInterval,
	}
	g.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = g.root
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return stdout.Bytes(), nil
	}
	return g
}

// AuthStatus runs `gh auth status` and maps a non-zero exit to
// ErrPreflightGHAuth (declared in lifecycle_git.go, Task 1) so
// PreflightLifecycle's fourth check and this file's own auth probe report
// the same named refusal.
func (g *execGH) AuthStatus(ctx context.Context) error {
	if _, err := g.run(ctx, "gh", "auth", "status"); err != nil {
		return fmt.Errorf("%w: %v", ErrPreflightGHAuth, err)
	}
	return nil
}

// DispatchWorkflow runs `gh workflow run <workflow> --ref <ref> -f k=v ...`
// with one field flag per inputs entry, sorted by key for deterministic
// argv (and deterministic test assertions).
func (g *execGH) DispatchWorkflow(ctx context.Context, workflow, ref string, inputs map[string]string) error {
	args := []string{"workflow", "run", workflow, "--ref", ref}

	keys := make([]string, 0, len(inputs))
	for k := range inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-f", fmt.Sprintf("%s=%s", k, inputs[k]))
	}

	if _, err := g.run(ctx, "gh", args...); err != nil {
		return fmt.Errorf("dispatch workflow %s: %w", workflow, err)
	}
	return nil
}

// latestRunRecord decodes one element of `gh run list --json
// databaseId,createdAt,status,conclusion`.
type latestRunRecord struct {
	ID        int64  `json:"databaseId"`
	CreatedAt string `json:"createdAt"`
	Status    string `json:"status"`
}

// LatestRunID runs `gh run list` scoped to workflow and ref, requesting
// databaseId/createdAt/status/conclusion, and returns the newest run
// created at or after notBefore -- ignoring any older run for the same
// workflow (T-16-06-04: never attach to, and report the outcome of, the
// wrong run). Because dispatch and run-list visibility are not
// simultaneous, it polls with a short bounded backoff before giving up
// with ErrLatestRunNotFound.
func (g *execGH) LatestRunID(ctx context.Context, workflow, ref string, notBefore time.Time) (string, error) {
	for attempt := 0; attempt < latestRunIDMaxAttempts; attempt++ {
		out, err := g.run(ctx, "gh", "run", "list",
			"--workflow", workflow,
			"--branch", ref,
			"--json", "databaseId,createdAt,status,conclusion",
			"--limit", strconv.Itoa(latestRunListLimit),
		)
		if err != nil {
			return "", fmt.Errorf("list runs for workflow %s: %w", workflow, err)
		}

		var runs []latestRunRecord
		if err := json.Unmarshal(out, &runs); err != nil {
			return "", fmt.Errorf("list runs for workflow %s: decode: %w", workflow, err)
		}

		var newestID string
		var newestAt time.Time
		for _, r := range runs {
			createdAt, err := time.Parse(time.RFC3339, r.CreatedAt)
			if err != nil || createdAt.Before(notBefore) {
				continue
			}
			if newestID == "" || createdAt.After(newestAt) {
				newestID = strconv.FormatInt(r.ID, 10)
				newestAt = createdAt
			}
		}
		if newestID != "" {
			return newestID, nil
		}

		if attempt < latestRunIDMaxAttempts-1 {
			g.sleep(latestRunIDBackoff)
		}
	}

	return "", fmt.Errorf("%w: workflow %s on ref %s (dispatch may not have registered yet)", ErrLatestRunNotFound, workflow, ref)
}

// watchRunPayload decodes `gh run view <id> --json status,conclusion`.
type watchRunPayload struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// WatchRun polls `gh run view <runID>` to a terminal conclusion, writing a
// progress line to w on every status change. When status is "waiting" (the
// terraform-apply environment's required-reviewer gate, D-19), it writes a
// line naming that explicitly and keeps waiting -- never an error, never a
// timeout (T-16-06-03: kv must never bypass, disable, or work around that
// gate). Returns nil only on a "success" conclusion; any other terminal
// conclusion is an error naming the run id and the conclusion.
func (g *execGH) WatchRun(ctx context.Context, runID string, w io.Writer) error {
	var lastStatus string
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		out, err := g.run(ctx, "gh", "run", "view", runID, "--json", "status,conclusion")
		if err != nil {
			return fmt.Errorf("watch run %s: %w", runID, err)
		}

		var payload watchRunPayload
		if err := json.Unmarshal(out, &payload); err != nil {
			return fmt.Errorf("watch run %s: decode status: %w", runID, err)
		}

		if payload.Status != lastStatus {
			lastStatus = payload.Status
			if payload.Status == runStatusWaiting {
				fmt.Fprintf(w, "run %s is awaiting the terraform-apply reviewer approval\n", runID)
			} else {
				fmt.Fprintf(w, "run %s: %s\n", runID, payload.Status)
			}
		}

		if payload.Status == runStatusCompleted {
			if payload.Conclusion == runConclusionSuccess {
				return nil
			}
			return fmt.Errorf("run %s finished with conclusion %q", runID, payload.Conclusion)
		}

		g.sleep(g.watchPollInterval)
	}
}
