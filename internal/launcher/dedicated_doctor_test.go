package launcher

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/backend"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/egressguard"
)

func TestRunnerRejectsUnsupportedBackendBeforeCreatingEgressGuard(t *testing.T) {
	configPath := writeLauncherDedicatedDoctorConfig(t)
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	contents = []byte(strings.Replace(string(contents), `backend: "docker-sandbox"`, `backend: "unsupported"`, 1))
	if err := os.WriteFile(configPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	guardCreated := false
	runner := Runner{
		sandbox: backend.DockerSandbox{},
		newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			guardCreated = true
			return nil, fmt.Errorf("must not create guard")
		},
	}
	var stdout, stderr bytes.Buffer

	code := runner.Run(Request{
		Target:     DoctorTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code == 0 || !strings.Contains(stderr.String(), `sandbox backend invalid: unsupported backend "unsupported"`) {
		t.Fatalf("expected unsupported backend failure, got code %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if guardCreated {
		t.Fatal("unsupported backend reached egress guard creation")
	}
}

func TestDoctorSupportedDedicatedAdapterRunsThroughLauncherAndCleansUpInOrder(t *testing.T) {
	configPath := writeLauncherDedicatedDoctorConfig(t)
	log := make([]string, 0, 16)
	runner := &doctorTestRunner{log: &log, egressIP: "203.0.113.10"}
	sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
	guard := &doctorTestGuard{log: &log}
	var stdout, stderr bytes.Buffer

	launcher := Runner{
		sandbox: sandbox,
		newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
	}
	code := launcher.Run(Request{
		Target:     DoctorTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code != 0 {
		t.Fatalf("doctor failed with code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	for _, want := range []string{
		"gateway controller ok: authenticated loopback controller",
		"sandboxd lease ok: exclusive command-scoped upstream active",
		"sandbox backend ok: sbx version: v0.34.0 fake",
		"sandbox egress ok: observed IP 203.0.113.10",
		"controller isolation ok: endpoint unreachable from main sandbox",
		"sandboxd lease revoked: launcher-owned daemon state restored",
		"sandbox inspection ok",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output %q, got:\n%s", want, stdout.String())
		}
	}
	assertLogOrder(t, log,
		"guard acquire",
		"sbx version",
		"sbx create --name claude-sbx claude .",
		"guard validate",
		"guard revoke",
		"sbx stop claude-sbx",
	)
}

func TestDoctorSupportedDedicatedAdapterFailsClosed(t *testing.T) {
	tests := []struct {
		name          string
		acquireErr    error
		validateErr   error
		egressIP      string
		wantError     string
		wantPreflight bool
	}{
		{
			name:       "gateway unavailable",
			acquireErr: fmt.Errorf("gateway controller invalid: loopback controller unavailable"),
			wantError:  "gateway controller invalid: loopback controller unavailable",
		},
		{
			name:       "exclusive lease conflict",
			acquireErr: fmt.Errorf("sandboxd lease invalid: unrelated sandbox conflict: other-sbx"),
			wantError:  "sandboxd lease invalid: unrelated sandbox conflict: other-sbx",
		},
		{
			name:          "main egress mismatch",
			egressIP:      "198.51.100.20",
			wantError:     "sandbox-egress-mismatch",
			wantPreflight: true,
		},
		{
			name:          "controller exposed to main",
			validateErr:   fmt.Errorf("controller isolation invalid: controller endpoint is reachable from main sandbox"),
			wantError:     "controller isolation invalid: controller endpoint is reachable",
			wantPreflight: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := writeLauncherDedicatedDoctorConfig(t)
			log := make([]string, 0, 16)
			egressIP := tt.egressIP
			if egressIP == "" {
				egressIP = "203.0.113.10"
			}
			runner := &doctorTestRunner{log: &log, egressIP: egressIP}
			sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
			guard := &doctorScenarioGuard{log: &log, acquireErr: tt.acquireErr, validateErr: tt.validateErr}
			var stdout, stderr bytes.Buffer

			launcher := Runner{
				sandbox: sandbox,
				newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
					return guard, nil
				},
			}
			code := launcher.Run(Request{
				Target:     DoctorTarget,
				ConfigPath: configPath,
				Stdout:     &stdout,
				Stderr:     &stderr,
			})

			if code == 0 || !strings.Contains(stderr.String(), tt.wantError) {
				t.Fatalf("expected %q failure, got code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", tt.wantError, code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
			}
			if tt.wantPreflight {
				assertLogOrder(t, log, "guard acquire", "sbx create --name claude-sbx claude .", "guard revoke", "sbx stop claude-sbx")
				return
			}
			for _, entry := range log {
				if strings.HasPrefix(entry, "sbx ") {
					t.Fatalf("acquire failure reached backend mutation:\n%s", strings.Join(log, "\n"))
				}
			}
		})
	}
}

func TestDoctorSupportedDedicatedAdapterPreservesExistingMainAfterInspectionFailure(t *testing.T) {
	const inspectionSecret = "EXISTING_MAIN_SECRET_VALUE"
	configPath := writeLauncherDedicatedDoctorConfig(t)
	log := make([]string, 0, 16)
	runner := &doctorTestRunner{
		log:        &log,
		egressIP:   "203.0.113.10",
		mainExists: true,
		mainStatus: "running",
		env:        "PATH=/usr/bin\nTZ=America/Chicago\nLANG=en_US.UTF-8\nLC_ALL=C.UTF-8\nOPENAI_API_KEY=" + inspectionSecret + "\n",
	}
	sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
	guard := &doctorScenarioGuard{log: &log}
	var stdout, stderr bytes.Buffer

	launcher := Runner{
		sandbox: sandbox,
		newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
	}
	code := launcher.Run(Request{
		Target:     DoctorTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code == 0 || !strings.Contains(stderr.String(), "main sandbox inspection invalid") {
		t.Fatalf("expected existing main inspection failure, got code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	if strings.Contains(stderr.String(), inspectionSecret) {
		t.Fatalf("inspection failure leaked secret:\n%s", stderr.String())
	}
	for _, entry := range log {
		if strings.HasPrefix(entry, "sbx create ") || entry == "sbx stop claude-sbx" || strings.HasPrefix(entry, "sbx rm ") {
			t.Fatalf("doctor destructively changed existing main with %q:\n%s", entry, strings.Join(log, "\n"))
		}
	}
	assertLogOrder(t, log, "guard acquire", "sbx version", "guard revoke")
}

func TestDoctorSupportedDedicatedAdapterRuntimeFailuresRevokeBeforeCleanup(t *testing.T) {
	for _, wantError := range []string{
		"gateway controller invalid: authenticated health request failed",
		"sandboxd lease invalid: dedicated daemon exited",
		"sandboxd lease invalid: unrelated sandbox conflict: late-sbx",
	} {
		t.Run(wantError, func(t *testing.T) {
			configPath := writeLauncherDedicatedDoctorConfig(t)
			log := make([]string, 0, 16)
			runner := &doctorTestRunner{log: &log, egressIP: "203.0.113.10"}
			sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
			guard := &doctorScenarioGuard{log: &log, validateErr: fmt.Errorf("%s", wantError)}
			var stdout, stderr bytes.Buffer

			launcher := Runner{
				sandbox: sandbox,
				newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
					return guard, nil
				},
			}
			code := launcher.Run(Request{
				Target:     DoctorTarget,
				ConfigPath: configPath,
				Stdout:     &stdout,
				Stderr:     &stderr,
			})

			if code == 0 || !strings.Contains(stderr.String(), wantError) {
				t.Fatalf("expected %q failure, got code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", wantError, code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
			}
			assertLogOrder(t, log, "guard acquire", "guard validate", "guard revoke", "sbx stop claude-sbx")
		})
	}
}

type doctorTestGuard struct {
	log *[]string
}

func (g *doctorTestGuard) Acquire(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard acquire")
	return egressguard.Result{
		Messages: []string{
			"gateway controller ok: authenticated loopback controller",
			"sandboxd lease ok: exclusive command-scoped upstream active",
		},
		CleanupCreatedMain: true,
	}, nil
}

func (g *doctorTestGuard) ValidateMain(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard validate")
	return egressguard.Result{Messages: []string{"controller isolation ok: endpoint unreachable from main sandbox"}}, nil
}

func (*doctorTestGuard) Watch(context.Context, egressguard.WatchInput) egressguard.RuntimeWatch {
	return egressguard.RuntimeWatch{}
}

func (g *doctorTestGuard) Revoke(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard revoke")
	return egressguard.Result{Messages: []string{"sandboxd lease revoked: launcher-owned daemon state restored"}}, nil
}

type doctorScenarioGuard struct {
	log         *[]string
	acquireErr  error
	validateErr error
}

func (g *doctorScenarioGuard) Acquire(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard acquire")
	if g.acquireErr != nil {
		return egressguard.Result{}, g.acquireErr
	}
	return egressguard.Result{CleanupCreatedMain: true}, nil
}

func (g *doctorScenarioGuard) ValidateMain(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard validate")
	return egressguard.Result{}, g.validateErr
}

func (*doctorScenarioGuard) Watch(context.Context, egressguard.WatchInput) egressguard.RuntimeWatch {
	return egressguard.RuntimeWatch{}
}

func (g *doctorScenarioGuard) Revoke(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard revoke")
	return egressguard.Result{}, nil
}

type doctorTestRunner struct {
	log        *[]string
	egressIP   string
	mainExists bool
	mainStatus string
	env        string
}

func (r *doctorTestRunner) LookPath(string) (string, error) {
	return "/fake/sbx", nil
}

func (r *doctorTestRunner) Run(_ context.Context, name string, args ...string) (backend.CommandResult, error) {
	entry := strings.TrimSpace(name + " " + strings.Join(args, " "))
	*r.log = append(*r.log, entry)
	if name != "sbx" || len(args) == 0 {
		return backend.CommandResult{ExitCode: 1, Stderr: "unexpected command"}, fmt.Errorf("unexpected command")
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
	default:
		return backend.CommandResult{ExitCode: 1, Stderr: "unexpected command"}, fmt.Errorf("unexpected command")
	}
}

func (r *doctorTestRunner) exec(args []string) (backend.CommandResult, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "sh -lc workspace="):
		return backend.CommandResult{Stdout: "ok\n"}, nil
	case args[len(args)-1] == "env":
		environment := r.env
		if environment == "" {
			environment = "PATH=/usr/bin\nTZ=America/Chicago\nLANG=en_US.UTF-8\nLC_ALL=C.UTF-8\nHTTP_PROXY=http://gateway.docker.internal:3128\nHTTPS_PROXY=http://gateway.docker.internal:3128\nNO_PROXY=localhost,127.0.0.1,gateway.docker.internal\n"
		}
		return backend.CommandResult{Stdout: environment}, nil
	case args[len(args)-1] == "pwd":
		return backend.CommandResult{Stdout: "/workspace\n"}, nil
	case args[len(args)-1] == "mount":
		return backend.CommandResult{Stdout: "/dev/disk1 on /workspace type virtiofs\n"}, nil
	case args[len(args)-1] == "date":
		return backend.CommandResult{Stdout: "Sun Jul  5 12:00:00 UTC 2026\n"}, nil
	case args[len(args)-1] == "locale":
		return backend.CommandResult{Stdout: "LANG=en_US.UTF-8\nLC_ALL=C.UTF-8\n"}, nil
	case strings.Contains(joined, "curl -fsS"):
		return backend.CommandResult{Stdout: r.egressIP + "\n"}, nil
	default:
		return backend.CommandResult{ExitCode: 1, Stderr: "unexpected exec"}, fmt.Errorf("unexpected exec")
	}
}

func writeLauncherDedicatedDoctorConfig(t *testing.T) string {
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

func assertLogOrder(t *testing.T, log []string, entries ...string) {
	t.Helper()
	position := -1
	for _, entry := range entries {
		found := -1
		for i := position + 1; i < len(log); i++ {
			if log[i] == entry {
				found = i
				break
			}
		}
		if found < 0 {
			t.Fatalf("expected %q after position %d, got:\n%s", entry, position, strings.Join(log, "\n"))
		}
		position = found
	}
}
