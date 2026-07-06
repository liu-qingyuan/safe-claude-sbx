package watchdog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/backend"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
)

func TestRuntimeCheckerFailsWhenDefaultRouteLeavesStartupTUN(t *testing.T) {
	sandboxCalls := 0
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "en0",
			interfaces:     map[string]bool{"utun9": true},
		},
		Sandbox: fakeProbeBackend{result: backend.ProbeResult{
			Egress: network.EgressResult{OK: true, ObservedIP: "203.0.113.10", ExpectedIP: "203.0.113.10"},
		}, calls: &sandboxCalls},
	}

	result, err := checker.Check(context.Background(), Event{Source: "route-monitor"})
	if err == nil {
		t.Fatal("expected route mismatch error")
	}
	if result.OK {
		t.Fatal("runtime check unexpectedly passed")
	}
	if !strings.Contains(err.Error(), "default route changed from startup interface utun9 to en0") {
		t.Fatalf("expected route change reason, got %v", err)
	}
	if sandboxCalls != 0 {
		t.Fatalf("route mismatch should fail before sandbox egress checks, got %d calls", sandboxCalls)
	}
}

func TestRuntimeCheckerFailsWhenStartupTUNDisappears(t *testing.T) {
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "utun9",
			interfaces:     map[string]bool{"utun9": false},
		},
		Sandbox: fakeProbeBackend{result: backend.ProbeResult{
			Egress: network.EgressResult{OK: true, ObservedIP: "203.0.113.10", ExpectedIP: "203.0.113.10"},
		}},
	}

	result, err := checker.Check(context.Background(), Event{Source: "route-monitor"})
	if err == nil {
		t.Fatal("expected missing TUN error")
	}
	if result.OK {
		t.Fatal("runtime check unexpectedly passed")
	}
	if !strings.Contains(err.Error(), "TUN interface utun9 missing") {
		t.Fatalf("expected missing interface reason, got %v", err)
	}
}

func TestRuntimeCheckerFailsWhenSandboxEgressMismatches(t *testing.T) {
	sandboxCalls := 0
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "utun9",
			interfaces:     map[string]bool{"utun9": true},
		},
		Sandbox: fakeProbeBackend{
			result: backend.ProbeResult{Egress: network.EgressResult{
				OK:            false,
				ExpectedIP:    "203.0.113.10",
				ObservedIP:    "198.51.100.77",
				FailureKind:   network.EgressFailureKind("sandbox-egress-mismatch"),
				FailureReason: "sandbox egress observed IP 198.51.100.77 does not match expected IP 203.0.113.10",
			}},
			err:   errors.New("sandbox-egress-mismatch: observed IP mismatch"),
			calls: &sandboxCalls,
		},
		RetryPolicy: RuntimeEgressRetryPolicy{
			Attempts:       10,
			InitialBackoff: time.Second,
			MaxBackoff:     time.Second,
			Sleeper:        &recordingSleeper{},
		},
	}

	result, err := checker.Check(context.Background(), Event{Source: "route-monitor"})
	if err == nil {
		t.Fatal("expected sandbox egress error")
	}
	if result.OK {
		t.Fatal("runtime check unexpectedly passed")
	}
	if !strings.Contains(err.Error(), "sandbox egress invalid") {
		t.Fatalf("expected sandbox egress reason, got %v", err)
	}
	if sandboxCalls != 1 {
		t.Fatalf("explicit egress mismatch should not retry, got %d calls", sandboxCalls)
	}
}

func TestRuntimeCheckerFailsSandboxEgressWithoutDeepProbe(t *testing.T) {
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "utun9",
			interfaces:     map[string]bool{"utun9": true},
		},
		Sandbox: &fakeFastSandbox{
			egressErr: errors.New("sandbox-egress-mismatch: observed IP mismatch"),
			probeDone: make(chan struct{}),
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, err := checker.Check(ctx, Event{Source: "route-monitor"})
	if err == nil {
		t.Fatal("expected sandbox egress error")
	}
	if result.OK {
		t.Fatal("runtime check unexpectedly passed")
	}
	if !strings.Contains(err.Error(), "sandbox-egress-mismatch") {
		t.Fatalf("expected sandbox egress mismatch reason, got %v", err)
	}
	if checker.Sandbox.(*fakeFastSandbox).probeCalled {
		t.Fatal("runtime egress mismatch should not run startup deep probe")
	}
}

func TestRuntimeCheckerFailsWhenSandboxEgressResultIsNotOK(t *testing.T) {
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "utun9",
			interfaces:     map[string]bool{"utun9": true},
		},
		Sandbox: &fakeFastSandbox{
			egressResult: backend.ProbeResult{Egress: network.EgressResult{
				OK:            false,
				ExpectedIP:    "203.0.113.10",
				ObservedIP:    "198.51.100.77",
				FailureReason: "sandbox egress observed IP 198.51.100.77 does not match expected IP 203.0.113.10",
			}},
			probeDone: make(chan struct{}),
		},
	}

	result, err := checker.Check(context.Background(), Event{Source: "route-monitor"})
	if err == nil {
		t.Fatal("expected sandbox egress policy error")
	}
	if result.OK {
		t.Fatal("runtime check unexpectedly passed")
	}
	if !strings.Contains(err.Error(), "198.51.100.77") {
		t.Fatalf("expected structured egress failure reason, got %v", err)
	}
}

func TestRuntimeCheckerRetriesIndeterminateRuntimeEgressUntilTenthAttemptSucceeds(t *testing.T) {
	attempts := make([]runtimeAttempt, 0, 10)
	for range 9 {
		attempts = append(attempts, indeterminateRuntimeAttempt("configured sandbox egress URL timed out"))
	}
	attempts = append(attempts, runtimeAttempt{
		result: backend.ProbeResult{Egress: network.EgressResult{
			OK:         true,
			ObservedIP: "203.0.113.10",
			ExpectedIP: "203.0.113.10",
		}},
	})
	sandbox := &retryRuntimeSandbox{
		results: attempts,
	}
	sleeper := &recordingSleeper{}
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "utun9",
			interfaces:     map[string]bool{"utun9": true},
		},
		Sandbox: sandbox,
		RetryPolicy: RuntimeEgressRetryPolicy{
			Attempts:       10,
			InitialBackoff: time.Second,
			MaxBackoff:     4 * time.Second,
			Sleeper:        sleeper,
		},
	}

	result, err := checker.Check(context.Background(), Event{Source: "route-monitor"})

	if err != nil {
		t.Fatalf("runtime check should retry transient egress failure: %v", err)
	}
	if !result.OK {
		t.Fatalf("runtime check did not pass after retry: %#v", result)
	}
	if sandbox.calls != 10 {
		t.Fatalf("expected tenth attempt to recover indeterminate runtime egress, got %d calls", sandbox.calls)
	}
	wantBackoffs := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		4 * time.Second,
		4 * time.Second,
		4 * time.Second,
		4 * time.Second,
		4 * time.Second,
		4 * time.Second,
	}
	if got := sleeper.durations; !equalDurations(got, wantBackoffs) {
		t.Fatalf("expected capped exponential backoff %v, got %v", wantBackoffs, got)
	}
	if sandbox.probeCalled {
		t.Fatal("runtime egress retry should not run startup deep probe")
	}
}

func TestRuntimeCheckerFailsIndeterminateRuntimeEgressAfterRetryExhaustion(t *testing.T) {
	attempts := make([]runtimeAttempt, 0, 10)
	for range 10 {
		attempts = append(attempts, indeterminateRuntimeAttempt("configured sandbox egress URL timed out"))
	}
	sandbox := &retryRuntimeSandbox{
		results: attempts,
	}
	sleeper := &recordingSleeper{}
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "utun9",
			interfaces:     map[string]bool{"utun9": true},
		},
		Sandbox: sandbox,
		RetryPolicy: RuntimeEgressRetryPolicy{
			Sleeper: sleeper,
		},
	}

	result, err := checker.Check(context.Background(), Event{Source: "route-monitor"})

	if err == nil {
		t.Fatal("expected runtime egress failure after retry exhaustion")
	}
	if result.OK {
		t.Fatal("runtime check unexpectedly passed")
	}
	if !strings.Contains(err.Error(), "indeterminate runtime egress check failed") {
		t.Fatalf("expected indeterminate failure diagnostic, got %v", err)
	}
	if strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("indeterminate failure should not be reported as mismatch: %v", err)
	}
	if !strings.Contains(err.Error(), "runtime egress indeterminate retry 10/10") {
		t.Fatalf("expected final retry attempt diagnostic, got %v", err)
	}
	if sandbox.calls != 10 {
		t.Fatalf("expected bounded retry attempts, got %d", sandbox.calls)
	}
	if len(sleeper.durations) != 9 {
		t.Fatalf("expected sleep only between attempts, got %d sleeps", len(sleeper.durations))
	}
}

func TestRuntimeCheckerStopsIndeterminateRetryWhenRouteBecomesUnsafe(t *testing.T) {
	sandbox := &retryRuntimeSandbox{
		results: []runtimeAttempt{
			indeterminateRuntimeAttempt("configured sandbox egress URL timed out"),
			indeterminateRuntimeAttempt("should not be called"),
		},
	}
	sleeper := &recordingSleeper{}
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: &sequenceRouteRunner{
			routeInterfaces: []string{"utun9", "en0"},
			interfaces:      map[string]bool{"utun9": true},
		},
		Sandbox: sandbox,
		RetryPolicy: RuntimeEgressRetryPolicy{
			Attempts:       10,
			InitialBackoff: time.Second,
			MaxBackoff:     time.Second,
			Sleeper:        sleeper,
		},
	}

	result, err := checker.Check(context.Background(), Event{Source: "route-monitor"})

	if err == nil {
		t.Fatal("expected route failure during indeterminate retry")
	}
	if result.OK {
		t.Fatal("runtime check unexpectedly passed")
	}
	if !strings.Contains(err.Error(), "default route changed from startup interface utun9 to en0") {
		t.Fatalf("expected retry to stop on unsafe route, got %v", err)
	}
	if sandbox.calls != 1 {
		t.Fatalf("expected retry to stop before second egress attempt, got %d calls", sandbox.calls)
	}
	if len(sleeper.durations) != 0 {
		t.Fatalf("unsafe route should interrupt retry before backoff sleep, got %v", sleeper.durations)
	}
}

func TestRuntimeCheckerPassesWhenTUNAndSandboxEgressRemainValid(t *testing.T) {
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "utun9",
			interfaces:     map[string]bool{"utun9": true},
		},
		Sandbox: fakeProbeBackend{result: backend.ProbeResult{
			Egress: network.EgressResult{OK: true, ObservedIP: "203.0.113.10", ExpectedIP: "203.0.113.10"},
		}},
	}

	result, err := checker.Check(context.Background(), Event{Source: "route-monitor"})
	if err != nil {
		t.Fatalf("runtime check failed: %v", err)
	}
	if !result.OK {
		t.Fatalf("runtime check did not pass: %#v", result)
	}
}

func runtimeCheckConfig() config.Config {
	return config.Config{
		Network: config.Network{
			ClashVerge: config.ClashVerge{RouteCheckTarget: "1.1.1.1", TUNInterfacePrefix: "utun"},
			EgressIP:   config.EgressIP{ExpectedIP: "203.0.113.10", SandboxCheckURL: "https://example.invalid/ip", TimeoutSeconds: 10},
		},
		Sandbox: config.Sandbox{ProbeName: "probe"},
	}
}

type fakeRouteRunner struct {
	routeInterface string
	interfaces     map[string]bool
}

func (r fakeRouteRunner) Run(name string, args ...string) (string, error) {
	if name == "route" && len(args) == 2 && args[0] == "get" {
		return "interface: " + r.routeInterface + "\n", nil
	}
	if name == "ifconfig" && len(args) == 1 {
		if r.interfaces[args[0]] {
			return args[0] + ": flags=8051<UP>\n", nil
		}
		return "", errors.New("interface missing")
	}
	return "", errors.New("unexpected command")
}

type sequenceRouteRunner struct {
	routeInterfaces []string
	routeCalls      int
	interfaces      map[string]bool
}

func (r *sequenceRouteRunner) Run(name string, args ...string) (string, error) {
	if name == "route" && len(args) == 2 && args[0] == "get" {
		index := r.routeCalls
		r.routeCalls++
		if index >= len(r.routeInterfaces) {
			index = len(r.routeInterfaces) - 1
		}
		return "interface: " + r.routeInterfaces[index] + "\n", nil
	}
	if name == "ifconfig" && len(args) == 1 {
		if r.interfaces[args[0]] {
			return args[0] + ": flags=8051<UP>\n", nil
		}
		return "", errors.New("interface missing")
	}
	return "", errors.New("unexpected command")
}

type fakeProbeBackend struct {
	result backend.ProbeResult
	err    error
	calls  *int
}

func (b fakeProbeBackend) CheckRuntimeEgress(context.Context, config.Config) (backend.ProbeResult, error) {
	if b.calls != nil {
		(*b.calls)++
	}
	return b.result, b.err
}

func (b fakeProbeBackend) Probe(context.Context, config.Config) (backend.ProbeResult, error) {
	return b.result, b.err
}

type fakeFastSandbox struct {
	egressResult backend.ProbeResult
	egressErr    error
	probeDone    chan struct{}
	probeCalled  bool
}

func (b *fakeFastSandbox) CheckRuntimeEgress(context.Context, config.Config) (backend.ProbeResult, error) {
	return b.egressResult, b.egressErr
}

func (b *fakeFastSandbox) Probe(ctx context.Context, _ config.Config) (backend.ProbeResult, error) {
	b.probeCalled = true
	select {
	case <-ctx.Done():
		return backend.ProbeResult{}, ctx.Err()
	case <-b.probeDone:
		return backend.ProbeResult{}, nil
	}
}

type runtimeAttempt struct {
	result backend.ProbeResult
	err    error
}

func indeterminateRuntimeAttempt(reason string) runtimeAttempt {
	return runtimeAttempt{
		result: backend.ProbeResult{Egress: network.EgressResult{
			OK:            false,
			ExpectedIP:    "203.0.113.10",
			FailureKind:   network.EgressFailureIndeterminate,
			FailureReason: reason,
		}},
		err: fmt.Errorf("%s: %s", network.EgressFailureIndeterminate, reason),
	}
}

type retryRuntimeSandbox struct {
	results     []runtimeAttempt
	calls       int
	probeCalled bool
}

func (b *retryRuntimeSandbox) CheckRuntimeEgress(context.Context, config.Config) (backend.ProbeResult, error) {
	index := b.calls
	b.calls++
	if index >= len(b.results) {
		index = len(b.results) - 1
	}
	attempt := b.results[index]
	return attempt.result, attempt.err
}

func (b *retryRuntimeSandbox) Probe(context.Context, config.Config) (backend.ProbeResult, error) {
	b.probeCalled = true
	return backend.ProbeResult{}, nil
}

type recordingSleeper struct {
	durations []time.Duration
}

func (s *recordingSleeper) Sleep(ctx context.Context, duration time.Duration) error {
	s.durations = append(s.durations, duration)
	return ctx.Err()
}

func equalDurations(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func waitRuntimeCheck(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("runtime check did not finish")
		return nil
	}
}
