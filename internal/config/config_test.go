package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsSensitiveWorkspaceMountChildren(t *testing.T) {
	path := writeConfig(t, strings.ReplaceAll(validConfigYAML(), `mount: "."`, `mount: "~/.ssh/id_ed25519"`))

	_, err := Load(path)

	if err == nil {
		t.Fatalf("expected sensitive workspace mount child to be rejected")
	}
	if !strings.Contains(err.Error(), "workspace.mount") {
		t.Fatalf("expected workspace.mount error, got %v", err)
	}
}

func TestLoadAcceptsProjectRelativeWorkspaceMount(t *testing.T) {
	path := writeConfig(t, validConfigYAML())

	_, err := Load(path)

	if err != nil {
		t.Fatalf("expected project-relative mount to be accepted: %v", err)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func validConfigYAML() string {
	return `
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
    - "~/.claude"
    - "~/.config/clash"
    - "~/Library/Keychains"
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
`
}
