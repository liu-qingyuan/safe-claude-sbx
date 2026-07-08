package watchdog

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
)

func TestClashAppHomeMonitorEmitsMetadataChange(t *testing.T) {
	appHome := t.TempDir()
	writeWatchdogFile(t, appHome+"/clash-verge.yaml", "tun:\n  enable: true\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, errs := ClashAppHomeMonitor{
		Policy:       config.ClashVerge{AppHome: appHome},
		PollInterval: 10 * time.Millisecond,
		Paths:        []string{"clash-verge.yaml"},
	}.Start(ctx)

	select {
	case err := <-errs:
		t.Fatalf("monitor failed before metadata change: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	writeWatchdogFile(t, appHome+"/clash-verge.yaml", "tun:\n  enable: true\n  secret: redacted-fixture\n")

	select {
	case event := <-events:
		if event.Source != ClashAppHomeEventSource {
			t.Fatalf("expected clash app-home event source, got %#v", event)
		}
		if !strings.Contains(event.Detail, "clash-verge.yaml") {
			t.Fatalf("expected changed metadata path, got %#v", event)
		}
		if strings.Contains(event.Detail, "redacted-fixture") {
			t.Fatalf("event detail must not include file content: %#v", event)
		}
	case err := <-errs:
		t.Fatalf("monitor failed after metadata change: %v", err)
	case <-time.After(time.Second):
		t.Fatal("monitor did not emit metadata event")
	}
}

func TestClashAppHomeMonitorErrorsWhenAppHomeMissing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, errs := ClashAppHomeMonitor{
		Policy:       config.ClashVerge{AppHome: t.TempDir() + "/missing"},
		PollInterval: 10 * time.Millisecond,
	}.Start(ctx)

	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("expected app-home error")
		}
		if !strings.Contains(err.Error(), "network.clash_verge.app_home") {
			t.Fatalf("expected config-path diagnostic, got %v", err)
		}
		if strings.Contains(err.Error(), "/missing") {
			t.Fatalf("diagnostic should not print the local app-home path: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("monitor did not report missing app-home")
	}
}

func TestClashAppHomeMonitorErrorsWhenAppHomeUnreadable(t *testing.T) {
	appHome := t.TempDir()
	if err := os.Chmod(appHome, 0); err != nil {
		t.Fatalf("make app-home unreadable: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(appHome, 0o700)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, errs := ClashAppHomeMonitor{
		Policy:       config.ClashVerge{AppHome: appHome},
		PollInterval: 10 * time.Millisecond,
	}.Start(ctx)

	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("expected unreadable app-home error")
		}
		if !strings.Contains(err.Error(), "network.clash_verge.app_home") {
			t.Fatalf("expected config-path diagnostic, got %v", err)
		}
		if strings.Contains(err.Error(), appHome) {
			t.Fatalf("diagnostic should not print the local app-home path: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("monitor did not report unreadable app-home")
	}
}

func TestMergedRouteAndClashEventsShareSupervisorDebounce(t *testing.T) {
	routeEvents := make(chan Event, 1)
	clashEvents := make(chan Event, 1)
	backendExit := make(chan error)
	checked := make(chan Event, 2)
	cleanup := &recordingCleanup{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, errs := MergeEventStreams(ctx, []<-chan Event{routeEvents, clashEvents}, nil)
	supervisor := Supervisor{
		Events:        events,
		EventErrors:   errs,
		BackendExit:   backendExit,
		EventDebounce: 25 * time.Millisecond,
		Check: CheckFunc(func(_ context.Context, event Event) (CheckResult, error) {
			checked <- event
			return CheckResult{OK: true}, nil
		}),
		Cleanup: cleanup,
	}

	done := make(chan error, 1)
	go func() {
		done <- supervisor.Run(ctx)
	}()

	routeEvents <- Event{Source: "route-monitor", Detail: "route changed"}
	time.Sleep(5 * time.Millisecond)
	clashEvents <- Event{Source: ClashAppHomeEventSource, Detail: "metadata changed: clash-verge.yaml"}

	select {
	case <-checked:
	case <-time.After(time.Second):
		t.Fatal("runtime check was not called after merged events")
	}
	select {
	case event := <-checked:
		t.Fatalf("expected one debounced runtime check, got extra %#v", event)
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

func writeWatchdogFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
