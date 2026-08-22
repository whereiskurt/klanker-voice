package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// --------------------------------------------------------------------------
// fakeDrainECS: a small stateful ECS fake for DrainToZero/
// WaitForServicesRunning. DescribeServices reflects live per-service state
// that UpdateDesiredCount (and the scripted profiles below) can change
// between calls -- deterministic and call-count driven, never dependent on
// a wall-clock race, so bounce-back/timeout/grace assertions are exact
// (D-31: no test here constructs a real ECS client or reaches AWS).

// serviceProfile is one service's scripted behavior in fakeDrainECS.
type serviceProfile struct {
	running, desired int32

	// bounceOnce: once this service is observed at running=0/desired=0,
	// the NEXT DescribeServices call bounces it back to 1/1 exactly once
	// -- simulating Application Auto Scaling scaling back out to
	// MinCapacity in the §5.2 ordering-hazard window.
	bounceOnce    bool
	bounced       bool
	pendingBounce bool

	// naturalZeroAtCall, when > 0: on and after this 1-based
	// DescribeServices call count, force running=desired=0 -- simulates
	// an ordinary drain completing with no hazard and no correction.
	naturalZeroAtCall int

	// rampToRunningAtCall, when > 0: on and after this 1-based
	// DescribeServices call count, force running=desired -- simulates
	// tasks starting up during a resume wait.
	rampToRunningAtCall int

	// ignoreUpdate simulates a genuinely wedged task: UpdateDesiredCount
	// is recorded but never changes this service's observed state.
	ignoreUpdate bool
}

type drainUpdateCall struct {
	Cluster string
	Service string
	Desired int32
}

// fakeDrainECS implements ECSAPI entirely in memory.
type fakeDrainECS struct {
	profiles map[string]*serviceProfile

	describeCalls     int
	descErrsRemaining int
	descPermanentErr  bool

	updateCalls []drainUpdateCall

	// In-flight-session scripting, keyed by service name.
	tasksByService        map[string][]string
	protectedByCall       map[string][]int
	protectErrByCall      map[string][]error
	protectCallsByService map[string]int
}

func newFakeDrainECS(profiles map[string]*serviceProfile) *fakeDrainECS {
	return &fakeDrainECS{
		profiles:              profiles,
		tasksByService:        map[string][]string{},
		protectedByCall:       map[string][]int{},
		protectErrByCall:      map[string][]error{},
		protectCallsByService: map[string]int{},
	}
}

func (f *fakeDrainECS) DescribeServices(_ context.Context, _ string, services []string) ([]ServicePosture, error) {
	f.describeCalls++
	if f.descPermanentErr {
		return nil, errors.New("simulated permanent describe-services failure")
	}
	if f.descErrsRemaining > 0 {
		f.descErrsRemaining--
		return nil, errors.New("simulated transient describe-services failure")
	}

	var out []ServicePosture
	for _, name := range services {
		p := f.profiles[name]
		if p == nil {
			out = append(out, ServicePosture{Name: name})
			continue
		}
		if p.pendingBounce {
			p.running, p.desired = 1, 1
			p.bounced = true
			p.pendingBounce = false
		}
		if p.naturalZeroAtCall > 0 && f.describeCalls >= p.naturalZeroAtCall {
			p.running, p.desired = 0, 0
		}
		if p.rampToRunningAtCall > 0 && f.describeCalls >= p.rampToRunningAtCall {
			p.running = p.desired
		}
		out = append(out, ServicePosture{Name: name, Desired: p.desired, Running: p.running})
		if p.bounceOnce && !p.bounced && p.running == 0 && p.desired == 0 {
			p.pendingBounce = true
		}
	}
	return out, nil
}

func (f *fakeDrainECS) UpdateDesiredCount(_ context.Context, cluster, service string, desired int32) error {
	f.updateCalls = append(f.updateCalls, drainUpdateCall{Cluster: cluster, Service: service, Desired: desired})
	p := f.profiles[service]
	if p == nil || p.ignoreUpdate {
		return nil
	}
	p.running, p.desired = desired, desired
	p.pendingBounce = false
	return nil
}

func (f *fakeDrainECS) ListRunningTasks(_ context.Context, _ string, service string) ([]string, error) {
	return f.tasksByService[service], nil
}

func (f *fakeDrainECS) GetTaskProtection(_ context.Context, _ string, taskARNs []string) (int, error) {
	svc := ""
	for name, tasks := range f.tasksByService {
		if len(tasks) > 0 && len(taskARNs) > 0 && tasks[0] == taskARNs[0] {
			svc = name
			break
		}
	}
	call := f.protectCallsByService[svc]
	f.protectCallsByService[svc] = call + 1

	if errs, ok := f.protectErrByCall[svc]; ok && call < len(errs) && errs[call] != nil {
		return 0, errs[call]
	}
	seq, ok := f.protectedByCall[svc]
	if !ok || len(seq) == 0 {
		return 0, nil
	}
	idx := call
	if idx >= len(seq) {
		idx = len(seq) - 1
	}
	return seq[idx], nil
}

// --------------------------------------------------------------------------
// Task 1: TestDrainToZero, TestWaitForServicesRunning.

func TestDrainToZero(t *testing.T) {
	ctx := context.Background()

	t.Run("AllZeroFirstPollNoCorrection", func(t *testing.T) {
		fake := newFakeDrainECS(map[string]*serviceProfile{
			"voice":          {running: 0, desired: 0},
			"auth":           {running: 0, desired: 0},
			"telephony-edge": {running: 0, desired: 0},
		})
		var buf bytes.Buffer
		report, err := DrainToZero(ctx, fake, "cluster", []string{"voice", "auth", "telephony-edge"}, &buf, DrainOptions{
			Timeout: time.Second, PollInterval: time.Millisecond, GraceAfterCorrection: time.Millisecond, Correct: true,
		})
		if err != nil {
			t.Fatalf("DrainToZero() error = %v, want nil", err)
		}
		if len(fake.updateCalls) != 0 {
			t.Errorf("updateCalls = %v, want none (already at zero)", fake.updateCalls)
		}
		if len(report.Corrected) != 0 {
			t.Errorf("Corrected = %v, want none", report.Corrected)
		}
	})

	t.Run("TimeoutTriggersCorrectionThenConverges", func(t *testing.T) {
		fake := newFakeDrainECS(map[string]*serviceProfile{
			"voice": {running: 1, desired: 1},
		})
		var buf bytes.Buffer
		report, err := DrainToZero(ctx, fake, "cluster", []string{"voice"}, &buf, DrainOptions{
			Timeout: 15 * time.Millisecond, PollInterval: 5 * time.Millisecond, GraceAfterCorrection: 200 * time.Millisecond, Correct: true,
		})
		if err != nil {
			t.Fatalf("DrainToZero() error = %v, want nil", err)
		}
		if len(fake.updateCalls) != 1 {
			t.Fatalf("updateCalls = %v, want exactly 1", fake.updateCalls)
		}
		if fake.updateCalls[0].Service != "voice" || fake.updateCalls[0].Desired != 0 {
			t.Errorf("updateCalls[0] = %+v, want {Service: voice, Desired: 0}", fake.updateCalls[0])
		}
		if len(report.Corrected) != 1 || report.Corrected[0] != "voice" {
			t.Errorf("Corrected = %v, want [voice]", report.Corrected)
		}
		out := buf.String()
		if !strings.Contains(strings.ToLower(out), "correct") || !strings.Contains(out, "voice") {
			t.Errorf("writer output %q does not contain 'correct' and 'voice'", out)
		}
	})

	t.Run("BounceBackTriggersImmediateCorrection", func(t *testing.T) {
		fake := newFakeDrainECS(map[string]*serviceProfile{
			"voice": {running: 0, desired: 0, bounceOnce: true},
			"auth":  {running: 1, desired: 1, naturalZeroAtCall: 3},
		})
		var buf bytes.Buffer
		report, err := DrainToZero(ctx, fake, "cluster", []string{"voice", "auth"}, &buf, DrainOptions{
			// Timeout is deliberately large -- the bounce-back correction
			// must fire long before it, proving the fix does not wait out
			// the full timeout.
			Timeout: time.Second, PollInterval: time.Millisecond, GraceAfterCorrection: 200 * time.Millisecond, Correct: true,
		})
		if err != nil {
			t.Fatalf("DrainToZero() error = %v, want nil", err)
		}
		if len(fake.updateCalls) != 1 || fake.updateCalls[0].Service != "voice" {
			t.Fatalf("updateCalls = %v, want exactly one correction for voice", fake.updateCalls)
		}
		if len(report.Corrected) != 1 || report.Corrected[0] != "voice" {
			t.Errorf("Corrected = %v, want [voice]", report.Corrected)
		}
	})

	t.Run("StuckAfterCorrectionAndGraceReturnsError", func(t *testing.T) {
		fake := newFakeDrainECS(map[string]*serviceProfile{
			"voice": {running: 1, desired: 1, ignoreUpdate: true},
		})
		var buf bytes.Buffer
		_, err := DrainToZero(ctx, fake, "cluster", []string{"voice"}, &buf, DrainOptions{
			Timeout: 10 * time.Millisecond, PollInterval: 5 * time.Millisecond, GraceAfterCorrection: 10 * time.Millisecond, Correct: true,
		})
		if err == nil {
			t.Fatal("DrainToZero() error = nil, want non-nil for a service stuck after correction+grace")
		}
		if !strings.Contains(err.Error(), "voice") || !strings.Contains(err.Error(), "running=1") {
			t.Errorf("error = %q, want it to name voice and running=1", err.Error())
		}
	})

	t.Run("TransientDescribeErrorRetried", func(t *testing.T) {
		fake := newFakeDrainECS(map[string]*serviceProfile{
			"voice": {running: 0, desired: 0},
		})
		fake.descErrsRemaining = 2
		var buf bytes.Buffer
		_, err := DrainToZero(ctx, fake, "cluster", []string{"voice"}, &buf, DrainOptions{
			Timeout: time.Second, PollInterval: time.Millisecond, GraceAfterCorrection: time.Millisecond, Correct: true,
		})
		if err != nil {
			t.Fatalf("DrainToZero() error = %v, want nil once the transient errors resolve", err)
		}
	})

	t.Run("PersistentDescribeErrorReturnsError", func(t *testing.T) {
		fake := newFakeDrainECS(map[string]*serviceProfile{
			"voice": {running: 0, desired: 0},
		})
		fake.descPermanentErr = true
		var buf bytes.Buffer
		_, err := DrainToZero(ctx, fake, "cluster", []string{"voice"}, &buf, DrainOptions{
			Timeout: time.Second, PollInterval: time.Millisecond, GraceAfterCorrection: time.Millisecond, Correct: true,
		})
		if err == nil {
			t.Fatal("DrainToZero() error = nil, want non-nil on a persistent describe-services error")
		}
	})
}

func TestWaitForServicesRunning(t *testing.T) {
	ctx := context.Background()

	t.Run("ReturnsOnceRunningAtOrAboveDesired", func(t *testing.T) {
		fake := newFakeDrainECS(map[string]*serviceProfile{
			"voice": {desired: 1, running: 0, rampToRunningAtCall: 3},
		})
		var buf bytes.Buffer
		postures, err := WaitForServicesRunning(ctx, fake, "cluster", []string{"voice"}, &buf, time.Second, time.Millisecond)
		if err != nil {
			t.Fatalf("WaitForServicesRunning() error = %v, want nil", err)
		}
		if len(postures) != 1 {
			t.Fatalf("postures = %v, want 1 entry", postures)
		}
		if postures[0].Desired <= 0 || postures[0].Running < postures[0].Desired {
			t.Errorf("postures[0] = %+v, want Desired > 0 and Running >= Desired", postures[0])
		}
	})
}

// --------------------------------------------------------------------------
// Task 2: TestCountInFlightSessions, TestDrainToZeroInFlight.

// scriptedProtectionECS is a minimal ECSAPI fake for CountInFlightSessions
// unit tests -- DescribeServices/UpdateDesiredCount are never called by
// CountInFlightSessions, so they simply fail the test if invoked.
type scriptedProtectionECS struct {
	tasks         map[string][]string
	tasksErr      map[string]error
	protectedARNs map[string]bool
	protectErr    error
	protectCalls  int
}

func (s *scriptedProtectionECS) DescribeServices(context.Context, string, []string) ([]ServicePosture, error) {
	return nil, errors.New("scriptedProtectionECS: DescribeServices unexpectedly called")
}

func (s *scriptedProtectionECS) UpdateDesiredCount(context.Context, string, string, int32) error {
	return errors.New("scriptedProtectionECS: UpdateDesiredCount unexpectedly called")
}

func (s *scriptedProtectionECS) ListRunningTasks(_ context.Context, _ string, service string) ([]string, error) {
	if err, ok := s.tasksErr[service]; ok {
		return nil, err
	}
	return s.tasks[service], nil
}

func (s *scriptedProtectionECS) GetTaskProtection(_ context.Context, _ string, taskARNs []string) (int, error) {
	s.protectCalls++
	if s.protectErr != nil {
		return 0, s.protectErr
	}
	count := 0
	for _, arn := range taskARNs {
		if s.protectedARNs[arn] {
			count++
		}
	}
	return count, nil
}

func TestCountInFlightSessions(t *testing.T) {
	ctx := context.Background()

	t.Run("SumsProtectedAcrossServices", func(t *testing.T) {
		s := &scriptedProtectionECS{
			tasks: map[string][]string{
				"voice":          {"task-voice-1", "task-voice-2"},
				"telephony-edge": {"task-tel-1"},
			},
			protectedARNs: map[string]bool{"task-voice-1": true, "task-tel-1": true},
		}
		got, err := CountInFlightSessions(ctx, s, "cluster", []string{"voice", "telephony-edge"})
		if err != nil {
			t.Fatalf("CountInFlightSessions() error = %v, want nil", err)
		}
		if got != 2 {
			t.Errorf("CountInFlightSessions() = %d, want 2", got)
		}
	})

	t.Run("SkipsProtectionLookupWhenNoTasks", func(t *testing.T) {
		s := &scriptedProtectionECS{tasks: map[string][]string{}}
		got, err := CountInFlightSessions(ctx, s, "cluster", []string{"voice", "auth"})
		if err != nil {
			t.Fatalf("CountInFlightSessions() error = %v, want nil", err)
		}
		if got != 0 {
			t.Errorf("CountInFlightSessions() = %d, want 0", got)
		}
		if s.protectCalls != 0 {
			t.Errorf("GetTaskProtection call count = %d, want 0 when no service has running tasks", s.protectCalls)
		}
	})
}

func TestDrainToZeroInFlight(t *testing.T) {
	ctx := context.Background()

	t.Run("ReportsWaitingLineWithCount", func(t *testing.T) {
		fake := newFakeDrainECS(map[string]*serviceProfile{
			"voice": {running: 1, desired: 1, naturalZeroAtCall: 3},
		})
		fake.tasksByService["voice"] = []string{"task-1", "task-2"}
		fake.protectedByCall["voice"] = []int{2}

		var buf bytes.Buffer
		_, err := DrainToZero(ctx, fake, "cluster", []string{"voice"}, &buf, DrainOptions{
			Timeout: time.Second, PollInterval: time.Millisecond, GraceAfterCorrection: time.Second, Correct: true,
		})
		if err != nil {
			t.Fatalf("DrainToZero() error = %v, want nil", err)
		}
		if !strings.Contains(buf.String(), "waiting for 2 in-flight session(s) to drain") {
			t.Errorf("writer output %q does not contain the D-21 in-flight line for 2 sessions", buf.String())
		}
	})

	t.Run("DedupesUnchangedCountThenReprintsOnChange", func(t *testing.T) {
		fake := newFakeDrainECS(map[string]*serviceProfile{
			"voice": {running: 1, desired: 1, naturalZeroAtCall: 5},
		})
		fake.tasksByService["voice"] = []string{"task-1"}
		fake.protectedByCall["voice"] = []int{2, 2, 1, 0}

		var buf bytes.Buffer
		_, err := DrainToZero(ctx, fake, "cluster", []string{"voice"}, &buf, DrainOptions{
			Timeout: time.Second, PollInterval: time.Millisecond, GraceAfterCorrection: time.Second, Correct: true,
		})
		if err != nil {
			t.Fatalf("DrainToZero() error = %v, want nil", err)
		}
		out := buf.String()
		if strings.Count(out, "waiting for 2 in-flight session(s) to drain") != 1 {
			t.Errorf("writer output %q, want exactly one '2 in-flight' line (unchanged count not repeated)", out)
		}
		if strings.Count(out, "waiting for 1 in-flight session(s) to drain") != 1 {
			t.Errorf("writer output %q, want exactly one '1 in-flight' line (reprinted on change)", out)
		}
	})

	t.Run("ZeroProtectedProducesNoLineAtAll", func(t *testing.T) {
		fake := newFakeDrainECS(map[string]*serviceProfile{
			"voice": {running: 1, desired: 1, naturalZeroAtCall: 3},
		})
		// No tasksByService entry for voice -- CountInFlightSessions
		// returns 0 without ever calling GetTaskProtection.

		var buf bytes.Buffer
		_, err := DrainToZero(ctx, fake, "cluster", []string{"voice"}, &buf, DrainOptions{
			Timeout: time.Second, PollInterval: time.Millisecond, GraceAfterCorrection: time.Second, Correct: true,
		})
		if err != nil {
			t.Fatalf("DrainToZero() error = %v, want nil", err)
		}
		if strings.Contains(buf.String(), "in-flight") {
			t.Errorf("writer output %q contains an in-flight line, want none for a quiet pause", buf.String())
		}
	})

	t.Run("DegradesGracefullyOnProtectionError", func(t *testing.T) {
		fake := newFakeDrainECS(map[string]*serviceProfile{
			"voice": {running: 1, desired: 1, naturalZeroAtCall: 3},
		})
		fake.tasksByService["voice"] = []string{"task-1"}
		fake.protectErrByCall["voice"] = []error{errors.New("simulated GetTaskProtection failure")}

		var buf bytes.Buffer
		_, err := DrainToZero(ctx, fake, "cluster", []string{"voice"}, &buf, DrainOptions{
			Timeout: time.Second, PollInterval: time.Millisecond, GraceAfterCorrection: time.Second, Correct: true,
		})
		if err != nil {
			t.Fatalf("DrainToZero() error = %v, want nil -- a protection-lookup failure must not abort the drain", err)
		}
		if !strings.Contains(buf.String(), "unavailable") {
			t.Errorf("writer output %q does not contain the degrade-gracefully 'unavailable' line", buf.String())
		}
	})

	t.Run("RecordsInFlightPeak", func(t *testing.T) {
		fake := newFakeDrainECS(map[string]*serviceProfile{
			"voice": {running: 1, desired: 1, naturalZeroAtCall: 4},
		})
		fake.tasksByService["voice"] = []string{"task-1"}
		fake.protectedByCall["voice"] = []int{1, 3, 2}

		var buf bytes.Buffer
		report, err := DrainToZero(ctx, fake, "cluster", []string{"voice"}, &buf, DrainOptions{
			Timeout: time.Second, PollInterval: time.Millisecond, GraceAfterCorrection: time.Second, Correct: true,
		})
		if err != nil {
			t.Fatalf("DrainToZero() error = %v, want nil", err)
		}
		if report.InFlightPeak != 3 {
			t.Errorf("InFlightPeak = %d, want 3", report.InFlightPeak)
		}
	})
}
