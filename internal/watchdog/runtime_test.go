package watchdog

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/backend"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
)

func TestRuntimeCheckerFailsWhenDefaultRouteLeavesStartupTUN(t *testing.T) {
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "en0",
			interfaces:     map[string]bool{"utun9": true},
		},
		Sandbox: fakeProbeBackend{result: backend.ProbeResult{
			Egress: network.EgressResult{OK: true, ObservedIP: "203.0.113.10", ExpectedIP: "203.0.113.10"},
		}},
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
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "utun9",
			interfaces:     map[string]bool{"utun9": true},
		},
		Sandbox: fakeProbeBackend{err: errors.New("sandbox-egress-mismatch: observed IP mismatch")},
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
}

func TestRuntimeCheckerFailsSandboxEgressBeforeDeepProbe(t *testing.T) {
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

func TestRuntimeCheckerUsesIsolatedProbeNamesForOverlappingChecks(t *testing.T) {
	sandbox := newOverlappingProbeSandbox()
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "utun9",
			interfaces:     map[string]bool{"utun9": true},
		},
		Sandbox: sandbox,
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := checker.Check(context.Background(), Event{Source: "route-monitor"})
		firstDone <- err
	}()
	firstName := sandbox.waitFirstProbe(t)

	secondDone := make(chan error, 1)
	go func() {
		_, err := checker.Check(context.Background(), Event{Source: "route-monitor"})
		secondDone <- err
	}()

	var secondName string
	select {
	case err := <-secondDone:
		t.Fatalf("second runtime check finished before isolated probe started: %v", err)
	case secondName = <-sandbox.secondProbeStarted:
	case <-time.After(time.Second):
		t.Fatal("second runtime check did not reach isolated probe")
	}
	if secondName == firstName {
		t.Fatalf("overlapping runtime checks reused probe name %q", firstName)
	}

	sandbox.release()
	if err := waitRuntimeCheck(t, firstDone); err != nil {
		t.Fatalf("first runtime check failed: %v", err)
	}
	if err := waitRuntimeCheck(t, secondDone); err != nil {
		t.Fatalf("second runtime check failed: %v", err)
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

type fakeProbeBackend struct {
	result backend.ProbeResult
	err    error
}

func (b fakeProbeBackend) CheckSandboxEgress(context.Context, config.Config) (backend.ProbeResult, error) {
	return b.result, b.err
}

func (b fakeProbeBackend) Probe(context.Context, config.Config) (backend.ProbeResult, error) {
	return b.result, b.err
}

type fakeFastSandbox struct {
	egressResult backend.ProbeResult
	egressErr    error
	probeDone    chan struct{}
}

func (b *fakeFastSandbox) CheckSandboxEgress(context.Context, config.Config) (backend.ProbeResult, error) {
	return b.egressResult, b.egressErr
}

func (b *fakeFastSandbox) Probe(ctx context.Context, _ config.Config) (backend.ProbeResult, error) {
	select {
	case <-ctx.Done():
		return backend.ProbeResult{}, ctx.Err()
	case <-b.probeDone:
		return backend.ProbeResult{}, nil
	}
}

type overlappingProbeSandbox struct {
	mu                 sync.Mutex
	active             map[string]bool
	firstProbeStarted  chan string
	secondProbeStarted chan string
	releaseProbes      chan struct{}
}

func newOverlappingProbeSandbox() *overlappingProbeSandbox {
	return &overlappingProbeSandbox{
		active:             map[string]bool{},
		firstProbeStarted:  make(chan string, 1),
		secondProbeStarted: make(chan string, 1),
		releaseProbes:      make(chan struct{}),
	}
}

func (b *overlappingProbeSandbox) CheckSandboxEgress(_ context.Context, cfg config.Config) (backend.ProbeResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active[cfg.Sandbox.ProbeName] {
		return backend.ProbeResult{}, errors.New("probe name collision")
	}
	return backend.ProbeResult{
		Egress: network.EgressResult{OK: true, ObservedIP: cfg.Network.EgressIP.ExpectedIP, ExpectedIP: cfg.Network.EgressIP.ExpectedIP},
	}, nil
}

func (b *overlappingProbeSandbox) Probe(ctx context.Context, cfg config.Config) (backend.ProbeResult, error) {
	b.mu.Lock()
	name := cfg.Sandbox.ProbeName
	if b.active[name] {
		b.mu.Unlock()
		return backend.ProbeResult{}, errors.New("probe name collision")
	}
	b.active[name] = true
	activeCount := len(b.active)
	b.mu.Unlock()

	if activeCount == 1 {
		b.firstProbeStarted <- name
	} else {
		b.secondProbeStarted <- name
	}

	select {
	case <-ctx.Done():
		return backend.ProbeResult{}, ctx.Err()
	case <-b.releaseProbes:
		return backend.ProbeResult{
			Egress: network.EgressResult{OK: true, ObservedIP: cfg.Network.EgressIP.ExpectedIP, ExpectedIP: cfg.Network.EgressIP.ExpectedIP},
		}, nil
	}
}

func (b *overlappingProbeSandbox) waitFirstProbe(t *testing.T) string {
	t.Helper()
	select {
	case name := <-b.firstProbeStarted:
		return name
	case <-time.After(time.Second):
		t.Fatal("first runtime check did not reach deep probe")
		return ""
	}
}

func (b *overlappingProbeSandbox) release() {
	close(b.releaseProbes)
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
