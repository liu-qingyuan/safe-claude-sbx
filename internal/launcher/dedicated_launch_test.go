package launcher

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/backend"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/egressguard"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/watchdog"
)

type synchronizedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func TestDedicatedSafeHerdrRevalidatesExistingMainAndRevokesBeforeCleanup(t *testing.T) {
	configPath := writeLauncherDedicatedLaunchConfig(t)
	log := make([]string, 0, 32)
	writeAttachedSBX(t)
	runner := &launchTestRunner{
		log:        &log,
		egressIP:   "203.0.113.10",
		mainExists: true,
		mainStatus: "running",
	}
	sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
	guard := &launchTestGuard{log: &log}
	var stdout synchronizedBuffer
	var stderr bytes.Buffer

	code := runLaunchWithAdapters(
		configPath,
		herdrTUITarget,
		nil,
		&stdout,
		&stderr,
		sandbox,
		func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
	)

	if code != 0 {
		t.Fatalf("dedicated safe-herdr failed with code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	for _, want := range []string{
		"sandbox egress ok: observed IP 203.0.113.10",
		"controller isolation ok: endpoint unreachable from main sandbox",
		"Herdr TUI started: claude-sbx",
		"sandbox exited; cleanup complete",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output %q, got:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "TUN preflight") || strings.Contains(stdout.String(), "host egress") {
		t.Fatalf("dedicated launch unexpectedly ran host-inherited checks:\n%s", stdout.String())
	}
	assertLogOrder(t, log,
		"guard acquire",
		"sbx version",
		"guard validate",
		"guard revoke",
		"sbx exec claude-sbx herdr server stop",
		"sbx stop claude-sbx",
	)
	for _, forbidden := range []string{"sbx create", "sbx rm"} {
		for _, entry := range log {
			if strings.HasPrefix(entry, forbidden) {
				t.Fatalf("reuse path mutated existing main with %q:\n%s", entry, strings.Join(log, "\n"))
			}
		}
	}
}

func TestDedicatedSafeHerdrCreatesNewMainAndRevokesBeforeOwnedCleanup(t *testing.T) {
	configPath := writeLauncherDedicatedLaunchConfig(t)
	log := make([]string, 0, 32)
	writeAttachedSBX(t)
	runner := &launchTestRunner{log: &log, egressIP: "203.0.113.10"}
	sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
	guard := &launchTestGuard{log: &log}
	var stdout synchronizedBuffer
	var stderr bytes.Buffer

	code := runLaunchWithAdapters(
		configPath,
		herdrTUITarget,
		nil,
		&stdout,
		&stderr,
		sandbox,
		func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
	)

	if code != 0 {
		t.Fatalf("dedicated safe-herdr failed with code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	assertLogOrder(t, log,
		"guard acquire",
		"sbx create --name claude-sbx --template safe-claude-sbx-herdr:latest claude .",
		"guard validate",
		"guard revoke",
		"sbx exec claude-sbx herdr server stop",
		"sbx stop claude-sbx",
	)
}

func TestDedicatedSafeHerdrAttachesExpectedCommand(t *testing.T) {
	configPath := writeLauncherDedicatedLaunchConfig(t)
	log := make([]string, 0, 32)
	writeAttachedSBX(t)
	runner := &launchTestRunner{
		log:        &log,
		egressIP:   "203.0.113.10",
		mainExists: true,
		mainStatus: "running",
	}
	sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
	guard := &launchTestGuard{log: &log}
	var stdout synchronizedBuffer
	var stderr bytes.Buffer

	code := runLaunchWithAdapters(
		configPath,
		herdrTUITarget,
		nil,
		&stdout,
		&stderr,
		sandbox,
		func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
	)

	if code != 0 {
		t.Fatalf("dedicated safe-herdr failed with code %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "attached argv:exec -it claude-sbx herdr") {
		t.Fatalf("expected exact Herdr attach command, got:\n%s", stdout.String())
	}
}

func TestDedicatedSafeHerdrFailsClosedBeforeAttach(t *testing.T) {
	tests := []struct {
		name          string
		mainExists    bool
		egressIP      string
		acquireErr    error
		validateErr   error
		failCommand   string
		attachFailure bool
		wantError     string
		wantCreate    bool
		wantValidate  bool
		wantMainClean bool
	}{
		{
			name:       "acquire failure",
			acquireErr: fmt.Errorf("sandboxd lease invalid: unrelated sandbox conflict: other-sbx"),
			wantError:  "sandboxd lease invalid: unrelated sandbox conflict: other-sbx",
		},
		{
			name:       "existing main preflight failure",
			mainExists: true,
			egressIP:   "198.51.100.20",
			wantError:  "sandbox-egress-mismatch",
		},
		{
			name:          "new main preflight failure",
			egressIP:      "198.51.100.20",
			wantError:     "sandbox-egress-mismatch",
			wantCreate:    true,
			wantMainClean: true,
		},
		{
			name:         "existing main validation failure",
			mainExists:   true,
			egressIP:     "203.0.113.10",
			validateErr:  fmt.Errorf("controller isolation invalid: controller endpoint is reachable from main sandbox"),
			wantError:    "controller isolation invalid",
			wantValidate: true,
		},
		{
			name:         "existing main Herdr prepare failure",
			mainExists:   true,
			egressIP:     "203.0.113.10",
			failCommand:  "herdr --version",
			wantError:    "verify sandbox-local Herdr",
			wantValidate: true,
		},
		{
			name:          "existing main Herdr attach failure",
			mainExists:    true,
			egressIP:      "203.0.113.10",
			attachFailure: true,
			wantError:     "start sandbox-local Herdr TUI",
			wantValidate:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := writeLauncherDedicatedLaunchConfig(t)
			log := make([]string, 0, 32)
			if tt.attachFailure {
				writeUnstartableSBX(t)
			} else {
				writeAttachedSBX(t)
			}
			runner := &launchTestRunner{
				log:         &log,
				egressIP:    tt.egressIP,
				mainExists:  tt.mainExists,
				mainStatus:  "running",
				failCommand: tt.failCommand,
			}
			sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
			guard := &launchTestGuard{log: &log, acquireErr: tt.acquireErr, validateErr: tt.validateErr}
			var stdout, stderr bytes.Buffer

			code := runLaunchWithAdapters(
				configPath,
				herdrTUITarget,
				nil,
				&stdout,
				&stderr,
				sandbox,
				func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
					return guard, nil
				},
			)

			if code == 0 || !strings.Contains(stderr.String(), tt.wantError) {
				t.Fatalf("expected %q failure, got code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", tt.wantError, code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
			}
			if strings.Contains(stdout.String(), "Herdr TUI started") {
				t.Fatalf("failure entered attached Herdr lifecycle:\n%s", stdout.String())
			}
			if containsLogEntry(log, "sbx create") != tt.wantCreate {
				t.Fatalf("unexpected create behavior:\n%s", strings.Join(log, "\n"))
			}
			if containsLogEntry(log, "guard validate") != tt.wantValidate {
				t.Fatalf("unexpected validation behavior:\n%s", strings.Join(log, "\n"))
			}
			if containsLogEntry(log, "sbx stop claude-sbx") != tt.wantMainClean {
				t.Fatalf("unexpected main cleanup behavior:\n%s", strings.Join(log, "\n"))
			}
			assertLogOrder(t, log, "guard acquire", "guard revoke")
			if tt.wantMainClean {
				assertLogOrder(t, log, "guard revoke", "sbx exec claude-sbx herdr server stop", "sbx stop claude-sbx")
			}
		})
	}
}

func TestDedicatedSafeHerdrRevokesBeforeCancelingAttachedProcess(t *testing.T) {
	configPath := writeLauncherDedicatedLaunchConfig(t)
	log := make([]string, 0, 32)
	pidPath := writeBlockingSBX(t)
	runner := &launchTestRunner{
		log:        &log,
		egressIP:   "203.0.113.10",
		mainExists: true,
		mainStatus: "running",
	}
	sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
	guard := &launchOrderingGuard{log: &log, attachedPIDPath: pidPath}
	var stdout, stderr bytes.Buffer

	code := runLaunchWithPlatformAdapters(
		configPath,
		herdrTUITarget,
		nil,
		&stdout,
		&stderr,
		sandbox,
		func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
		launchPlatform{
			signalContext: func() (context.Context, context.CancelFunc) {
				waitForFile(t, pidPath)
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
		},
	)

	if code != 130 {
		t.Fatalf("expected signal exit 130, got %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	assertLogOrder(t, log,
		"guard revoke",
		"sbx exec claude-sbx herdr server stop",
		"sbx stop claude-sbx",
	)
}

func TestHostInheritedSafeHerdrKeepsPlatformPreflightAndRuntimeWatchdog(t *testing.T) {
	configPath := writeLauncherHostLaunchConfig(t)
	log := make([]string, 0, 32)
	writeBlockingSBX(t)
	runner := &launchTestRunner{
		log:        &log,
		egressIP:   "203.0.113.10",
		mainExists: true,
		mainStatus: "running",
	}
	sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
	guard := &hostLaunchGuard{log: &log}
	var stdout, stderr bytes.Buffer

	code := runLaunchWithPlatformAdapters(
		configPath,
		herdrTUITarget,
		nil,
		&stdout,
		&stderr,
		sandbox,
		func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
		launchPlatform{
			checkTUN: func(config.ClashVerge) (network.TUNPreflightResult, error) {
				log = append(log, "platform TUN preflight")
				return network.TUNPreflightResult{StartupTUNInterface: "utun9"}, nil
			},
			runtime: func(context.Context, config.Config, network.TUNPreflightResult) (<-chan watchdog.Event, <-chan error, watchdog.Checker) {
				log = append(log, "platform runtime start")
				events := make(chan watchdog.Event, 1)
				events <- watchdog.Event{Source: "host-route"}
				checker := watchdog.CheckFunc(func(context.Context, watchdog.Event) (watchdog.CheckResult, error) {
					log = append(log, "platform runtime check")
					return watchdog.CheckResult{OK: false, Reason: "host route drift"}, nil
				})
				return events, nil, checker
			},
			signalContext: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
		},
	)

	if code == 0 || !strings.Contains(stderr.String(), "host-route runtime policy failed: host route drift") {
		t.Fatalf("expected host runtime failure, got code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	for _, want := range []string{
		"TUN preflight ok: startup interface utun9",
		"host egress ok: observed IP 203.0.113.10",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected host-inherited output %q, got:\n%s", want, stdout.String())
		}
	}
	assertLogOrder(t, log,
		"platform TUN preflight",
		"guard acquire",
		"guard validate",
		"platform runtime start",
		"platform runtime check",
		"guard revoke",
		"sbx exec claude-sbx herdr server stop",
		"sbx stop claude-sbx",
	)
}

type launchOrderingGuard struct {
	log             *[]string
	attachedPIDPath string
}

type hostLaunchGuard struct {
	log *[]string
}

func (g *hostLaunchGuard) Acquire(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard acquire")
	return egressguard.Result{Messages: []string{"host egress ok: observed IP 203.0.113.10"}}, nil
}

func (g *hostLaunchGuard) ValidateMain(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard validate")
	return egressguard.Result{}, nil
}

func (g *hostLaunchGuard) Revoke(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard revoke")
	return egressguard.Result{}, nil
}

func (g *launchOrderingGuard) Acquire(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard acquire")
	return egressguard.Result{}, nil
}

func (g *launchOrderingGuard) ValidateMain(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard validate")
	return egressguard.Result{}, nil
}

func (g *launchOrderingGuard) Revoke(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard revoke")
	pidText, err := os.ReadFile(g.attachedPIDPath)
	if err != nil {
		return egressguard.Result{}, fmt.Errorf("read attached process PID: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidText)))
	if err != nil {
		return egressguard.Result{}, fmt.Errorf("parse attached process PID: %w", err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return egressguard.Result{}, fmt.Errorf("attached Herdr stopped before egress revoke: %w", err)
	}
	return egressguard.Result{}, nil
}

type launchTestGuard struct {
	log         *[]string
	acquireErr  error
	validateErr error
}

func (g *launchTestGuard) Acquire(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard acquire")
	if g.acquireErr != nil {
		return egressguard.Result{}, g.acquireErr
	}
	return egressguard.Result{Messages: []string{
		"gateway controller ok: authenticated loopback controller",
		"sandboxd lease ok: exclusive command-scoped upstream active",
	}}, nil
}

func (g *launchTestGuard) ValidateMain(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard validate")
	if g.validateErr != nil {
		return egressguard.Result{}, g.validateErr
	}
	return egressguard.Result{Messages: []string{"controller isolation ok: endpoint unreachable from main sandbox"}}, nil
}

func (g *launchTestGuard) Revoke(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard revoke")
	return egressguard.Result{Messages: []string{"sandboxd lease revoked: launcher-owned daemon state restored"}}, nil
}

type launchTestRunner struct {
	log         *[]string
	egressIP    string
	mainExists  bool
	mainStatus  string
	failCommand string
}

func (r *launchTestRunner) LookPath(string) (string, error) {
	return "/fake/sbx", nil
}

func (r *launchTestRunner) Run(_ context.Context, name string, args ...string) (backend.CommandResult, error) {
	entry := strings.TrimSpace(name + " " + strings.Join(args, " "))
	*r.log = append(*r.log, entry)
	if name != "sbx" || len(args) == 0 {
		return backend.CommandResult{ExitCode: 1, Stderr: "unexpected command"}, fmt.Errorf("unexpected command")
	}
	if r.failCommand != "" && strings.Contains(entry, r.failCommand) {
		return backend.CommandResult{ExitCode: 1, Stderr: "injected failure"}, fmt.Errorf("injected failure")
	}
	switch args[0] {
	case "version":
		return backend.CommandResult{Stdout: "sbx version: v0.34.0 fake\n"}, nil
	case "ls":
		if !r.mainExists {
			return backend.CommandResult{Stdout: "No sandboxes found.\n"}, nil
		}
		status := r.mainStatus
		if status == "" {
			status = "running"
		}
		return backend.CommandResult{Stdout: fmt.Sprintf("SANDBOX AGENT STATUS PORTS WORKSPACE\nclaude-sbx claude %s - .\n", status)}, nil
	case "create":
		r.mainExists = true
		r.mainStatus = "running"
		return backend.CommandResult{Stdout: "created\n"}, nil
	case "exec":
		return r.exec(args)
	case "stop":
		r.mainStatus = "stopped"
		return backend.CommandResult{Stdout: "stopped\n"}, nil
	case "rm":
		r.mainExists = false
		return backend.CommandResult{Stdout: "removed\n"}, nil
	default:
		return backend.CommandResult{ExitCode: 1, Stderr: "unexpected command"}, fmt.Errorf("unexpected command")
	}
}

func containsLogEntry(log []string, prefix string) bool {
	for _, entry := range log {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func (r *launchTestRunner) exec(args []string) (backend.CommandResult, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "sh -lc workspace="):
		return backend.CommandResult{Stdout: "ok\n"}, nil
	case strings.Contains(joined, "sh -lc command -v"):
		return backend.CommandResult{Stdout: "/usr/local/bin/tool\n"}, nil
	case strings.HasSuffix(joined, " herdr --version"):
		return backend.CommandResult{Stdout: "herdr 1.0.0\n"}, nil
	case strings.HasSuffix(joined, " herdr integration install claude"):
		return backend.CommandResult{Stdout: "installed\n"}, nil
	case strings.HasSuffix(joined, " cc --version"):
		return backend.CommandResult{Stdout: "cc 1.0.0\n"}, nil
	case strings.HasSuffix(joined, " herdr server stop"):
		return backend.CommandResult{Stdout: "stopped\n"}, nil
	case args[len(args)-1] == "env":
		return backend.CommandResult{Stdout: "PATH=/usr/bin\nTZ=America/Chicago\nLANG=en_US.UTF-8\nLC_ALL=en_US.UTF-8\nHTTP_PROXY=http://gateway.docker.internal:3128\nHTTPS_PROXY=http://gateway.docker.internal:3128\nNO_PROXY=localhost,127.0.0.1,gateway.docker.internal\n"}, nil
	case args[len(args)-1] == "pwd":
		return backend.CommandResult{Stdout: "/workspace\n"}, nil
	case args[len(args)-1] == "mount":
		return backend.CommandResult{Stdout: "/dev/disk1 on /workspace type virtiofs\n"}, nil
	case args[len(args)-1] == "date":
		return backend.CommandResult{Stdout: "Sun Jul  5 12:00:00 UTC 2026\n"}, nil
	case args[len(args)-1] == "locale":
		return backend.CommandResult{Stdout: "LANG=en_US.UTF-8\nLC_ALL=en_US.UTF-8\n"}, nil
	case strings.Contains(joined, "curl -fsS"):
		return backend.CommandResult{Stdout: r.egressIP + "\n"}, nil
	default:
		return backend.CommandResult{ExitCode: 1, Stderr: "unexpected exec"}, fmt.Errorf("unexpected exec")
	}
}

func writeAttachedSBX(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sbx")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'attached argv:%s\\n' \"$*\"\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write attached sbx: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeUnstartableSBX(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sbx")
	if err := os.WriteFile(path, []byte("not executable\n"), 0o600); err != nil {
		t.Fatalf("write unstartable sbx: %v", err)
	}
	t.Setenv("PATH", dir)
}

func writeBlockingSBX(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attached.pid")
	path := filepath.Join(dir, "sbx")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' \"$$\" > %s\nwhile :; do sleep 1; done\n", strconv.Quote(pidPath))
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write blocking sbx: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return pidPath
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func writeLauncherDedicatedLaunchConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `network:
  egress:
    mode: "dedicated-gateway"
    dedicated_gateway:
      upstream_url: "http://127.0.0.1:17890"
      controller_url: "http://127.0.0.1:19090"
      controller_secret_env: "SAFE_CLAUDE_SBX_MIHOMO_SECRET"
  egress_ip:
    expected_ip: "203.0.113.10"
    sandbox_check_url: "https://api.ipify.org"
    timeout_seconds: 3
sandbox:
  backend: "docker-sandbox"
  main_name: "claude-sbx"
  probe_name: "claude-sbx-probe"
  agent: "claude"
  template: "safe-claude-sbx-herdr:latest"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: false
      socket_path: "/home/agent/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"
workspace:
  mount: "."
  use_clone_mode: false
  forbidden_paths:
    - "~"
    - "~/.ssh"
environment:
  timezone: "America/Chicago"
  locale: "en_US.UTF-8"
  forbidden_env_vars:
    - HTTP_PROXY
    - HTTPS_PROXY
    - ALL_PROXY
    - NO_PROXY
watchdog:
  enabled: true
  log_level: "info"
  log_file: ""
cleanup:
  stop_main_sandbox: true
  remove_probe_sandbox: true
  remove_main_sandbox: false
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func writeLauncherHostLaunchConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := fmt.Sprintf(`network:
  clash_verge:
    app_home: %q
    route_check_target: "1.1.1.1"
    tun_interface_prefix: "utun"
  egress_ip:
    expected_ip: "203.0.113.10"
    host_check_url: "https://api.ipify.org"
    sandbox_check_url: "https://api.ipify.org"
    timeout_seconds: 3
sandbox:
  backend: "docker-sandbox"
  main_name: "claude-sbx"
  probe_name: "claude-sbx-probe"
  agent: "claude"
  template: "safe-claude-sbx-herdr:latest"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: false
      socket_path: "/home/agent/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"
workspace:
  mount: "."
  use_clone_mode: false
  forbidden_paths:
    - "~"
    - "~/.ssh"
environment:
  timezone: "America/Chicago"
  locale: "en_US.UTF-8"
  forbidden_env_vars:
    - HTTP_PROXY
    - HTTPS_PROXY
    - ALL_PROXY
    - NO_PROXY
watchdog:
  enabled: true
  log_level: "info"
  log_file: ""
cleanup:
  stop_main_sandbox: true
  remove_probe_sandbox: true
  remove_main_sandbox: false
`, t.TempDir())
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
