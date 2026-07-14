package egressguard

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/watchdog"
)

func TestHostInheritedAdapterThroughEgressGuardInterface(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)
	cfg := config.Config{
		Network: config.Network{
			Egress: config.Egress{Mode: "host-inherited"},
			EgressIP: config.EgressIP{
				ExpectedIP:     "203.0.113.10",
				HostCheckURL:   server.URL,
				TimeoutSeconds: 1,
			},
		},
	}

	var guard EgressGuard
	guard, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("create guard: %v", err)
	}
	acquired, err := guard.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire host egress: %v", err)
	}
	if len(acquired.Messages) != 1 || !strings.Contains(acquired.Messages[0], "host egress ok: observed IP 203.0.113.10") {
		t.Fatalf("unexpected acquisition result %#v", acquired)
	}
	if _, err := guard.ValidateMain(context.Background()); err != nil {
		t.Fatalf("validate host main: %v", err)
	}
	if _, err := guard.Revoke(context.Background()); err != nil {
		t.Fatalf("revoke host lease: %v", err)
	}
}

func TestHostInheritedAdapterWatchProvidesExistingEventsAndChecker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)
	routeEvents := make(chan watchdog.Event, 1)
	routeErrors := make(chan error)
	clashEvents := make(chan watchdog.Event)
	clashErrors := make(chan error)
	guard := hostInheritedAdapter{
		cfg: config.Config{Network: config.Network{
			ClashVerge: config.ClashVerge{RouteCheckTarget: "1.1.1.1"},
			EgressIP: config.EgressIP{
				ExpectedIP:     "203.0.113.10",
				HostCheckURL:   server.URL,
				TimeoutSeconds: 1,
			},
		}},
		routeEvents: fakeRuntimeEventSource{events: routeEvents, errs: routeErrors},
		clashEvents: fakeRuntimeEventSource{events: clashEvents, errs: clashErrors},
		routeRunner: hostRuntimeRouteRunner{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := guard.Watch(ctx, WatchInput{StartupTUNInterface: "utun9"})
	routeEvents <- watchdog.Event{Source: "route-monitor", Detail: "route changed"}

	event := <-runtime.Events
	if event.Source != "route-monitor" {
		t.Fatalf("expected route event, got %#v", event)
	}
	result, err := runtime.Checker.Check(ctx, event)
	if err != nil || !result.OK {
		t.Fatalf("expected host runtime check to pass, result=%#v err=%v", result, err)
	}
}

func TestDedicatedGatewayAdapterRejectsUnsupportedProtocolIsolationBeforeLease(t *testing.T) {
	executor := newFakeCommandExecutor()
	guard := newDedicatedGatewayAdapter(
		dedicatedConfig("http://127.0.0.1:19090"),
		&fakeMainSandbox{},
		executor,
		&http.Client{Timeout: time.Second},
	)

	_, err := guard.Acquire(context.Background())

	if err == nil || !strings.Contains(err.Error(), "dedicated protocol isolation unsupported: sbx v0.34.0 provides HTTP upstream only; generic TCP and DNS are not fail closed") {
		t.Fatalf("expected protocol capability failure, got %v", err)
	}
	if got := executor.commandLog(); got != "sbx version" {
		t.Fatalf("expected capability check before lease side effects, got:\n%s", got)
	}
	if executor.started {
		t.Fatal("unsupported protocol isolation must not start sandboxd")
	}
}

func TestDedicatedGatewayAdapterRejectsUnknownProtocolCapabilityWithoutAssumingItsBehavior(t *testing.T) {
	executor := newFakeCommandExecutor()
	executor.version = "sbx version: v9.9.9 future-build\n"
	guard := newDedicatedGatewayAdapter(
		dedicatedConfig("http://127.0.0.1:19090"),
		&fakeMainSandbox{},
		executor,
		&http.Client{Timeout: time.Second},
	)

	_, err := guard.Acquire(context.Background())

	want := "dedicated protocol isolation unsupported: sbx v9.9.9 has no validated generic TCP and DNS contract"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("expected unknown capability failure %q, got %v", want, err)
	}
	if strings.Contains(err.Error(), "provides HTTP upstream only") {
		t.Fatalf("unknown version diagnostic assumed unsupported behavior: %v", err)
	}
	if got := executor.commandLog(); got != "sbx version" {
		t.Fatalf("expected capability check before lease side effects, got:\n%s", got)
	}
}

func TestDedicatedGatewayAdapterSuccessThroughEgressGuardInterface(t *testing.T) {
	const secret = "controller-secret-value"
	t.Setenv("SAFE_CLAUDE_SBX_MIHOMO_SECRET", secret)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"version":"v1.19.28"}`)
	}))
	t.Cleanup(controller.Close)

	main := &fakeMainSandbox{}
	executor := newFakeCommandExecutor()
	cfg := dedicatedConfig(controller.URL)
	var guard EgressGuard = newSupportedDedicatedGatewayAdapter(cfg, main, executor, controller.Client())

	acquired, err := guard.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire dedicated lease: %v", err)
	}
	if !acquired.CleanupCreatedMain {
		t.Fatal("expected dedicated doctor to clean a command-created main")
	}
	if !containsMessage(acquired.Messages, "gateway controller ok") || !containsMessage(acquired.Messages, "sandboxd lease ok") {
		t.Fatalf("unexpected acquisition messages %#v", acquired.Messages)
	}
	startedEnv := executor.startedEnvironment()
	if !containsEnvironment(startedEnv, "DOCKER_SANDBOXES_PROXY=http://127.0.0.1:17890") {
		t.Fatalf("dedicated daemon missing upstream environment: %#v", startedEnv)
	}
	for _, forbidden := range []string{"HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", secret} {
		if strings.Contains(strings.Join(startedEnv, "\n"), forbidden) {
			t.Fatalf("dedicated daemon environment contains forbidden value %q: %#v", forbidden, startedEnv)
		}
	}

	validated, err := guard.ValidateMain(context.Background())
	if err != nil {
		t.Fatalf("validate dedicated main: %v", err)
	}
	if !containsMessage(validated.Messages, "controller isolation ok") {
		t.Fatalf("unexpected validation messages %#v", validated.Messages)
	}
	if main.endpoint != controller.URL {
		t.Fatalf("expected controller isolation endpoint %q, got %q", controller.URL, main.endpoint)
	}

	revoked, err := guard.Revoke(context.Background())
	if err != nil {
		t.Fatalf("revoke dedicated lease: %v", err)
	}
	if !containsMessage(revoked.Messages, "sandboxd lease revoked") {
		t.Fatalf("unexpected revoke messages %#v", revoked.Messages)
	}
	if got := executor.commandLog(); !strings.HasSuffix(got, "sbx daemon stop\nsbx ls") {
		t.Fatalf("expected revoke then normal daemon restoration, got:\n%s", got)
	}
}

func TestDedicatedGatewayAdapterFailsWhenOwnedDaemonExits(t *testing.T) {
	const secret = "controller-secret-value"
	t.Setenv("SAFE_CLAUDE_SBX_MIHOMO_SECRET", secret)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fmt.Fprintln(w, `{"version":"v1.19.28"}`)
	}))
	t.Cleanup(controller.Close)
	executor := newFakeCommandExecutor()
	guard := newSupportedDedicatedGatewayAdapter(dedicatedConfig(controller.URL), &fakeMainSandbox{}, executor, controller.Client())

	if _, err := guard.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire dedicated lease: %v", err)
	}
	executor.process.finish(fmt.Errorf("daemon exited"))
	time.Sleep(10 * time.Millisecond)

	_, err := guard.ValidateMain(context.Background())
	if err == nil || !strings.Contains(err.Error(), "sandboxd lease invalid: dedicated daemon exited") {
		t.Fatalf("expected daemon drift failure, got %v", err)
	}
	if _, err := guard.Revoke(context.Background()); err != nil {
		t.Fatalf("revoke after daemon exit: %v", err)
	}
}

func TestDedicatedGatewayAdapterWatchFailsImmediatelyWhenOwnedDaemonExits(t *testing.T) {
	controller := newAuthenticatedController(t)
	executor := newFakeCommandExecutor()
	guard := newSupportedDedicatedGatewayAdapter(dedicatedConfig(controller.URL), &fakeMainSandbox{}, executor, controller.Client())
	if _, err := guard.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire dedicated lease: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := guard.Watch(ctx, WatchInput{})
	executor.process.finish(fmt.Errorf("daemon exited"))

	select {
	case event := <-runtime.Events:
		if event.Source != DedicatedDaemonEventSource {
			t.Fatalf("expected dedicated daemon event, got %#v", event)
		}
		result, err := runtime.Checker.Check(ctx, event)
		if err == nil || result.OK || !strings.Contains(result.Reason, "dedicated daemon exited") {
			t.Fatalf("expected daemon exit runtime failure, result=%#v err=%v", result, err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dedicated daemon exit event")
	}
	if _, err := guard.Revoke(context.Background()); err != nil {
		t.Fatalf("revoke after daemon exit: %v", err)
	}
}

func TestDedicatedGatewayAdapterWatchChecksLeaseAndControllerBeforeDedicatedEgress(t *testing.T) {
	controller := newAuthenticatedController(t)
	executor := newFakeCommandExecutor()
	main := &fakeMainSandbox{egress: network.EgressResult{
		OK:         true,
		ExpectedIP: "203.0.113.10",
		ObservedIP: "203.0.113.10",
	}}
	guard := newSupportedDedicatedGatewayAdapter(dedicatedConfig(controller.URL), main, executor, controller.Client())
	adapter := guard.(*dedicatedGatewayAdapter)
	adapter.runtimeInterval = 5 * time.Millisecond
	if _, err := guard.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire dedicated lease: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := guard.Watch(ctx, WatchInput{})
	select {
	case event := <-runtime.Events:
		if event.Source != DedicatedHealthEventSource {
			t.Fatalf("expected dedicated health event, got %#v", event)
		}
		result, err := runtime.Checker.Check(ctx, event)
		if err != nil || !result.OK {
			t.Fatalf("expected dedicated runtime check to pass, result=%#v err=%v", result, err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for dedicated health event")
	}
	if main.egressCalls != 1 {
		t.Fatalf("expected one bounded dedicated egress check, got %d", main.egressCalls)
	}
	log := executor.commandLog()
	if !strings.Contains(log, "sbx daemon status") || strings.Count(log, "sbx ls") < 2 {
		t.Fatalf("expected daemon and lease checks before egress, got:\n%s", log)
	}
	if _, err := guard.Revoke(context.Background()); err != nil {
		t.Fatalf("revoke dedicated lease: %v", err)
	}
}

func TestDedicatedGatewayAdapterWatchBoundsSandboxdHealthCheck(t *testing.T) {
	controller := newAuthenticatedController(t)
	executor := &blockingRuntimeStatusExecutor{fakeCommandExecutor: newFakeCommandExecutor()}
	main := &fakeMainSandbox{egress: network.EgressResult{OK: true}}
	cfg := dedicatedConfig(controller.URL)
	cfg.Network.EgressIP.TimeoutSeconds = 1
	guard := newSupportedDedicatedGatewayAdapter(cfg, main, executor, controller.Client())
	if _, err := guard.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire dedicated lease: %v", err)
	}

	runtime := guard.Watch(context.Background(), WatchInput{})
	started := time.Now()
	result, err := runtime.Checker.Check(context.Background(), watchdog.Event{Source: DedicatedHealthEventSource})
	if err == nil || result.OK || !strings.Contains(result.Reason, "dedicated daemon status failed") {
		t.Fatalf("expected bounded daemon status failure, result=%#v err=%v", result, err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("runtime health check exceeded configured timeout: %s", elapsed)
	}
	if main.egressCalls != 0 {
		t.Fatalf("stalled daemon status should fail before egress, got %d calls", main.egressCalls)
	}
	if _, err := guard.Revoke(context.Background()); err != nil {
		t.Fatalf("revoke after bounded health failure: %v", err)
	}
}

func TestDedicatedGatewayAdapterWatchStopsBeforeEgressWhenControllerIsLost(t *testing.T) {
	controller := newAuthenticatedController(t)
	executor := newFakeCommandExecutor()
	main := &fakeMainSandbox{egress: network.EgressResult{OK: true}}
	guard := newSupportedDedicatedGatewayAdapter(dedicatedConfig(controller.URL), main, executor, controller.Client())
	adapter := guard.(*dedicatedGatewayAdapter)
	adapter.runtimeInterval = 5 * time.Millisecond
	if _, err := guard.Acquire(context.Background()); err != nil {
		controller.Close()
		t.Fatalf("acquire dedicated lease: %v", err)
	}
	controller.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := guard.Watch(ctx, WatchInput{})
	event := <-runtime.Events
	result, err := runtime.Checker.Check(ctx, event)
	if err == nil || result.OK || !strings.Contains(result.Reason, "gateway controller invalid") {
		t.Fatalf("expected controller loss runtime failure, result=%#v err=%v", result, err)
	}
	if main.egressCalls != 0 {
		t.Fatalf("controller loss should fail before dedicated egress check, got %d calls", main.egressCalls)
	}
	if _, err := guard.Revoke(context.Background()); err != nil {
		t.Fatalf("revoke after controller loss: %v", err)
	}
}

func TestDedicatedGatewayAdapterWatchStopsBeforeEgressWhenLeaseDrifts(t *testing.T) {
	controller := newAuthenticatedController(t)
	executor := &scopeDriftExecutor{fakeCommandExecutor: newFakeCommandExecutor()}
	main := &fakeMainSandbox{egress: network.EgressResult{OK: true}}
	guard := newSupportedDedicatedGatewayAdapter(dedicatedConfig(controller.URL), main, executor, controller.Client())
	adapter := guard.(*dedicatedGatewayAdapter)
	adapter.runtimeInterval = 5 * time.Millisecond
	if _, err := guard.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire dedicated lease: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := guard.Watch(ctx, WatchInput{})
	event := <-runtime.Events
	result, err := runtime.Checker.Check(ctx, event)
	if err == nil || result.OK || !strings.Contains(result.Reason, "unrelated sandbox conflict: late-sbx") {
		t.Fatalf("expected exclusive lease runtime failure, result=%#v err=%v", result, err)
	}
	if main.egressCalls != 0 {
		t.Fatalf("lease drift should fail before dedicated egress check, got %d calls", main.egressCalls)
	}
	if _, err := guard.Revoke(context.Background()); err != nil {
		t.Fatalf("revoke after lease drift: %v", err)
	}
}

func TestDedicatedGatewayAdapterWatchFailsOnDedicatedEgressDrift(t *testing.T) {
	controller := newAuthenticatedController(t)
	executor := newFakeCommandExecutor()
	main := &fakeMainSandbox{
		egress: network.EgressResult{
			OK:            false,
			ExpectedIP:    "203.0.113.10",
			ObservedIP:    "198.51.100.77",
			FailureKind:   "sandbox-egress-mismatch",
			FailureReason: "sandbox egress observed IP 198.51.100.77 does not match expected IP 203.0.113.10",
		},
		egressErr: fmt.Errorf("sandbox-egress-mismatch: observed IP mismatch"),
	}
	guard := newSupportedDedicatedGatewayAdapter(dedicatedConfig(controller.URL), main, executor, controller.Client())
	adapter := guard.(*dedicatedGatewayAdapter)
	adapter.runtimeInterval = 5 * time.Millisecond
	if _, err := guard.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire dedicated lease: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := guard.Watch(ctx, WatchInput{})
	event := <-runtime.Events
	result, err := runtime.Checker.Check(ctx, event)
	if err == nil || result.OK || !strings.Contains(result.Reason, "dedicated egress drift") {
		t.Fatalf("expected dedicated egress drift failure, result=%#v err=%v", result, err)
	}
	if main.egressCalls != 1 {
		t.Fatalf("expected one bounded dedicated egress check, got %d", main.egressCalls)
	}
	if _, err := guard.Revoke(context.Background()); err != nil {
		t.Fatalf("revoke after egress drift: %v", err)
	}
}

func TestDedicatedGatewayAdapterFailsWhenControllerStopsAfterAcquire(t *testing.T) {
	const secret = "controller-secret-value"
	t.Setenv("SAFE_CLAUDE_SBX_MIHOMO_SECRET", secret)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fmt.Fprintln(w, `{"version":"v1.19.28"}`)
	}))
	executor := newFakeCommandExecutor()
	guard := newSupportedDedicatedGatewayAdapter(dedicatedConfig(controller.URL), &fakeMainSandbox{}, executor, controller.Client())
	if _, err := guard.Acquire(context.Background()); err != nil {
		controller.Close()
		t.Fatalf("acquire dedicated lease: %v", err)
	}
	controller.Close()

	_, err := guard.ValidateMain(context.Background())
	if err == nil || !strings.Contains(err.Error(), "gateway controller invalid: loopback controller unavailable") {
		t.Fatalf("expected stopped controller failure, got %v", err)
	}
	if _, err := guard.Revoke(context.Background()); err != nil {
		t.Fatalf("revoke after controller stop: %v", err)
	}
}

func TestDedicatedGatewayAdapterRestoresExistingRunningMain(t *testing.T) {
	const secret = "controller-secret-value"
	t.Setenv("SAFE_CLAUDE_SBX_MIHOMO_SECRET", secret)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fmt.Fprintln(w, `{"version":"v1.19.28"}`)
	}))
	t.Cleanup(controller.Close)
	executor := newFakeCommandExecutor()
	executor.listOutput = "SANDBOX AGENT STATUS PORTS WORKSPACE\nclaude-sbx claude running - .\n"
	cfg := dedicatedConfig(controller.URL)
	cfg.Workspace.Mount = "."
	guard := newSupportedDedicatedGatewayAdapter(cfg, &fakeMainSandbox{}, executor, controller.Client())

	if _, err := guard.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire with existing main: %v", err)
	}
	if _, err := guard.ValidateMain(context.Background()); err != nil {
		t.Fatalf("validate existing main: %v", err)
	}
	if _, err := guard.Revoke(context.Background()); err != nil {
		t.Fatalf("revoke existing main lease: %v", err)
	}

	log := executor.commandLog()
	if strings.Count(log, "sbx exec claude-sbx true") != 2 {
		t.Fatalf("expected existing main reload under dedicated and restored daemon, got:\n%s", log)
	}
}

func TestDedicatedGatewayAdapterFailsClosedOnScopeDrift(t *testing.T) {
	const secret = "controller-secret-value"
	t.Setenv("SAFE_CLAUDE_SBX_MIHOMO_SECRET", secret)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fmt.Fprintln(w, `{"version":"v1.19.28"}`)
	}))
	t.Cleanup(controller.Close)
	executor := &scopeDriftExecutor{fakeCommandExecutor: newFakeCommandExecutor()}
	guard := newSupportedDedicatedGatewayAdapter(dedicatedConfig(controller.URL), &fakeMainSandbox{}, executor, controller.Client())

	if _, err := guard.Acquire(context.Background()); err != nil {
		t.Fatalf("acquire dedicated lease: %v", err)
	}
	_, err := guard.ValidateMain(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unrelated sandbox conflict: late-sbx") {
		t.Fatalf("expected scope drift failure, got %v", err)
	}
	if _, err := guard.Revoke(context.Background()); err != nil {
		t.Fatalf("revoke after scope drift: %v", err)
	}
}

func TestInspectLeaseScopeRejectsMalformedSandboxRows(t *testing.T) {
	_, err := inspectLeaseScope("SANDBOX AGENT STATUS PORTS WORKSPACE\nbroken-row\n", "claude-sbx", "claude", ".")

	if err == nil || !strings.Contains(err.Error(), "unrecognized sbx ls output") {
		t.Fatalf("expected malformed scope failure, got %v", err)
	}
}

func TestInspectLeaseScopeNormalizesConfiguredWorkspace(t *testing.T) {
	workspace, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	output := "SANDBOX AGENT STATUS PORTS WORKSPACE\nclaude-sbx claude running - " + workspace + "\n"

	scope, err := inspectLeaseScope(output, "claude-sbx", "claude", ".")

	if err != nil {
		t.Fatalf("expected relative and absolute workspace to match: %v", err)
	}
	if !scope.mainWasRunning {
		t.Fatal("expected configured main to be recognized as running")
	}
}

func TestInspectLeaseScopeRejectsStoppedUnrelatedSandbox(t *testing.T) {
	output := "SANDBOX AGENT STATUS PORTS WORKSPACE\nother-sbx shell stopped - /tmp/other\n"

	scope, err := inspectLeaseScope(output, "claude-sbx", "claude", ".")

	if err != nil {
		t.Fatalf("inspect scope: %v", err)
	}
	if len(scope.conflicts) != 1 || scope.conflicts[0] != "other-sbx" {
		t.Fatalf("expected stopped unrelated sandbox conflict, got %#v", scope)
	}
}

func TestDedicatedGatewayAdapterReportsRestoreFailureAfterStartError(t *testing.T) {
	const secret = "controller-secret-value"
	t.Setenv("SAFE_CLAUDE_SBX_MIHOMO_SECRET", secret)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fmt.Fprintln(w, `{"version":"v1.19.28"}`)
	}))
	t.Cleanup(controller.Close)
	executor := &startRestoreFailureExecutor{}
	guard := newSupportedDedicatedGatewayAdapter(dedicatedConfig(controller.URL), &fakeMainSandbox{}, executor, controller.Client())

	_, err := guard.Acquire(context.Background())
	if err == nil || !strings.Contains(err.Error(), "restore normal daemon failed") {
		t.Fatalf("expected surfaced restore failure, got %v", err)
	}
	if _, err := guard.Revoke(context.Background()); err != nil {
		t.Fatalf("expected restore retry to succeed, got %v", err)
	}
}

func dedicatedConfig(controllerURL string) config.Config {
	return config.Config{
		Network: config.Network{
			Egress: config.Egress{
				Mode: "dedicated-gateway",
				DedicatedGateway: &config.DedicatedGateway{
					UpstreamURL:         "http://127.0.0.1:17890",
					ControllerURL:       controllerURL,
					ControllerSecretEnv: "SAFE_CLAUDE_SBX_MIHOMO_SECRET",
				},
			},
			EgressIP: config.EgressIP{TimeoutSeconds: 1},
		},
		Sandbox: config.Sandbox{MainName: "claude-sbx"},
	}
}

func newAuthenticatedController(t *testing.T) *httptest.Server {
	t.Helper()
	const secret = "controller-secret-value"
	t.Setenv("SAFE_CLAUDE_SBX_MIHOMO_SECRET", secret)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fmt.Fprintln(w, `{"version":"v1.19.28"}`)
	}))
	t.Cleanup(controller.Close)
	return controller
}

func newSupportedDedicatedGatewayAdapter(
	cfg config.Config,
	main MainSandbox,
	executor commandExecutor,
	client *http.Client,
) EgressGuard {
	return newDedicatedGatewayAdapterWithProtocolCheck(
		cfg,
		main,
		executor,
		client,
		func(context.Context) error { return nil },
	)
}

type fakeMainSandbox struct {
	endpoint    string
	err         error
	egress      network.EgressResult
	egressErr   error
	egressCalls int
}

type fakeRuntimeEventSource struct {
	events <-chan watchdog.Event
	errs   <-chan error
}

func (s fakeRuntimeEventSource) Start(context.Context) (<-chan watchdog.Event, <-chan error) {
	return s.events, s.errs
}

type hostRuntimeRouteRunner struct{}

func (hostRuntimeRouteRunner) Run(name string, args ...string) (string, error) {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	switch command {
	case "route get 1.1.1.1":
		return "interface: utun9\n", nil
	case "ifconfig utun9":
		return "utun9: flags=8051<UP,POINTOPOINT,RUNNING,MULTICAST>", nil
	default:
		return "", fmt.Errorf("unexpected route command %q", command)
	}
}

func (f *fakeMainSandbox) CheckMainEndpointIsolation(ctx context.Context, sandboxName, endpoint string) error {
	f.endpoint = endpoint
	return f.err
}

func (f *fakeMainSandbox) CheckRuntimeEgress(context.Context, config.Config) (network.EgressResult, error) {
	f.egressCalls++
	return f.egress, f.egressErr
}

type fakeCommandExecutor struct {
	mu         sync.Mutex
	commands   []string
	startEnv   []string
	process    *fakeProcess
	started    bool
	listOutput string
	version    string
}

type scopeDriftExecutor struct {
	*fakeCommandExecutor
	listCalls int
}

type blockingRuntimeStatusExecutor struct {
	*fakeCommandExecutor
	statusMu    sync.Mutex
	statusCalls int
}

func (e *blockingRuntimeStatusExecutor) Run(ctx context.Context, env []string, name string, args ...string) (commandResult, error) {
	if name == "sbx" && strings.Join(args, " ") == "daemon status" {
		e.statusMu.Lock()
		e.statusCalls++
		calls := e.statusCalls
		e.statusMu.Unlock()
		if calls > 1 {
			<-ctx.Done()
			return commandResult{}, ctx.Err()
		}
	}
	return e.fakeCommandExecutor.Run(ctx, env, name, args...)
}

func (e *scopeDriftExecutor) Run(ctx context.Context, env []string, name string, args ...string) (commandResult, error) {
	if name == "sbx" && strings.Join(args, " ") == "ls" {
		e.listCalls++
		if e.listCalls > 1 {
			e.fakeCommandExecutor.mu.Lock()
			e.fakeCommandExecutor.commands = append(e.fakeCommandExecutor.commands, "sbx ls")
			e.fakeCommandExecutor.mu.Unlock()
			return commandResult{stdout: "SANDBOX AGENT STATUS PORTS WORKSPACE\nlate-sbx shell running - /tmp/late\n"}, nil
		}
	}
	return e.fakeCommandExecutor.Run(ctx, env, name, args...)
}

type startRestoreFailureExecutor struct {
	listCalls int
}

func (e *startRestoreFailureExecutor) Run(ctx context.Context, env []string, name string, args ...string) (commandResult, error) {
	if name == "sbx" && strings.Join(args, " ") == "ls" {
		e.listCalls++
		if e.listCalls == 2 {
			return commandResult{}, fmt.Errorf("restore failed")
		}
		return commandResult{stdout: "No sandboxes found.\n"}, nil
	}
	return commandResult{}, nil
}

func (*startRestoreFailureExecutor) Start(env []string, name string, args ...string) (runningProcess, error) {
	return nil, fmt.Errorf("start failed")
}

func newFakeCommandExecutor() *fakeCommandExecutor {
	return &fakeCommandExecutor{
		process:    newFakeProcess(),
		listOutput: "No sandboxes found.\n",
		version:    "sbx version: v0.34.0 test-build\n",
	}
}

func (f *fakeCommandExecutor) Run(ctx context.Context, env []string, name string, args ...string) (commandResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	command := strings.Join(append([]string{name}, args...), " ")
	f.commands = append(f.commands, command)
	if command == "sbx daemon stop" && f.started {
		f.process.finish(nil)
	}
	if command == "sbx ls" {
		return commandResult{stdout: f.listOutput}, nil
	}
	if command == "sbx version" {
		return commandResult{stdout: f.version}, nil
	}
	return commandResult{}, nil
}

func (f *fakeCommandExecutor) Start(env []string, name string, args ...string) (runningProcess, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, strings.Join(append([]string{name}, args...), " "))
	f.startEnv = append([]string(nil), env...)
	f.started = true
	return f.process, nil
}

func (f *fakeCommandExecutor) startedEnvironment() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.startEnv...)
}

func (f *fakeCommandExecutor) commandLog() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.commands, "\n")
}

type fakeProcess struct {
	once sync.Once
	done chan error
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{done: make(chan error, 1)}
}

func (p *fakeProcess) Wait() error {
	return <-p.done
}

func (p *fakeProcess) Kill() error {
	p.finish(nil)
	return nil
}

func (p *fakeProcess) finish(err error) {
	p.once.Do(func() {
		p.done <- err
		close(p.done)
	})
}

func containsMessage(messages []string, want string) bool {
	for _, message := range messages {
		if strings.Contains(message, want) {
			return true
		}
	}
	return false
}

func containsEnvironment(environment []string, want string) bool {
	for _, entry := range environment {
		if entry == want {
			return true
		}
	}
	return false
}
