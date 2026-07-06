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

func TestSupervisorContinuesAfterRouteEventWhenRuntimeCheckPasses(t *testing.T) {
	events := make(chan Event)
	backendExit := make(chan error)
	checkCalled := make(chan struct{}, 1)
	cleanup := &recordingCleanup{}

	supervisor := Supervisor{
		Events:      events,
		BackendExit: backendExit,
		Check: CheckFunc(func(context.Context, Event) (CheckResult, error) {
			checkCalled <- struct{}{}
			return CheckResult{OK: true}, nil
		}),
		Cleanup: cleanup,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- supervisor.Run(ctx)
	}()

	events <- Event{Source: "route-monitor"}
	select {
	case <-checkCalled:
	case <-time.After(time.Second):
		t.Fatal("runtime check was not called after route event")
	}
	if cleanup.calls != 0 {
		t.Fatalf("cleanup ran after a valid runtime check, calls=%d", cleanup.calls)
	}
	select {
	case err := <-done:
		t.Fatalf("watchdog exited despite valid runtime state: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	backendExit <- nil
	if err := waitDone(t, done); err != nil {
		t.Fatalf("watchdog returned error after backend exit: %v", err)
	}
	if cleanup.calls != 1 {
		t.Fatalf("expected one cleanup after backend exit, got %d", cleanup.calls)
	}
}

func TestSupervisorStopsAndCleansUpWhenRuntimeCheckFails(t *testing.T) {
	events := make(chan Event)
	cleanup := &recordingCleanup{}

	supervisor := Supervisor{
		Events: events,
		Check: CheckFunc(func(context.Context, Event) (CheckResult, error) {
			return CheckResult{OK: false, Reason: "default route changed"}, errRuntimeCheck
		}),
		Cleanup: cleanup,
	}

	done := make(chan error, 1)
	go func() {
		done <- supervisor.Run(context.Background())
	}()

	events <- Event{Source: "route-monitor"}
	err := waitDone(t, done)
	if err == nil {
		t.Fatal("expected watchdog to fail closed")
	}
	if !errors.Is(err, errRuntimeCheck) {
		t.Fatalf("expected runtime check error, got %v", err)
	}
	if cleanup.calls != 1 {
		t.Fatalf("expected one cleanup after runtime failure, got %d", cleanup.calls)
	}
}

func TestSupervisorDebouncesRouteEvents(t *testing.T) {
	events := make(chan Event, 2)
	backendExit := make(chan error)
	checked := make(chan Event, 2)
	cleanup := &recordingCleanup{}
	supervisor := Supervisor{
		Events:        events,
		BackendExit:   backendExit,
		EventDebounce: 20 * time.Millisecond,
		Check: CheckFunc(func(_ context.Context, event Event) (CheckResult, error) {
			checked <- event
			return CheckResult{OK: true}, nil
		}),
		Cleanup: cleanup,
	}

	done := make(chan error, 1)
	go func() {
		done <- supervisor.Run(context.Background())
	}()

	events <- Event{Source: "route-monitor", Detail: "intermediate"}
	events <- Event{Source: "route-monitor", Detail: "stable"}

	select {
	case event := <-checked:
		if event.Detail != "stable" {
			t.Fatalf("expected debounced stable event, got %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime check was not called after debounce")
	}
	select {
	case event := <-checked:
		t.Fatalf("expected one debounced check, got extra %#v", event)
	case <-time.After(40 * time.Millisecond):
	}

	backendExit <- nil
	if err := waitDone(t, done); err != nil {
		t.Fatalf("watchdog returned error after backend exit: %v", err)
	}
	if cleanup.calls != 1 {
		t.Fatalf("expected one cleanup after backend exit, got %d", cleanup.calls)
	}
}

func TestSupervisorFailsClosedOnRouteChangeWhileDeepProbeIsBlocked(t *testing.T) {
	events := make(chan Event, 2)
	cleanup := &recordingCleanup{}
	routes := &switchingRouteRunner{
		routeInterface: "utun9",
		interfaces:     map[string]bool{"utun9": true},
	}
	probe := &blockingProbe{started: make(chan struct{})}
	supervisor := Supervisor{
		Events: events,
		Check: RuntimeChecker{
			Config:              runtimeCheckConfig(),
			StartupTUNInterface: "utun9",
			RouteRunner:         routes,
			Sandbox:             probe,
		},
		Cleanup: cleanup,
	}

	done := make(chan error, 1)
	go func() {
		done <- supervisor.Run(context.Background())
	}()

	events <- Event{Source: "route-monitor", Detail: "initial route event"}
	select {
	case <-probe.started:
	case <-time.After(time.Second):
		t.Fatal("deep probe did not start")
	}

	routes.setRouteInterface("en0")
	events <- Event{Source: "route-monitor", Detail: "route changed"}

	err := waitDone(t, done)
	if err == nil {
		t.Fatal("expected watchdog to fail closed")
	}
	if !strings.Contains(err.Error(), "default route changed from startup interface utun9 to en0") {
		t.Fatalf("expected route failure, got %v", err)
	}
	if cleanup.calls != 1 {
		t.Fatalf("expected one cleanup after route failure, got %d", cleanup.calls)
	}
	probe.release()
}

func TestSupervisorCleansUpOnFastSandboxEgressMismatch(t *testing.T) {
	events := make(chan Event, 1)
	cleanup := &recordingCleanup{}
	supervisor := Supervisor{
		Events: events,
		Check: RuntimeChecker{
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
		},
		Cleanup: cleanup,
	}

	done := make(chan error, 1)
	go func() {
		done <- supervisor.Run(context.Background())
	}()

	events <- Event{Source: "route-monitor", Detail: "route changed"}
	err := waitDone(t, done)
	if err == nil {
		t.Fatal("expected watchdog to fail closed")
	}
	if !strings.Contains(err.Error(), "sandbox-egress-mismatch") {
		t.Fatalf("expected sandbox egress mismatch, got %v", err)
	}
	if cleanup.calls != 1 {
		t.Fatalf("expected one cleanup after egress failure, got %d", cleanup.calls)
	}
}

func TestSupervisorCleansUpWhenContextIsCanceled(t *testing.T) {
	cleanup := &recordingCleanup{}
	supervisor := Supervisor{Cleanup: cleanup}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- supervisor.Run(ctx)
	}()

	cancel()
	err := waitDone(t, done)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if cleanup.calls != 1 {
		t.Fatalf("expected one cleanup after cancellation, got %d", cleanup.calls)
	}
}

func waitDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("watchdog did not exit")
		return nil
	}
}

type recordingCleanup struct {
	calls int
	err   error
}

func (c *recordingCleanup) Cleanup(context.Context) error {
	c.calls++
	return c.err
}

var errRuntimeCheck = errors.New("runtime check failed")

type switchingRouteRunner struct {
	mu             sync.Mutex
	routeInterface string
	interfaces     map[string]bool
}

func (r *switchingRouteRunner) setRouteInterface(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routeInterface = name
}

func (r *switchingRouteRunner) Run(name string, args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
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

type blockingProbe struct {
	once    sync.Once
	started chan struct{}
	done    chan struct{}
}

func (p *blockingProbe) CheckSandboxEgress(ctx context.Context, cfg config.Config) (backend.ProbeResult, error) {
	return backend.ProbeResult{
		Egress: network.EgressResult{OK: true, ObservedIP: cfg.Network.EgressIP.ExpectedIP, ExpectedIP: cfg.Network.EgressIP.ExpectedIP},
	}, nil
}

func (p *blockingProbe) Probe(ctx context.Context, cfg config.Config) (backend.ProbeResult, error) {
	p.once.Do(func() {
		p.done = make(chan struct{})
		close(p.started)
	})
	select {
	case <-ctx.Done():
		return backend.ProbeResult{}, ctx.Err()
	case <-p.done:
		return backend.ProbeResult{
			Egress: network.EgressResult{OK: true, ObservedIP: cfg.Network.EgressIP.ExpectedIP, ExpectedIP: cfg.Network.EgressIP.ExpectedIP},
		}, nil
	}
}

func (p *blockingProbe) release() {
	if p.done != nil {
		close(p.done)
	}
}
