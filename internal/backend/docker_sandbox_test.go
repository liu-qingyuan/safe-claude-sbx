package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
)

func TestDockerSandboxAvailabilityDistinguishesStates(t *testing.T) {
	tests := []struct {
		name           string
		runner         stubRunner
		wantKind       AvailabilityKind
		wantOK         bool
		wantDiagnostic string
	}{
		{
			name: "available",
			runner: stubRunner{
				path: "/tmp/sbx",
				results: map[string]CommandResult{
					"sbx version": {Stdout: "sbx version: v0.34.0 fake\n"},
					"sbx ls":      {Stdout: "No sandboxes found.\n"},
				},
			},
			wantKind: AvailabilityAvailable,
			wantOK:   true,
		},
		{
			name:     "binary unavailable",
			runner:   stubRunner{lookPathErr: errors.New("not found")},
			wantKind: AvailabilityBinaryMissing,
		},
		{
			name: "version incompatible",
			runner: stubRunner{
				path: "/tmp/sbx",
				results: map[string]CommandResult{
					"sbx version": {Stdout: "unexpected tool\n"},
				},
			},
			wantKind: AvailabilityIncompatible,
		},
		{
			name: "backend unreachable",
			runner: stubRunner{
				path: "/tmp/sbx",
				results: map[string]CommandResult{
					"sbx version": {Stdout: "sbx version: v0.34.0 fake\n"},
					"sbx ls":      {Stderr: "ERROR: Not authenticated to Docker\n"},
				},
				errors: map[string]error{
					"sbx ls": errors.New("exit status 1"),
				},
			},
			wantKind: AvailabilityUnavailable,
		},
		{
			name: "control plane killed",
			runner: stubRunner{
				path: "/tmp/sbx",
				results: map[string]CommandResult{
					"sbx version": {Stdout: "sbx version: v0.34.0 fake\n"},
				},
				errors: map[string]error{
					"sbx ls": errors.New("signal: killed"),
				},
			},
			wantKind:       AvailabilityControlPlaneUnavailable,
			wantDiagnostic: "sbx control-plane unavailable",
		},
		{
			name: "control plane context canceled",
			runner: stubRunner{
				path: "/tmp/sbx",
				results: map[string]CommandResult{
					"sbx version": {Stdout: "sbx version: v0.34.0 fake\n"},
					"sbx ls":      {Stderr: "request failed: context canceled\n"},
				},
				errors: map[string]error{
					"sbx ls": context.Canceled,
				},
			},
			wantKind:       AvailabilityControlPlaneUnavailable,
			wantDiagnostic: "sbx control-plane unavailable",
		},
		{
			name: "control plane deadline exceeded",
			runner: stubRunner{
				path: "/tmp/sbx",
				results: map[string]CommandResult{
					"sbx version": {Stdout: "sbx version: v0.34.0 fake\n"},
				},
				errors: map[string]error{
					"sbx ls": context.DeadlineExceeded,
				},
			},
			wantKind:       AvailabilityControlPlaneUnavailable,
			wantDiagnostic: "command timed out",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (DockerSandbox{Runner: tt.runner, Binary: "sbx"}).CheckAvailability(context.Background())

			if got.Kind != tt.wantKind {
				t.Fatalf("expected kind %q, got %#v with err %v", tt.wantKind, got, err)
			}
			if got.OK != tt.wantOK {
				t.Fatalf("expected OK %v, got %#v", tt.wantOK, got)
			}
			if tt.wantOK && err != nil {
				t.Fatalf("availability unexpectedly failed: %v", err)
			}
			if !tt.wantOK && err == nil {
				t.Fatalf("availability unexpectedly succeeded: %#v", got)
			}
			if tt.wantDiagnostic != "" && !strings.Contains(err.Error(), tt.wantDiagnostic) {
				t.Fatalf("expected diagnostic %q in error %q", tt.wantDiagnostic, err)
			}
		})
	}
}

func TestDockerSandboxProbeReturnsStructuredInspectionAndIdempotentCleanup(t *testing.T) {
	runner := stubRunner{
		path: "/tmp/sbx",
		results: map[string]CommandResult{
			"sbx create --name probe shell .":                                        {Stdout: "created\n"},
			"sbx exec -e TZ=UTC -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 probe env": {Stdout: "PATH=/usr/bin\nHTTP_PROXY=http://gateway.docker.internal:3128\n"},
			"sbx exec probe pwd":                                                     {Stdout: "/workspace\n"},
			"sbx exec probe mount":                                                   {Stdout: "/dev/disk1 on /workspace type virtiofs\n"},
			"sbx exec -e TZ=UTC probe date":                                          {Stdout: "Sun Jul 5 00:00:00 UTC 2026\n"},
			"sbx exec -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 probe locale":        {Stdout: "LANG=en_US.UTF-8\n"},
			"sbx exec probe curl -fsS https://example.test/ip":                       {Stdout: "203.0.113.10\n"},
			"sbx stop probe":                                                         {Stderr: "sandbox not found\n"},
			"sbx rm --force probe":                                                   {Stderr: "sandbox not found\n"},
		},
		errors: map[string]error{
			"sbx stop probe":       errors.New("exit status 1"),
			"sbx rm --force probe": errors.New("exit status 1"),
		},
	}

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).Probe(context.Background(), probeConfig())

	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if !result.CleanupDone {
		t.Fatalf("expected cleanup to be marked done")
	}
	if result.Egress.ObservedIP != "203.0.113.10" {
		t.Fatalf("expected observed egress IP, got %#v", result.Egress)
	}
	if result.Inspection.Environment["HTTP_PROXY"] != "http://gateway.docker.internal:3128" {
		t.Fatalf("expected structured env observation, got %#v", result.Inspection.Environment)
	}
	if result.Inspection.WorkingDirectory != "/workspace" || !strings.Contains(result.Inspection.Mounts, "/workspace") {
		t.Fatalf("expected pwd and mount observations, got %#v", result.Inspection)
	}

	if err := (DockerSandbox{Runner: runner, Binary: "sbx"}).CleanupProbe(context.Background(), probeConfig()); err != nil {
		t.Fatalf("cleanup should be idempotent for already missing probe: %v", err)
	}
}

func TestDockerSandboxProbeRemovesStaleProbeSandboxAndRetriesCreate(t *testing.T) {
	runner := &staleProbeCreateRunner{}

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).Probe(context.Background(), probeConfig())

	if err != nil {
		t.Fatalf("probe failed after stale probe cleanup: %v", err)
	}
	if !result.CleanupDone {
		t.Fatalf("expected final probe cleanup to be marked done")
	}
	got := strings.Join(runner.calls, "\n")
	for _, want := range []string{
		"sbx create --name probe shell .",
		"sbx stop probe",
		"sbx rm --force probe",
		"sbx exec -e TZ=UTC -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 probe env",
		"sbx exec probe pwd",
		"sbx exec probe mount",
		"sbx exec -e TZ=UTC probe date",
		"sbx exec -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 probe locale",
		"sbx exec probe curl -fsS https://example.test/ip",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected stale probe cleanup and create retry command %q, got:\n%s", want, got)
		}
	}
	if !runner.sawPrefix("sbx exec probe sh -lc workspace=") {
		t.Fatalf("expected workspace visibility inspection, got:\n%s", got)
	}
}

func TestDockerSandboxCheckRuntimeEgressUsesMainSandboxAndConfiguredURL(t *testing.T) {
	calls := []string{}
	runner := stubRunner{
		path:  "/tmp/sbx",
		calls: &calls,
		results: map[string]CommandResult{
			"sbx exec main-sbx curl -fsS https://example.test/ip": {Stdout: "203.0.113.10\n"},
		},
	}
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).CheckRuntimeEgress(context.Background(), cfg)

	if err != nil {
		t.Fatalf("runtime egress check failed: %v", err)
	}
	if result.CleanupDone {
		t.Fatal("runtime egress check should not clean up a probe sandbox")
	}
	if !result.Egress.OK || result.Egress.ObservedIP != "203.0.113.10" {
		t.Fatalf("expected matching sandbox egress, got %#v", result.Egress)
	}
	got := strings.Join(calls, "\n")
	if got != "sbx exec main-sbx curl -fsS https://example.test/ip" {
		t.Fatalf("runtime egress check should only curl from main sandbox, got:\n%s", got)
	}
}

func TestDockerSandboxCheckRuntimeEgressUsesConfiguredTimeout(t *testing.T) {
	runner := &deadlineRecordingRunner{
		results: map[string]CommandResult{
			"sbx exec main-sbx curl -fsS https://example.test/ip": {Stdout: "203.0.113.10\n"},
		},
	}
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Network.EgressIP.TimeoutSeconds = 1

	_, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).CheckRuntimeEgress(context.Background(), cfg)

	if err != nil {
		t.Fatalf("runtime egress check failed: %v", err)
	}
	if runner.fastDeadlineCount != 1 {
		t.Fatalf("expected main sandbox curl to use runtime egress deadline, got %d", runner.fastDeadlineCount)
	}
	if runner.cleanupDeadlineCount != 0 {
		t.Fatalf("runtime egress should not run probe cleanup, got %d cleanup commands", runner.cleanupDeadlineCount)
	}
}

func TestDockerSandboxCheckRuntimeEgressClassifiesTimeoutAsIndeterminate(t *testing.T) {
	const sandboxCheckURL = "https://example.test/ip?token=secret-token&cookie=secret-cookie"
	runner := stubRunner{
		path: "/tmp/sbx",
		results: map[string]CommandResult{
			"sbx exec main-sbx curl -fsS " + sandboxCheckURL: {Stderr: "Get \"https://registry-1.docker.io/v2/\": net/http: request canceled while waiting for connection\n"},
		},
		errors: map[string]error{
			"sbx exec main-sbx curl -fsS " + sandboxCheckURL: context.DeadlineExceeded,
		},
	}
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Network.EgressIP.SandboxCheckURL = sandboxCheckURL

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).CheckRuntimeEgress(context.Background(), cfg)

	if err == nil {
		t.Fatal("expected runtime egress timeout")
	}
	if result.Egress.OK {
		t.Fatalf("runtime egress unexpectedly passed: %#v", result.Egress)
	}
	if result.Egress.FailureKind != "sandbox-egress-indeterminate" {
		t.Fatalf("expected indeterminate failure kind, got %#v", result.Egress)
	}
	if strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("timeout should not be reported as IP mismatch: %v", err)
	}
	if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "secret-cookie") {
		t.Fatalf("runtime egress diagnostic leaked sandbox_check_url secret: %v", err)
	}
}

func TestDockerSandboxPreflightMainSandboxCreatesAndInspectsMain(t *testing.T) {
	calls := []string{}
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Workspace.Mount = "/work/project"
	runner := mainPreflightRunner(cfg, &calls)

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).PreflightMainSandbox(context.Background(), cfg)

	if err != nil {
		t.Fatalf("main preflight failed: %v", err)
	}
	if result.SandboxName != "main-sbx" || !result.CreatedByCommand {
		t.Fatalf("expected new main sandbox result, got %#v", result)
	}
	if !result.Egress.OK || result.Egress.ObservedIP != "203.0.113.10" {
		t.Fatalf("expected matching egress, got %#v", result.Egress)
	}
	if result.Inspection.Environment["HTTP_PROXY"] != "http://gateway.docker.internal:3128" {
		t.Fatalf("expected environment observation, got %#v", result.Inspection.Environment)
	}
	got := strings.Join(calls, "\n")
	want := strings.Join([]string{
		"sbx ls",
		"sbx create --name main-sbx claude /work/project",
		"sbx exec -e TZ=UTC -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 main-sbx env",
		"sbx exec main-sbx pwd",
		"sbx exec main-sbx mount",
		"sbx exec main-sbx sh -lc " + workspaceVisibilityScript("/work/project"),
		"sbx exec -e TZ=UTC main-sbx date",
		"sbx exec -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 main-sbx locale",
		"sbx exec main-sbx curl -fsS https://example.test/ip",
	}, "\n")
	if got != want {
		t.Fatalf("expected main sandbox preflight command contract, got:\n%s", got)
	}
}

func TestDockerSandboxPreflightMainSandboxFailsOnEgressMismatch(t *testing.T) {
	calls := []string{}
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Workspace.Mount = "/work/project"
	runner := mainPreflightRunner(cfg, &calls)
	runner.results["sbx exec main-sbx curl -fsS https://example.test/ip"] = CommandResult{Stdout: "198.51.100.20\n"}

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).PreflightMainSandbox(context.Background(), cfg)

	if err == nil {
		t.Fatalf("main preflight unexpectedly passed")
	}
	if result.Egress.FailureKind != "sandbox-egress-mismatch" || result.Egress.ObservedIP != "198.51.100.20" {
		t.Fatalf("expected egress mismatch result, got %#v", result.Egress)
	}
	if !result.CreatedByCommand || !ShouldCleanupMainAfterStartError(err) {
		t.Fatalf("newly created failed main sandbox should be eligible for caller cleanup, result=%#v err=%v", result, err)
	}
}

func TestDockerSandboxPreflightMainSandboxCreateFailureCanCleanupAttemptedMain(t *testing.T) {
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Workspace.Mount = "/work/project"
	runner := mainPreflightRunner(cfg, nil)
	runner.results["sbx create --name main-sbx claude /work/project"] = CommandResult{Stderr: "create failed\n"}
	runner.errors = map[string]error{
		"sbx create --name main-sbx claude /work/project": errors.New("exit status 1"),
	}

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).PreflightMainSandbox(context.Background(), cfg)

	if err == nil {
		t.Fatalf("main preflight unexpectedly passed")
	}
	if !result.CreatedByCommand || !ShouldCleanupMainAfterStartError(err) {
		t.Fatalf("failed create attempt should be eligible for caller cleanup, result=%#v err=%v", result, err)
	}
}

func TestDockerSandboxPreflightMainSandboxRedactsEgressCommandFailure(t *testing.T) {
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Workspace.Mount = "/work/project"
	cfg.Network.EgressIP.SandboxCheckURL = "https://example.test/ip?token=secret-token&cookie=secret-cookie"
	runner := mainPreflightRunner(cfg, nil)
	key := "sbx exec main-sbx curl -fsS " + cfg.Network.EgressIP.SandboxCheckURL
	runner.results[key] = CommandResult{Stderr: "curl failed for secret-token\n"}
	runner.errors = map[string]error{key: errors.New("exit status 1")}

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).PreflightMainSandbox(context.Background(), cfg)

	if err == nil {
		t.Fatalf("main preflight unexpectedly passed")
	}
	if result.Egress.FailureKind != "sandbox-egress-indeterminate" {
		t.Fatalf("expected indeterminate egress result, got %#v", result.Egress)
	}
	if strings.Contains(err.Error(), "secret-token") || strings.Contains(err.Error(), "secret-cookie") {
		t.Fatalf("main preflight egress diagnostic leaked configured URL secret: %v", err)
	}
}

func TestDockerSandboxPreflightMainSandboxFailsClosedForUnsafeInspection(t *testing.T) {
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Workspace.Mount = "/work/project"
	runner := mainPreflightRunner(cfg, nil)
	runner.results["sbx exec -e TZ=UTC -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 main-sbx env"] = CommandResult{
		Stdout: "PATH=/usr/bin\nHTTP_PROXY=http://127.0.0.1:7897\n",
	}

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).PreflightMainSandbox(context.Background(), cfg)

	if err == nil {
		t.Fatalf("main preflight unexpectedly passed")
	}
	if !strings.Contains(err.Error(), "environment.inspection.env.HTTP_PROXY") {
		t.Fatalf("expected unsafe proxy diagnostic, got %v", err)
	}
	if strings.Contains(err.Error(), "127.0.0.1:7897") {
		t.Fatalf("main preflight diagnostic leaked proxy value: %v", err)
	}
	if !result.CreatedByCommand || !ShouldCleanupMainAfterStartError(err) {
		t.Fatalf("newly created unsafe main should be eligible for caller cleanup, result=%#v err=%v", result, err)
	}
}

func TestDockerSandboxPreflightMainSandboxPreservesExistingMainOnFailure(t *testing.T) {
	calls := []string{}
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Workspace.Mount = "/work/project"
	runner := mainPreflightRunner(cfg, &calls)
	runner.results["sbx ls"] = CommandResult{Stdout: "SANDBOX    AGENT    STATUS    PORTS    WORKSPACE\nmain-sbx   claude   running            /work/project\n"}
	runner.results["sbx exec main-sbx sh -lc "+workspaceVisibilityScript("/work/project")] = CommandResult{
		Stdout: "sibling-readable=/work/other/config.yaml\n",
	}

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).PreflightMainSandbox(context.Background(), cfg)

	if err == nil {
		t.Fatalf("main preflight unexpectedly passed")
	}
	if result.CreatedByCommand || ShouldCleanupMainAfterStartError(err) {
		t.Fatalf("existing failed main sandbox must not be caller-cleaned, result=%#v err=%v", result, err)
	}
	got := strings.Join(calls, "\n")
	if strings.Contains(got, "sbx create") || strings.Contains(got, "sbx stop main-sbx") || strings.Contains(got, "sbx rm --force main-sbx") {
		t.Fatalf("existing main sandbox should not be created or destroyed by preflight, got:\n%s", got)
	}
}

func TestDockerSandboxPreflightMainSandboxRejectsExistingWorkspaceMismatch(t *testing.T) {
	calls := []string{}
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Workspace.Mount = "/work/project"
	runner := mainPreflightRunner(cfg, &calls)
	runner.results["sbx ls"] = CommandResult{Stdout: "SANDBOX    AGENT    STATUS    PORTS    WORKSPACE\nmain-sbx   claude   running            /work/other\n"}

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).PreflightMainSandbox(context.Background(), cfg)

	if err == nil {
		t.Fatalf("main preflight unexpectedly passed")
	}
	if result.CreatedByCommand || ShouldCleanupMainAfterStartError(err) {
		t.Fatalf("workspace-mismatched existing main must be preserved, result=%#v err=%v", result, err)
	}
	if got := strings.Join(calls, "\n"); got != "sbx ls" {
		t.Fatalf("workspace mismatch should fail before main exec, got:\n%s", got)
	}
}

func TestDockerSandboxCheckMainWorkspaceVisibilityFailsClosed(t *testing.T) {
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Workspace.Mount = "/Users/alice/work/safe-claude-sbx"
	runner := stubRunner{
		path: "/tmp/sbx",
		results: map[string]CommandResult{
			"sbx exec main-sbx sh -lc " + workspaceVisibilityScript(cfg.Workspace.Mount): {
				Stdout: "sibling-readable=/Users/alice/work/other-project/config.yaml\n",
			},
		},
	}

	_, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).CheckMainWorkspaceVisibility(context.Background(), cfg)

	if err == nil {
		t.Fatalf("main sandbox visibility unexpectedly passed")
	}
	if !strings.Contains(err.Error(), "main sandbox inspection invalid") || !strings.Contains(err.Error(), "workspace.inspection.visibility.sibling") {
		t.Fatalf("expected sibling visibility diagnostic, got %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("main sandbox visibility diagnostic leaked file contents: %v", err)
	}
}

func TestDockerSandboxCheckMainWorkspaceVisibilityAllowsConfiguredWorkspaceOnly(t *testing.T) {
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Workspace.Mount = "/Users/alice/work/safe-claude-sbx"
	runner := stubRunner{
		path: "/tmp/sbx",
		results: map[string]CommandResult{
			"sbx exec main-sbx sh -lc " + workspaceVisibilityScript(cfg.Workspace.Mount): {Stdout: "ok\n"},
		},
	}

	visibility, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).CheckMainWorkspaceVisibility(context.Background(), cfg)

	if err != nil {
		t.Fatalf("expected configured workspace-only visibility to pass: %v", err)
	}
	if visibility.SiblingPath != "" {
		t.Fatalf("expected empty visibility observation, got %#v", visibility)
	}
}

func TestDockerSandboxStartMainPassesMainSandboxContract(t *testing.T) {
	calls := []string{}
	runner := stubRunner{
		path:    "/tmp/sbx",
		calls:   &calls,
		results: map[string]CommandResult{},
	}
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Workspace.Mount = "/work/project"
	plan := NewStartPlan(cfg)

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), plan)

	if err != nil {
		t.Fatalf("start main failed: %v", err)
	}
	if result.SandboxName != "main-sbx" || result.Agent != "claude" || result.Workspace != "/work/project" || result.Timezone != "UTC" || result.Locale != "en_US.UTF-8" {
		t.Fatalf("unexpected start result: %#v", result)
	}
	if plan.Environment["TZ"] != "UTC" || plan.Environment["LANG"] != "en_US.UTF-8" || plan.Environment["LC_ALL"] != "en_US.UTF-8" {
		t.Fatalf("expected allowed startup environment, got %#v", plan.Environment)
	}
	want := strings.Join([]string{
		"sbx ls",
		"sbx create --name main-sbx claude /work/project",
		"sbx exec main-sbx sh -lc " + workspaceVisibilityScript("/work/project"),
		"sbx run --name main-sbx",
	}, "\n")
	if got := strings.Join(calls, "\n"); got != want {
		t.Fatalf("direct mode should create, inspect, then attach main sandbox, got:\n%s", got)
	}
}

func TestSBXProcessEnvDropsHostHerdrState(t *testing.T) {
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", "/tmp/host-herdr.sock")
	t.Setenv("HERDR_PANE_ID", "host-pane")
	t.Setenv("HERDR_WORKSPACE_ID", "host-workspace")

	for _, entry := range sbxProcessEnv() {
		if strings.HasPrefix(entry, "HERDR_") {
			t.Fatalf("sbx subprocess environment inherited host Herdr state: %s", entry)
		}
	}
}

func TestDockerSandboxStartMainPreparesSandboxLocalHerdr(t *testing.T) {
	calls := []string{}
	runner := stubRunner{
		path:  "/tmp/sbx",
		calls: &calls,
		results: map[string]CommandResult{
			"sbx create --name main-sbx --template safe-claude-sbx-herdr:latest claude /work/project": {Stdout: "created\n"},
			"sbx exec main-sbx sh -lc command -v herdr":                                               {Stdout: "/usr/local/bin/herdr\n"},
			"sbx exec main-sbx herdr --version":                                                       {Stdout: "herdr 0.7.1\n"},
			"sbx exec main-sbx herdr integration install claude":                                      {Stdout: "installed\n"},
			"sbx exec main-sbx sh -lc command -v cc":                                                  {Stdout: "/usr/local/bin/cc\n"},
			"sbx exec main-sbx cc --version":                                                          {Stdout: "claude 1.0.0\n"},
			"sbx exec main-sbx herdr server":                                                          {Stdout: "server started\n"},
			"sbx exec main-sbx herdr status server --json":                                            {Stdout: `{"running":true,"socket":"/home/agent/.config/herdr/herdr.sock"}` + "\n"},
			"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude": {Stdout: "claude started\n"},
		},
	}
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Sandbox.Template = "safe-claude-sbx-herdr:latest"
	cfg.Workspace.Mount = "/work/project"
	installIfMissing := false
	cfg.Sandbox.Supervision = config.Supervision{
		Mode: "sandbox-local-herdr",
		Herdr: &config.HerdrSupervision{
			InstallIfMissing: &installIfMissing,
			SocketPath:       "/home/agent/.config/herdr/herdr.sock",
			PaneID:           "sandbox-claude",
		},
	}
	plan := NewStartPlan(cfg)

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), plan)

	if err != nil {
		t.Fatalf("start main failed: %v", err)
	}
	if result.SandboxName != "main-sbx" || result.Agent != "claude" || result.Workspace != "/work/project" {
		t.Fatalf("unexpected start result: %#v", result)
	}
	for _, key := range runner.keys() {
		if strings.Contains(key, "/tmp/host-herdr.sock") || strings.Contains(key, "host-pane") {
			t.Fatalf("start command used host Herdr state: %s", key)
		}
	}
	got := strings.Join(calls, "\n")
	want := strings.Join([]string{
		"sbx ls",
		"sbx create --name main-sbx --template safe-claude-sbx-herdr:latest claude /work/project",
		"sbx exec main-sbx sh -lc " + workspaceVisibilityScript("/work/project"),
		"sbx exec main-sbx sh -lc command -v herdr",
		"sbx exec main-sbx herdr --version",
		"sbx exec main-sbx herdr integration install claude",
		"sbx exec main-sbx sh -lc command -v cc",
		"sbx exec main-sbx cc --version",
		"sbx exec main-sbx herdr server",
		"sbx exec main-sbx herdr status server --json",
		"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("expected first Herdr startup to inspect then create main sandbox, got:\n%s", got)
	}
	if strings.Contains(got, "curl -fsSL https://herdr.dev/install.sh") {
		t.Fatalf("normal sandbox-local Herdr startup must not download Herdr, got:\n%s", got)
	}
}

func TestDockerSandboxStartMainWaitsForSandboxLocalHerdrReadiness(t *testing.T) {
	calls := []string{}
	runner := sequenceRunner{
		stubRunner: stubRunner{
			path:  "/tmp/sbx",
			calls: &calls,
			results: map[string]CommandResult{
				"sbx ls": {Stdout: "No sandboxes found.\n"},
				"sbx create --name main-sbx claude /work/project":    {Stdout: "created\n"},
				"sbx exec main-sbx sh -lc command -v herdr":          {Stdout: "/home/agent/.local/bin/herdr\n"},
				"sbx exec main-sbx herdr --version":                  {Stdout: "herdr 0.7.1\n"},
				"sbx exec main-sbx herdr integration install claude": {Stdout: "installed\n"},
				"sbx exec main-sbx sh -lc command -v cc":             {Stdout: "/usr/local/bin/cc\n"},
				"sbx exec main-sbx cc --version":                     {Stdout: "claude 1.0.0\n"},
				"sbx exec main-sbx herdr server":                     {Stdout: "server started\n"},
				"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude": {Stdout: "claude started\n"},
			},
		},
		sequences: map[string][]CommandResult{
			"sbx exec main-sbx herdr status server --json": {
				{Stdout: `{"running":false,"socket":"/home/agent/.config/herdr/herdr.sock"}` + "\n"},
				{Stdout: `{"running":true,"socket":"/home/agent/.config/herdr/herdr.sock"}` + "\n"},
			},
		},
	}

	_, err := (DockerSandbox{Runner: &runner, Binary: "sbx"}).StartMain(context.Background(), NewStartPlan(herdrConfig()))

	if err != nil {
		t.Fatalf("expected Herdr readiness wait to succeed: %v", err)
	}
	got := strings.Join(calls, "\n")
	want := strings.Join([]string{
		"sbx exec main-sbx herdr status server --json",
		"sbx exec main-sbx herdr status server --json",
		"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("expected Claude to start after repeated readiness checks, got:\n%s", got)
	}
}

func TestDockerSandboxStartMainFailsClosedWhenSandboxLocalHerdrReadinessFails(t *testing.T) {
	tests := []struct {
		name         string
		status       CommandResult
		statusErr    error
		wantErr      string
		shortTimeout bool
	}{
		{
			name:         "timeout",
			status:       CommandResult{Stdout: `{"running":false,"socket":"/home/agent/.config/herdr/herdr.sock"}` + "\n"},
			wantErr:      "timed out",
			shortTimeout: true,
		},
		{
			name:      "status command failure",
			status:    CommandResult{Stderr: "status failed\n"},
			statusErr: errors.New("exit status 1"),
			wantErr:   "status failed",
		},
		{
			name:    "socket mismatch",
			status:  CommandResult{Stdout: `{"running":true,"socket":"/tmp/wrong-herdr.sock"}` + "\n"},
			wantErr: "socket path mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			runner := stubRunner{
				path:  "/tmp/sbx",
				calls: &calls,
				results: map[string]CommandResult{
					"sbx ls": {Stdout: "No sandboxes found.\n"},
					"sbx create --name main-sbx claude /work/project":    {Stdout: "created\n"},
					"sbx exec main-sbx sh -lc command -v herdr":          {Stdout: "/home/agent/.local/bin/herdr\n"},
					"sbx exec main-sbx herdr --version":                  {Stdout: "herdr 0.7.1\n"},
					"sbx exec main-sbx herdr integration install claude": {Stdout: "installed\n"},
					"sbx exec main-sbx sh -lc command -v cc":             {Stdout: "/usr/local/bin/cc\n"},
					"sbx exec main-sbx cc --version":                     {Stdout: "claude 1.0.0\n"},
					"sbx exec main-sbx herdr server":                     {Stdout: "server started\n"},
					"sbx exec main-sbx herdr status server --json":       tt.status,
					"sbx exec main-sbx herdr server stop":                {Stdout: "stopped\n"},
					"sbx stop main-sbx":                                  {Stdout: "stopped\n"},
				},
				errors: map[string]error{
					"sbx exec main-sbx herdr status server --json": tt.statusErr,
				},
			}
			plan := NewStartPlan(herdrConfig())
			if tt.shortTimeout {
				plan.Supervision.Herdr.ReadinessTimeout = time.Nanosecond
			}

			_, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), plan)

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected readiness error containing %q, got %v", tt.wantErr, err)
			}
			got := strings.Join(calls, "\n")
			if strings.Contains(got, "HERDR_ENV=1") {
				t.Fatalf("Claude should not start after readiness failure, got:\n%s", got)
			}
			if !strings.Contains(got, "sbx exec main-sbx herdr server stop\nsbx stop main-sbx") {
				t.Fatalf("expected Herdr server and main sandbox cleanup after readiness failure, got:\n%s", got)
			}
		})
	}
}

func TestDockerSandboxStartMainRebuildsStoppedSandboxLocalHerdrMain(t *testing.T) {
	calls := []string{}
	runner := stubRunner{
		path:  "/tmp/sbx",
		calls: &calls,
		results: map[string]CommandResult{
			"sbx ls":                  {Stdout: "SANDBOX    AGENT    STATUS    PORTS    WORKSPACE\nmain-sbx   claude   stopped            /work/project\n"},
			"sbx stop main-sbx":       {Stdout: "Sandbox 'main-sbx' stopped\n"},
			"sbx rm --force main-sbx": {Stdout: "Sandbox 'main-sbx' removed\n"},
			"sbx create --name main-sbx claude /work/project":    {Stdout: "created\n"},
			"sbx exec main-sbx sh -lc command -v herdr":          {Stdout: "/home/agent/.local/bin/herdr\n"},
			"sbx exec main-sbx herdr --version":                  {Stdout: "herdr 0.7.1\n"},
			"sbx exec main-sbx herdr integration install claude": {Stdout: "installed\n"},
			"sbx exec main-sbx sh -lc command -v cc":             {Stdout: "/usr/local/bin/cc\n"},
			"sbx exec main-sbx cc --version":                     {Stdout: "claude 1.0.0\n"},
			"sbx exec main-sbx herdr server":                     {Stdout: "server started\n"},
			"sbx exec main-sbx herdr status server --json":       {Stdout: `{"running":true,"socket":"/home/agent/.config/herdr/herdr.sock"}` + "\n"},
			"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude": {Stdout: "claude started\n"},
		},
	}

	if _, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), NewStartPlan(herdrConfig())); err != nil {
		t.Fatalf("expected stopped main sandbox to be rebuilt safely: %v", err)
	}

	got := strings.Join(calls, "\n")
	want := strings.Join([]string{
		"sbx ls",
		"sbx stop main-sbx",
		"sbx rm --force main-sbx",
		"sbx create --name main-sbx --template safe-claude-sbx-herdr:latest claude /work/project",
		"sbx exec main-sbx sh -lc " + workspaceVisibilityScript("/work/project"),
		"sbx exec main-sbx sh -lc command -v herdr",
	}, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("expected stopped main sandbox rebuild before Herdr setup, got:\n%s", got)
	}
}

func TestDockerSandboxStartMainRejectsUnsafeExistingSandboxWithoutStoppingIt(t *testing.T) {
	calls := []string{}
	runner := stubRunner{
		path:  "/tmp/sbx",
		calls: &calls,
		results: map[string]CommandResult{
			"sbx ls": {Stdout: "SANDBOX    AGENT    STATUS    PORTS    WORKSPACE\nmain-sbx   claude   running            /work/project\n"},
		},
	}

	_, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), NewStartPlan(herdrConfig()))

	if err == nil {
		t.Fatalf("expected running existing main sandbox to fail closed")
	}
	if !strings.Contains(err.Error(), "unsafe status") || strings.Contains(err.Error(), "/work/project") {
		t.Fatalf("expected actionable non-sensitive diagnostic, got %v", err)
	}
	for _, call := range calls {
		if call == "sbx exec main-sbx herdr server stop" || call == "sbx stop main-sbx" || call == "sbx rm --force main-sbx" {
			t.Fatalf("unsafe existing sandbox should not be cleaned up by startup path, got:\n%s", strings.Join(calls, "\n"))
		}
	}
}

func TestDockerSandboxStartMainStopsSandboxLocalHerdrWhenClaudeStartFails(t *testing.T) {
	calls := []string{}
	runner := stubRunner{
		path:  "/tmp/sbx",
		calls: &calls,
		results: map[string]CommandResult{
			"sbx ls": {Stdout: "No sandboxes found.\n"},
			"sbx create --name main-sbx claude /work/project":    {Stdout: "created\n"},
			"sbx exec main-sbx sh -lc command -v herdr":          {Stdout: "/home/agent/.local/bin/herdr\n"},
			"sbx exec main-sbx herdr --version":                  {Stdout: "herdr 0.7.1\n"},
			"sbx exec main-sbx herdr integration install claude": {Stdout: "installed\n"},
			"sbx exec main-sbx herdr server":                     {Stdout: "server started\n"},
			"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude": {Stderr: "claude failed\n"},
			"sbx exec main-sbx herdr server stop": {Stdout: "stopped\n"},
			"sbx stop main-sbx":                   {Stdout: "stopped\n"},
		},
		errors: map[string]error{
			"sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude main-sbx claude": errors.New("exit status 1"),
		},
	}

	_, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), NewStartPlan(herdrConfig()))

	if err == nil {
		t.Fatalf("expected Claude startup failure")
	}
	got := strings.Join(calls, "\n")
	if !strings.Contains(got, "sbx exec main-sbx herdr server stop\nsbx stop main-sbx") {
		t.Fatalf("expected Herdr server and main sandbox cleanup after Claude failure, got:\n%s", got)
	}
}

func TestDockerSandboxProbeAllowsSandboxLocalHerdrInspectionWhenConfigured(t *testing.T) {
	cfg := herdrConfig()
	cfg.Sandbox.ProbeName = "probe"
	cfg.Workspace.Mount = "."
	runner := probeRunner(
		"PATH=/usr/bin\nHERDR_ENV=1\nHERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock\nHERDR_PANE_ID=sandbox-claude\n",
		"/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)\n",
	)

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).Probe(context.Background(), cfg)

	if err != nil {
		t.Fatalf("expected sandbox-local Herdr inspection to pass: %v", err)
	}
	if !result.CleanupDone {
		t.Fatalf("expected probe cleanup to be marked done")
	}
}

func TestDockerSandboxStartMainFailsClosedWhenTemplateToolsAreMissing(t *testing.T) {
	tests := []struct {
		name        string
		missingCall string
		wantErr     string
	}{
		{
			name:        "herdr missing",
			missingCall: "sbx exec main-sbx sh -lc command -v herdr",
			wantErr:     "sandbox-local Herdr unavailable",
		},
		{
			name:        "cc missing",
			missingCall: "sbx exec main-sbx sh -lc command -v cc",
			wantErr:     "sandbox-local cc unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := []string{}
			results := map[string]CommandResult{
				"sbx ls": {Stdout: "No sandboxes found.\n"},
				"sbx create --name main-sbx --template safe-claude-sbx-herdr:latest claude /work/project": {Stdout: "created\n"},
				"sbx exec main-sbx sh -lc command -v herdr":                                               {Stdout: "/usr/local/bin/herdr\n"},
				"sbx exec main-sbx herdr --version":                                                       {Stdout: "herdr 0.7.1\n"},
				"sbx exec main-sbx herdr integration install claude":                                      {Stdout: "installed\n"},
				"sbx exec main-sbx sh -lc command -v cc":                                                  {Stdout: "/usr/local/bin/cc\n"},
				"sbx exec main-sbx cc --version":                                                          {Stdout: "claude 1.0.0\n"},
				"sbx exec main-sbx herdr server stop":                                                     {Stdout: "stopped\n"},
				"sbx stop main-sbx":                                                                       {Stdout: "stopped\n"},
			}
			results[tt.missingCall] = CommandResult{Stderr: "missing /tmp/host-herdr.sock\n"}
			runner := stubRunner{
				path:    "/tmp/sbx",
				calls:   &calls,
				results: results,
				errors: map[string]error{
					tt.missingCall: errors.New("exit status 127"),
				},
			}

			_, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).StartMain(context.Background(), NewStartPlan(herdrConfig()))

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), "Docker Sandbox template") {
				t.Fatalf("expected actionable template diagnostic containing %q, got %v", tt.wantErr, err)
			}
			got := strings.Join(calls, "\n")
			if strings.Contains(got, "curl -fsSL https://herdr.dev/install.sh") {
				t.Fatalf("missing template tools must not trigger runtime Herdr download, got:\n%s", got)
			}
			if strings.Contains(err.Error(), "/tmp/host-herdr.sock") {
				t.Fatalf("missing tool diagnostic leaked host Herdr state: %v", err)
			}
			if strings.Contains(got, "HERDR_ENV=1") {
				t.Fatalf("Claude should not start after missing template tools, got:\n%s", got)
			}
		})
	}
}

func TestDockerSandboxCleanupMainStopsSandboxLocalHerdrBeforeSandbox(t *testing.T) {
	calls := []string{}
	runner := stubRunner{
		path:  "/tmp/sbx",
		calls: &calls,
		results: map[string]CommandResult{
			"sbx exec main-sbx herdr server stop": {Stdout: "stopped\n"},
			"sbx stop main-sbx":                   {Stdout: "sandbox stopped\n"},
		},
	}
	cfg := herdrConfig()
	cfg.Cleanup.StopMainSandbox = true

	err := (DockerSandbox{Runner: runner, Binary: "sbx"}).CleanupMain(context.Background(), cfg)

	if err != nil {
		t.Fatalf("cleanup main failed: %v", err)
	}
	if got := strings.Join(runner.keys(), "\n"); !strings.Contains(got, "sbx exec main-sbx herdr server stop\nsbx stop main-sbx") {
		t.Fatalf("expected Herdr cleanup before sandbox stop, got:\n%s", got)
	}
}

func TestDockerSandboxCleanupMainBoundsSandboxLocalHerdrStop(t *testing.T) {
	previousTimeout := sandboxLocalHerdrCleanupTimeout
	sandboxLocalHerdrCleanupTimeout = time.Nanosecond
	t.Cleanup(func() {
		sandboxLocalHerdrCleanupTimeout = previousTimeout
	})

	runner := &herdrStopTimeoutRunner{}
	cfg := herdrConfig()
	cfg.Cleanup.StopMainSandbox = true

	err := (DockerSandbox{Runner: runner, Binary: "sbx"}).CleanupMain(context.Background(), cfg)

	if err == nil || !strings.Contains(err.Error(), "stop sandbox-local Herdr server") {
		t.Fatalf("expected bounded Herdr stop diagnostic, got %v", err)
	}
	if !runner.herdrStopHadDeadline {
		t.Fatalf("Herdr stop did not receive a bounded context")
	}
	if !runner.sawSandboxStop {
		t.Fatalf("expected sandbox stop to continue after Herdr stop timeout")
	}
}

func TestDockerSandboxCleanupMainClassifiesMainStopControlPlaneFailure(t *testing.T) {
	runner := stubRunner{
		path: "/tmp/sbx",
		results: map[string]CommandResult{
			"sbx stop main-sbx": {Stderr: `Error: failed to stop sandbox 'main-sbx': stop runtime: Post "http://socket/sandbox/main-sbx/stop": context canceled` + "\n"},
		},
		errors: map[string]error{
			"sbx stop main-sbx": errors.New("exit status 1"),
		},
	}
	cfg := herdrConfig()
	cfg.Sandbox.Supervision.Mode = "direct-claude"
	cfg.Sandbox.Supervision.Herdr = nil
	cfg.Cleanup.StopMainSandbox = true

	err := (DockerSandbox{Runner: runner, Binary: "sbx"}).CleanupMain(context.Background(), cfg)

	if err == nil {
		t.Fatalf("expected main sandbox stop failure")
	}
	if !strings.Contains(err.Error(), "sbx control-plane failure") {
		t.Fatalf("expected sbx control-plane failure classification, got %v", err)
	}
	if !strings.Contains(err.Error(), "stop main sandbox") {
		t.Fatalf("expected main sandbox stop operation in diagnostic, got %v", err)
	}
}

func TestDockerSandboxCleanupMainUsesIndependentTimeoutForEachOperation(t *testing.T) {
	runner := &deadlineRecordingRunner{
		results: map[string]CommandResult{
			"sbx exec main-sbx herdr server stop": {Stdout: "stopped\n"},
			"sbx stop main-sbx":                   {Stdout: "stopped\n"},
			"sbx rm --force main-sbx":             {Stdout: "removed\n"},
		},
	}
	cfg := herdrConfig()
	cfg.Network.EgressIP.TimeoutSeconds = 1
	cfg.Cleanup.StopMainSandbox = true
	cfg.Cleanup.RemoveMainSandbox = true

	if err := (DockerSandbox{Runner: runner, Binary: "sbx"}).CleanupMain(context.Background(), cfg); err != nil {
		t.Fatalf("cleanup main failed: %v", err)
	}
	if runner.mainCleanupDeadlineCount != 3 {
		t.Fatalf("expected each main cleanup operation to have its own deadline, got %d", runner.mainCleanupDeadlineCount)
	}
}

func TestDockerSandboxStartMainAttachedConnectsStdin(t *testing.T) {
	dir := t.TempDir()
	stdinPath := filepath.Join(dir, "stdin.txt")
	fakeSBX := filepath.Join(dir, "sbx")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
case "$1" in
  ls)
    printf 'No sandboxes found.\n'
    ;;
  create)
    printf 'created\n'
    ;;
  exec)
    case "$*" in
      *" sh -lc workspace="*)
        printf 'ok\n'
        ;;
      *)
        printf 'unexpected exec: %%s\n' "$*" >&2
        exit 1
        ;;
    esac
    ;;
  run)
    IFS= read -r line
    printf '%%s' "$line" > %q
    printf 'main sandbox started\n'
    ;;
  *)
    printf 'unexpected command: %%s\n' "$*" >&2
    exit 1
    ;;
esac
`, stdinPath)
	if err := os.WriteFile(fakeSBX, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake sbx: %v", err)
	}

	cfg := probeConfig()
	plan := NewStartPlan(cfg)
	_, wait, err := (DockerSandbox{Binary: fakeSBX}).StartMainAttached(context.Background(), plan, strings.NewReader("terminal-input\n"), io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("start attached failed: %v", err)
	}
	if wait == nil {
		t.Fatal("expected wait channel")
	}
	if err := <-wait; err != nil {
		t.Fatalf("attached command failed: %v", err)
	}
	got, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read captured stdin: %v", err)
	}
	if string(got) != "terminal-input" {
		t.Fatalf("expected stdin to be connected to sbx run, got %q", got)
	}
}

func TestDockerSandboxProbeFailsClosedForUnsafeInspection(t *testing.T) {
	tests := []struct {
		name       string
		env        string
		mounts     string
		visibility CommandResult
		wantError  string
	}{
		{
			name:       "host proxy",
			env:        "PATH=/usr/bin\nHTTP_PROXY=http://127.0.0.1:7897\n",
			mounts:     "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)\n",
			visibility: CommandResult{Stdout: "ok\n"},
			wantError:  "environment.inspection.env.HTTP_PROXY",
		},
		{
			name:       "sensitive env",
			env:        "PATH=/usr/bin\nSSH_AUTH_SOCK=/Users/alice/.ssh/agent.sock\n",
			mounts:     "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)\n",
			visibility: CommandResult{Stdout: "ok\n"},
			wantError:  "environment.inspection.env.SSH_AUTH_SOCK",
		},
		{
			name:       "host Herdr config",
			env:        "PATH=/usr/bin\nHERDR_CONFIG_DIR=/Users/alice/.config/herdr\n",
			mounts:     "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)\n",
			visibility: CommandResult{Stdout: "ok\n"},
			wantError:  "environment.inspection.env.HERDR_CONFIG_DIR",
		},
		{
			name:       "sensitive mount",
			env:        "PATH=/usr/bin\nHTTP_PROXY=http://gateway.docker.internal:3128\n",
			mounts:     "/dev/disk1 on /workspace type virtiofs\n/dev/disk2 on /host-ssh type virtiofs (rw,source=/Users/alice/.ssh)\n",
			visibility: CommandResult{Stdout: "ok\n"},
			wantError:  "workspace.inspection.mounts",
		},
		{
			name:       "sibling project file readable",
			env:        "PATH=/usr/bin\nHTTP_PROXY=http://gateway.docker.internal:3128\n",
			mounts:     "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)\n",
			visibility: CommandResult{Stdout: "sibling-readable=/Users/alice/work/other-project/config.yaml\n"},
			wantError:  "workspace.inspection.visibility.sibling",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := probeRunner(tt.env, tt.mounts)
			runner.results["sbx exec probe sh -lc "+workspaceVisibilityScript("/Users/alice/work/safe-claude-sbx")] = tt.visibility
			cfg := probeConfig()
			cfg.Workspace.Mount = "/Users/alice/work/safe-claude-sbx"
			cfg.Workspace.ForbiddenPaths = append(cfg.Workspace.ForbiddenPaths, "/Users/alice/.ssh")

			result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).Probe(context.Background(), cfg)

			if err == nil {
				t.Fatalf("probe unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected %q in error, got %v", tt.wantError, err)
			}
			if strings.Contains(err.Error(), "127.0.0.1:7897") || strings.Contains(err.Error(), "/Users/alice/.ssh/agent.sock") || strings.Contains(err.Error(), "/Users/alice/.config/herdr") {
				t.Fatalf("probe error leaked sensitive value: %v", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("probe error leaked file contents: %v", err)
			}
			if !result.CleanupDone {
				t.Fatalf("expected unsafe inspection to trigger probe cleanup")
			}
		})
	}
}

func TestDockerSandboxProbeAllowsConfiguredWorkspaceOnlyVisibility(t *testing.T) {
	runner := probeRunner(
		"PATH=/usr/bin\nHTTP_PROXY=http://gateway.docker.internal:3128\n",
		"/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)\n",
	)
	cfg := probeConfig()
	cfg.Workspace.Mount = "/Users/alice/work/safe-claude-sbx"
	runner.results["sbx exec probe sh -lc "+workspaceVisibilityScript(cfg.Workspace.Mount)] = CommandResult{Stdout: "ok\n"}

	result, err := (DockerSandbox{Runner: runner, Binary: "sbx"}).Probe(context.Background(), cfg)

	if err != nil {
		t.Fatalf("expected configured workspace-only visibility to pass: %v", err)
	}
	if !result.CleanupDone {
		t.Fatalf("expected probe cleanup after successful inspection")
	}
}

func TestDockerSandboxProbeCleansUpWithIndependentContextAfterTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := timeoutCleanupRunner{
		results: map[string]CommandResult{
			"sbx create --name probe shell .": {Stdout: "created\n"},
			"sbx stop probe":                  {Stderr: "sandbox not found\n"},
			"sbx rm --force probe":            {Stderr: "sandbox not found\n"},
		},
		cancel: cancel,
	}

	result, err := (DockerSandbox{Runner: &runner, Binary: "sbx"}).Probe(ctx, probeConfig())

	if err == nil {
		t.Fatalf("probe unexpectedly succeeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if !result.CleanupDone {
		t.Fatalf("expected cleanup to be marked done after timeout")
	}
	if runner.cleanupUsedCanceledContext {
		t.Fatalf("cleanup reused the canceled probe context")
	}
	if !runner.sawStop || !runner.sawRemove {
		t.Fatalf("expected stop and rm cleanup after timeout, got stop=%v rm=%v", runner.sawStop, runner.sawRemove)
	}
}

func probeConfig() config.Config {
	return config.Config{
		Network: config.Network{
			EgressIP: config.EgressIP{
				ExpectedIP:      "203.0.113.10",
				SandboxCheckURL: "https://example.test/ip",
				TimeoutSeconds:  10,
			},
		},
		Sandbox: config.Sandbox{
			Backend:   "docker-sandbox",
			ProbeName: "probe",
			Agent:     "claude",
		},
		Workspace: config.Workspace{
			Mount:          ".",
			UseCloneMode:   false,
			ForbiddenPaths: []string{"~", "~/.ssh", "~/.claude", "~/.config/clash", "~/Library/Keychains"},
		},
		Environment: config.Environment{
			Timezone:         "UTC",
			Locale:           "en_US.UTF-8",
			ForbiddenEnvVars: []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"},
		},
		Cleanup: config.Cleanup{
			RemoveProbeSandbox: true,
		},
	}
}

func herdrConfig() config.Config {
	cfg := probeConfig()
	cfg.Sandbox.MainName = "main-sbx"
	cfg.Sandbox.Template = "safe-claude-sbx-herdr:latest"
	cfg.Workspace.Mount = "/work/project"
	installIfMissing := false
	cfg.Sandbox.Supervision = config.Supervision{
		Mode: "sandbox-local-herdr",
		Herdr: &config.HerdrSupervision{
			InstallIfMissing: &installIfMissing,
			SocketPath:       "/home/agent/.config/herdr/herdr.sock",
			PaneID:           "sandbox-claude",
		},
	}
	return cfg
}

func probeRunner(env, mounts string) stubRunner {
	return stubRunner{
		path: "/tmp/sbx",
		results: map[string]CommandResult{
			"sbx create --name probe shell .":                                        {Stdout: "created\n"},
			"sbx create --name probe shell /Users/alice/work/safe-claude-sbx":        {Stdout: "created\n"},
			"sbx exec -e TZ=UTC -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 probe env": {Stdout: env},
			"sbx exec probe pwd":            {Stdout: "/workspace\n"},
			"sbx exec probe mount":          {Stdout: mounts},
			"sbx exec -e TZ=UTC probe date": {Stdout: "Sun Jul 5 00:00:00 UTC 2026\n"},
			"sbx exec -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 probe locale": {Stdout: "LANG=en_US.UTF-8\n"},
			"sbx exec probe curl -fsS https://example.test/ip":                {Stdout: "203.0.113.10\n"},
			"sbx stop probe":       {Stderr: "sandbox not found\n"},
			"sbx rm --force probe": {Stderr: "sandbox not found\n"},
		},
		errors: map[string]error{
			"sbx stop probe":       errors.New("exit status 1"),
			"sbx rm --force probe": errors.New("exit status 1"),
		},
	}
}

func mainPreflightRunner(cfg config.Config, calls *[]string) stubRunner {
	return stubRunner{
		path:  "/tmp/sbx",
		calls: calls,
		results: map[string]CommandResult{
			"sbx ls": {Stdout: "No sandboxes found.\n"},
			"sbx create --name main-sbx claude /work/project": {Stdout: "created\n"},
			"sbx exec -e TZ=UTC -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 main-sbx env": {
				Stdout: "PATH=/usr/bin\nHTTP_PROXY=http://gateway.docker.internal:3128\n",
			},
			"sbx exec main-sbx pwd": {
				Stdout: "/work/project\n",
			},
			"sbx exec main-sbx mount": {
				Stdout: "/dev/disk1 on /work/project type virtiofs (rw,source=/work/project)\n",
			},
			"sbx exec main-sbx sh -lc " + workspaceVisibilityScript(cfg.Workspace.Mount): {
				Stdout: "ok\n",
			},
			"sbx exec -e TZ=UTC main-sbx date": {
				Stdout: "Sun Jul 5 00:00:00 UTC 2026\n",
			},
			"sbx exec -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 main-sbx locale": {
				Stdout: "LANG=en_US.UTF-8\n",
			},
			"sbx exec main-sbx curl -fsS https://example.test/ip": {
				Stdout: "203.0.113.10\n",
			},
		},
	}
}

type timeoutCleanupRunner struct {
	results                    map[string]CommandResult
	cancel                     context.CancelFunc
	sawStop                    bool
	sawRemove                  bool
	cleanupUsedCanceledContext bool
}

func (r *timeoutCleanupRunner) LookPath(file string) (string, error) {
	return file, nil
}

func (r *timeoutCleanupRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	switch key {
	case "sbx exec -e TZ=UTC -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 probe env":
		r.cancel()
		return CommandResult{}, context.DeadlineExceeded
	case "sbx stop probe":
		r.sawStop = true
		if ctx.Err() != nil {
			r.cleanupUsedCanceledContext = true
			return CommandResult{}, ctx.Err()
		}
		return r.results[key], errors.New("exit status 1")
	case "sbx rm --force probe":
		r.sawRemove = true
		if ctx.Err() != nil {
			r.cleanupUsedCanceledContext = true
			return CommandResult{}, ctx.Err()
		}
		return r.results[key], errors.New("exit status 1")
	default:
		result, ok := r.results[key]
		if !ok {
			return CommandResult{}, fmt.Errorf("unexpected command %q", key)
		}
		return result, nil
	}
}

type herdrStopTimeoutRunner struct {
	herdrStopHadDeadline bool
	sawSandboxStop       bool
}

func (r *herdrStopTimeoutRunner) LookPath(file string) (string, error) {
	return file, nil
}

func (r *herdrStopTimeoutRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	switch key {
	case "sbx exec main-sbx herdr server stop":
		_, r.herdrStopHadDeadline = ctx.Deadline()
		<-ctx.Done()
		return CommandResult{}, ctx.Err()
	case "sbx stop main-sbx":
		r.sawSandboxStop = true
		return CommandResult{Stdout: "stopped\n"}, nil
	default:
		return CommandResult{}, fmt.Errorf("unexpected command %q", key)
	}
}

type staleProbeCreateRunner struct {
	calls       []string
	createCalls int
}

func (r *staleProbeCreateRunner) LookPath(file string) (string, error) {
	return file, nil
}

func (r *staleProbeCreateRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, key)
	if strings.HasPrefix(key, "sbx exec probe sh -lc workspace=") {
		return CommandResult{Stdout: "ok\n"}, nil
	}
	switch key {
	case "sbx create --name probe shell .":
		r.createCalls++
		if r.createCalls == 1 {
			return CommandResult{Stderr: "ERROR: sandbox 'probe' already exists\n"}, errors.New("exit status 1")
		}
		return CommandResult{Stdout: "created\n"}, nil
	case "sbx exec -e TZ=UTC -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 probe env":
		return CommandResult{Stdout: "PATH=/usr/bin\nHTTP_PROXY=http://gateway.docker.internal:3128\n"}, nil
	case "sbx exec probe pwd":
		return CommandResult{Stdout: "/workspace\n"}, nil
	case "sbx exec probe mount":
		return CommandResult{Stdout: "/dev/disk1 on /workspace type virtiofs\n"}, nil
	case "sbx exec -e TZ=UTC probe date":
		return CommandResult{Stdout: "Sun Jul 5 00:00:00 UTC 2026\n"}, nil
	case "sbx exec -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 probe locale":
		return CommandResult{Stdout: "LANG=en_US.UTF-8\n"}, nil
	case "sbx exec probe curl -fsS https://example.test/ip":
		return CommandResult{Stdout: "203.0.113.10\n"}, nil
	case "sbx stop probe":
		return CommandResult{Stdout: "stopped\n"}, nil
	case "sbx rm --force probe":
		return CommandResult{Stdout: "removed\n"}, nil
	default:
		return CommandResult{}, fmt.Errorf("unexpected command %q", key)
	}
}

func (r *staleProbeCreateRunner) sawPrefix(prefix string) bool {
	for _, call := range r.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}

type stubRunner struct {
	path        string
	lookPathErr error
	results     map[string]CommandResult
	errors      map[string]error
	calls       *[]string
}

type deadlineRecordingRunner struct {
	results                  map[string]CommandResult
	errors                   map[string]error
	fastDeadlineCount        int
	cleanupDeadlineCount     int
	mainCleanupDeadlineCount int
}

func (r *deadlineRecordingRunner) LookPath(file string) (string, error) {
	return file, nil
}

func (r *deadlineRecordingRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if strings.Contains(key, "create --name probe") || strings.Contains(key, "exec probe curl") || strings.Contains(key, "exec main-sbx curl") {
			if remaining > 0 && remaining <= 2*time.Second {
				r.fastDeadlineCount++
			}
		}
		if strings.Contains(key, "stop probe") || strings.Contains(key, "rm --force probe") {
			if remaining > 0 && remaining <= 2*time.Second {
				r.cleanupDeadlineCount++
			}
		}
		if strings.Contains(key, "herdr server stop") || strings.Contains(key, "stop main-sbx") || strings.Contains(key, "rm --force main-sbx") {
			if remaining > 0 && remaining <= 2*time.Second {
				r.mainCleanupDeadlineCount++
			}
		}
	}
	result := r.results[key]
	return result, r.errors[key]
}

func (r stubRunner) LookPath(file string) (string, error) {
	if r.lookPathErr != nil {
		return "", r.lookPathErr
	}
	if r.path != "" {
		return r.path, nil
	}
	return file, nil
}

func (r stubRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	if r.calls != nil {
		*r.calls = append(*r.calls, key)
	}
	result := r.results[key]
	return result, r.errors[key]
}

func (r stubRunner) keys() []string {
	if r.calls != nil {
		return append([]string(nil), (*r.calls)...)
	}
	keys := make([]string, 0, len(r.results))
	for key := range r.results {
		keys = append(keys, key)
	}
	return keys
}

func (r stubRunner) saw(key string) bool {
	for _, got := range r.keys() {
		if got == key {
			return true
		}
	}
	return false
}

type sequenceRunner struct {
	stubRunner
	sequences map[string][]CommandResult
	counts    map[string]int
}

func (r *sequenceRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	if r.calls != nil {
		*r.calls = append(*r.calls, key)
	}
	if sequence := r.sequences[key]; len(sequence) > 0 {
		if r.counts == nil {
			r.counts = map[string]int{}
		}
		index := r.counts[key]
		r.counts[key] = index + 1
		if index >= len(sequence) {
			index = len(sequence) - 1
		}
		return sequence[index], nil
	}
	result := r.results[key]
	return result, r.errors[key]
}

func isMainWorkspacePreparationCommand(key string) bool {
	return strings.HasPrefix(key, "sbx exec main-sbx sh -lc workspace=")
}
