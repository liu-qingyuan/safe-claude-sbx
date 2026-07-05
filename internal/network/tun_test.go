package network

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
)

func TestClashVergeTUNPreflightAcceptsEnabledTUNRoute(t *testing.T) {
	home := writeClashVergeHome(t, true, true, "utun9")
	runner := fakeRunner{
		outputs: map[string]string{
			"route get 1.1.1.1": routeGetOutput("utun9"),
			"ifconfig utun9":    "utun9: flags=8051<UP,POINTOPOINT,RUNNING>\n",
		},
	}

	result, err := Inspector{FS: os.DirFS(home), Runner: runner}.CheckClashVergeTUN(clashPolicy("."))

	if err != nil {
		t.Fatalf("preflight failed: %v", err)
	}
	if !result.Ready {
		t.Fatalf("expected ready result: %#v", result)
	}
	if result.StartupTUNInterface != "utun9" {
		t.Fatalf("expected startup TUN interface utun9, got %q", result.StartupTUNInterface)
	}
}

func TestClashVergeTUNPreflightFailsClosedByLayer(t *testing.T) {
	tests := []struct {
		name          string
		vergeTUN      bool
		runtimeTUN    bool
		routeOutput   string
		ifconfigError error
		wantLayer     FailureLayer
		wantMessage   string
	}{
		{
			name:        "clash verge declaration disabled",
			vergeTUN:    false,
			runtimeTUN:  true,
			routeOutput: routeGetOutput("utun9"),
			wantLayer:   FailureLayerConfiguration,
			wantMessage: "verge.yaml enable_tun_mode is not true",
		},
		{
			name:        "mihomo runtime TUN disabled",
			vergeTUN:    true,
			runtimeTUN:  false,
			routeOutput: routeGetOutput("utun9"),
			wantLayer:   FailureLayerConfiguration,
			wantMessage: "clash-verge.yaml tun.enable is not true",
		},
		{
			name:        "default route uses non TUN interface",
			vergeTUN:    true,
			runtimeTUN:  true,
			routeOutput: routeGetOutput("en0"),
			wantLayer:   FailureLayerSystemRoute,
			wantMessage: "default route interface en0 does not match TUN prefix utun",
		},
		{
			name:          "TUN interface missing",
			vergeTUN:      true,
			runtimeTUN:    true,
			routeOutput:   routeGetOutput("utun9"),
			ifconfigError: errors.New("interface not found"),
			wantLayer:     FailureLayerSystemInterface,
			wantMessage:   "TUN interface utun9 missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := writeClashVergeHome(t, tt.vergeTUN, tt.runtimeTUN, "utun9")
			runner := fakeRunner{
				outputs: map[string]string{
					"route get 1.1.1.1": tt.routeOutput,
					"ifconfig utun9":    "utun9: flags=8051<UP,POINTOPOINT,RUNNING>\n",
				},
				errors: map[string]error{
					"ifconfig utun9": tt.ifconfigError,
				},
			}

			result, err := Inspector{FS: os.DirFS(home), Runner: runner}.CheckClashVergeTUN(clashPolicy("."))

			if err == nil {
				t.Fatalf("preflight unexpectedly succeeded: %#v", result)
			}
			if result.Ready {
				t.Fatalf("expected fail-closed result: %#v", result)
			}
			if result.FailureLayer != tt.wantLayer {
				t.Fatalf("expected failure layer %q, got %q", tt.wantLayer, result.FailureLayer)
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("expected error containing %q, got %q", tt.wantMessage, err.Error())
			}
		})
	}
}

func TestClashVergeTUNPreflightFailsClosedWhenConfigMissing(t *testing.T) {
	home := t.TempDir()
	runner := fakeRunner{
		outputs: map[string]string{
			"route get 1.1.1.1": routeGetOutput("utun4"),
			"ifconfig utun4":    "utun4: flags=8051<UP,POINTOPOINT,RUNNING>\n",
		},
	}

	result, err := Inspector{FS: os.DirFS(home), Runner: runner}.CheckClashVergeTUN(clashPolicy("."))

	if err == nil {
		t.Fatalf("preflight unexpectedly succeeded: %#v", result)
	}
	if result.FailureLayer != FailureLayerConfiguration {
		t.Fatalf("expected configuration failure, got %q", result.FailureLayer)
	}
	if !strings.Contains(err.Error(), "read verge.yaml") {
		t.Fatalf("expected missing verge.yaml error, got %q", err.Error())
	}
}

func TestRouteGetInterfaceParsing(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		err         error
		want        string
		wantErrText string
	}{
		{
			name:   "normal output",
			output: routeGetOutput("utun9"),
			want:   "utun9",
		},
		{
			name:   "non TUN interface",
			output: routeGetOutput("en0"),
			want:   "en0",
		},
		{
			name:        "no interface",
			output:      "route to: 1.1.1.1\n",
			wantErrText: "route output has no interface",
		},
		{
			name:        "command failed",
			err:         errors.New("route failed"),
			wantErrText: "route get 1.1.1.1 failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := fakeRunner{
				outputs: map[string]string{"route get 1.1.1.1": tt.output},
				errors:  map[string]error{"route get 1.1.1.1": tt.err},
			}

			got, err := RouteInterface(runner, "1.1.1.1")

			if tt.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrText, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("route parse failed: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func writeClashVergeHome(t *testing.T, vergeTUN, runtimeTUN bool, device string) string {
	t.Helper()

	home := t.TempDir()
	writeFile(t, filepath.Join(home, "verge.yaml"), boolYAML("enable_tun_mode", vergeTUN))
	writeFile(t, filepath.Join(home, "clash-verge.yaml"), strings.TrimSpace(`
tun:
  enable: `+boolString(runtimeTUN)+`
  device: "`+device+`"
  auto-route: true
  auto-detect-interface: true
  strict-route: true
`)+"\n")
	return home
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func boolYAML(key string, value bool) string {
	return key + ": " + boolString(value) + "\n"
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func routeGetOutput(iface string) string {
	return "route to: 1.1.1.1\n" +
		"destination: default\n" +
		"gateway: 10.0.0.1\n" +
		"interface: " + iface + "\n"
}

func clashPolicy(appHome string) config.ClashVerge {
	return config.ClashVerge{
		AppHome:            appHome,
		RouteCheckTarget:   "1.1.1.1",
		TUNInterfacePrefix: "utun",
	}
}

type fakeRunner struct {
	outputs map[string]string
	errors  map[string]error
}

func (r fakeRunner) Run(name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	if err := r.errors[key]; err != nil {
		return r.outputs[key], err
	}
	return r.outputs[key], nil
}
