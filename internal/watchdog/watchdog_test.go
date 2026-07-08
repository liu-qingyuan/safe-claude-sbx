package watchdog

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestSupervisorFailsClosedOnRouteChangeBeforeHostEgressCheck(t *testing.T) {
	events := make(chan Event, 1)
	cleanup := &recordingCleanup{}
	hostEgressCalls := 0
	supervisor := Supervisor{
		Events: events,
		Check: RuntimeChecker{
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
	if !strings.Contains(err.Error(), "default route changed from startup interface utun9 to en0") {
		t.Fatalf("expected route failure, got %v", err)
	}
	if hostEgressCalls != 0 {
		t.Fatalf("route failure should happen before host egress check, got %d calls", hostEgressCalls)
	}
	if cleanup.calls != 1 {
		t.Fatalf("expected one cleanup after route failure, got %d", cleanup.calls)
	}
}

func TestSupervisorCleansUpOnHostEgressMismatch(t *testing.T) {
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
			HostEgress: fakeHostEgressChecker{
				result: network.EgressResult{
					OK:            false,
					ExpectedIP:    "203.0.113.10",
					ObservedIP:    "198.51.100.77",
					FailureKind:   network.EgressFailureMismatch,
					FailureReason: "host egress observed IP 198.51.100.77 does not match expected IP 203.0.113.10",
				},
				err: errors.New("host-egress-mismatch: observed IP mismatch"),
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
	if !strings.Contains(err.Error(), "host egress drift") {
		t.Fatalf("expected host egress drift, got %v", err)
	}
	if strings.Contains(err.Error(), "sandbox egress") {
		t.Fatalf("host drift should not be reported as sandbox egress failure: %v", err)
	}
	if cleanup.calls != 1 {
		t.Fatalf("expected one cleanup after egress failure, got %d", cleanup.calls)
	}
}

func TestSupervisorKeepsRunningWhenHostCenteredRuntimeCheckPasses(t *testing.T) {
	events := make(chan Event, 1)
	backendExit := make(chan error)
	cleanup := &recordingCleanup{}
	supervisor := Supervisor{
		Events:      events,
		BackendExit: backendExit,
		Check: RuntimeChecker{
			Config:              runtimeCheckConfig(),
			StartupTUNInterface: "utun9",
			RouteRunner: fakeRouteRunner{
				routeInterface: "utun9",
				interfaces:     map[string]bool{"utun9": true},
			},
			HostEgress: fakeHostEgressChecker{
				result: network.EgressResult{OK: true, ObservedIP: "203.0.113.10", ExpectedIP: "203.0.113.10"},
			},
		},
		Cleanup: cleanup,
	}

	done := make(chan error, 1)
	go func() {
		done <- supervisor.Run(context.Background())
	}()

	events <- Event{Source: "route-monitor", Detail: "route unchanged"}
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

func TestOnceCleanupRunsOnlyOneCleanupConcurrently(t *testing.T) {
	cleanup := &blockingCleanup{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	once := &onceCleanup{cleanup: cleanup}
	results := make(chan error, 3)

	for range 3 {
		go func() {
			results <- once.Do(context.Background())
		}()
	}

	select {
	case <-cleanup.started:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not start")
	}
	close(cleanup.release)
	for range 3 {
		if err := waitDone(t, results); err != nil {
			t.Fatalf("cleanup returned error: %v", err)
		}
	}
	if cleanup.calls != 1 {
		t.Fatalf("expected one actual cleanup call, got %d", cleanup.calls)
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

type blockingCleanup struct {
	calls   int
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *blockingCleanup) Cleanup(context.Context) error {
	c.calls++
	c.once.Do(func() {
		close(c.started)
	})
	<-c.release
	return nil
}

var errRuntimeCheck = errors.New("runtime check failed")
