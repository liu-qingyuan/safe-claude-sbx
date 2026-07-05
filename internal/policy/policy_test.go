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
		Timezone: "America/Los_Angeles",
		Locale:   "en_US.UTF-8",
	}, InspectionObservation{
		Environment: map[string]string{
			"PATH":        "/usr/bin",
			"TZ":          "America/Los_Angeles",
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
		Timezone: "America/Los_Angeles",
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
		Timezone: "America/Los_Angeles",
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

func TestValidateInspectionAllowsSSHAgentForwardingOnlyWhenConfigured(t *testing.T) {
	socket := "/run/host-services/ssh-auth.sock"
	observation := InspectionObservation{
		Environment: map[string]string{
			"SSH_AUTH_SOCK": socket,
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
		Timezone: "America/Los_Angeles",
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

	policy.AllowSSHAgentForwarding = true
	if err := ValidateInspection(policy, observation); err != nil {
		t.Fatalf("expected configured SSH agent forwarding to be accepted: %v", err)
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
				Timezone: "America/Los_Angeles",
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
		Timezone: "America/Los_Angeles",
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
