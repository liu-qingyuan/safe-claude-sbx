package config

import (
	"bytes"
	"fmt"
	"net/netip"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/policy"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Network     Network     `yaml:"network"`
	Sandbox     Sandbox     `yaml:"sandbox"`
	Workspace   Workspace   `yaml:"workspace"`
	Environment Environment `yaml:"environment"`
	Watchdog    Watchdog    `yaml:"watchdog"`
	Cleanup     Cleanup     `yaml:"cleanup"`
}

type Network struct {
	ClashVerge ClashVerge `yaml:"clash_verge"`
	EgressIP   EgressIP   `yaml:"egress_ip"`
}

type ClashVerge struct {
	AppHome            string `yaml:"app_home"`
	RouteCheckTarget   string `yaml:"route_check_target"`
	TUNInterfacePrefix string `yaml:"tun_interface_prefix"`
}

type EgressIP struct {
	ExpectedIP      string `yaml:"expected_ip"`
	HostCheckURL    string `yaml:"host_check_url"`
	SandboxCheckURL string `yaml:"sandbox_check_url"`
	TimeoutSeconds  int    `yaml:"timeout_seconds"`
}

type Sandbox struct {
	Backend     string      `yaml:"backend"`
	MainName    string      `yaml:"main_name"`
	ProbeName   string      `yaml:"probe_name"`
	Agent       string      `yaml:"agent"`
	Template    string      `yaml:"template"`
	Supervision Supervision `yaml:"supervision"`
}

type Supervision struct {
	Mode  string            `yaml:"mode"`
	Herdr *HerdrSupervision `yaml:"herdr"`
}

type HerdrSupervision struct {
	InstallIfMissing *bool  `yaml:"install_if_missing"`
	SocketPath       string `yaml:"socket_path"`
	PaneID           string `yaml:"pane_id"`
}

type Workspace struct {
	Mount          string   `yaml:"mount"`
	UseCloneMode   bool     `yaml:"use_clone_mode"`
	ForbiddenPaths []string `yaml:"forbidden_paths"`
}

type Environment struct {
	Timezone                string   `yaml:"timezone"`
	Locale                  string   `yaml:"locale"`
	AllowSSHAgentForwarding bool     `yaml:"allow_ssh_agent_forwarding"`
	ForbiddenEnvVars        []string `yaml:"forbidden_env_vars"`
}

type Watchdog struct {
	Enabled  bool   `yaml:"enabled"`
	LogLevel string `yaml:"log_level"`
	LogFile  string `yaml:"log_file"`
}

type Cleanup struct {
	StopMainSandbox    bool `yaml:"stop_main_sandbox"`
	RemoveProbeSandbox bool `yaml:"remove_probe_sandbox"`
	RemoveMainSandbox  bool `yaml:"remove_main_sandbox"`
}

func LoadAndValidate(path string) error {
	_, err := Load(path)
	return err
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if err := inspectTopLevelConfig(data); err != nil {
		return Config{}, err
	}

	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse YAML: %w", err)
	}

	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func inspectTopLevelConfig(data []byte) error {
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse YAML: %w", err)
	}

	legacyPaths := map[string]string{
		"expected_egress_ip":           "network.egress_ip.expected_ip",
		"clash_verge_app_home":         "network.clash_verge.app_home",
		"route_check_target":           "network.clash_verge.route_check_target",
		"ip_check_url":                 "network.egress_ip.host_check_url and network.egress_ip.sandbox_check_url",
		"ip_check_timeout_seconds":     "network.egress_ip.timeout_seconds",
		"sandbox_name":                 "sandbox.main_name",
		"probe_sandbox_name":           "sandbox.probe_name",
		"backend":                      "sandbox.backend",
		"agent":                        "sandbox.agent",
		"workspace_mount":              "workspace.mount",
		"use_clone_mode":               "workspace.use_clone_mode",
		"timezone":                     "environment.timezone",
		"locale":                       "environment.locale",
		"require_tun_interface_prefix": "network.clash_verge.tun_interface_prefix",
		"forbidden_env_vars":           "environment.forbidden_env_vars",
		"forbidden_mount_paths":        "workspace.forbidden_paths",
		"log_level":                    "watchdog.log_level",
		"log_file":                     "watchdog.log_file",
	}

	var moves []string
	for key, newPath := range legacyPaths {
		if _, ok := root[key]; ok {
			moves = append(moves, fmt.Sprintf("%s -> %s", key, newPath))
		}
	}
	if cleanup, ok := root["cleanup"].(map[string]any); ok {
		legacyCleanupPaths := map[string]string{
			"stop_on_exit":                "cleanup.stop_main_sandbox",
			"remove_main_sandbox_on_exit": "cleanup.remove_main_sandbox",
		}
		for key, newPath := range legacyCleanupPaths {
			if _, ok := cleanup[key]; ok {
				moves = append(moves, fmt.Sprintf("cleanup.%s -> %s", key, newPath))
			}
		}
	}
	if len(moves) > 0 {
		sort.Strings(moves)
		return fmt.Errorf("legacy flat configuration is not supported; move fields to object paths: %s", strings.Join(moves, "; "))
	}

	for _, objectPath := range []string{"network", "sandbox", "workspace", "environment", "watchdog", "cleanup"} {
		if _, ok := root[objectPath]; !ok {
			return fmt.Errorf("missing required object %s", objectPath)
		}
	}
	return nil
}

func (c Config) Validate() error {
	required := []struct {
		path  string
		value string
	}{
		{"network.clash_verge.route_check_target", c.Network.ClashVerge.RouteCheckTarget},
		{"network.clash_verge.tun_interface_prefix", c.Network.ClashVerge.TUNInterfacePrefix},
		{"network.egress_ip.expected_ip", c.Network.EgressIP.ExpectedIP},
		{"network.egress_ip.host_check_url", c.Network.EgressIP.HostCheckURL},
		{"network.egress_ip.sandbox_check_url", c.Network.EgressIP.SandboxCheckURL},
		{"sandbox.backend", c.Sandbox.Backend},
		{"sandbox.main_name", c.Sandbox.MainName},
		{"sandbox.probe_name", c.Sandbox.ProbeName},
		{"sandbox.agent", c.Sandbox.Agent},
		{"workspace.mount", c.Workspace.Mount},
		{"environment.timezone", c.Environment.Timezone},
		{"environment.locale", c.Environment.Locale},
		{"watchdog.log_level", c.Watchdog.LogLevel},
	}

	for _, field := range required {
		if field.value == "" {
			return fmt.Errorf("missing required field %s", field.path)
		}
	}
	if c.Network.EgressIP.TimeoutSeconds <= 0 {
		return fmt.Errorf("invalid field network.egress_ip.timeout_seconds: must be greater than 0")
	}
	if _, err := netip.ParseAddr(strings.TrimSpace(c.Network.EgressIP.ExpectedIP)); err != nil {
		return fmt.Errorf("invalid field network.egress_ip.expected_ip: must be an IP address")
	}
	if len(c.Workspace.ForbiddenPaths) == 0 {
		return fmt.Errorf("missing required field workspace.forbidden_paths")
	}
	if err := policy.ValidateWorkspaceMount(policy.WorkspacePolicy{
		Mount:          c.Workspace.Mount,
		ForbiddenPaths: c.Workspace.ForbiddenPaths,
	}); err != nil {
		return err
	}
	if len(c.Environment.ForbiddenEnvVars) == 0 {
		return fmt.Errorf("missing required field environment.forbidden_env_vars")
	}
	if err := c.Sandbox.Supervision.Validate(); err != nil {
		return err
	}
	if c.Sandbox.Supervision.Mode == "sandbox-local-herdr" && strings.TrimSpace(c.Sandbox.Template) == "" {
		return fmt.Errorf("missing required field sandbox.template for sandbox-local-herdr")
	}
	return nil
}

func (c *Config) ApplyDefaults() {
	if c.Sandbox.Supervision.Mode == "" {
		c.Sandbox.Supervision.Mode = "direct-claude"
	}
}

func (s Supervision) Validate() error {
	mode := s.Mode
	if mode == "" {
		mode = "direct-claude"
	}
	switch mode {
	case "direct-claude":
		if s.Herdr != nil {
			return s.Herdr.Validate()
		}
		return nil
	case "sandbox-local-herdr":
		if s.Herdr == nil {
			return fmt.Errorf("missing required object sandbox.supervision.herdr")
		}
		return s.Herdr.Validate()
	default:
		return fmt.Errorf("invalid field sandbox.supervision.mode: supported values are direct-claude, sandbox-local-herdr")
	}
}

func (h HerdrSupervision) Validate() error {
	if h.InstallIfMissing == nil {
		return fmt.Errorf("missing required field sandbox.supervision.herdr.install_if_missing")
	}
	if *h.InstallIfMissing {
		return fmt.Errorf("invalid field sandbox.supervision.herdr.install_if_missing: runtime Herdr install is disabled; build and use a Docker Sandbox template with Herdr and cc preinstalled")
	}
	if strings.TrimSpace(h.SocketPath) == "" {
		return fmt.Errorf("missing required field sandbox.supervision.herdr.socket_path")
	}
	cleanSocketPath := path.Clean(strings.TrimSpace(h.SocketPath))
	if !strings.HasPrefix(cleanSocketPath, "/home/agent/") {
		return fmt.Errorf("invalid field sandbox.supervision.herdr.socket_path: must point inside sandbox home")
	}
	if strings.TrimSpace(h.PaneID) == "" {
		return fmt.Errorf("missing required field sandbox.supervision.herdr.pane_id")
	}
	return nil
}
