// lifecycle_ecs.go closes the two real hazards of `kv pause` in code
// (D-20, D-21): the Application Auto Scaling apply-ordering race, and the
// in-flight session drain. Every ECS call sits behind the narrow ECSAPI
// seam so both hazard paths are provable against a fake, never against
// live AWS (D-30, D-31).
package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

// ECSAPI is the narrow seam DrainToZero, WaitForServicesRunning, and
// CountInFlightSessions poll and mutate ECS service state through (D-30).
// Every method a test needs to script is here and nowhere else -- no
// caller in this file constructs an *ecs.Client directly.
type ECSAPI interface {
	// DescribeServices returns the current posture (running/desired/
	// pending) of every named service in cluster.
	DescribeServices(ctx context.Context, cluster string, services []string) ([]ServicePosture, error)
	// UpdateDesiredCount issues `update-service --desired-count` for one
	// service. This is the D-20 correction call: it only ever sets a value
	// terraform already records, so it introduces no drift.
	UpdateDesiredCount(ctx context.Context, cluster, service string, desired int32) error
	// ListRunningTasks lists the task ARNs a service currently has in the
	// RUNNING desired-status.
	ListRunningTasks(ctx context.Context, cluster, service string) ([]string, error)
	// GetTaskProtection reports how many of taskARNs currently hold ECS
	// task-scale-in protection.
	GetTaskProtection(ctx context.Context, cluster string, taskARNs []string) (protectedCount int, err error)
}

// ServicePosture is one ECS service's observed task counts.
type ServicePosture struct {
	Name    string
	Desired int32
	Running int32
	Pending int32
}

// ecsClientAPI adapts an *ecs.Client onto ECSAPI -- the only production
// implementation; every test in lifecycle_ecs_test.go injects a fake
// instead, so no test ever constructs one of these.
type ecsClientAPI struct {
	api *ecs.Client
}

// NewECSAPI builds an ECSAPI backed by api.
func NewECSAPI(api *ecs.Client) ECSAPI {
	return &ecsClientAPI{api: api}
}

func (e *ecsClientAPI) DescribeServices(ctx context.Context, cluster string, services []string) ([]ServicePosture, error) {
	out, err := e.api.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  aws.String(cluster),
		Services: services,
	})
	if err != nil {
		return nil, fmt.Errorf("describe services in cluster %s: %w", cluster, err)
	}
	postures := make([]ServicePosture, 0, len(out.Services))
	for _, s := range out.Services {
		postures = append(postures, ServicePosture{
			Name:    aws.ToString(s.ServiceName),
			Desired: s.DesiredCount,
			Running: s.RunningCount,
			Pending: s.PendingCount,
		})
	}
	return postures, nil
}

func (e *ecsClientAPI) UpdateDesiredCount(ctx context.Context, cluster, service string, desired int32) error {
	_, err := e.api.UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster:      aws.String(cluster),
		Service:      aws.String(service),
		DesiredCount: aws.Int32(desired),
	})
	if err != nil {
		return fmt.Errorf("update service %s desired count to %d: %w", service, desired, err)
	}
	return nil
}

func (e *ecsClientAPI) ListRunningTasks(ctx context.Context, cluster, service string) ([]string, error) {
	out, err := e.api.ListTasks(ctx, &ecs.ListTasksInput{
		Cluster:       aws.String(cluster),
		ServiceName:   aws.String(service),
		DesiredStatus: types.DesiredStatusRunning,
	})
	if err != nil {
		return nil, fmt.Errorf("list running tasks for service %s: %w", service, err)
	}
	return out.TaskArns, nil
}

func (e *ecsClientAPI) GetTaskProtection(ctx context.Context, cluster string, taskARNs []string) (int, error) {
	if len(taskARNs) == 0 {
		return 0, nil
	}
	out, err := e.api.GetTaskProtection(ctx, &ecs.GetTaskProtectionInput{
		Cluster: aws.String(cluster),
		Tasks:   taskARNs,
	})
	if err != nil {
		return 0, fmt.Errorf("get task protection in cluster %s: %w", cluster, err)
	}
	count := 0
	for _, t := range out.ProtectedTasks {
		if t.ProtectionEnabled {
			count++
		}
	}
	return count, nil
}

// --------------------------------------------------------------------------
// Drain-to-zero (D-20, D-21).

// DrainOptions tunes DrainToZero's polling, its ten-minute default budget,
// and whether it is permitted to apply the D-20 correction at all. Every
// field is injectable so a test drives the whole hazard/timeout/grace
// state machine at test speed (a few milliseconds), never at the real
// ten-minute production budget.
type DrainOptions struct {
	// Timeout bounds the whole drain. Zero uses defaultDrainTimeout.
	Timeout time.Duration
	// PollInterval is the wait between DescribeServices polls. Zero uses
	// defaultDrainPollInterval.
	PollInterval time.Duration
	// Correct, when true, permits DrainToZero to issue the D-20
	// UpdateDesiredCount correction. When false, a service that never
	// reaches zero on its own still ends the drain in a non-nil error
	// (reaching zero is the only success condition either way) but
	// DrainToZero never mutates ECS itself.
	Correct bool
	// GraceAfterCorrection bounds how long DrainToZero waits, after
	// issuing a correction for a service, before giving up on that
	// service. Zero uses defaultGraceAfterCorrection.
	GraceAfterCorrection time.Duration
}

// defaultDrainTimeout is the production ~10-minute verification budget
// (D-20, spec 5.3 step 6).
const defaultDrainTimeout = 10 * time.Minute

// defaultDrainPollInterval is the production DescribeServices poll cadence.
const defaultDrainPollInterval = 10 * time.Second

// defaultGraceAfterCorrection is how long DrainToZero waits, after issuing
// the D-20 correction for a service, before treating that service as
// genuinely stuck.
const defaultGraceAfterCorrection = 90 * time.Second

// maxDrainDescribeErrors bounds consecutive DescribeServices failures
// before DrainToZero gives up rather than retrying forever.
const maxDrainDescribeErrors = 5

// DrainReport summarizes one DrainToZero run: how long it took, which
// services needed the D-20 correction (so a corrected pause is never
// silently indistinguishable from a clean one, D-20/T-16-07-04), the
// final observed posture of every service, and the highest in-flight
// session count observed along the way (D-21).
type DrainReport struct {
	Elapsed      time.Duration
	Corrected    []string
	Final        []ServicePosture
	InFlightPeak int
}

// DrainToZero polls DescribeServices for every named service until each
// reports both Running and Desired at zero -- the only success condition
// (D-20). It applies the §5.2 correction (UpdateDesiredCount to zero) the
// moment a service that was previously observed at zero is observed above
// zero again (an Application Auto Scaling bounce-back, detected and
// corrected immediately, never waiting out the full timeout), and also
// corrects any service still above zero once Timeout has elapsed. A
// correction is never issued for a service already at zero, and this
// function never touches autoscaling settings -- terraform already
// records desired_count = 0, which is precisely why the correction
// introduces no drift. After a correction, DrainToZero keeps polling that
// service for up to GraceAfterCorrection; a service still above zero once
// that grace window elapses makes DrainToZero return a non-nil error
// naming the service and its counts. A transient DescribeServices error is
// retried; a persistent one (maxDrainDescribeErrors consecutive failures)
// returns a non-nil error and never a false success.
func DrainToZero(ctx context.Context, api ECSAPI, cluster string, services []string, w io.Writer, opts DrainOptions) (DrainReport, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultDrainTimeout
	}
	pollInterval := opts.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultDrainPollInterval
	}
	grace := opts.GraceAfterCorrection
	if grace <= 0 {
		grace = defaultGraceAfterCorrection
	}

	start := time.Now()
	report := DrainReport{}
	everZero := map[string]bool{}
	corrected := map[string]bool{}
	correctionDeadline := map[string]time.Time{}
	consecutiveErrs := 0
	lastInFlight := -1

	for {
		if err := ctx.Err(); err != nil {
			return report, err
		}

		postures, err := api.DescribeServices(ctx, cluster, services)
		if err != nil {
			consecutiveErrs++
			fmt.Fprintf(w, "describe-services error (attempt %d/%d): %v\n", consecutiveErrs, maxDrainDescribeErrors, err)
			if consecutiveErrs >= maxDrainDescribeErrors {
				return report, fmt.Errorf("drain to zero: persistent describe-services error after %d attempts: %w", consecutiveErrs, err)
			}
			if err := sleepCtx(ctx, pollInterval); err != nil {
				return report, err
			}
			continue
		}
		consecutiveErrs = 0
		report.Final = postures
		report.Elapsed = time.Since(start)

		allZero := true
		var above []ServicePosture
		for _, p := range postures {
			zero := p.Running == 0 && p.Desired == 0
			if !zero {
				allZero = false
			}

			// Bounce-back: this service was previously observed at zero
			// and is now observed above zero again -- the §5.2 hazard
			// actually occurring. Correct immediately, without waiting
			// for the timeout.
			if everZero[p.Name] && !zero && !corrected[p.Name] {
				fmt.Fprintf(w, "correcting %s: observed desired=%d running=%d after previously reaching zero (Application Auto Scaling bounce-back, D-20)\n", p.Name, p.Desired, p.Running)
				if opts.Correct {
					if err := api.UpdateDesiredCount(ctx, cluster, p.Name, 0); err != nil {
						return report, fmt.Errorf("drain to zero: correct %s: %w", p.Name, err)
					}
				}
				corrected[p.Name] = true
				report.Corrected = append(report.Corrected, p.Name)
				correctionDeadline[p.Name] = time.Now().Add(grace)
			}
			if zero {
				everZero[p.Name] = true
			}
			if !zero {
				above = append(above, p)
			}
		}

		if allZero {
			return report, nil
		}

		fmt.Fprintf(w, "still above zero after %s:", report.Elapsed.Round(time.Second))
		for _, p := range above {
			fmt.Fprintf(w, " %s(running=%d desired=%d)", p.Name, p.Running, p.Desired)
		}
		fmt.Fprintln(w)

		// D-21: report in-flight sessions rather than appearing hung.
		inFlight, ferr := CountInFlightSessions(ctx, api, cluster, services)
		if ferr != nil {
			fmt.Fprintf(w, "in-flight session count unavailable: %v\n", ferr)
		} else {
			if inFlight > report.InFlightPeak {
				report.InFlightPeak = inFlight
			}
			if inFlight > 0 && inFlight != lastInFlight {
				fmt.Fprintf(w, "waiting for %d in-flight session(s) to drain\n", inFlight)
			}
			lastInFlight = inFlight
		}

		// D-20 timeout correction: any service still above zero once the
		// budget has elapsed gets corrected here too, not just on a
		// bounce-back.
		if report.Elapsed >= timeout {
			for _, p := range above {
				if corrected[p.Name] {
					continue
				}
				fmt.Fprintf(w, "correcting %s: still above zero after the %s drain timeout (desired=%d running=%d, D-20)\n", p.Name, timeout, p.Desired, p.Running)
				if opts.Correct {
					if err := api.UpdateDesiredCount(ctx, cluster, p.Name, 0); err != nil {
						return report, fmt.Errorf("drain to zero: correct %s: %w", p.Name, err)
					}
				}
				corrected[p.Name] = true
				report.Corrected = append(report.Corrected, p.Name)
				correctionDeadline[p.Name] = time.Now().Add(grace)
			}
		}

		// A service that has been corrected and is still above zero once
		// its own grace window has elapsed is genuinely stuck -- reaching
		// zero is the only success condition, so this is an error, never
		// a best-effort success.
		var stuck []string
		now := time.Now()
		for _, p := range above {
			deadline, ok := correctionDeadline[p.Name]
			if ok && now.After(deadline) {
				stuck = append(stuck, fmt.Sprintf("%s (running=%d desired=%d)", p.Name, p.Running, p.Desired))
			}
		}
		if len(stuck) > 0 {
			return report, fmt.Errorf("drain to zero: service(s) still above zero after correction and the grace window: %s", strings.Join(stuck, ", "))
		}

		if err := sleepCtx(ctx, pollInterval); err != nil {
			return report, err
		}
	}
}

// WaitForServicesRunning is resume's counterpart to DrainToZero: it
// returns once every named service reports Desired above zero and
// Running at or above Desired. Resume has no equivalent §5.2 ordering
// hazard (the same ordering works in its favour, spec 5.2), so this
// function has no correction path -- it only observes.
func WaitForServicesRunning(ctx context.Context, api ECSAPI, cluster string, services []string, w io.Writer, timeout, interval time.Duration) ([]ServicePosture, error) {
	if timeout <= 0 {
		timeout = defaultDrainTimeout
	}
	if interval <= 0 {
		interval = defaultDrainPollInterval
	}

	start := time.Now()
	consecutiveErrs := 0

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		postures, err := api.DescribeServices(ctx, cluster, services)
		if err != nil {
			consecutiveErrs++
			fmt.Fprintf(w, "describe-services error (attempt %d/%d): %v\n", consecutiveErrs, maxDrainDescribeErrors, err)
			if consecutiveErrs >= maxDrainDescribeErrors {
				return nil, fmt.Errorf("wait for services running: persistent describe-services error after %d attempts: %w", consecutiveErrs, err)
			}
			if err := sleepCtx(ctx, interval); err != nil {
				return nil, err
			}
			continue
		}
		consecutiveErrs = 0

		allRunning := true
		for _, p := range postures {
			if p.Desired <= 0 || p.Running < p.Desired {
				allRunning = false
			}
		}
		if allRunning {
			return postures, nil
		}

		elapsed := time.Since(start)
		fmt.Fprintf(w, "waiting for services to reach their desired running count after %s:", elapsed.Round(time.Second))
		for _, p := range postures {
			fmt.Fprintf(w, " %s(running=%d desired=%d)", p.Name, p.Running, p.Desired)
		}
		fmt.Fprintln(w)

		if elapsed >= timeout {
			return postures, fmt.Errorf("wait for services running: timeout after %s", timeout)
		}

		if err := sleepCtx(ctx, interval); err != nil {
			return nil, err
		}
	}
}

// --------------------------------------------------------------------------
// In-flight session reporting (D-21).

// CountInFlightSessions sums the ECS task-scale-in-protected task count
// across services, by listing each service's running tasks and asking ECS
// for their protection state. Only voice tasks are expected to carry
// protection today, but this does not special-case the service name, so
// the count stays correct if telephony-edge later adopts the same
// mechanism.
//
// What was found in the voice service: protection is a per-TASK boolean
// the process drives to track its OWN live session count -- protection ON
// iff active_session_count() > 0 -- via the ECS control-plane
// UpdateTaskProtection API called through boto3 directly (NOT the ECS
// agent's local metadata/protection endpoint), serialized under a process
// lock so a start()/release() race can never strand protection ON with
// zero sessions. See apps/voice/src/klanker_voice/session.py:475-509
// (_set_scale_in_protection / _reconcile_scale_in_protection).
func CountInFlightSessions(ctx context.Context, api ECSAPI, cluster string, services []string) (int, error) {
	total := 0
	for _, svc := range services {
		tasks, err := api.ListRunningTasks(ctx, cluster, svc)
		if err != nil {
			return 0, fmt.Errorf("count in-flight sessions: list running tasks for %s: %w", svc, err)
		}
		if len(tasks) == 0 {
			continue
		}
		protected, err := api.GetTaskProtection(ctx, cluster, tasks)
		if err != nil {
			return 0, fmt.Errorf("count in-flight sessions: get task protection for %s: %w", svc, err)
		}
		total += protected
	}
	return total, nil
}

// --------------------------------------------------------------------------
// Shared helpers.

// sleepCtx sleeps for d, returning early with ctx.Err() if ctx is
// cancelled first -- shared by DrainToZero, WaitForServicesRunning
// (this file), and WaitForTargetsHealthy (lifecycle_alb.go) so no poll
// loop in this package can outlive a cancelled context.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
