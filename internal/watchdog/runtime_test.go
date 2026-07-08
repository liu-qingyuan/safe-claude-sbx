package watchdog

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
)

func TestRuntimeCheckerFailsWhenDefaultRouteLeavesStartupTUN(t *testing.T) {
	hostEgressCalls := 0
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "en0",
			interfaces:     map[string]bool{"utun9": true},
		},
		HostEgress: fakeHostEgressChecker{
			result: network.EgressResult{OK: true, ObservedIP: "203.0.113.10", ExpectedIP: "203.0.113.10"},
			calls:  &hostEgressCalls,
		},
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
	if hostEgressCalls != 0 {
		t.Fatalf("route mismatch should fail before host egress checks, got %d calls", hostEgressCalls)
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
		HostEgress: fakeHostEgressChecker{
			result: network.EgressResult{OK: true, ObservedIP: "203.0.113.10", ExpectedIP: "203.0.113.10"},
		},
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

func TestRuntimeCheckerFailsWhenHostEgressMismatches(t *testing.T) {
	hostEgressCalls := 0
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "utun9",
			interfaces:     map[string]bool{"utun9": true},
		},
		HostEgress: fakeHostEgressChecker{
			result: network.EgressResult{
				OK:            false,
				ExpectedIP:    "203.0.113.10",
				ObservedIP:    "198.51.100.77",
				FailureKind:   network.EgressFailureMismatch,
				FailureReason: "host egress observed IP 198.51.100.77 does not match expected IP 203.0.113.10",
			},
			err:   errors.New("host-egress-mismatch: observed IP mismatch"),
			calls: &hostEgressCalls,
		},
	}

	result, err := checker.Check(context.Background(), Event{Source: "route-monitor"})
	if err == nil {
		t.Fatal("expected host egress error")
	}
	if result.OK {
		t.Fatal("runtime check unexpectedly passed")
	}
	if !strings.Contains(err.Error(), "host egress drift") {
		t.Fatalf("expected host egress drift reason, got %v", err)
	}
	if strings.Contains(err.Error(), "sandbox egress") {
		t.Fatalf("runtime host drift should not be reported as sandbox egress failure: %v", err)
	}
	if hostEgressCalls != 1 {
		t.Fatalf("runtime host egress should be checked once per event, got %d calls", hostEgressCalls)
	}
}

func TestRuntimeCheckerFailsWhenHostEgressEndpointFails(t *testing.T) {
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "utun9",
			interfaces:     map[string]bool{"utun9": true},
		},
		HostEgress: fakeHostEgressChecker{
			result: network.EgressResult{
				OK:            false,
				ExpectedIP:    "203.0.113.10",
				FailureKind:   network.EgressFailureEndpointFailure,
				FailureReason: "host egress endpoint request failed",
			},
			err: errors.New("endpoint-failure: host egress endpoint request failed"),
		},
	}

	result, err := checker.Check(context.Background(), Event{Source: "route-monitor"})
	if err == nil {
		t.Fatal("expected host egress endpoint error")
	}
	if result.OK {
		t.Fatal("runtime check unexpectedly passed")
	}
	if !strings.Contains(err.Error(), "host egress check failed") {
		t.Fatalf("expected host egress endpoint failure reason, got %v", err)
	}
}

func TestRuntimeCheckerChecksHostEgressAfterRouteAndInterfaceRemainValid(t *testing.T) {
	hostEgressCalls := 0
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "utun9",
			interfaces:     map[string]bool{"utun9": true},
		},
		HostEgress: fakeHostEgressChecker{
			result: network.EgressResult{OK: true, ObservedIP: "203.0.113.10", ExpectedIP: "203.0.113.10"},
			calls:  &hostEgressCalls,
		},
	}

	result, err := checker.Check(context.Background(), Event{Source: "route-monitor"})
	if err != nil {
		t.Fatalf("runtime check failed: %v", err)
	}
	if !result.OK {
		t.Fatalf("runtime check did not pass: %#v", result)
	}
	if hostEgressCalls != 1 {
		t.Fatalf("expected one host egress check, got %d", hostEgressCalls)
	}
}

func TestRuntimeCheckerPassesWhenTUNAndHostEgressRemainValid(t *testing.T) {
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "utun9",
			interfaces:     map[string]bool{"utun9": true},
		},
		HostEgress: fakeHostEgressChecker{
			result: network.EgressResult{OK: true, ObservedIP: "203.0.113.10", ExpectedIP: "203.0.113.10"},
		},
	}

	result, err := checker.Check(context.Background(), Event{Source: "route-monitor"})
	if err != nil {
		t.Fatalf("runtime check failed: %v", err)
	}
	if !result.OK {
		t.Fatalf("runtime check did not pass: %#v", result)
	}
}

func TestRuntimeCheckerPropagatesCancellationToHostEgressCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	checker := RuntimeChecker{
		Config:              runtimeCheckConfig(),
		StartupTUNInterface: "utun9",
		RouteRunner: fakeRouteRunner{
			routeInterface: "utun9",
			interfaces:     map[string]bool{"utun9": true},
		},
		HostEgress: fakeHostEgressChecker{
			result: network.EgressResult{
				OK:            false,
				ExpectedIP:    "203.0.113.10",
				FailureKind:   network.EgressFailureEndpointFailure,
				FailureReason: "host egress endpoint request canceled",
			},
			err: context.Canceled,
		},
	}

	result, err := checker.Check(ctx, Event{Source: "route-monitor"})
	if err == nil {
		t.Fatal("expected canceled host egress error")
	}
	if result.OK {
		t.Fatal("runtime check unexpectedly passed")
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "host egress endpoint request canceled") {
		t.Fatalf("expected cancellation diagnostic, got %v", err)
	}
}

func runtimeCheckConfig() config.Config {
	return config.Config{
		Network: config.Network{
			ClashVerge: config.ClashVerge{RouteCheckTarget: "1.1.1.1", TUNInterfacePrefix: "utun"},
			EgressIP:   config.EgressIP{ExpectedIP: "203.0.113.10", HostCheckURL: "https://example.invalid/ip", SandboxCheckURL: "https://example.invalid/ip", TimeoutSeconds: 10},
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

type fakeHostEgressChecker struct {
	result network.EgressResult
	err    error
	calls  *int
}

func (c fakeHostEgressChecker) CheckHostContext(context.Context, config.EgressIP) (network.EgressResult, error) {
	if c.calls != nil {
		(*c.calls)++
	}
	return c.result, c.err
}
