package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLaunchStartsMainSandboxAfterAllPreflightsPass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, validLaunchConfig(t, server.URL, "203.0.113.10", 10))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommands(t, "utun9", false)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{EgressIP: "203.0.113.10", LogPath: logPath})

	cmd := exec.Command("go", "run", ".", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launch failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "sandbox started: claude-sbx") {
		t.Fatalf("expected launch success output, got:\n%s", output)
	}
	log := readFile(t, logPath)
	if !strings.Contains(log, "run claude --name claude-sbx .") {
		t.Fatalf("expected main sandbox run command, got:\n%s", log)
	}
}

func TestLaunchDoesNotPassHostSensitiveEnvironmentToMainSandboxCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, validLaunchConfig(t, server.URL, "203.0.113.10", 10))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommands(t, "utun9", false)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EgressIP:          "203.0.113.10",
		LogPath:           logPath,
		LogRunEnvironment: true,
	})

	cmd := exec.Command("go", "run", ".", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"),
		"OPENAI_API_KEY=SECRET_SHOULD_NOT_LEAK",
		"SSH_AUTH_SOCK=/tmp/ssh-agent.sock",
		"SSH_AUTH_SOCK_GATEWAY=gateway.docker.internal",
		"HTTP_PROXY=http://127.0.0.1:7897",
		"HERDR_SOCKET_PATH=/tmp/herdr.sock",
		"HERDR_PANE_ID=w1:p1",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launch failed: %v\n%s", err, output)
	}
	log := readFile(t, logPath)
	for _, forbidden := range []string{"OPENAI_API_KEY=", "SSH_AUTH_SOCK=", "SSH_AUTH_SOCK_GATEWAY=", "HTTP_PROXY=", "HERDR_SOCKET_PATH=", "HERDR_PANE_ID="} {
		if strings.Contains(log, forbidden) {
			t.Fatalf("main sandbox command inherited forbidden host env %q:\n%s", forbidden, log)
		}
	}
}

func TestLaunchStartsSandboxLocalHerdrAfterAllPreflightsPass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, withSandboxLocalHerdr(validLaunchConfig(t, server.URL, "203.0.113.10", 10)))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommands(t, "utun9", false)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EgressIP:          "203.0.113.10",
		LogPath:           logPath,
		LogRunEnvironment: true,
	})

	cmd := exec.Command("go", "run", ".", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HERDR_ENV=1",
		"HERDR_SOCKET_PATH=/tmp/host-herdr.sock",
		"HERDR_PANE_ID=host-pane",
		"HERDR_WORKSPACE_ID=host-workspace",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launch failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "sandbox inspection ok") || !strings.Contains(string(output), "sandbox started: claude-sbx") {
		t.Fatalf("expected launch success after existing safety checks, got:\n%s", output)
	}
	log := readFile(t, logPath)
	for _, want := range []string{
		"create --name claude-sbx claude .",
		"exec claude-sbx command -v herdr",
		"exec claude-sbx herdr --version",
		"exec claude-sbx herdr integration install claude",
		"exec claude-sbx herdr server",
		"exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-claude claude-sbx claude",
	} {
		if !containsLogLine(log, want) {
			t.Fatalf("expected Herdr startup command %q, got:\n%s", want, log)
		}
	}
	for _, forbidden := range []string{"/tmp/host-herdr.sock", "host-pane", "host-workspace"} {
		if strings.Contains(log, forbidden) {
			t.Fatalf("Herdr startup leaked host Herdr value %q:\n%s", forbidden, log)
		}
	}
}

func TestLaunchFailsClosedAndCleansMainSandboxWhenHerdrSetupFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, withSandboxLocalHerdr(validLaunchConfig(t, server.URL, "203.0.113.10", 10)))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommands(t, "utun9", false)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EgressIP:      "203.0.113.10",
		LogPath:       logPath,
		FailHerdrHook: true,
	})

	cmd := exec.Command("go", "run", ".", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("launch unexpectedly succeeded after Herdr hook failure:\n%s", output)
	}
	if !strings.Contains(string(output), "install Claude Herdr integration") {
		t.Fatalf("expected Herdr hook failure in output, got:\n%s", output)
	}
	log := readFile(t, logPath)
	if !containsLogLine(log, "stop claude-sbx") {
		t.Fatalf("expected main sandbox cleanup after Herdr setup failure, got:\n%s", log)
	}
}

func TestLaunchWatchdogStopsMainSandboxWhenRouteEventChangesDefaultRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, validLaunchConfig(t, server.URL, "203.0.113.10", 10))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommandsWithRuntime(t, "utun9", "en0", false, true)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{EgressIP: "203.0.113.10", LogPath: logPath, BlockRun: true})

	cmd := exec.Command("go", "run", ".", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("launch unexpectedly succeeded after route change:\n%s", output)
	}
	if !strings.Contains(string(output), "route-monitor runtime check failed") {
		t.Fatalf("expected event source in watchdog output, got:\n%s", output)
	}
	if !strings.Contains(string(output), "default route changed from startup interface utun9 to en0") {
		t.Fatalf("expected route change reason, got:\n%s", output)
	}
	log := readFile(t, logPath)
	if !containsLogLine(log, "stop claude-sbx") {
		t.Fatalf("expected watchdog cleanup to stop main sandbox, got:\n%s", log)
	}
}

func TestLaunchFailsClosedBeforeMainSandbox(t *testing.T) {
	tests := []struct {
		name          string
		routeIface    string
		hostEgress    string
		fakeSBX       fakeSBXOptions
		configMutator func(string) string
		wantError     string
		wantCleanup   bool
		wantStopMain  bool
	}{
		{
			name:       "TUN route missing",
			routeIface: "en0",
			hostEgress: "203.0.113.10",
			fakeSBX:    fakeSBXOptions{EgressIP: "203.0.113.10"},
			wantError:  "system-route",
		},
		{
			name:       "host egress mismatch",
			routeIface: "utun9",
			hostEgress: "198.51.100.77",
			fakeSBX:    fakeSBXOptions{EgressIP: "203.0.113.10"},
			wantError:  "host-egress-mismatch",
		},
		{
			name:       "backend incompatible",
			routeIface: "utun9",
			hostEgress: "203.0.113.10",
			fakeSBX: fakeSBXOptions{
				EgressIP:      "203.0.113.10",
				VersionOutput: "unexpected tool",
			},
			wantError: "version-incompatible",
		},
		{
			name:        "sandbox egress mismatch",
			routeIface:  "utun9",
			hostEgress:  "203.0.113.10",
			fakeSBX:     fakeSBXOptions{EgressIP: "198.51.100.77"},
			wantError:   "sandbox-egress-mismatch",
			wantCleanup: true,
		},
		{
			name:       "environment inspection failure",
			routeIface: "utun9",
			hostEgress: "203.0.113.10",
			fakeSBX: fakeSBXOptions{
				EgressIP:  "203.0.113.10",
				EnvOutput: "PATH=/usr/bin\nSSH_AUTH_SOCK=/Users/alice/.ssh/agent.sock\n",
			},
			wantError:   "environment.inspection.env.SSH_AUTH_SOCK",
			wantCleanup: true,
		},
		{
			name:       "host proxy visible in sandbox",
			routeIface: "utun9",
			hostEgress: "203.0.113.10",
			fakeSBX: fakeSBXOptions{
				EgressIP:  "203.0.113.10",
				EnvOutput: "PATH=/usr/bin\nHTTP_PROXY=http://127.0.0.1:7897\n",
			},
			wantError:   "environment.inspection.env.HTTP_PROXY",
			wantCleanup: true,
		},
		{
			name:       "forbidden workspace mount",
			routeIface: "utun9",
			hostEgress: "203.0.113.10",
			fakeSBX:    fakeSBXOptions{EgressIP: "203.0.113.10"},
			configMutator: func(body string) string {
				return strings.Replace(body, `mount: "."`, `mount: "~"`, 1)
			},
			wantError: "workspace.mount",
		},
		{
			name:         "main sandbox start failure",
			routeIface:   "utun9",
			hostEgress:   "203.0.113.10",
			fakeSBX:      fakeSBXOptions{EgressIP: "203.0.113.10", FailRun: true},
			wantError:    "start main sandbox",
			wantCleanup:  true,
			wantStopMain: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintln(w, tt.hostEgress)
			}))
			t.Cleanup(server.Close)

			configBody := validLaunchConfig(t, server.URL, "203.0.113.10", 10)
			if tt.configMutator != nil {
				configBody = tt.configMutator(configBody)
			}
			configPath := writeTestConfig(t, configBody)
			logPath := filepath.Join(t.TempDir(), "sbx.log")
			tt.fakeSBX.LogPath = logPath
			fakeBin := writeFakeSystemCommands(t, tt.routeIface, false)
			fakeSBX := writeFakeSBX(t, tt.fakeSBX)

			cmd := exec.Command("go", "run", ".", "--config", configPath)
			cmd.Dir = "."
			cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("launch unexpectedly succeeded:\n%s", output)
			}
			if !strings.Contains(string(output), tt.wantError) {
				t.Fatalf("expected %q in output, got:\n%s", tt.wantError, output)
			}
			log := readOptionalFile(t, logPath)
			if !tt.wantStopMain && (strings.Contains(log, "\nrun ") || strings.HasPrefix(log, "run ")) {
				t.Fatalf("main sandbox started despite preflight failure:\n%s", log)
			}
			if tt.wantCleanup && !strings.Contains(log, "rm --force claude-sbx-probe") {
				t.Fatalf("expected probe cleanup after startup failure, got:\n%s", log)
			}
			if tt.wantStopMain && !containsLogLine(log, "stop claude-sbx") {
				t.Fatalf("expected main sandbox cleanup after start failure, got:\n%s", log)
			}
		})
	}
}

func TestDoctorAcceptsValidStructuredConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, validConfig(server.URL, "203.0.113.10", 10))
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{EgressIP: "203.0.113.10"})

	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "configuration ok") {
		t.Fatalf("expected config success output, got:\n%s", output)
	}
	if !strings.Contains(string(output), "host egress ok: observed IP 203.0.113.10") {
		t.Fatalf("expected host egress observed IP, got:\n%s", output)
	}
	if !strings.Contains(string(output), "sandbox backend ok: sbx version: v0.34.0") {
		t.Fatalf("expected backend availability output, got:\n%s", output)
	}
	if !strings.Contains(string(output), "sandbox egress ok: observed IP 203.0.113.10") {
		t.Fatalf("expected sandbox egress output, got:\n%s", output)
	}
	if !strings.Contains(string(output), "sandbox inspection ok") {
		t.Fatalf("expected sandbox inspection output, got:\n%s", output)
	}
	if strings.Contains(string(output), "SECRET_SHOULD_NOT_LEAK") {
		t.Fatalf("doctor leaked raw sandbox env:\n%s", output)
	}
}

func TestDoctorAcceptsSandboxLocalHerdrSupervisionConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configBody := strings.Replace(
		validConfig(server.URL, "203.0.113.10", 10),
		`agent: "claude"`,
		`agent: "claude"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: true
      socket_path: "/home/agent/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"`,
		1,
	)
	configPath := writeTestConfig(t, configBody)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{EgressIP: "203.0.113.10"})

	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor rejected sandbox-local Herdr supervision config: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "configuration ok") {
		t.Fatalf("expected config success output, got:\n%s", output)
	}
}

func TestDoctorAcceptsSandboxLocalHerdrInspectionEnv(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, withSandboxLocalHerdr(validConfig(server.URL, "203.0.113.10", 10)))
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EgressIP:  "203.0.113.10",
		EnvOutput: "PATH=/usr/bin\nHERDR_ENV=1\nHERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock\nHERDR_PANE_ID=sandbox-claude\n",
	})

	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor rejected sandbox-local Herdr inspection env: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "sandbox inspection ok") {
		t.Fatalf("expected sandbox inspection success, got:\n%s", output)
	}
}

func TestDoctorAcceptsExplicitDirectClaudeSupervisionConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configBody := strings.Replace(
		validConfig(server.URL, "203.0.113.10", 10),
		`agent: "claude"`,
		`agent: "claude"
  supervision:
    mode: "direct-claude"`,
		1,
	)
	configPath := writeTestConfig(t, configBody)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{EgressIP: "203.0.113.10"})

	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor rejected direct Claude supervision config: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "configuration ok") {
		t.Fatalf("expected config success output, got:\n%s", output)
	}
}

func TestDoctorRejectsInvalidSupervisionConfig(t *testing.T) {
	tests := []struct {
		name        string
		supervision string
		wantError   string
		notLeak     string
	}{
		{
			name: "invalid mode",
			supervision: `  supervision:
    mode: "host-herdr"`,
			wantError: "sandbox.supervision.mode",
		},
		{
			name: "missing Herdr object",
			supervision: `  supervision:
    mode: "sandbox-local-herdr"`,
			wantError: "sandbox.supervision.herdr",
		},
		{
			name: "missing install behavior",
			supervision: `  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      socket_path: "/home/agent/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"`,
			wantError: "sandbox.supervision.herdr.install_if_missing",
		},
		{
			name: "host-looking socket path",
			supervision: `  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: true
      socket_path: "/Users/alice/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"`,
			wantError: "sandbox.supervision.herdr.socket_path",
			notLeak:   "/Users/alice/.config/herdr/herdr.sock",
		},
		{
			name: "direct mode with host-looking Herdr socket path",
			supervision: `  supervision:
    mode: "direct-claude"
    herdr:
      install_if_missing: true
      socket_path: "/Users/alice/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"`,
			wantError: "sandbox.supervision.herdr.socket_path",
			notLeak:   "/Users/alice/.config/herdr/herdr.sock",
		},
		{
			name: "traversing Herdr socket path",
			supervision: `  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: true
      socket_path: "/home/agent/../../Users/alice/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"`,
			wantError: "sandbox.supervision.herdr.socket_path",
			notLeak:   "/home/agent/../../Users/alice/.config/herdr/herdr.sock",
		},
		{
			name: "tmp Herdr socket path",
			supervision: `  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: true
      socket_path: "/tmp/herdr.sock"
      pane_id: "sandbox-claude"`,
			wantError: "sandbox.supervision.herdr.socket_path",
			notLeak:   "/tmp/herdr.sock",
		},
		{
			name: "empty pane id",
			supervision: `  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: true
      socket_path: "/home/agent/.config/herdr/herdr.sock"
      pane_id: "   "`,
			wantError: "sandbox.supervision.herdr.pane_id",
			notLeak:   "pane_id: \"   \"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintln(w, "203.0.113.10")
			}))
			t.Cleanup(server.Close)

			configBody := strings.Replace(
				validConfig(server.URL, "203.0.113.10", 10),
				`agent: "claude"`,
				"agent: \"claude\"\n"+tt.supervision,
				1,
			)
			configPath := writeTestConfig(t, configBody)

			cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
			cmd.Dir = "."

			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("doctor unexpectedly accepted invalid supervision config:\n%s", output)
			}
			if !strings.Contains(string(output), tt.wantError) {
				t.Fatalf("expected %q in output, got:\n%s", tt.wantError, output)
			}
			if tt.notLeak != "" && strings.Contains(string(output), tt.notLeak) {
				t.Fatalf("doctor leaked raw supervision value:\n%s", output)
			}
		})
	}
}

func TestDoctorRejectsHostHerdrEnvironmentConfigWithoutLeakingValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	hostSocket := "/Users/alice/.config/herdr/herdr.sock"
	configPath := writeTestConfig(t, validConfig(server.URL, "203.0.113.10", 10)+`
HERDR_SOCKET_PATH: "`+hostSocket+`"
`)

	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("doctor unexpectedly accepted host HERDR_* config:\n%s", output)
	}
	if !strings.Contains(string(output), "HERDR_SOCKET_PATH") {
		t.Fatalf("expected HERDR_SOCKET_PATH diagnostic, got:\n%s", output)
	}
	if strings.Contains(string(output), hostSocket) {
		t.Fatalf("doctor leaked host Herdr socket path:\n%s", output)
	}
}

func TestDoctorAcceptsDockerManagedCredentialPlaceholders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, validConfig(server.URL, "203.0.113.10", 10))
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EnvOutput: "PATH=/usr/bin\nOPENAI_API_KEY=proxy-managed\nANTHROPIC_API_KEY=proxy-managed\nHTTP_PROXY=http://gateway.docker.internal:3128\nNO_PROXY=localhost,127.0.0.1,gateway.docker.internal\n",
	})

	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "sandbox inspection ok") {
		t.Fatalf("expected sandbox inspection success, got:\n%s", output)
	}
	if strings.Contains(string(output), "proxy-managed") {
		t.Fatalf("doctor leaked raw sandbox env:\n%s", output)
	}
}

func TestDoctorFailsClosedForSandboxBackendProblems(t *testing.T) {
	tests := []struct {
		name      string
		fake      fakeSBXOptions
		wantError string
	}{
		{
			name:      "sandbox egress mismatch",
			fake:      fakeSBXOptions{EgressIP: "198.51.100.77"},
			wantError: "sandbox-egress-mismatch",
		},
		{
			name:      "probe command failure",
			fake:      fakeSBXOptions{EgressIP: "203.0.113.10", FailCurl: true},
			wantError: "probe command failed",
		},
		{
			name:      "version command incompatible",
			fake:      fakeSBXOptions{EgressIP: "203.0.113.10", VersionOutput: "unexpected tool"},
			wantError: "version-incompatible",
		},
		{
			name:      "probe create failure",
			fake:      fakeSBXOptions{EgressIP: "203.0.113.10", FailCreate: true},
			wantError: "create probe sandbox",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintln(w, "203.0.113.10")
			}))
			t.Cleanup(server.Close)

			configPath := writeTestConfig(t, validConfig(server.URL, "203.0.113.10", 10))
			fakeSBX := writeFakeSBX(t, tt.fake)

			cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
			cmd.Dir = "."
			cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("doctor unexpectedly succeeded:\n%s", output)
			}
			if !strings.Contains(string(output), tt.wantError) {
				t.Fatalf("expected %q in output, got:\n%s", tt.wantError, output)
			}
			if strings.Contains(string(output), "SECRET_SHOULD_NOT_LEAK") {
				t.Fatalf("doctor leaked raw sandbox env:\n%s", output)
			}
		})
	}
}

func TestDoctorFailsClosedForUnsafeSandboxInspection(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("read home dir: %v", err)
	}
	tests := []struct {
		name      string
		fake      fakeSBXOptions
		wantError string
		notLeak   string
	}{
		{
			name: "host proxy",
			fake: fakeSBXOptions{
				EnvOutput: "PATH=/usr/bin\nHTTP_PROXY=http://127.0.0.1:7897\n",
			},
			wantError: "environment.inspection.env.HTTP_PROXY",
			notLeak:   "127.0.0.1:7897",
		},
		{
			name: "sensitive env",
			fake: fakeSBXOptions{
				EnvOutput: "PATH=/usr/bin\nSSH_AUTH_SOCK=/Users/alice/.ssh/agent.sock\n",
			},
			wantError: "environment.inspection.env.SSH_AUTH_SOCK",
			notLeak:   "/Users/alice/.ssh/agent.sock",
		},
		{
			name: "OpenAI API key",
			fake: fakeSBXOptions{
				EnvOutput: "PATH=/usr/bin\nOPENAI_API_KEY=SECRET_SHOULD_NOT_LEAK\n",
			},
			wantError: "environment.inspection.env.OPENAI_API_KEY",
			notLeak:   "SECRET_SHOULD_NOT_LEAK",
		},
		{
			name: "host Herdr env",
			fake: fakeSBXOptions{
				EnvOutput: "PATH=/usr/bin\nHERDR_SOCKET_PATH=/tmp/host-herdr.sock\n",
			},
			wantError: "environment.inspection.env.HERDR_SOCKET_PATH",
			notLeak:   "/tmp/host-herdr.sock",
		},
		{
			name: "sensitive mount",
			fake: fakeSBXOptions{
				MountOutput: fmt.Sprintf("/dev/disk1 on /workspace type virtiofs\n/dev/disk2 on /host-ssh type virtiofs (rw,source=%s/.ssh)\n", home),
			},
			wantError: "workspace.inspection.mounts",
			notLeak:   home + "/.ssh",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintln(w, "203.0.113.10")
			}))
			t.Cleanup(server.Close)

			configPath := writeTestConfig(t, validConfig(server.URL, "203.0.113.10", 10))
			fakeSBX := writeFakeSBX(t, tt.fake)

			cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
			cmd.Dir = "."
			cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("doctor unexpectedly succeeded:\n%s", output)
			}
			if !strings.Contains(string(output), tt.wantError) {
				t.Fatalf("expected %q in output, got:\n%s", tt.wantError, output)
			}
			if strings.Contains(string(output), tt.notLeak) {
				t.Fatalf("doctor leaked sensitive value:\n%s", output)
			}
		})
	}
}

func TestDoctorAllowsSSHAgentForwardingOnlyWhenConfigured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	socket := "/run/host-services/ssh-auth.sock"
	gateway := "gateway.docker.internal"
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EnvOutput: "PATH=/usr/bin\nSSH_AUTH_SOCK=" + socket + "\nSSH_AUTH_SOCK_GATEWAY=" + gateway + "\nHTTP_PROXY=http://gateway.docker.internal:3128\nNO_PROXY=localhost,127.0.0.1,gateway.docker.internal\n",
	})

	defaultConfig := writeTestConfig(t, validConfig(server.URL, "203.0.113.10", 10))
	cmd := exec.Command("go", "run", ".", "doctor", "--config", defaultConfig)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("doctor unexpectedly accepted SSH agent forwarding by default:\n%s", output)
	}
	if !strings.Contains(string(output), "environment.inspection.env.SSH_AUTH_SOCK") {
		t.Fatalf("expected SSH_AUTH_SOCK diagnostic, got:\n%s", output)
	}
	if strings.Contains(string(output), socket) {
		t.Fatalf("doctor leaked SSH_AUTH_SOCK value:\n%s", output)
	}
	if strings.Contains(string(output), gateway) {
		t.Fatalf("doctor leaked SSH_AUTH_SOCK_GATEWAY value:\n%s", output)
	}

	allowedConfig := writeTestConfig(t, strings.Replace(
		validConfig(server.URL, "203.0.113.10", 10),
		`locale: "en_US.UTF-8"`,
		"locale: \"en_US.UTF-8\"\n  allow_ssh_agent_forwarding: true",
		1,
	))
	cmd = exec.Command("go", "run", ".", "doctor", "--config", allowedConfig)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor rejected configured SSH agent forwarding: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "sandbox inspection ok") {
		t.Fatalf("expected sandbox inspection success, got:\n%s", output)
	}
	if strings.Contains(string(output), socket) {
		t.Fatalf("doctor leaked SSH_AUTH_SOCK value:\n%s", output)
	}
	if strings.Contains(string(output), gateway) {
		t.Fatalf("doctor leaked SSH_AUTH_SOCK_GATEWAY value:\n%s", output)
	}
}

func TestDoctorFailsClosedForHostEgressProblems(t *testing.T) {
	tests := []struct {
		name        string
		serverBody  string
		handler     http.HandlerFunc
		closeServer bool
		wantError   string
	}{
		{
			name:       "mismatch",
			serverBody: "198.51.100.77\n",
			wantError:  "host-egress-mismatch",
		},
		{
			name:       "empty response",
			serverBody: "\n",
			wantError:  "response-parse-failure",
		},
		{
			name:       "non IP response",
			serverBody: "not an ip\n",
			wantError:  "response-parse-failure",
		},
		{
			name: "timeout",
			handler: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(1500 * time.Millisecond)
				fmt.Fprintln(w, "203.0.113.10")
			},
			wantError: "endpoint-failure",
		},
		{
			name:        "network error",
			serverBody:  "203.0.113.10\n",
			closeServer: true,
			wantError:   "endpoint-failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := tt.handler
			if handler == nil {
				handler = func(w http.ResponseWriter, r *http.Request) {
					fmt.Fprint(w, tt.serverBody)
				}
			}
			server := httptest.NewServer(handler)
			if tt.closeServer {
				server.Close()
			} else {
				t.Cleanup(server.Close)
			}

			configPath := writeTestConfig(t, validConfig(server.URL, "203.0.113.10", 1))
			cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
			cmd.Dir = "."

			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("doctor unexpectedly succeeded:\n%s", output)
			}
			if !strings.Contains(string(output), tt.wantError) {
				t.Fatalf("expected %q in output, got:\n%s", tt.wantError, output)
			}
		})
	}
}

func validConfig(hostCheckURL, expectedIP string, timeoutSeconds int) string {
	return fmt.Sprintf(`
network:
  clash_verge:
    route_check_target: "1.1.1.1"
    tun_interface_prefix: "utun"
  egress_ip:
    expected_ip: %q
    host_check_url: %q
    sandbox_check_url: "https://api.ipify.org"
    timeout_seconds: %d
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
`, expectedIP, hostCheckURL, timeoutSeconds)
}

func validLaunchConfig(t *testing.T, hostCheckURL, expectedIP string, timeoutSeconds int) string {
	t.Helper()

	appHome := writeClashVergeHome(t, true, true, "utun9")
	return fmt.Sprintf(`
network:
  clash_verge:
    app_home: %q
    route_check_target: "1.1.1.1"
    tun_interface_prefix: "utun"
  egress_ip:
    expected_ip: %q
    host_check_url: %q
    sandbox_check_url: "https://api.ipify.org"
    timeout_seconds: %d
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
`, appHome, expectedIP, hostCheckURL, timeoutSeconds)
}

func withSandboxLocalHerdr(body string) string {
	return strings.Replace(
		body,
		`agent: "claude"`,
		`agent: "claude"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: true
      socket_path: "/home/agent/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"`,
		1,
	)
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
  timezone: "America/Chicago"
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
  timezone: "America/Chicago"
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
  timezone: "America/Chicago"
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

type fakeSBXOptions struct {
	EgressIP          string
	VersionOutput     string
	EnvOutput         string
	MountOutput       string
	LogPath           string
	LogRunEnvironment bool
	FailCreate        bool
	FailCurl          bool
	FailRun           bool
	FailHerdrHook     bool
	FailHerdrServer   bool
	BlockRun          bool
}

func writeFakeSBX(t *testing.T, opts fakeSBXOptions) string {
	t.Helper()

	dir := t.TempDir()
	version := opts.VersionOutput
	if version == "" {
		version = "sbx version: v0.34.0 fake"
	}
	egressIP := opts.EgressIP
	if egressIP == "" {
		egressIP = "203.0.113.10"
	}
	envOutput := opts.EnvOutput
	if envOutput == "" {
		envOutput = "PATH=/usr/bin\nTZ=America/Chicago\nLANG=en_US.UTF-8\nLC_ALL=C.UTF-8\nHTTP_PROXY=http://gateway.docker.internal:3128\nHTTPS_PROXY=http://gateway.docker.internal:3128\nNO_PROXY=localhost,127.0.0.1,gateway.docker.internal\n"
	}
	mountOutput := opts.MountOutput
	if mountOutput == "" {
		mountOutput = "/dev/disk1 on /workspace type virtiofs\n"
	}
	logSnippet := ":"
	if opts.LogPath != "" {
		logSnippet = fmt.Sprintf("printf '%%s\\n' \"$*\" >> %q", opts.LogPath)
	}
	logEnvironmentSnippet := ":"
	if opts.LogPath != "" && opts.LogRunEnvironment {
		logEnvironmentSnippet = fmt.Sprintf("env | grep -E '^(OPENAI_API_KEY|SSH_AUTH_SOCK|SSH_AUTH_SOCK_GATEWAY|HTTP_PROXY|HTTPS_PROXY|ALL_PROXY|NO_PROXY|http_proxy|https_proxy|all_proxy|no_proxy|HERDR_ENV|HERDR_PANE_ID|HERDR_SOCKET_PATH|HERDR_TAB_ID|HERDR_WORKSPACE_ID)=' >> %q || true", opts.LogPath)
	}
	stopFile := filepath.Join(dir, "main-stopped")
	herdrStopFile := filepath.Join(dir, "herdr-stopped")

	script := fmt.Sprintf(`#!/bin/sh
set -eu
%s
%s
case "$1" in
  version)
    printf '%%s\n' %q
    ;;
  ls)
    printf 'No sandboxes found.\n'
    ;;
  create)
    if [ %q = "true" ]; then
      printf 'create failed\n' >&2
      exit 1
    fi
    printf 'probe created\n'
    ;;
  exec)
    case "$*" in
      *" curl "*)
        if [ %q = "true" ]; then
          printf 'curl failed\n' >&2
          exit 1
        fi
        printf '%%s\n' %q
        ;;
      *" env")
        printf '%%b' %q
        ;;
      *" pwd")
        printf '/workspace\n'
        ;;
      *" mount")
        printf '%%b' %q
        ;;
      *" date")
        printf 'Sun Jul  5 12:00:00 UTC 2026\n'
        ;;
      *" locale")
        printf 'LANG=en_US.UTF-8\nLC_ALL=C.UTF-8\n'
        ;;
      *" command -v herdr")
        printf '/home/agent/.local/bin/herdr\n'
        ;;
      *" herdr --version")
        printf 'herdr 0.7.1\n'
        ;;
      *" herdr integration install claude")
        if [ %q = "true" ]; then
          printf 'hook install failed\n' >&2
          exit 1
        fi
        printf 'installed\n'
        ;;
      *" herdr server stop")
        touch %q
        printf 'server stopped\n'
        ;;
      *" herdr server")
        if [ %q = "true" ]; then
          printf 'server failed\n' >&2
          exit 1
        fi
        printf 'server started\n'
        while [ ! -f %q ]; do
          sleep 0.05
        done
        ;;
      *"HERDR_ENV=1"*" claude")
        if [ %q = "true" ]; then
          printf 'run failed\n' >&2
          exit 1
        fi
        printf 'main sandbox started\n'
        if [ %q = "true" ]; then
          while [ ! -f %q ]; do
            sleep 0.05
          done
        fi
        ;;
      *)
        printf 'unknown exec: %%s\n' "$*" >&2
        exit 1
        ;;
    esac
    ;;
  run)
    if [ %q = "true" ]; then
      printf 'run failed\n' >&2
      exit 1
    fi
    printf 'main sandbox started\n'
    if [ %q = "true" ]; then
      while [ ! -f %q ]; do
        sleep 0.05
      done
    fi
    ;;
  stop)
    if [ %q = "true" ] && [ "${2:-}" = "claude-sbx" ]; then
      touch %q
      printf 'sandbox stopped\n'
      exit 0
    fi
    printf 'sandbox not found\n' >&2
    exit 1
    ;;
  rm)
    printf 'sandbox not found\n' >&2
    exit 1
    ;;
  *)
    printf 'unknown command: %%s\n' "$*" >&2
    exit 1
    ;;
esac
`, logSnippet,
		logEnvironmentSnippet,
		version,
		shellBool(opts.FailCreate),
		shellBool(opts.FailCurl),
		egressIP,
		envOutput,
		mountOutput,
		shellBool(opts.FailHerdrHook),
		herdrStopFile,
		shellBool(opts.FailHerdrServer),
		herdrStopFile,
		shellBool(opts.FailRun),
		shellBool(opts.BlockRun),
		stopFile,
		shellBool(opts.FailRun),
		shellBool(opts.BlockRun),
		stopFile,
		shellBool(opts.BlockRun),
		stopFile)

	path := filepath.Join(dir, "sbx")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake sbx: %v", err)
	}
	return dir
}

func writeFakeSystemCommands(t *testing.T, routeInterface string, missingInterface bool) string {
	return writeFakeSystemCommandsWithRuntime(t, routeInterface, routeInterface, missingInterface, false)
}

func writeFakeSystemCommandsWithRuntime(t *testing.T, startupInterface, runtimeInterface string, runtimeMissingInterface, emitMonitorEvent bool) string {
	t.Helper()

	dir := t.TempDir()
	routeState := filepath.Join(dir, "route-checked")
	ifconfigState := filepath.Join(dir, "ifconfig-checked")
	writeExecutable(t, filepath.Join(dir, "route"), fmt.Sprintf(`#!/bin/sh
set -eu
if [ "$1" = "get" ]; then
  iface=%q
  if [ -f %q ]; then
    iface=%q
  else
    touch %q
  fi
  printf '   route to: %%s\n' "$2"
  printf 'interface: %%s\n' "$iface"
  exit 0
fi
if [ "$1" = "-n" ] && [ "${2:-}" = "monitor" ]; then
  if [ %q = "true" ]; then
    sleep 0.2
    printf 'RTM_CHANGE route changed\n'
  fi
  while true; do
    sleep 1
  done
fi
printf 'unknown route command\n' >&2
exit 1
`, startupInterface, routeState, runtimeInterface, routeState, shellBool(emitMonitorEvent)))
	missingRuntime := "0"
	if runtimeMissingInterface {
		missingRuntime = "1"
	}
	writeExecutable(t, filepath.Join(dir, "ifconfig"), fmt.Sprintf(`#!/bin/sh
set -eu
missing=0
if [ -f %q ]; then
  missing=%q
else
  touch %q
fi
if [ "$missing" = "1" ]; then
  printf 'interface missing\n' >&2
  exit 1
fi
printf '%%s: flags=8051<UP,POINTOPOINT,RUNNING>\n' "$1"
`, ifconfigState, missingRuntime, ifconfigState))
	return dir
}

func writeClashVergeHome(t *testing.T, vergeTUN, runtimeTUN bool, device string) string {
	t.Helper()

	home := t.TempDir()
	writeFile(t, filepath.Join(home, "verge.yaml"), "enable_tun_mode: "+shellBool(vergeTUN)+"\n")
	writeFile(t, filepath.Join(home, "clash-verge.yaml"), strings.TrimSpace(`
tun:
  enable: `+shellBool(runtimeTUN)+`
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

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func readOptionalFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func containsLogLine(log, line string) bool {
	for _, got := range strings.Split(log, "\n") {
		if got == line {
			return true
		}
	}
	return false
}

func shellBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
