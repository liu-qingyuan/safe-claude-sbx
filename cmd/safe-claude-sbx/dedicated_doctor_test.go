package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorRejectsDedicatedGatewayWhenProtocolIsolationUnsupported(t *testing.T) {
	const secret = "DEDICATED_CONTROLLER_SECRET_VALUE"
	t.Setenv("SAFE_CLAUDE_SBX_MIHOMO_SECRET", secret)
	configPath := writeTestConfig(t, dedicatedDoctorConfig("http://127.0.0.1:19090", "203.0.113.10"))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeSBX := writeProtocolUnsupportedSBX(t, logPath)
	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()

	want := "dedicated protocol isolation unsupported: sbx v0.34.0 provides HTTP upstream only; generic TCP and DNS are not fail closed"
	if err == nil || !strings.Contains(string(output), want) {
		t.Fatalf("expected %q failure, got err %v\n%s", want, err, output)
	}
	if strings.Contains(string(output), secret) {
		t.Fatalf("protocol capability failure leaked controller secret:\n%s", output)
	}
	log := readFile(t, logPath)
	if log != "version\n" {
		t.Fatalf("expected only the protocol capability check, got:\n%s", log)
	}
}

func TestDoctorRejectsUnsupportedBackendBeforeDedicatedMutation(t *testing.T) {
	configBody := strings.Replace(
		dedicatedDoctorConfig("http://127.0.0.1:19090", "203.0.113.10"),
		`backend: "docker-sandbox"`,
		`backend: "unsupported"`,
		1,
	)
	configPath := writeTestConfig(t, configBody)
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeSBX := writeProtocolUnsupportedSBX(t, logPath)
	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()

	if err == nil || !strings.Contains(string(output), `sandbox backend invalid: unsupported backend "unsupported"`) {
		t.Fatalf("expected unsupported backend failure, got err %v\n%s", err, output)
	}
	if log := readOptionalFile(t, logPath); log != "" {
		t.Fatalf("unsupported backend invoked sbx:\n%s", log)
	}
}

func TestDoctorFailsClosedWhenDedicatedCapabilityCannotBeInspected(t *testing.T) {
	configPath := writeTestConfig(t, dedicatedDoctorConfig("http://127.0.0.1:19090", "203.0.113.10"))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeSBX := writeUnavailableDedicatedSBX(t, logPath)
	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()

	want := "dedicated protocol isolation unsupported: cannot inspect Docker Sandbox version"
	if err == nil || !strings.Contains(string(output), want) {
		t.Fatalf("expected %q failure, got err %v\n%s", want, err, output)
	}
	if log := readFile(t, logPath); log != "version\n" {
		t.Fatalf("version inspection failure should stop before other sbx commands, got:\n%s", log)
	}
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

func writeProtocolUnsupportedSBX(t *testing.T, logPath string) string {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$*" >> %q
case "$1" in
  version) printf 'sbx version: v0.34.0 fake\n' ;;
  ls) printf 'No sandboxes found.\n' ;;
  *) printf 'unexpected mutation\n' >&2; exit 1 ;;
esac
`, logPath)
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
  version) printf 'version unavailable\n' >&2; exit 1 ;;
  *) exit 1 ;;
esac
`, logPath)
	writeExecutable(t, filepath.Join(dir, "sbx"), script)
	return dir
}
