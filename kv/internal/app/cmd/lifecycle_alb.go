// lifecycle_alb.go gates `kv resume` on observed ALB target-group health
// (D-22) rather than on a clean terragrunt apply. A clean apply that never
// reaches healthy is exactly the failure worth catching -- an empty
// target group is precisely the paused-stack shape (spec §5.5), so it is
// explicitly not treated as success.
package cmd

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
)

// TargetHealthAPI is the narrow seam WaitForTargetsHealthy polls target
// health through (D-30). Every test in lifecycle_alb_test.go injects a
// fake -- no test ever constructs a real ELBv2 client or reaches AWS
// (D-31).
type TargetHealthAPI interface {
	// DescribeTargetHealth returns the current health of every target
	// registered against targetGroupARN.
	DescribeTargetHealth(ctx context.Context, targetGroupARN string) ([]TargetState, error)
}

// TargetState is one registered target's observed health.
type TargetState struct {
	ID          string
	State       string
	Reason      string
	Description string
}

// targetHealthStateHealthy is the elasticloadbalancingv2 "healthy" target
// state literal -- the only state WaitForTargetsHealthy accepts.
const targetHealthStateHealthy = "healthy"

// elbv2ClientAPI adapts an *elasticloadbalancingv2.Client onto
// TargetHealthAPI -- the only production implementation.
type elbv2ClientAPI struct {
	api *elasticloadbalancingv2.Client
}

// NewTargetHealthAPI builds a TargetHealthAPI backed by api.
func NewTargetHealthAPI(api *elasticloadbalancingv2.Client) TargetHealthAPI {
	return &elbv2ClientAPI{api: api}
}

func (e *elbv2ClientAPI) DescribeTargetHealth(ctx context.Context, targetGroupARN string) ([]TargetState, error) {
	out, err := e.api.DescribeTargetHealth(ctx, &elasticloadbalancingv2.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(targetGroupARN),
	})
	if err != nil {
		return nil, fmt.Errorf("describe target health for %s: %w", targetGroupARN, err)
	}
	states := make([]TargetState, 0, len(out.TargetHealthDescriptions))
	for _, d := range out.TargetHealthDescriptions {
		ts := TargetState{}
		if d.Target != nil {
			ts.ID = aws.ToString(d.Target.Id)
		}
		if d.TargetHealth != nil {
			ts.State = string(d.TargetHealth.State)
			ts.Reason = string(d.TargetHealth.Reason)
			ts.Description = aws.ToString(d.TargetHealth.Description)
		}
		states = append(states, ts)
	}
	return states, nil
}

// defaultHealthTimeout is WaitForTargetsHealthy's production budget.
const defaultHealthTimeout = 10 * time.Minute

// defaultHealthPollInterval is WaitForTargetsHealthy's production poll
// cadence.
const defaultHealthPollInterval = 10 * time.Second

// maxTargetHealthDescribeErrors bounds consecutive DescribeTargetHealth
// failures before WaitForTargetsHealthy gives up rather than retrying
// forever.
const maxTargetHealthDescribeErrors = 5

// WaitForTargetsHealthy polls every named target group (keyed as
// ResolveECSPosture's target-group map is, key -> ARN) until each has at
// least one registered target and every registered target reports the
// healthy state. An empty target group is explicitly NOT satisfied -- that
// is exactly the paused-stack shape resume exists to clear (spec §5.5), so
// treating it as healthy would let resume report success while the stack
// is still returning 503. A transient DescribeTargetHealth error is
// retried; a persistent one returns a non-nil error, never a false
// success. On timeout, the returned error names every unsatisfied group
// with its last-observed target states.
func WaitForTargetsHealthy(ctx context.Context, api TargetHealthAPI, targetGroups map[string]string, w io.Writer, timeout, interval time.Duration) error {
	if timeout <= 0 {
		timeout = defaultHealthTimeout
	}
	if interval <= 0 {
		interval = defaultHealthPollInterval
	}

	keys := make([]string, 0, len(targetGroups))
	for k := range targetGroups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	start := time.Now()
	lastLine := map[string]string{}
	consecutiveErrs := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		unsatisfied := map[string][]TargetState{}
		var describeErr error

		for _, key := range keys {
			states, err := api.DescribeTargetHealth(ctx, targetGroups[key])
			if err != nil {
				describeErr = fmt.Errorf("describe target health for %q: %w", key, err)
				break
			}

			summary := summarizeTargetStates(key, states)
			if summary != lastLine[key] {
				fmt.Fprintln(w, summary)
				lastLine[key] = summary
			}

			if !targetGroupSatisfied(states) {
				unsatisfied[key] = states
			}
		}

		if describeErr != nil {
			consecutiveErrs++
			fmt.Fprintf(w, "describe-target-health error (attempt %d/%d): %v\n", consecutiveErrs, maxTargetHealthDescribeErrors, describeErr)
			if consecutiveErrs >= maxTargetHealthDescribeErrors {
				return fmt.Errorf("wait for targets healthy: persistent describe-target-health error after %d attempts: %w", consecutiveErrs, describeErr)
			}
			if err := sleepCtx(ctx, interval); err != nil {
				return err
			}
			continue
		}
		consecutiveErrs = 0

		if len(unsatisfied) == 0 {
			return nil
		}

		if time.Since(start) >= timeout {
			return fmt.Errorf("wait for targets healthy: timeout after %s waiting on: %s", timeout, describeUnsatisfiedGroups(unsatisfied))
		}

		if err := sleepCtx(ctx, interval); err != nil {
			return err
		}
	}
}

// targetGroupSatisfied reports whether every registered target in states
// is healthy. An empty slice (zero registered targets) is never
// satisfied -- see WaitForTargetsHealthy's doc comment.
func targetGroupSatisfied(states []TargetState) bool {
	if len(states) == 0 {
		return false
	}
	for _, s := range states {
		if s.State != targetHealthStateHealthy {
			return false
		}
	}
	return true
}

// summarizeTargetStates renders one target group's current state as a
// single human-readable progress line naming the group, its target count,
// and the observed states -- used both as the writer output and as the
// dedup key so an unchanged state is never printed twice in a row.
func summarizeTargetStates(key string, states []TargetState) string {
	if len(states) == 0 {
		return fmt.Sprintf("%s: 0 registered targets, awaiting registration", key)
	}
	parts := make([]string, 0, len(states))
	for _, s := range states {
		parts = append(parts, fmt.Sprintf("%s=%s", s.ID, s.State))
	}
	return fmt.Sprintf("%s: %d target(s): %s", key, len(states), strings.Join(parts, ", "))
}

// describeUnsatisfiedGroups renders every still-unsatisfied target group
// and its last-observed states for WaitForTargetsHealthy's timeout error.
func describeUnsatisfiedGroups(unsatisfied map[string][]TargetState) string {
	keys := make([]string, 0, len(unsatisfied))
	for k := range unsatisfied {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, summarizeTargetStates(k, unsatisfied[k]))
	}
	return strings.Join(parts, "; ")
}

// --------------------------------------------------------------------------
// VoiceAndAuthTargetGroups.

// voiceAndAuthTargetGroupServices are the two ECS service names whose
// target groups resume waits on (spec §5.3): telephony-edge intentionally
// has no ALB target group and its absence is not an error (D-25).
var voiceAndAuthTargetGroupServices = []string{"voice", "auth"}

// VoiceAndAuthTargetGroups selects the voice and auth entries out of all
// -- the full target-group map ResolveECSPosture returns -- matching a
// key by exact service name or by the ecs-service module's
// "<service>-<region label>-lb-<idx>" naming shape (D-30: match the real
// terraform key shape, not just the simplified fixture form). It tolerates
// the absence of a telephony-edge entry (that service has no load
// balancer) but returns an error when neither voice nor auth is present,
// so a silent empty wait is impossible.
func VoiceAndAuthTargetGroups(all map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for key, arn := range all {
		for _, svc := range voiceAndAuthTargetGroupServices {
			if targetGroupKeyMatchesService(key, svc) {
				out[key] = arn
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no voice or auth target group found among %d target group(s)", len(all))
	}
	return out, nil
}

// targetGroupKeyMatchesService reports whether key names service --
// either exactly (the simplified fixture form ResolveECSPosture's tests
// use) or as a "<service>-..." prefix (the real
// "<service>-<region_label>-lb-<idx>" key the ecs-service module's
// target_groups output produces, per
// infra/terraform/modules/ecs-service/v1.0.0/main.tf's lb_map key
// construction).
func targetGroupKeyMatchesService(key, service string) bool {
	return key == service || strings.HasPrefix(key, service+"-")
}
