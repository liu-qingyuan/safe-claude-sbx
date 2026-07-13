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

func TestLoadDefaultsEgressModeToHostInherited(t *testing.T) {
	path := writeConfig(t, validConfigYAML())

	cfg, err := Load(path)

	if err != nil {
		t.Fatalf("expected legacy config without egress mode to load: %v", err)
	}
	if cfg.Network.Egress.Mode != "host-inherited" {
		t.Fatalf("expected host-inherited egress mode, got %q", cfg.Network.Egress.Mode)
	}
}

func TestLoadAcceptsDedicatedGatewayEgress(t *testing.T) {
	path := writeConfig(t, strings.Replace(
		validConfigYAML(),
		`network:
  clash_verge:
    route_check_target: "1.1.1.1"
    tun_interface_prefix: "utun"
  egress_ip:
    expected_ip: "203.0.113.10"
    host_check_url: "https://api.ipify.org"`,
		`network:
  egress:
    mode: "dedicated-gateway"
    dedicated_gateway:
      upstream_url: "http://127.0.0.1:17890"
      controller_url: "http://127.0.0.1:19090"
      controller_secret_env: "SAFE_CLAUDE_SBX_MIHOMO_SECRET"
  egress_ip:
    expected_ip: "203.0.113.10"`,
		1,
	))

	cfg, err := Load(path)

	if err != nil {
		t.Fatalf("expected dedicated gateway config to load: %v", err)
	}
	if cfg.Network.Egress.DedicatedGateway == nil {
		t.Fatal("expected dedicated gateway config")
	}
	if cfg.Network.Egress.DedicatedGateway.UpstreamURL != "http://127.0.0.1:17890" {
		t.Fatalf("unexpected upstream URL %q", cfg.Network.Egress.DedicatedGateway.UpstreamURL)
	}
	if cfg.Network.Egress.DedicatedGateway.ControllerSecretEnv != "SAFE_CLAUDE_SBX_MIHOMO_SECRET" {
		t.Fatalf("unexpected controller secret reference %q", cfg.Network.Egress.DedicatedGateway.ControllerSecretEnv)
	}
}

func TestLoadRejectsUnsafeDedicatedGatewayEgress(t *testing.T) {
	base := strings.Replace(
		validConfigYAML(),
		`network:
  clash_verge:
    route_check_target: "1.1.1.1"
    tun_interface_prefix: "utun"
  egress_ip:
    expected_ip: "203.0.113.10"
    host_check_url: "https://api.ipify.org"`,
		`network:
  egress:
    mode: "dedicated-gateway"
    dedicated_gateway:
      upstream_url: "http://127.0.0.1:17890"
      controller_url: "http://127.0.0.1:19090"
      controller_secret_env: "SAFE_CLAUDE_SBX_MIHOMO_SECRET"
  egress_ip:
    expected_ip: "203.0.113.10"`,
		1,
	)
	tests := []struct {
		name        string
		body        string
		wantMessage string
	}{
		{
			name:        "non-loopback upstream",
			body:        strings.Replace(base, "http://127.0.0.1:17890", "http://192.0.2.10:17890", 1),
			wantMessage: "upstream_url",
		},
		{
			name:        "credentialed upstream",
			body:        strings.Replace(base, "http://127.0.0.1:17890", "http://user:secret@127.0.0.1:17890", 1),
			wantMessage: "must not include credentials",
		},
		{
			name:        "unsupported upstream scheme",
			body:        strings.Replace(base, "http://127.0.0.1:17890", "socks5://127.0.0.1:17890", 1),
			wantMessage: "HTTP URL",
		},
		{
			name:        "non-loopback controller",
			body:        strings.Replace(base, "http://127.0.0.1:19090", "http://192.0.2.10:19090", 1),
			wantMessage: "controller_url",
		},
		{
			name:        "invalid secret reference",
			body:        strings.Replace(base, "SAFE_CLAUDE_SBX_MIHOMO_SECRET", "secret-value", 1),
			wantMessage: "environment variable name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.body))

			if err == nil || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("expected %q validation error, got %v", tt.wantMessage, err)
			}
		})
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
