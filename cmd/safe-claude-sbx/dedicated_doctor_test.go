package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDoctorValidatesDedicatedGatewayAndCleansUpInOrder(t *testing.T) {
	const secret = "DEDICATED_CONTROLLER_SECRET_VALUE"
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
		fmt.Fprintln(w, `{"version":"v1.19.28"}`)
	}))
	t.Cleanup(controller.Close)

	configPath := writeTestConfig(t, dedicatedDoctorConfig(controller.URL, "203.0.113.10"))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeSBX := writeDedicatedFakeSBX(t, logPath, "203.0.113.10", false, false)
	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(),
		"PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HTTP_PROXY=http://127.0.0.1:7897",
		"HTTPS_PROXY=http://127.0.0.1:7897",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dedicated doctor failed: %v\n%s\nsbx log:\n%s", err, output, readOptionalFile(t, logPath))
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
		if !strings.Contains(string(output), want) {
			t.Fatalf("expected output %q, got:\n%s", want, output)
		}
	}
	log := readFile(t, logPath)
	if !strings.Contains(log, "daemon-start DOCKER_SANDBOXES_PROXY=http://127.0.0.1:17890") {
		t.Fatalf("expected command-scoped dedicated upstream, got:\n%s", log)
	}
	for _, forbidden := range []string{"HTTP_PROXY=", "HTTPS_PROXY=", secret, "run --name", "herdr"} {
		if strings.Contains(log, forbidden) || strings.Contains(string(output), forbidden) {
			t.Fatalf("dedicated doctor leaked or invoked forbidden value %q\noutput:\n%s\nlog:\n%s", forbidden, output, log)
		}
	}
	revokeIndex := strings.LastIndex(log, "daemon stop")
	restoreIndex := strings.LastIndex(log, "ls")
	cleanupIndex := strings.LastIndex(log, "stop claude-sbx")
	if revokeIndex < 0 || restoreIndex < 0 || cleanupIndex < 0 || !(revokeIndex < restoreIndex && restoreIndex < cleanupIndex) {
		t.Fatalf("expected revoke, daemon restore, then main cleanup, got:\n%s", log)
	}
}

func TestDoctorDedicatedGatewayFailsClosed(t *testing.T) {
	tests := []struct {
		name                string
		closeController     bool
		unrelatedRunning    bool
		egressIP            string
		controllerReachable bool
		wantError           string
		wantLease           bool
		wantMainCleanup     bool
	}{
		{
			name:            "gateway unavailable",
			closeController: true,
			wantError:       "gateway controller invalid: loopback controller unavailable",
		},
		{
			name:             "exclusive lease conflict",
			unrelatedRunning: true,
			egressIP:         "203.0.113.10",
			wantError:        "sandboxd lease invalid: unrelated sandbox conflict: other-sbx",
		},
		{
			name:            "main egress mismatch",
			egressIP:        "198.51.100.20",
			wantError:       "sandbox-egress-mismatch",
			wantLease:       true,
			wantMainCleanup: true,
		},
		{
			name:                "controller exposed to main",
			egressIP:            "203.0.113.10",
			controllerReachable: true,
			wantError:           "controller isolation invalid: controller endpoint is reachable",
			wantLease:           true,
			wantMainCleanup:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const secret = "DEDICATED_CONTROLLER_SECRET_VALUE"
			t.Setenv("SAFE_CLAUDE_SBX_MIHOMO_SECRET", secret)
			controller := newDedicatedController(t, secret)
			if tt.closeController {
				controller.Close()
			}
			configPath := writeTestConfig(t, dedicatedDoctorConfig(controller.URL, "203.0.113.10"))
			logPath := filepath.Join(t.TempDir(), "sbx.log")
			egressIP := tt.egressIP
			if egressIP == "" {
				egressIP = "203.0.113.10"
			}
			fakeSBX := writeDedicatedFakeSBX(t, logPath, egressIP, tt.unrelatedRunning, tt.controllerReachable)
			cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
			cmd.Dir = "."
			cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

			output, err := cmd.CombinedOutput()

			if err == nil || !strings.Contains(string(output), tt.wantError) {
				t.Fatalf("expected %q failure, got err %v\n%s\nsbx log:\n%s", tt.wantError, err, output, readOptionalFile(t, logPath))
			}
			if strings.Contains(string(output), secret) {
				t.Fatalf("failure leaked controller secret:\n%s", output)
			}
			log := readOptionalFile(t, logPath)
			if tt.wantLease != strings.Contains(log, "daemon-start DOCKER_SANDBOXES_PROXY=http://127.0.0.1:17890") {
				t.Fatalf("unexpected lease state, want %v:\n%s", tt.wantLease, log)
			}
			if !tt.wantLease && strings.Contains(log, "daemon start") {
				t.Fatalf("doctor mutated daemon before acquisition checks passed:\n%s", log)
			}
			if tt.wantMainCleanup {
				revokeIndex := strings.LastIndex(log, "daemon stop")
				restoreIndex := strings.LastIndex(log, "ls")
				cleanupIndex := strings.LastIndex(log, "stop claude-sbx")
				if revokeIndex < 0 || restoreIndex < 0 || cleanupIndex < 0 || !(revokeIndex < restoreIndex && restoreIndex < cleanupIndex) {
					t.Fatalf("expected fail-closed cleanup ordering, got:\n%s", log)
				}
			}
		})
	}
}

func TestDoctorRejectsUnsupportedBackendBeforeDedicatedMutation(t *testing.T) {
	const secret = "DEDICATED_CONTROLLER_SECRET_VALUE"
	t.Setenv("SAFE_CLAUDE_SBX_MIHOMO_SECRET", secret)
	controller := newDedicatedController(t, secret)
	configBody := strings.Replace(
		dedicatedDoctorConfig(controller.URL, "203.0.113.10"),
		`backend: "docker-sandbox"`,
		`backend: "unsupported"`,
		1,
	)
	configPath := writeTestConfig(t, configBody)
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeSBX := writeDedicatedFakeSBX(t, logPath, "203.0.113.10", false, false)
	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()

	if err == nil || !strings.Contains(string(output), `sandbox backend invalid: unsupported backend "unsupported"`) {
		t.Fatalf("expected unsupported backend failure, got err %v\n%s", err, output)
	}
	if log := readOptionalFile(t, logPath); log != "" {
		t.Fatalf("unsupported backend mutated Docker state:\n%s", log)
	}
}

func TestDoctorChecksDedicatedBackendBeforeDaemonMutation(t *testing.T) {
	const secret = "DEDICATED_CONTROLLER_SECRET_VALUE"
	t.Setenv("SAFE_CLAUDE_SBX_MIHOMO_SECRET", secret)
	controller := newDedicatedController(t, secret)
	configPath := writeTestConfig(t, dedicatedDoctorConfig(controller.URL, "203.0.113.10"))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeSBX := writeUnavailableDedicatedSBX(t, logPath)
	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()

	if err == nil || !strings.Contains(string(output), "sandbox backend invalid") {
		t.Fatalf("expected backend precheck failure, got err %v\n%s", err, output)
	}
	if log := readOptionalFile(t, logPath); strings.Contains(log, "daemon stop") || strings.Contains(log, "daemon start") {
		t.Fatalf("backend precheck failure mutated daemon:\n%s", log)
	}
}

func TestDoctorDedicatedGatewayPreservesExistingMainAfterInspectionFailure(t *testing.T) {
	const controllerSecret = "DEDICATED_CONTROLLER_SECRET_VALUE"
	const inspectionSecret = "EXISTING_MAIN_SECRET_VALUE"
	t.Setenv("SAFE_CLAUDE_SBX_MIHOMO_SECRET", controllerSecret)
	controller := newDedicatedController(t, controllerSecret)
	configPath := writeTestConfig(t, dedicatedDoctorConfig(controller.URL, "203.0.113.10"))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeSBX := writeDedicatedExistingMainFakeSBX(t, logPath, inspectionSecret)
	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()

	if err == nil || !strings.Contains(string(output), "main sandbox inspection invalid") {
		t.Fatalf("expected existing main inspection failure, got err %v\n%s", err, output)
	}
	if strings.Contains(string(output), inspectionSecret) {
		t.Fatalf("inspection failure leaked secret:\n%s", output)
	}
	log := readFile(t, logPath)
	if strings.Count(log, "exec claude-sbx true") != 2 {
		t.Fatalf("expected existing main reload under dedicated and restored daemon, got:\n%s", log)
	}
	for _, forbidden := range []string{"create --name claude-sbx", "stop claude-sbx", "rm --force claude-sbx"} {
		if strings.Contains(log, forbidden) {
			t.Fatalf("doctor destructively cleaned existing main with %q:\n%s", forbidden, log)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(log), "exec claude-sbx true") {
		t.Fatalf("expected existing main to finish restored and running, got:\n%s", log)
	}
}

func TestDoctorDedicatedGatewayRuntimeFailures(t *testing.T) {
	tests := []struct {
		name              string
		controllerStops   bool
		exitDaemon        bool
		lateScopeConflict bool
		wantError         string
	}{
		{name: "controller stops after acquire", controllerStops: true, wantError: "gateway controller invalid"},
		{name: "dedicated daemon exits", exitDaemon: true, wantError: "sandboxd lease invalid"},
		{name: "late scope conflict", lateScopeConflict: true, wantError: "unrelated sandbox conflict: late-sbx"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const secret = "DEDICATED_CONTROLLER_SECRET_VALUE"
			t.Setenv("SAFE_CLAUDE_SBX_MIHOMO_SECRET", secret)
			var authenticated atomic.Int32
			controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+secret {
					http.Error(w, "unauthorized", http.StatusUnauthorized)
					return
				}
				if tt.controllerStops && authenticated.Add(1) > 1 {
					http.Error(w, "controller stopped", http.StatusServiceUnavailable)
					return
				}
				fmt.Fprintln(w, `{"version":"v1.19.28"}`)
			}))
			t.Cleanup(controller.Close)
			configPath := writeTestConfig(t, dedicatedDoctorConfig(controller.URL, "203.0.113.10"))
			logPath := filepath.Join(t.TempDir(), "sbx.log")
			fakeSBX := writeDedicatedFakeSBXWithOptions(t, logPath, dedicatedFakeSBXOptions{
				EgressIP:              "203.0.113.10",
				ExitDaemonAfterEgress: tt.exitDaemon,
				LateScopeAfterEgress:  tt.lateScopeConflict,
			})
			cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
			cmd.Dir = "."
			cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

			output, err := cmd.CombinedOutput()

			if err == nil || !strings.Contains(string(output), tt.wantError) {
				t.Fatalf("expected %q failure, got err %v\n%s\nsbx log:\n%s", tt.wantError, err, output, readOptionalFile(t, logPath))
			}
			log := readFile(t, logPath)
			restoreIndex := strings.LastIndex(log, "ls")
			cleanupIndex := strings.LastIndex(log, "stop claude-sbx")
			if restoreIndex < 0 || cleanupIndex < 0 || restoreIndex >= cleanupIndex {
				t.Fatalf("expected daemon restore before command-created main cleanup, got:\n%s", log)
			}
		})
	}
}

func newDedicatedController(t *testing.T, secret string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		fmt.Fprintln(w, `{"version":"v1.19.28"}`)
	}))
	t.Cleanup(server.Close)
	return server
}

func dedicatedDoctorConfig(controllerURL, expectedIP string) string {
	return fmt.Sprintf(`
network:
  egress:
    mode: "dedicated-gateway"
    dedicated_gateway:
      upstream_url: "http://127.0.0.1:17890"
      controller_url: %q
      controller_secret_env: "SAFE_CLAUDE_SBX_MIHOMO_SECRET"
  egress_ip:
    expected_ip: %q
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
`, controllerURL, expectedIP)
}

func writeDedicatedFakeSBX(t *testing.T, logPath, egressIP string, unrelatedRunning, controllerReachable bool) string {
	return writeDedicatedFakeSBXWithOptions(t, logPath, dedicatedFakeSBXOptions{
		EgressIP:            egressIP,
		UnrelatedSandbox:    unrelatedRunning,
		ControllerReachable: controllerReachable,
	})
}

type dedicatedFakeSBXOptions struct {
	EgressIP              string
	UnrelatedSandbox      bool
	ControllerReachable   bool
	ExitDaemonAfterEgress bool
	LateScopeAfterEgress  bool
}

func writeDedicatedFakeSBXWithOptions(t *testing.T, logPath string, opts dedicatedFakeSBXOptions) string {
	t.Helper()
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "daemon-ready")
	stopPath := filepath.Join(dir, "daemon-stop")
	createdPath := filepath.Join(dir, "main-created")
	latePath := filepath.Join(dir, "late-sandbox")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$*" >> %q
case "$1" in
  version)
    printf 'sbx version: v0.34.0 fake\n'
    ;;
  ls)
    if [ %q = "true" ]; then
      printf 'SANDBOX AGENT STATUS PORTS WORKSPACE\n'
      printf 'other-sbx shell running - /tmp/other\n'
    elif [ -f %q ] || [ -f %q ]; then
      printf 'SANDBOX AGENT STATUS PORTS WORKSPACE\n'
	  if [ -f %q ]; then printf 'claude-sbx claude running - .\n'; fi
	  if [ -f %q ]; then printf 'late-sbx shell stopped - /tmp/late\n'; fi
    else
      printf 'No sandboxes found.\n'
    fi
    ;;
  daemon)
    case "${2:-}" in
      stop)
        if [ -f %q ]; then touch %q; fi
        ;;
      start)
        env | sort | sed -n '/DOCKER_SANDBOXES_PROXY=/p;/HTTP_PROXY=/p;/HTTPS_PROXY=/p;/ALL_PROXY=/p' | sed 's/^/daemon-start /' >> %q
        rm -f %q
        touch %q
        while [ ! -f %q ]; do sleep 0.02; done
        rm -f %q
        ;;
      status)
        [ -f %q ]
        ;;
      *) exit 1 ;;
    esac
    ;;
  create)
    touch %q
    printf 'created\n'
    ;;
  exec)
    case "$*" in
      *"curl -sS --connect-timeout 1"*)
        if [ %q = "true" ]; then exit 42; fi
        exit 0
        ;;
      *" curl "*)
	    printf '%%s\n' %q
	    if [ %q = "true" ]; then touch %q; fi
	    if [ %q = "true" ]; then
	      touch %q
	      while [ -f %q ]; do sleep 0.02; done
	    fi
	    ;;
      *" sh -lc workspace="*) printf 'ok\n' ;;
      *" env") printf 'PATH=/usr/bin\nTZ=America/Chicago\nLANG=en_US.UTF-8\nLC_ALL=C.UTF-8\nHTTP_PROXY=http://gateway.docker.internal:3128\nHTTPS_PROXY=http://gateway.docker.internal:3128\nNO_PROXY=localhost,127.0.0.1,gateway.docker.internal\n' ;;
      *" pwd") printf '/workspace\n' ;;
      *" mount") printf '/dev/disk1 on /workspace type virtiofs\n' ;;
      *" date") printf 'Sun Jul  5 12:00:00 UTC 2026\n' ;;
      *" locale") printf 'LANG=en_US.UTF-8\nLC_ALL=C.UTF-8\n' ;;
      *) printf 'unknown exec\n' >&2; exit 1 ;;
    esac
    ;;
  stop)
    printf 'stopped\n'
    ;;
  rm)
    printf 'not found\n' >&2
    exit 1
    ;;
  *)
    printf 'unknown command\n' >&2
    exit 1
    ;;
esac
`, logPath,
		shellBool(opts.UnrelatedSandbox),
		createdPath, latePath, createdPath, latePath,
		readyPath, stopPath,
		logPath, stopPath, readyPath, stopPath, readyPath,
		readyPath,
		createdPath,
		shellBool(opts.ControllerReachable),
		opts.EgressIP,
		shellBool(opts.LateScopeAfterEgress), latePath,
		shellBool(opts.ExitDaemonAfterEgress), stopPath, readyPath,
	)
	writeExecutable(t, filepath.Join(dir, "sbx"), script)
	return dir
}

func writeDedicatedExistingMainFakeSBX(t *testing.T, logPath, inspectionSecret string) string {
	t.Helper()
	dir := t.TempDir()
	readyPath := filepath.Join(dir, "daemon-ready")
	stopPath := filepath.Join(dir, "daemon-stop")
	runningPath := filepath.Join(dir, "main-running")
	writeFile(t, runningPath, "running\n")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$*" >> %q
case "$1" in
  version) printf 'sbx version: v0.34.0 fake\n' ;;
  ls)
    status=stopped
    if [ -f %q ]; then status=running; fi
    printf 'SANDBOX AGENT STATUS PORTS WORKSPACE\n'
    printf 'claude-sbx claude %%s - .\n' "$status"
    ;;
  daemon)
    case "${2:-}" in
      stop)
        rm -f %q
        if [ -f %q ]; then touch %q; fi
        ;;
      start)
        rm -f %q
        touch %q
        while [ ! -f %q ]; do sleep 0.02; done
        rm -f %q
        ;;
      status) [ -f %q ] ;;
      *) exit 1 ;;
    esac
    ;;
  exec)
    case "$*" in
      "exec claude-sbx true") touch %q ;;
      *"curl -sS --connect-timeout 1"*) exit 0 ;;
      *" curl "*) printf '203.0.113.10\n' ;;
      *" sh -lc workspace="*) printf 'ok\n' ;;
      *" env") printf 'PATH=/usr/bin\nTZ=America/Chicago\nLANG=en_US.UTF-8\nLC_ALL=C.UTF-8\nHTTP_PROXY=http://gateway.docker.internal:3128\nHTTPS_PROXY=http://gateway.docker.internal:3128\nNO_PROXY=localhost,127.0.0.1,gateway.docker.internal\nOPENAI_API_KEY=%%s\n' %q ;;
      *" pwd") printf '/workspace\n' ;;
      *" mount") printf '/dev/disk1 on /workspace type virtiofs\n' ;;
      *" date") printf 'Sun Jul  5 12:00:00 UTC 2026\n' ;;
      *" locale") printf 'LANG=en_US.UTF-8\nLC_ALL=C.UTF-8\n' ;;
      *) printf 'unknown exec\n' >&2; exit 1 ;;
    esac
    ;;
  stop|rm)
    printf 'destructive cleanup forbidden\n' >&2
    exit 1
    ;;
  *) exit 1 ;;
esac
`, logPath, runningPath, runningPath, readyPath, stopPath, stopPath, readyPath, stopPath, readyPath, readyPath, runningPath, inspectionSecret)
	writeExecutable(t, filepath.Join(dir, "sbx"), script)
	return dir
}

func writeUnavailableDedicatedSBX(t *testing.T, logPath string) string {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$*" >> %q
case "$1" in
  version) printf 'sbx version: v0.34.0 fake\n' ;;
  ls) printf 'control plane unavailable\n' >&2; exit 1 ;;
  *) exit 1 ;;
esac
`, logPath)
	writeExecutable(t, filepath.Join(dir, "sbx"), script)
	return dir
}
