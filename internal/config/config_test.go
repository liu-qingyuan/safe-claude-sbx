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

func TestLoadDefaultsSandboxSupervisionModeToDirectClaude(t *testing.T) {
	path := writeConfig(t, validConfigYAML())

	cfg, err := Load(path)

	if err != nil {
		t.Fatalf("expected config to load: %v", err)
	}
	if cfg.Sandbox.Supervision.Mode != "direct-claude" {
		t.Fatalf("expected default supervision mode direct-claude, got %q", cfg.Sandbox.Supervision.Mode)
	}
}

func TestLoadAcceptsSandboxLocalHerdrSupervision(t *testing.T) {
	path := writeConfig(t, strings.Replace(
		validConfigYAML(),
		`agent: "claude"`,
		`agent: "claude"
  template: "docker.io/example/safe-claude-sbx-herdr:latest"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: false
      socket_path: "/home/agent/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"`,
		1,
	))

	cfg, err := Load(path)

	if err != nil {
		t.Fatalf("expected sandbox-local Herdr config to load: %v", err)
	}
	if cfg.Sandbox.Supervision.Mode != "sandbox-local-herdr" {
		t.Fatalf("expected Herdr supervision mode, got %q", cfg.Sandbox.Supervision.Mode)
	}
	if cfg.Sandbox.Supervision.Herdr == nil || cfg.Sandbox.Supervision.Herdr.InstallIfMissing == nil || *cfg.Sandbox.Supervision.Herdr.InstallIfMissing {
		t.Fatalf("expected explicit install_if_missing=false to be preserved, got %#v", cfg.Sandbox.Supervision.Herdr)
	}
	if cfg.Sandbox.Template != "docker.io/example/safe-claude-sbx-herdr:latest" {
		t.Fatalf("expected sandbox template to load, got %q", cfg.Sandbox.Template)
	}
}

func TestLoadRejectsSandboxLocalHerdrWithoutTemplate(t *testing.T) {
	path := writeConfig(t, strings.Replace(
		validConfigYAML(),
		`agent: "claude"`,
		`agent: "claude"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: false
      socket_path: "/home/agent/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"`,
		1,
	))

	_, err := Load(path)

	if err == nil || !strings.Contains(err.Error(), "sandbox.template") {
		t.Fatalf("expected sandbox.template error, got %v", err)
	}
}

func TestLoadRejectsSandboxLocalHerdrRuntimeInstall(t *testing.T) {
	path := writeConfig(t, strings.Replace(
		validConfigYAML(),
		`agent: "claude"`,
		`agent: "claude"
  template: "docker.io/example/safe-claude-sbx-herdr:latest"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: true
      socket_path: "/home/agent/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"`,
		1,
	))

	_, err := Load(path)

	if err == nil || !strings.Contains(err.Error(), "install_if_missing") || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected runtime install disabled error, got %v", err)
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
}
