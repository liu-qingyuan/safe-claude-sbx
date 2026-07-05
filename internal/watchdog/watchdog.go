package watchdog

import (
	"context"
	"fmt"
	"sync"
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

type CleanupCoordinator interface {
	Cleanup(ctx context.Context) error
}

type CleanupFunc func(context.Context) error

func (f CleanupFunc) Cleanup(ctx context.Context) error {
	return f(ctx)
}

type Supervisor struct {
	Events      <-chan Event
	EventErrors <-chan error
	BackendExit <-chan error
	Check       Checker
	Cleanup     CleanupCoordinator
}

func (s Supervisor) Run(ctx context.Context) error {
	cleanup := onceCleanup{cleanup: s.Cleanup}
	for {
		select {
		case <-ctx.Done():
			if err := cleanup.Do(context.Background()); err != nil {
				return err
			}
			return ctx.Err()
		case err, ok := <-s.BackendExit:
			if !ok {
				return cleanup.Do(context.Background())
			}
			if err != nil {
				if cleanupErr := cleanup.Do(context.Background()); cleanupErr != nil {
					return fmt.Errorf("backend exited: %w; cleanup failed: %v", err, cleanupErr)
				}
				return fmt.Errorf("backend exited: %w", err)
			}
			return cleanup.Do(context.Background())
		case event, ok := <-s.Events:
			if !ok {
				s.Events = nil
				continue
			}
			result, err := s.Check.Check(ctx, event)
			if err != nil {
				if cleanupErr := cleanup.Do(context.Background()); cleanupErr != nil {
					return fmt.Errorf("%s runtime check failed: %w; cleanup failed: %v", event.Source, err, cleanupErr)
				}
				return fmt.Errorf("%s runtime check failed: %w", event.Source, err)
			}
			if !result.OK {
				if cleanupErr := cleanup.Do(context.Background()); cleanupErr != nil {
					return fmt.Errorf("%s runtime policy failed: %s; cleanup failed: %v", event.Source, result.Reason, cleanupErr)
				}
				return fmt.Errorf("%s runtime policy failed: %s", event.Source, result.Reason)
			}
		case err, ok := <-s.EventErrors:
			if !ok {
				s.EventErrors = nil
				continue
			}
			if err != nil {
				if cleanupErr := cleanup.Do(context.Background()); cleanupErr != nil {
					return fmt.Errorf("watchdog event source failed: %w; cleanup failed: %v", err, cleanupErr)
				}
				return fmt.Errorf("watchdog event source failed: %w", err)
			}
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
