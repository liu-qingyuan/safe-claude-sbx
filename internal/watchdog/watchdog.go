package watchdog

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Event struct {
	Source string
	Detail string
}

type CheckResult struct {
	OK     bool
	Reason string
}

type Checker interface {
	Check(ctx context.Context, event Event) (CheckResult, error)
}

type CheckFunc func(context.Context, Event) (CheckResult, error)

func (f CheckFunc) Check(ctx context.Context, event Event) (CheckResult, error) {
	return f(ctx, event)
}

const DefaultEventDebounce = 200 * time.Millisecond

type CleanupCoordinator interface {
	Cleanup(ctx context.Context) error
}

type CleanupFunc func(context.Context) error

func (f CleanupFunc) Cleanup(ctx context.Context) error {
	return f(ctx)
}

type Supervisor struct {
	Events        <-chan Event
	EventErrors   <-chan error
	BackendExit   <-chan error
	Check         Checker
	Cleanup       CleanupCoordinator
	EventDebounce time.Duration
}

func (s Supervisor) Run(ctx context.Context) error {
	cleanup := onceCleanup{cleanup: s.Cleanup}
	checkCtx, cancelChecks := context.WithCancel(ctx)
	defer cancelChecks()
	cleanupAfterCancel := func(ctx context.Context) error {
		cancelChecks()
		return cleanup.Do(ctx)
	}

	checks := make(chan checkOutcome, 1)
	var debounceTimer *time.Timer
	var debounceC <-chan time.Time
	var pendingEvent Event
	var hasPendingEvent bool
	defer stopTimer(debounceTimer)

	startCheck := func(event Event) {
		if s.Check == nil {
			return
		}
		go func() {
			result, err := s.Check.Check(checkCtx, event)
			select {
			case checks <- checkOutcome{event: event, result: result, err: err}:
			case <-checkCtx.Done():
			}
		}()
	}
	scheduleCheck := func(event Event) {
		if s.EventDebounce <= 0 {
			startCheck(event)
			return
		}
		pendingEvent = event
		hasPendingEvent = true
		if debounceTimer == nil {
			debounceTimer = time.NewTimer(s.EventDebounce)
			debounceC = debounceTimer.C
			return
		}
		if !debounceTimer.Stop() {
			select {
			case <-debounceTimer.C:
			default:
			}
		}
		debounceTimer.Reset(s.EventDebounce)
		debounceC = debounceTimer.C
	}

	for {
		select {
		case <-ctx.Done():
			if err := cleanupAfterCancel(context.Background()); err != nil {
				return err
			}
			return ctx.Err()
		case err, ok := <-s.BackendExit:
			if !ok {
				return cleanupAfterCancel(context.Background())
			}
			if err != nil {
				if cleanupErr := cleanupAfterCancel(context.Background()); cleanupErr != nil {
					return fmt.Errorf("backend exited: %w; cleanup failed: %v", err, cleanupErr)
				}
				return fmt.Errorf("backend exited: %w", err)
			}
			return cleanupAfterCancel(context.Background())
		case event, ok := <-s.Events:
			if !ok {
				s.Events = nil
				continue
			}
			scheduleCheck(event)
		case <-debounceC:
			debounceC = nil
			if hasPendingEvent {
				hasPendingEvent = false
				startCheck(pendingEvent)
			}
		case check := <-checks:
			if check.err != nil {
				if cleanupErr := cleanupAfterCancel(context.Background()); cleanupErr != nil {
					return fmt.Errorf("%s runtime check failed: %w; cleanup failed: %v", check.event.Source, check.err, cleanupErr)
				}
				return fmt.Errorf("%s runtime check failed: %w", check.event.Source, check.err)
			}
			if !check.result.OK {
				if cleanupErr := cleanupAfterCancel(context.Background()); cleanupErr != nil {
					return fmt.Errorf("%s runtime policy failed: %s; cleanup failed: %v", check.event.Source, check.result.Reason, cleanupErr)
				}
				return fmt.Errorf("%s runtime policy failed: %s", check.event.Source, check.result.Reason)
			}
		case err, ok := <-s.EventErrors:
			if !ok {
				s.EventErrors = nil
				continue
			}
			if err != nil {
				if cleanupErr := cleanupAfterCancel(context.Background()); cleanupErr != nil {
					return fmt.Errorf("watchdog event source failed: %w; cleanup failed: %v", err, cleanupErr)
				}
				return fmt.Errorf("watchdog event source failed: %w", err)
			}
		}
	}
}

type checkOutcome struct {
	event  Event
	result CheckResult
	err    error
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

type onceCleanup struct {
	cleanup CleanupCoordinator
	once    sync.Once
	err     error
}

func (c *onceCleanup) Do(ctx context.Context) error {
	c.once.Do(func() {
		if c.cleanup != nil {
			c.err = c.cleanup.Cleanup(ctx)
		}
	})
	return c.err
}
