package watchdog

import (
	"context"
	"errors"
	"testing"
	"time"
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
