package policy

import (
	"strings"
	"testing"
)

func TestValidateWorkspaceMountRejectsSensitivePathsAndChildren(t *testing.T) {
	home := t.TempDir()
	forbidden := []string{"~", "~/.ssh", "~/.claude", "~/.config/clash", "~/Library/Keychains"}

	tests := []struct {
		name  string
		mount string
	}{
		{name: "home", mount: "~"},
		{name: "ssh child", mount: "~/.ssh/id_ed25519"},
		{name: "claude child", mount: "~/.claude/projects"},
		{name: "clash child", mount: "~/.config/clash/profiles.yaml"},
		{name: "keychain child", mount: "~/Library/Keychains/login.keychain-db"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWorkspaceMount(WorkspacePolicy{
				Mount:          tt.mount,
				ForbiddenPaths: forbidden,
				HomeDir:        home,
				WorkingDir:     home + "/work/safe-claude-sbx",
			})
			if err == nil {
				t.Fatalf("expected %q to be rejected", tt.mount)
			}
			if !strings.Contains(err.Error(), "workspace.mount") {
				t.Fatalf("expected workspace policy error, got %v", err)
			}
		})
	}
}

func TestValidateWorkspaceMountAllowsProjectRelativePath(t *testing.T) {
	home := t.TempDir()

	err := ValidateWorkspaceMount(WorkspacePolicy{
		Mount:          ".",
		ForbiddenPaths: []string{"~", "~/.ssh"},
		HomeDir:        home,
		WorkingDir:     home + "/work/safe-claude-sbx",
	})
	if err != nil {
		t.Fatalf("expected project-relative mount to be accepted: %v", err)
	}
}

func TestValidateInspectionAllowsDockerManagedProxyAndRuntimeEnv(t *testing.T) {
	err := ValidateInspection(InspectionPolicy{
		Workspace: WorkspacePolicy{
			Mount:          ".",
			ForbiddenPaths: []string{"~", "~/.ssh"},
			HomeDir:        "/Users/alice",
			WorkingDir:     "/Users/alice/work/safe-claude-sbx",
		},
		Timezone: "America/Chicago",
		Locale:   "en_US.UTF-8",
	}, InspectionObservation{
		Environment: map[string]string{
			"PATH":        "/usr/bin",
			"TZ":          "America/Chicago",
			"LANG":        "en_US.UTF-8",
			"LC_ALL":      "C.UTF-8",
			"HTTP_PROXY":  "http://gateway.docker.internal:3128",
			"HTTPS_PROXY": "http://gateway.docker.internal:3128",
			"NO_PROXY":    "localhost,127.0.0.1,gateway.docker.internal",
		},
		WorkingDirectory: "/workspace",
		Mounts:           "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)",
		Date:             "Sun Jul  5 12:00:00 PDT 2026",
		Locale:           "LANG=en_US.UTF-8\nLC_ALL=C.UTF-8",
	})
	if err != nil {
		t.Fatalf("expected Docker-managed proxy env to be accepted: %v", err)
	}
}

func TestValidateInspectionAllowsDockerManagedCredentialPlaceholders(t *testing.T) {
	err := ValidateInspection(InspectionPolicy{
		Workspace: WorkspacePolicy{
			Mount:          ".",
			ForbiddenPaths: []string{"~", "~/.ssh"},
			HomeDir:        "/Users/alice",
			WorkingDir:     "/Users/alice/work/safe-claude-sbx",
		},
		Timezone: "America/Chicago",
		Locale:   "en_US.UTF-8",
	}, InspectionObservation{
		Environment: map[string]string{
			"OPENAI_API_KEY":    "proxy-managed",
			"ANTHROPIC_API_KEY": "proxy-managed",
		},
		WorkingDirectory: "/workspace",
		Mounts:           "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)",
		Date:             "Sun Jul  5 12:00:00 PDT 2026",
		Locale:           "LANG=en_US.UTF-8",
	})
	if err != nil {
		t.Fatalf("expected Docker-managed credential placeholders to be accepted: %v", err)
	}
}

func TestValidateInspectionFailsClosedForForbiddenEnvWithoutLeakingValue(t *testing.T) {
	secret := "/Users/alice/.ssh/agent.sock"
	err := ValidateInspection(InspectionPolicy{
		Workspace: WorkspacePolicy{
			Mount:          ".",
			ForbiddenPaths: []string{"~", "~/.ssh"},
			HomeDir:        "/Users/alice",
			WorkingDir:     "/Users/alice/work/safe-claude-sbx",
		},
		Timezone: "America/Chicago",
		Locale:   "en_US.UTF-8",
	}, InspectionObservation{
		Environment: map[string]string{
			"SSH_AUTH_SOCK": secret,
		},
		WorkingDirectory: "/workspace",
		Mounts:           "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)",
		Date:             "Sun Jul  5 12:00:00 PDT 2026",
		Locale:           "LANG=en_US.UTF-8",
	})
	if err == nil {
		t.Fatalf("expected forbidden env to fail closed")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("policy error leaked secret value: %v", err)
	}
	if !strings.Contains(err.Error(), "SSH_AUTH_SOCK") {
		t.Fatalf("expected env var name in error, got %v", err)
	}
}

func TestValidateInspectionRejectsHostHerdrEnvWithoutLeakingValue(t *testing.T) {
	tests := []struct {
		name     string
		envName  string
		envValue string
	}{
		{name: "enabled flag", envName: "HERDR_ENV", envValue: "1"},
		{name: "host socket", envName: "HERDR_SOCKET_PATH", envValue: "/tmp/host-herdr.sock"},
		{name: "host pane", envName: "HERDR_PANE_ID", envValue: "host-pane"},
		{name: "host tab", envName: "HERDR_TAB_ID", envValue: "host-tab"},
		{name: "host workspace", envName: "HERDR_WORKSPACE_ID", envValue: "host-workspace"},
		{name: "host config", envName: "HERDR_CONFIG_DIR", envValue: "/Users/alice/.config/herdr"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInspection(InspectionPolicy{
				Workspace: WorkspacePolicy{
					Mount:          ".",
					ForbiddenPaths: []string{"~", "~/.ssh"},
					HomeDir:        "/Users/alice",
					WorkingDir:     "/Users/alice/work/safe-claude-sbx",
				},
				Timezone: "America/Chicago",
				Locale:   "en_US.UTF-8",
			}, InspectionObservation{
				Environment: map[string]string{
					tt.envName: tt.envValue,
				},
				WorkingDirectory: "/workspace",
				Mounts:           "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)",
				Date:             "Sun Jul  5 12:00:00 PDT 2026",
				Locale:           "LANG=en_US.UTF-8",
			})
			if err == nil {
				t.Fatalf("expected host Herdr env to fail closed")
			}
			if !strings.Contains(err.Error(), tt.envName) {
				t.Fatalf("expected Herdr env name in diagnostic, got %v", err)
			}
			if strings.Contains(err.Error(), tt.envValue) {
				t.Fatalf("policy error leaked Herdr value: %v", err)
			}
		})
	}
}

func TestValidateInspectionAllowsConfiguredSandboxLocalHerdrRuntimeEnv(t *testing.T) {
	err := ValidateInspection(InspectionPolicy{
		Workspace: WorkspacePolicy{
			Mount:          ".",
			ForbiddenPaths: []string{"~", "~/.ssh"},
			HomeDir:        "/Users/alice",
			WorkingDir:     "/Users/alice/work/safe-claude-sbx",
		},
		Timezone: "America/Chicago",
		Locale:   "en_US.UTF-8",
		HerdrRuntime: HerdrRuntimePolicy{
			Enabled:    true,
			SocketPath: "/home/agent/.config/herdr/herdr.sock",
			PaneID:     "sandbox-claude",
		},
	}, InspectionObservation{
		Environment: map[string]string{
			"HERDR_ENV":         "1",
			"HERDR_SOCKET_PATH": "/home/agent/.config/herdr/herdr.sock",
			"HERDR_PANE_ID":     "sandbox-claude",
		},
		WorkingDirectory: "/workspace",
		Mounts:           "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)",
		Date:             "Sun Jul  5 12:00:00 PDT 2026",
		Locale:           "LANG=en_US.UTF-8",
	})
	if err != nil {
		t.Fatalf("expected sandbox-local Herdr runtime env to be accepted: %v", err)
	}
}

func TestValidateInspectionRejectsUnexpectedHerdrRuntimeEnvWithoutLeakingValue(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		leak     string
		wantName string
	}{
		{
			name: "wrong env flag",
			env: map[string]string{
				"HERDR_ENV":         "true",
				"HERDR_SOCKET_PATH": "/home/agent/.config/herdr/herdr.sock",
				"HERDR_PANE_ID":     "sandbox-claude",
			},
			leak:     "true",
			wantName: "HERDR_ENV",
		},
		{
			name: "host socket path",
			env: map[string]string{
				"HERDR_ENV":         "1",
				"HERDR_SOCKET_PATH": "/tmp/host-herdr.sock",
				"HERDR_PANE_ID":     "sandbox-claude",
			},
			leak:     "/tmp/host-herdr.sock",
			wantName: "HERDR_SOCKET_PATH",
		},
		{
			name: "host pane id",
			env: map[string]string{
				"HERDR_ENV":         "1",
				"HERDR_SOCKET_PATH": "/home/agent/.config/herdr/herdr.sock",
				"HERDR_PANE_ID":     "host-pane",
			},
			leak:     "host-pane",
			wantName: "HERDR_PANE_ID",
		},
		{
			name: "unsupported Herdr var",
			env: map[string]string{
				"HERDR_ENV":          "1",
				"HERDR_SOCKET_PATH":  "/home/agent/.config/herdr/herdr.sock",
				"HERDR_PANE_ID":      "sandbox-claude",
				"HERDR_WORKSPACE_ID": "host-workspace",
			},
			leak:     "host-workspace",
			wantName: "HERDR_WORKSPACE_ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInspection(InspectionPolicy{
				Workspace: WorkspacePolicy{
					Mount:          ".",
					ForbiddenPaths: []string{"~", "~/.ssh"},
					HomeDir:        "/Users/alice",
					WorkingDir:     "/Users/alice/work/safe-claude-sbx",
				},
				Timezone: "America/Chicago",
				Locale:   "en_US.UTF-8",
				HerdrRuntime: HerdrRuntimePolicy{
					Enabled:    true,
					SocketPath: "/home/agent/.config/herdr/herdr.sock",
					PaneID:     "sandbox-claude",
				},
			}, InspectionObservation{
				Environment:      tt.env,
				WorkingDirectory: "/workspace",
				Mounts:           "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)",
				Date:             "Sun Jul  5 12:00:00 PDT 2026",
				Locale:           "LANG=en_US.UTF-8",
			})
			if err == nil {
				t.Fatalf("expected unexpected Herdr runtime env to fail closed")
			}
			if !strings.Contains(err.Error(), tt.wantName) {
				t.Fatalf("expected %s diagnostic, got %v", tt.wantName, err)
			}
			if strings.Contains(err.Error(), tt.leak) {
				t.Fatalf("policy error leaked Herdr value: %v", err)
			}
		})
	}
}

func TestValidateInspectionAllowsSSHAgentForwardingOnlyWhenConfigured(t *testing.T) {
	socket := "/run/host-services/ssh-auth.sock"
	gateway := "gateway.docker.internal"
	observation := InspectionObservation{
		Environment: map[string]string{
			"SSH_AUTH_SOCK":         socket,
			"SSH_AUTH_SOCK_GATEWAY": gateway,
		},
		WorkingDirectory: "/workspace",
		Mounts:           "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)",
		Date:             "Sun Jul  5 12:00:00 PDT 2026",
		Locale:           "LANG=en_US.UTF-8",
	}
	policy := InspectionPolicy{
		Workspace: WorkspacePolicy{
			Mount:          ".",
			ForbiddenPaths: []string{"~", "~/.ssh"},
			HomeDir:        "/Users/alice",
			WorkingDir:     "/Users/alice/work/safe-claude-sbx",
		},
		Timezone: "America/Chicago",
		Locale:   "en_US.UTF-8",
	}

	err := ValidateInspection(policy, observation)
	if err == nil {
		t.Fatalf("expected SSH agent forwarding to fail closed by default")
	}
	if strings.Contains(err.Error(), socket) {
		t.Fatalf("policy error leaked SSH_AUTH_SOCK value: %v", err)
	}
	if !strings.Contains(err.Error(), "SSH_AUTH_SOCK") {
		t.Fatalf("expected SSH_AUTH_SOCK in error, got %v", err)
	}
	if strings.Contains(err.Error(), gateway) {
		t.Fatalf("policy error leaked SSH_AUTH_SOCK_GATEWAY value: %v", err)
	}

	policy.AllowSSHAgentForwarding = true
	if err := ValidateInspection(policy, observation); err != nil {
		t.Fatalf("expected configured SSH agent forwarding to be accepted: %v", err)
	}
}

func TestValidateInspectionRejectsUnexpectedSSHAgentForwardingShapes(t *testing.T) {
	tests := []struct {
		name       string
		envName    string
		envValue   string
		wantReason string
	}{
		{
			name:       "socket is not a path",
			envName:    "SSH_AUTH_SOCK",
			envValue:   "not-a-socket-path",
			wantReason: "ssh agent socket path is not Docker-managed",
		},
		{
			name:       "gateway is not Docker-managed",
			envName:    "SSH_AUTH_SOCK_GATEWAY",
			envValue:   "host.docker.internal",
			wantReason: "ssh agent gateway is not Docker-managed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInspection(InspectionPolicy{
				Workspace: WorkspacePolicy{
					Mount:          ".",
					ForbiddenPaths: []string{"~", "~/.ssh"},
					HomeDir:        "/Users/alice",
					WorkingDir:     "/Users/alice/work/safe-claude-sbx",
				},
				Timezone:                "America/Chicago",
				Locale:                  "en_US.UTF-8",
				AllowSSHAgentForwarding: true,
			}, InspectionObservation{
				Environment: map[string]string{
					tt.envName: tt.envValue,
				},
				WorkingDirectory: "/workspace",
				Mounts:           "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)",
				Date:             "Sun Jul  5 12:00:00 PDT 2026",
				Locale:           "LANG=en_US.UTF-8",
			})
			if err == nil {
				t.Fatalf("expected unexpected SSH forwarding shape to fail closed")
			}
			if !strings.Contains(err.Error(), tt.envName) || !strings.Contains(err.Error(), tt.wantReason) {
				t.Fatalf("expected %s diagnostic with %q, got %v", tt.envName, tt.wantReason, err)
			}
			if strings.Contains(err.Error(), tt.envValue) {
				t.Fatalf("policy error leaked SSH forwarding value: %v", err)
			}
		})
	}
}

func TestValidateInspectionRejectsHostAndUnknownProxyTargets(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "host clash", value: "http://127.0.0.1:7897"},
		{name: "unknown target", value: "http://proxy.example.test:3128"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInspection(InspectionPolicy{
				Workspace: WorkspacePolicy{
					Mount:          ".",
					ForbiddenPaths: []string{"~", "~/.ssh"},
					HomeDir:        "/Users/alice",
					WorkingDir:     "/Users/alice/work/safe-claude-sbx",
				},
				Timezone: "America/Chicago",
				Locale:   "en_US.UTF-8",
			}, InspectionObservation{
				Environment: map[string]string{
					"HTTP_PROXY": tt.value,
				},
				WorkingDirectory: "/workspace",
				Mounts:           "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)",
				Date:             "Sun Jul  5 12:00:00 PDT 2026",
				Locale:           "LANG=en_US.UTF-8",
			})
			if err == nil {
				t.Fatalf("expected proxy target %q to be rejected", tt.value)
			}
			if strings.Contains(err.Error(), tt.value) {
				t.Fatalf("policy error leaked proxy value: %v", err)
			}
			if !strings.Contains(err.Error(), "HTTP_PROXY") {
				t.Fatalf("expected proxy variable name in error, got %v", err)
			}
		})
	}
}

func TestValidateInspectionRejectsSensitiveMountObservation(t *testing.T) {
	err := ValidateInspection(InspectionPolicy{
		Workspace: WorkspacePolicy{
			Mount:          ".",
			ForbiddenPaths: []string{"~", "~/.ssh"},
			HomeDir:        "/Users/alice",
			WorkingDir:     "/Users/alice/work/safe-claude-sbx",
		},
		Timezone: "America/Chicago",
		Locale:   "en_US.UTF-8",
	}, InspectionObservation{
		Environment:      map[string]string{"PATH": "/usr/bin"},
		WorkingDirectory: "/workspace",
		Mounts:           "/dev/disk1 on /workspace type virtiofs\n/dev/disk2 on /host-ssh type virtiofs (rw,source=/Users/alice/.ssh)",
		Date:             "Sun Jul  5 12:00:00 PDT 2026",
		Locale:           "LANG=en_US.UTF-8",
	})
	if err == nil {
		t.Fatalf("expected sensitive mount observation to fail closed")
	}
	if !strings.Contains(err.Error(), "workspace.inspection.mounts") {
		t.Fatalf("expected mount inspection error, got %v", err)
	}
}

func TestValidateInspectionRejectsWorkspaceVisibilityEscapeWithoutFileContents(t *testing.T) {
	err := ValidateInspection(InspectionPolicy{
		Workspace: WorkspacePolicy{
			Mount:          ".",
			ForbiddenPaths: []string{"~", "~/.ssh"},
			HomeDir:        "/Users/alice",
			WorkingDir:     "/Users/alice/work/safe-claude-sbx",
		},
		Timezone: "America/Chicago",
		Locale:   "en_US.UTF-8",
	}, InspectionObservation{
		Environment:      map[string]string{"PATH": "/usr/bin"},
		WorkingDirectory: "/workspace",
		Mounts:           "/dev/disk1 on /workspace type virtiofs (rw,source=/Users/alice/work/safe-claude-sbx)",
		Date:             "Sun Jul  5 12:00:00 PDT 2026",
		Locale:           "LANG=en_US.UTF-8",
		WorkspaceVisibility: WorkspaceVisibilityObservation{
			SiblingPath: "/Users/alice/work/other-project/config.yaml",
		},
	})
	if err == nil {
		t.Fatalf("expected sibling workspace visibility escape to fail closed")
	}
	if !strings.Contains(err.Error(), "workspace.inspection.visibility.sibling") {
		t.Fatalf("expected sibling visibility error, got %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("policy error leaked file contents: %v", err)
	}
}
