package watchdog

import (
	"context"
	"errors"
	"strings"
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
