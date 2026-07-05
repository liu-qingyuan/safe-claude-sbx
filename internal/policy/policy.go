package policy

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type WorkspacePolicy struct {
	Mount          string
	ForbiddenPaths []string
	HomeDir        string
	WorkingDir     string
}

type InspectionPolicy struct {
	Workspace               WorkspacePolicy
	Timezone                string
	Locale                  string
	AllowSSHAgentForwarding bool
	ForbiddenEnvVars        []string
}

type InspectionObservation struct {
	Environment      map[string]string
	WorkingDirectory string
	Mounts           string
	Date             string
	Locale           string
}

func ValidateWorkspaceMount(policy WorkspacePolicy) error {
	mount, err := normalizePath(policy.Mount, policy)
	if err != nil {
		return fmt.Errorf("workspace.mount: %w", err)
	}
	if mount == "" {
		return fmt.Errorf("workspace.mount: path is required")
	}

	for _, forbiddenPath := range allForbiddenPaths(policy) {
		forbidden, err := normalizePath(forbiddenPath, policy)
		if err != nil || forbidden == "" {
			continue
		}
		if forbiddenPathIsHome(forbiddenPath) && mount == forbidden {
			return fmt.Errorf("workspace.mount: path is forbidden by workspace policy")
		}
		if !forbiddenPathIsHome(forbiddenPath) && sameOrChild(mount, forbidden) {
			return fmt.Errorf("workspace.mount: path is forbidden by workspace policy")
		}
	}
	return nil
}

func ValidateInspection(policy InspectionPolicy, observation InspectionObservation) error {
	if err := ValidateWorkspaceMount(policy.Workspace); err != nil {
		return err
	}
	if strings.TrimSpace(observation.WorkingDirectory) == "" {
		return fmt.Errorf("workspace.inspection.pwd: missing sandbox working directory observation")
	}
	if strings.TrimSpace(observation.Mounts) == "" {
		return fmt.Errorf("workspace.inspection.mounts: missing sandbox mount observation")
	}
	if err := validateMountObservation(policy.Workspace, observation.Mounts); err != nil {
		return err
	}
	if err := validateEnvironment(policy, observation.Environment); err != nil {
		return err
	}
	if err := validateRuntimeObservation(policy, observation); err != nil {
		return err
	}
	return nil
}

func validateMountObservation(policy WorkspacePolicy, mounts string) error {
	forbidden := allForbiddenPaths(policy)
	for _, forbiddenPath := range forbidden {
		normalized, err := normalizePath(forbiddenPath, policy)
		if err != nil || normalized == "" {
			continue
		}
		if forbiddenPathIsHome(forbiddenPath) {
			continue
		}
		if observationMentionsPath(mounts, normalized) {
			return fmt.Errorf("workspace.inspection.mounts: sensitive host path visible")
		}
	}
	return nil
}

func validateEnvironment(policy InspectionPolicy, env map[string]string) error {
	for name, value := range env {
		if isProxyEnv(name) {
			if err := validateProxyEnv(name, value); err != nil {
				return err
			}
			continue
		}
		if isSSHAuthSockEnv(name) {
			if err := validateSSHAgentEnv(policy, name, value); err != nil {
				return err
			}
			continue
		}
		if isForbiddenEnvName(name, policy.ForbiddenEnvVars) {
			if isDockerManagedCredentialPlaceholder(name, value) {
				continue
			}
			if isCredentialEnvName(name) {
				return fmt.Errorf("environment.inspection.env.%s: raw credential value visible", name)
			}
			return fmt.Errorf("environment.inspection.env.%s: forbidden host environment variable visible", name)
		}
		if valueMentionsSensitivePath(value, policy.Workspace) {
			return fmt.Errorf("environment.inspection.env.%s: sensitive host path visible", name)
		}
	}
	return nil
}

func validateSSHAgentEnv(policy InspectionPolicy, name, value string) error {
	if !policy.AllowSSHAgentForwarding {
		return fmt.Errorf("environment.inspection.env.%s: ssh agent forwarding is not allowed", name)
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("environment.inspection.env.%s: ssh agent socket path missing", name)
	}
	return nil
}

func validateProxyEnv(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if isNoProxyEnv(name) {
		if dockerManagedNoProxy(trimmed) {
			return nil
		}
		return fmt.Errorf("environment.inspection.env.%s: unknown proxy bypass policy", name)
	}
	host, port, ok := proxyTarget(trimmed)
	if !ok || !strings.EqualFold(host, "gateway.docker.internal") || port != "3128" {
		return fmt.Errorf("environment.inspection.env.%s: proxy target is not Docker-managed", name)
	}
	return nil
}

func validateRuntimeObservation(policy InspectionPolicy, observation InspectionObservation) error {
	timezone := strings.TrimSpace(policy.Timezone)
	if timezone != "" {
		if got := strings.TrimSpace(observation.Environment["TZ"]); got != "" && got != timezone {
			return fmt.Errorf("environment.inspection.timezone: sandbox TZ does not match configured timezone")
		}
		if strings.TrimSpace(observation.Date) == "" {
			return fmt.Errorf("environment.inspection.timezone: missing sandbox date observation")
		}
	}

	locale := strings.TrimSpace(policy.Locale)
	if locale != "" {
		if got := strings.TrimSpace(observation.Environment["LANG"]); got != "" && got != locale {
			return fmt.Errorf("environment.inspection.locale: sandbox LANG does not match configured locale")
		}
		if !strings.Contains(observation.Locale, "LANG="+locale) && !strings.Contains(observation.Locale, "LC_ALL="+locale) && !strings.Contains(observation.Locale, "LC_ALL=C.UTF-8") {
			return fmt.Errorf("environment.inspection.locale: sandbox locale observation does not match configured locale")
		}
	}
	return nil
}

func isForbiddenEnvName(name string, configured []string) bool {
	if slices.Contains(configured, name) {
		return true
	}
	upper := strings.ToUpper(name)
	if slices.Contains(defaultForbiddenEnvNames(), upper) {
		return true
	}
	sensitiveFragments := []string{"TOKEN", "SECRET", "PASSWORD", "CREDENTIAL", "API_KEY", "AUTH_SOCK", "KEYCHAIN"}
	for _, fragment := range sensitiveFragments {
		if strings.Contains(upper, fragment) {
			return true
		}
	}
	return false
}

func isCredentialEnvName(name string) bool {
	upper := strings.ToUpper(name)
	credentialFragments := []string{"TOKEN", "SECRET", "PASSWORD", "CREDENTIAL", "API_KEY"}
	for _, fragment := range credentialFragments {
		if strings.Contains(upper, fragment) {
			return true
		}
	}
	return false
}

func isDockerManagedCredentialPlaceholder(name, value string) bool {
	return isCredentialEnvName(name) && strings.TrimSpace(value) == "proxy-managed"
}

func isSSHAuthSockEnv(name string) bool {
	return strings.EqualFold(name, "SSH_AUTH_SOCK")
}

func isProxyEnv(name string) bool {
	switch strings.ToUpper(name) {
	case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
		return true
	default:
		return false
	}
}

func isNoProxyEnv(name string) bool {
	return strings.EqualFold(name, "NO_PROXY")
}

func proxyTarget(raw string) (string, string, bool) {
	candidate := raw
	if !strings.Contains(candidate, "://") {
		candidate = "http://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil {
		return "", "", false
	}
	host := parsed.Hostname()
	port := parsed.Port()
	if host == "" {
		return "", "", false
	}
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return strings.Trim(host, "[]"), port, true
}

func dockerManagedNoProxy(raw string) bool {
	for _, entry := range strings.Split(raw, ",") {
		item := strings.TrimSpace(entry)
		if item == "" {
			continue
		}
		host := item
		if strings.Contains(host, "://") {
			parsed, err := url.Parse(host)
			if err != nil {
				return false
			}
			host = parsed.Hostname()
		}
		if strings.Contains(host, ":") {
			splitHost, _, err := net.SplitHostPort(host)
			if err == nil {
				host = splitHost
			}
		}
		host = strings.Trim(strings.TrimSpace(host), "[]")
		if host == "*" || strings.EqualFold(host, "localhost") || strings.EqualFold(host, "gateway.docker.internal") {
			continue
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			continue
		}
		return false
	}
	return true
}

func valueMentionsSensitivePath(value string, policy WorkspacePolicy) bool {
	for _, forbiddenPath := range allForbiddenPaths(policy) {
		if forbiddenPathIsHome(forbiddenPath) {
			continue
		}
		normalized, err := normalizePath(forbiddenPath, policy)
		if err != nil || normalized == "" {
			continue
		}
		if observationMentionsPath(value, normalized) {
			return true
		}
	}
	return false
}

func observationMentionsPath(text, path string) bool {
	cleanPath := filepath.Clean(path)
	return strings.Contains(text, cleanPath) || strings.Contains(text, cleanPath+string(filepath.Separator))
}

func allForbiddenPaths(policy WorkspacePolicy) []string {
	paths := append([]string{}, policy.ForbiddenPaths...)
	paths = append(paths, defaultForbiddenPaths()...)
	return paths
}

func defaultForbiddenPaths() []string {
	return []string{
		"~",
		"~/.ssh",
		"~/.claude",
		"~/.config/clash",
		"~/Library/Application Support/io.github.clash-verge-rev.clash-verge-rev",
		"~/Library/Keychains",
		"/Library/Keychains",
	}
}

func defaultForbiddenEnvNames() []string {
	return []string{
		"SSH_AUTH_SOCK",
		"SSH_AGENT_PID",
		"ANTHROPIC_API_KEY",
		"CLAUDE_API_KEY",
		"CLAUDE_CONFIG_DIR",
		"CLAUDE_CONFIG_PATH",
		"CLASH_HOME",
		"CLASH_CONFIG",
	}
}

func forbiddenPathIsHome(path string) bool {
	return strings.TrimSpace(path) == "~"
}

func normalizePath(path string, policy WorkspacePolicy) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	home := policy.HomeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	workingDir := policy.WorkingDir
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	if trimmed == "~" {
		trimmed = home
	} else if strings.HasPrefix(trimmed, "~/") {
		trimmed = filepath.Join(home, strings.TrimPrefix(trimmed, "~/"))
	}
	if !filepath.IsAbs(trimmed) {
		trimmed = filepath.Join(workingDir, trimmed)
	}
	return filepath.Clean(trimmed), nil
}

func sameOrChild(path, parent string) bool {
	if path == parent {
		return true
	}
	rel, err := filepath.Rel(parent, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
