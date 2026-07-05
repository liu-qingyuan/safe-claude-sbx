package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorAcceptsValidStructuredConfig(t *testing.T) {
	configPath := writeTestConfig(t, `
network:
  clash_verge:
    route_check_target: "1.1.1.1"
    tun_interface_prefix: "utun"
  egress_ip:
    expected_ip: "203.0.113.10"
    host_check_url: "https://api.ipify.org"
    sandbox_check_url: "https://api.ipify.org"
    timeout_seconds: 10
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
  timezone: "America/Los_Angeles"
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
`)

	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "configuration ok") {
		t.Fatalf("expected success output, got:\n%s", output)
	}
}

func TestDoctorRejectsMissingRequiredObjectPath(t *testing.T) {
	configPath := writeTestConfig(t, `
network:
  egress_ip:
    expected_ip: "203.0.113.10"
    host_check_url: "https://api.ipify.org"
    sandbox_check_url: "https://api.ipify.org"
    timeout_seconds: 10
sandbox:
  backend: "docker-sandbox"
  main_name: "claude-sbx"
  probe_name: "claude-sbx-probe"
  agent: "claude"
workspace:
  mount: "."
  forbidden_paths:
    - "~"
environment:
  timezone: "America/Los_Angeles"
  locale: "en_US.UTF-8"
  forbidden_env_vars:
    - HTTP_PROXY
watchdog:
  enabled: true
  log_level: "info"
cleanup:
  stop_main_sandbox: true
  remove_probe_sandbox: true
`)

	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("doctor unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "network.clash_verge") {
		t.Fatalf("expected missing object path in output, got:\n%s", output)
	}
}

func TestDoctorRejectsLegacyFlatConfigWithMigrationMessage(t *testing.T) {
	configPath := writeTestConfig(t, `
expected_egress_ip: "203.0.113.10"
route_check_target: "1.1.1.1"
ip_check_url: "https://api.ipify.org"
sandbox_name: "claude-sbx"
backend: "docker-sandbox"
workspace_mount: "."
cleanup:
  stop_on_exit: true
`)

	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("doctor unexpectedly accepted legacy config:\n%s", output)
	}
	if !strings.Contains(string(output), "legacy flat configuration") {
		t.Fatalf("expected legacy migration message, got:\n%s", output)
	}
	if !strings.Contains(string(output), "network.egress_ip.expected_ip") {
		t.Fatalf("expected new object path in migration message, got:\n%s", output)
	}
	if !strings.Contains(string(output), "cleanup.stop_main_sandbox") {
		t.Fatalf("expected cleanup migration path, got:\n%s", output)
	}
}

func TestDoctorRejectsForbiddenWorkspaceMount(t *testing.T) {
	configPath := writeTestConfig(t, `
network:
  clash_verge:
    route_check_target: "1.1.1.1"
    tun_interface_prefix: "utun"
  egress_ip:
    expected_ip: "203.0.113.10"
    host_check_url: "https://api.ipify.org"
    sandbox_check_url: "https://api.ipify.org"
    timeout_seconds: 10
sandbox:
  backend: "docker-sandbox"
  main_name: "claude-sbx"
  probe_name: "claude-sbx-probe"
  agent: "claude"
workspace:
  mount: "~"
  forbidden_paths:
    - "~"
    - "~/.ssh"
environment:
  timezone: "America/Los_Angeles"
  locale: "en_US.UTF-8"
  forbidden_env_vars:
    - HTTP_PROXY
watchdog:
  enabled: true
  log_level: "info"
cleanup:
  stop_main_sandbox: true
  remove_probe_sandbox: true
`)

	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("doctor unexpectedly accepted forbidden mount:\n%s", output)
	}
	if !strings.Contains(string(output), "workspace.mount") {
		t.Fatalf("expected workspace.mount error, got:\n%s", output)
	}
}

func TestDoctorRejectsMissingCleanupObject(t *testing.T) {
	configPath := writeTestConfig(t, `
network:
  clash_verge:
    route_check_target: "1.1.1.1"
    tun_interface_prefix: "utun"
  egress_ip:
    expected_ip: "203.0.113.10"
    host_check_url: "https://api.ipify.org"
    sandbox_check_url: "https://api.ipify.org"
    timeout_seconds: 10
sandbox:
  backend: "docker-sandbox"
  main_name: "claude-sbx"
  probe_name: "claude-sbx-probe"
  agent: "claude"
workspace:
  mount: "."
  forbidden_paths:
    - "~"
environment:
  timezone: "America/Los_Angeles"
  locale: "en_US.UTF-8"
  forbidden_env_vars:
    - HTTP_PROXY
watchdog:
  enabled: true
  log_level: "info"
`)

	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("doctor unexpectedly accepted missing cleanup object:\n%s", output)
	}
	if !strings.Contains(string(output), "cleanup") {
		t.Fatalf("expected cleanup object error, got:\n%s", output)
	}
}

func writeTestConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
