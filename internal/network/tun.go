package network

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"gopkg.in/yaml.v3"
)

const defaultClashVergeAppHome = "Library/Application Support/io.github.clash-verge-rev.clash-verge-rev"

type FailureLayer string

const (
	FailureLayerNone            FailureLayer = ""
	FailureLayerConfiguration   FailureLayer = "configuration-declaration"
	FailureLayerSystemRoute     FailureLayer = "system-route"
	FailureLayerSystemInterface FailureLayer = "system-interface"
)

type CommandRunner interface {
	Run(name string, args ...string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(name string, args ...string) (string, error) {
	output, err := exec.Command(name, args...).CombinedOutput()
	return string(output), err
}

type Inspector struct {
	FS     fs.FS
	Runner CommandRunner
}

type TUNPreflightResult struct {
	Ready                 bool
	ClashVergeTUNDeclared bool
	RuntimeTUNEnabled     bool
	RuntimeTUNDevice      string
	DefaultRouteInterface string
	StartupTUNInterface   string
	FailureLayer          FailureLayer
	FailureReason         string
}

func NewInspector() Inspector {
	return Inspector{
		FS:     os.DirFS("/"),
		Runner: ExecRunner{},
	}
}

func (i Inspector) CheckClashVergeTUN(policy config.ClashVerge) (TUNPreflightResult, error) {
	result := TUNPreflightResult{}
	if i.FS == nil {
		i.FS = os.DirFS("/")
	}
	if i.Runner == nil {
		i.Runner = ExecRunner{}
	}

	verge, err := readVergeConfig(i.FS, policy.AppHome)
	if err != nil {
		return result.fail(FailureLayerConfiguration, "read verge.yaml: %v", err)
	}
	if verge.EnableTUNMode == nil || !*verge.EnableTUNMode {
		return result.fail(FailureLayerConfiguration, "verge.yaml enable_tun_mode is not true")
	}
	result.ClashVergeTUNDeclared = true

	runtime, err := readRuntimeConfig(i.FS, policy.AppHome)
	if err != nil {
		return result.fail(FailureLayerConfiguration, "read clash-verge.yaml: %v", err)
	}
	if runtime.TUN.Enable == nil || !*runtime.TUN.Enable {
		return result.fail(FailureLayerConfiguration, "clash-verge.yaml tun.enable is not true")
	}
	result.RuntimeTUNEnabled = true
	result.RuntimeTUNDevice = runtime.TUN.Device

	routeInterface, err := RouteInterface(i.Runner, policy.RouteCheckTarget)
	if err != nil {
		return result.fail(FailureLayerSystemRoute, "%v", err)
	}
	result.DefaultRouteInterface = routeInterface
	if !strings.HasPrefix(routeInterface, policy.TUNInterfacePrefix) {
		return result.fail(FailureLayerSystemRoute, "default route interface %s does not match TUN prefix %s", routeInterface, policy.TUNInterfacePrefix)
	}
	if result.RuntimeTUNDevice != "" && result.RuntimeTUNDevice != routeInterface {
		return result.fail(FailureLayerSystemRoute, "default route interface %s does not match mihomo tun.device %s", routeInterface, result.RuntimeTUNDevice)
	}

	if err := InterfaceExists(i.Runner, routeInterface); err != nil {
		result.StartupTUNInterface = routeInterface
		return result.fail(FailureLayerSystemInterface, "TUN interface %s missing: %v", routeInterface, err)
	}

	result.Ready = true
	result.StartupTUNInterface = routeInterface
	return result, nil
}

func (r TUNPreflightResult) fail(layer FailureLayer, format string, args ...any) (TUNPreflightResult, error) {
	r.Ready = false
	r.FailureLayer = layer
	r.FailureReason = fmt.Sprintf(format, args...)
	return r, fmt.Errorf("%s: %s", layer, r.FailureReason)
}

func RouteInterface(runner CommandRunner, target string) (string, error) {
	output, err := runner.Run("route", "get", target)
	if err != nil {
		return "", fmt.Errorf("route get %s failed: %w", target, err)
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "interface:") {
			iface := strings.TrimSpace(strings.TrimPrefix(line, "interface:"))
			if iface == "" {
				break
			}
			return iface, nil
		}
	}
	return "", fmt.Errorf("route output has no interface")
}

func InterfaceExists(runner CommandRunner, name string) error {
	_, err := runner.Run("ifconfig", name)
	return err
}

type vergeConfig struct {
	EnableTUNMode *bool `yaml:"enable_tun_mode"`
}

type runtimeConfig struct {
	TUN struct {
		Enable              *bool  `yaml:"enable"`
		Device              string `yaml:"device"`
		AutoRoute           *bool  `yaml:"auto-route"`
		AutoDetectInterface *bool  `yaml:"auto-detect-interface"`
		StrictRoute         *bool  `yaml:"strict-route"`
	} `yaml:"tun"`
}

func readVergeConfig(fileSystem fs.FS, appHome string) (vergeConfig, error) {
	var cfg vergeConfig
	err := readYAML(fileSystem, filepath.ToSlash(filepath.Join(appHomePath(appHome), "verge.yaml")), &cfg)
	return cfg, err
}

func readRuntimeConfig(fileSystem fs.FS, appHome string) (runtimeConfig, error) {
	var cfg runtimeConfig
	err := readYAML(fileSystem, filepath.ToSlash(filepath.Join(appHomePath(appHome), "clash-verge.yaml")), &cfg)
	return cfg, err
}

func readYAML(fileSystem fs.FS, path string, target any) error {
	data, err := fs.ReadFile(fileSystem, path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		return err
	}
	return nil
}

func ClashVergeAppHomePath(configured string) string {
	return strings.TrimPrefix(ClashVergeAppHomeHostPath(configured), string(filepath.Separator))
}

func ClashVergeAppHomeHostPath(configured string) string {
	if configured != "" {
		return filepath.Clean(configured)
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return defaultClashVergeAppHome
	}
	return filepath.Join(home, defaultClashVergeAppHome)
}

func appHomePath(configured string) string {
	return ClashVergeAppHomePath(configured)
}
