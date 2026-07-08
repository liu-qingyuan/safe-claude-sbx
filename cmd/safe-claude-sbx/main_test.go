package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
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
	createIndex := strings.Index(log, "create --name claude-sbx claude .")
	visibilityIndex := strings.Index(log, "exec claude-sbx sh -lc workspace=")
	runIndex := strings.Index(log, "run --name claude-sbx")
	if createIndex < 0 || visibilityIndex < 0 || runIndex < 0 || !(createIndex < visibilityIndex && visibilityIndex < runIndex) {
		t.Fatalf("expected create, main visibility preparation, then attach, got:\n%s", log)
	}
}

func TestLaunchDoesNotMutateParentGuidanceBeforeAgentAttach(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, validLaunchConfig(t, server.URL, "203.0.113.10", 10))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommands(t, "utun9", false)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EgressIP: "203.0.113.10",
		LogPath:  logPath,
	})

	cmd := exec.Command("go", "run", ".", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launch failed:\n%v\n%s\nsbx log:\n%s", err, output, readFile(t, logPath))
	}
	log := readFile(t, logPath)
	createIndex := strings.Index(log, "create --name claude-sbx claude .")
	visibilityIndex := strings.Index(log, "exec claude-sbx sh -lc workspace=")
	runIndex := strings.Index(log, "run --name claude-sbx")
	if createIndex < 0 || visibilityIndex < 0 || runIndex < 0 || !(createIndex < visibilityIndex && visibilityIndex < runIndex) {
		t.Fatalf("expected create, visibility check, then attach, got:\n%s", log)
	}
	assertNoParentGuidanceMutation(t, log)
}

func TestDoctorDoesNotMutateParentGuidanceDuringVisibilityPreflight(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, validLaunchConfig(t, server.URL, "203.0.113.10", 10))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommands(t, "utun9", false)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EgressIP: "203.0.113.10",
		LogPath:  logPath,
	})

	cmd := exec.Command("go", "run", ".", "doctor", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor failed:\n%v\n%s\nsbx log:\n%s", err, output, readFile(t, logPath))
	}
	assertNoParentGuidanceMutation(t, readFile(t, logPath))
}

func TestLaunchFailsClosedWhenMainSandboxCanReadSiblingProjectFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, validLaunchConfig(t, server.URL, "203.0.113.10", 10))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommands(t, "utun9", false)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EgressIP:             "203.0.113.10",
		LogPath:              logPath,
		MainVisibilityOutput: "sibling-readable=/Users/alice/work/other-project/config.yaml\n",
	})

	cmd := exec.Command("go", "run", ".", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("launch unexpectedly succeeded with escaping main sandbox visibility:\n%s\nsbx log:\n%s", output, readFile(t, logPath))
	}
	if !strings.Contains(string(output), "main sandbox inspection invalid") || !strings.Contains(string(output), "workspace.inspection.visibility.sibling") {
		t.Fatalf("expected main sandbox visibility diagnostic, got:\n%s", output)
	}
	if strings.Contains(string(output), "TOP_SECRET_SIBLING_CONFIG") {
		t.Fatalf("launch leaked sibling file contents:\n%s", output)
	}
	log := readFile(t, logPath)
	createIndex := strings.Index(log, "create --name claude-sbx claude .")
	visibilityIndex := strings.Index(log, "exec claude-sbx sh -lc workspace=")
	stopIndex := strings.LastIndex(log, "\nstop claude-sbx\n")
	if createIndex < 0 || visibilityIndex < 0 || stopIndex < 0 || !(createIndex < visibilityIndex && visibilityIndex < stopIndex) {
		t.Fatalf("expected main sandbox create, visibility inspection, then cleanup stop, got:\n%s", log)
	}
	if containsLogLine(log, "run --name claude-sbx") {
		t.Fatalf("agent should not attach after unsafe main visibility, got:\n%s", log)
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
		"create --name claude-sbx --template safe-claude-sbx-herdr:latest claude .",
		"exec claude-sbx sh -lc command -v herdr",
		"exec claude-sbx herdr --version",
		"exec claude-sbx herdr integration install claude",
		"exec claude-sbx sh -lc command -v cc",
		"exec claude-sbx cc --version",
		"exec claude-sbx herdr server",
		"exec claude-sbx herdr status server --json",
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
	assertNoParentGuidanceMutation(t, log)
}

func TestSafeHerdrStartsSandboxLocalTUIAfterAllPreflightsPass(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, withSandboxLocalHerdr(validLaunchConfig(t, server.URL, "203.0.113.10", 10)))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommands(t, "utun9", false)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EgressIP:           "203.0.113.10",
		LogPath:            logPath,
		LogRunEnvironment:  true,
		ExistingMainStatus: "running",
	})

	cmd := exec.Command("go", "run", "../safe-herdr", "--config", configPath)
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
		t.Fatalf("safe-herdr failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "sandbox inspection ok") || !strings.Contains(string(output), "Herdr TUI started: claude-sbx") {
		t.Fatalf("expected safe-herdr success after safety checks, got:\n%s", output)
	}
	log := readFile(t, logPath)
	assertLogLineOrder(t, log, []string{
		"create --name claude-sbx-probe shell .",
		"exec claude-sbx-probe curl -fsS https://api.ipify.org",
		"rm --force claude-sbx-probe",
		"ls",
		"exec claude-sbx sh -lc command -v herdr",
		"exec claude-sbx herdr --version",
		"exec claude-sbx herdr integration install claude",
		"exec claude-sbx sh -lc command -v cc",
		"exec claude-sbx cc --version",
		"exec -it claude-sbx herdr",
	})
	for _, forbidden := range []string{
		"create --name claude-sbx claude .",
		"curl -fsSL https://herdr.dev/install.sh | sh",
	} {
		if containsLogLine(log, forbidden) {
			t.Fatalf("safe-herdr should not create or download for existing main sandbox, got forbidden command %q:\n%s", forbidden, log)
		}
	}
	for _, forbidden := range []string{"/tmp/host-herdr.sock", "host-pane", "host-workspace"} {
		if strings.Contains(log, forbidden) {
			t.Fatalf("safe-herdr leaked host Herdr value %q:\n%s", forbidden, log)
		}
	}
	assertNoParentGuidanceMutation(t, log)
}

func TestSafeHerdrCreatesTemplateSandboxWhenMainSandboxIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, withSandboxLocalHerdr(validLaunchConfig(t, server.URL, "203.0.113.10", 10)))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommands(t, "utun9", false)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EgressIP: "203.0.113.10",
		LogPath:  logPath,
	})

	cmd := exec.Command("go", "run", "../safe-herdr", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("safe-herdr failed creating template sandbox: %v\n%s\nsbx log:\n%s", err, output, readOptionalFile(t, logPath))
	}
	if !strings.Contains(string(output), "Herdr TUI started: claude-sbx") {
		t.Fatalf("expected Herdr TUI success, got:\n%s", output)
	}
	log := readFile(t, logPath)
	assertLogLineOrder(t, log, []string{
		"create --name claude-sbx --template safe-claude-sbx-herdr:latest claude .",
		"exec claude-sbx sh -lc command -v herdr",
		"exec claude-sbx herdr --version",
		"exec claude-sbx herdr integration install claude",
		"exec claude-sbx sh -lc command -v cc",
		"exec claude-sbx cc --version",
		"exec -it claude-sbx herdr",
	})
	if !strings.Contains(log, "exec claude-sbx sh -lc workspace=") {
		t.Fatalf("expected main workspace visibility check before Herdr TUI attach, got:\n%s", log)
	}
	if strings.Contains(log, "curl -fsSL https://herdr.dev/install.sh") {
		t.Fatalf("safe-herdr must not download Herdr at startup, got:\n%s", log)
	}
}

func TestSafeHerdrChecksMainWorkspaceVisibilityBeforeTUIAttach(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, withSandboxLocalHerdr(validLaunchConfig(t, server.URL, "203.0.113.10", 10)))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommands(t, "utun9", false)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EgressIP:             "203.0.113.10",
		LogPath:              logPath,
		ExistingMainStatus:   "running",
		MainVisibilityOutput: "sibling-readable=/Users/alice/work/other-project/config.yaml\n",
	})

	cmd := exec.Command("go", "run", "../safe-herdr", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("safe-herdr unexpectedly attached TUI with unsafe main sandbox visibility:\n%s", output)
	}
	if !strings.Contains(string(output), "main sandbox inspection invalid") || !strings.Contains(string(output), "workspace.inspection.visibility.sibling") {
		t.Fatalf("expected main sandbox visibility diagnostic, got:\n%s", output)
	}
	log := readFile(t, logPath)
	if containsLogLine(log, "exec -it claude-sbx herdr") {
		t.Fatalf("Herdr TUI attached before main visibility gate failed:\n%s", log)
	}
	herdrCheckIndex := strings.Index(log, "exec claude-sbx sh -lc command -v herdr")
	visibilityIndex := strings.Index(log, "exec claude-sbx sh -lc workspace=")
	if herdrCheckIndex < 0 || visibilityIndex < 0 || herdrCheckIndex > visibilityIndex {
		t.Fatalf("expected existing Herdr check before main visibility gate, got:\n%s", log)
	}
}

func TestSafeHerdrRequiresExistingSandboxLocalHerdr(t *testing.T) {
	tests := []struct {
		name      string
		fakeSBX   fakeSBXOptions
		wantError string
	}{
		{
			name: "workspace mismatch",
			fakeSBX: fakeSBXOptions{
				ExistingMainStatus:    "running",
				ExistingMainWorkspace: "/other/workspace",
			},
			wantError: "workspace mismatch",
		},
		{
			name: "Herdr unavailable",
			fakeSBX: fakeSBXOptions{
				ExistingMainStatus: "running",
				MissingHerdr:       true,
			},
			wantError: "sandbox-local Herdr unavailable",
		},
		{
			name: "attach failure",
			fakeSBX: fakeSBXOptions{
				ExistingMainStatus: "running",
				FailHerdrTUI:       true,
			},
			wantError: "start sandbox-local Herdr TUI",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintln(w, "203.0.113.10")
			}))
			t.Cleanup(server.Close)

			configPath := writeTestConfig(t, withSandboxLocalHerdr(validLaunchConfig(t, server.URL, "203.0.113.10", 10)))
			logPath := filepath.Join(t.TempDir(), "sbx.log")
			fakeBin := writeFakeSystemCommands(t, "utun9", false)
			tt.fakeSBX.EgressIP = "203.0.113.10"
			tt.fakeSBX.LogPath = logPath
			fakeSBX := writeFakeSBX(t, tt.fakeSBX)

			cmd := exec.Command("go", "run", "../safe-herdr", "--config", configPath)
			cmd.Dir = "."
			cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("safe-herdr unexpectedly succeeded:\n%s", output)
			}
			if !strings.Contains(string(output), tt.wantError) {
				t.Fatalf("expected %q in output, got:\n%s", tt.wantError, output)
			}
			log := readFile(t, logPath)
			for _, forbidden := range []string{
				"create --name claude-sbx claude .",
				"curl -fsSL https://herdr.dev/install.sh | sh",
				"exec claude-sbx herdr server stop",
				"stop claude-sbx",
				"rm --force claude-sbx",
			} {
				if containsLogLine(log, forbidden) {
					t.Fatalf("safe-herdr should not prepare or clean existing main sandbox, got forbidden command %q:\n%s", forbidden, log)
				}
			}
		})
	}
}

func TestSafeHerdrWatchdogStopsSandboxWhenRouteEventChangesDefaultRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, withSandboxLocalHerdr(validLaunchConfig(t, server.URL, "203.0.113.10", 10)))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommandsWithRuntime(t, "utun9", "en0", false, true)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EgressIP:           "203.0.113.10",
		LogPath:            logPath,
		BlockRun:           true,
		ExistingMainStatus: "running",
	})

	cmd := exec.Command("go", "run", "../safe-herdr", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("safe-herdr unexpectedly succeeded after route change:\n%s", output)
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
	if containsLogLine(log, "exec claude-sbx curl -fsS https://api.ipify.org") {
		t.Fatalf("runtime route event should not run sandbox egress curl, got:\n%s", log)
	}
}

func TestSafeHerdrFailsClosedBeforeTUI(t *testing.T) {
	tests := []struct {
		name        string
		routeIface  string
		hostEgress  string
		fakeSBX     fakeSBXOptions
		wantError   string
		wantCleanup bool
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
			name:       "sandbox inspection failure",
			routeIface: "utun9",
			hostEgress: "203.0.113.10",
			fakeSBX: fakeSBXOptions{
				EgressIP:  "203.0.113.10",
				EnvOutput: "PATH=/usr/bin\nHERDR_SOCKET_PATH=/tmp/host-herdr.sock\n",
			},
			wantError:   "environment.inspection.env.HERDR_SOCKET_PATH",
			wantCleanup: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintln(w, tt.hostEgress)
			}))
			t.Cleanup(server.Close)

			configPath := writeTestConfig(t, withSandboxLocalHerdr(validLaunchConfig(t, server.URL, "203.0.113.10", 10)))
			logPath := filepath.Join(t.TempDir(), "sbx.log")
			tt.fakeSBX.LogPath = logPath
			fakeBin := writeFakeSystemCommands(t, tt.routeIface, false)
			fakeSBX := writeFakeSBX(t, tt.fakeSBX)

			cmd := exec.Command("go", "run", "../safe-herdr", "--config", configPath)
			cmd.Dir = "."
			cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("safe-herdr unexpectedly succeeded:\n%s", output)
			}
			if !strings.Contains(string(output), tt.wantError) {
				t.Fatalf("expected %q in output, got:\n%s", tt.wantError, output)
			}
			log := readOptionalFile(t, logPath)
			if containsLogLine(log, "exec -it claude-sbx herdr") {
				t.Fatalf("Herdr TUI started despite preflight failure:\n%s", log)
			}
			if tt.wantCleanup && !strings.Contains(log, "rm --force claude-sbx-probe") {
				t.Fatalf("expected probe cleanup after startup failure, got:\n%s", log)
			}
		})
	}
}

func TestLaunchRebuildsStoppedSandboxLocalHerdrMainAfterPreflights(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, withSandboxLocalHerdr(validLaunchConfig(t, server.URL, "203.0.113.10", 10)))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommands(t, "utun9", false)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EgressIP:           "203.0.113.10",
		LogPath:            logPath,
		ExistingMainStatus: "stopped",
	})

	cmd := exec.Command("go", "run", ".", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launch failed with stopped existing main sandbox: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "sandbox inspection ok") || !strings.Contains(string(output), "sandbox started: claude-sbx") {
		t.Fatalf("expected launch success after preflight and probe, got:\n%s", output)
	}
	log := readFile(t, logPath)
	assertLogLineOrder(t, log, []string{
		"create --name claude-sbx-probe shell .",
		"exec claude-sbx-probe curl -fsS https://api.ipify.org",
		"rm --force claude-sbx-probe",
		"ls",
		"stop claude-sbx",
		"rm --force claude-sbx",
		"create --name claude-sbx --template safe-claude-sbx-herdr:latest claude .",
		"exec claude-sbx sh -lc command -v herdr",
		"exec claude-sbx herdr --version",
		"exec claude-sbx herdr integration install claude",
		"exec claude-sbx sh -lc command -v cc",
		"exec claude-sbx cc --version",
		"exec claude-sbx herdr server",
		"exec claude-sbx herdr status server --json",
	})
}

func TestLaunchRejectsRunningSandboxLocalHerdrMainWithoutStoppingIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	configPath := writeTestConfig(t, withSandboxLocalHerdr(validLaunchConfig(t, server.URL, "203.0.113.10", 10)))
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommands(t, "utun9", false)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{
		EgressIP:           "203.0.113.10",
		LogPath:            logPath,
		ExistingMainStatus: "running",
	})

	cmd := exec.Command("go", "run", ".", "--config", configPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("launch unexpectedly succeeded with running existing main sandbox:\n%s", output)
	}
	if !strings.Contains(string(output), `unsafe status "running"`) || strings.Contains(string(output), "/work/project") {
		t.Fatalf("expected fail-closed non-sensitive diagnostic, got:\n%s", output)
	}
	log := readFile(t, logPath)
	for _, forbidden := range []string{
		"exec claude-sbx herdr server stop",
		"stop claude-sbx",
		"rm --force claude-sbx",
	} {
		if containsLogLine(log, forbidden) {
			t.Fatalf("running existing main sandbox should not be cleaned up by startup failure, got:\n%s", log)
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
	if containsLogLine(log, "exec claude-sbx curl -fsS https://api.ipify.org") {
		t.Fatalf("runtime route event should not run sandbox egress curl, got:\n%s", log)
	}
}

func TestLaunchWatchdogStopsMainSandboxWhenClashAppHomeMetadataChangesHostEgress(t *testing.T) {
	var runtimeMismatch atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if runtimeMismatch.Load() {
			fmt.Fprintln(w, "198.51.100.77")
			return
		}
		fmt.Fprintln(w, "203.0.113.10")
	}))
	t.Cleanup(server.Close)

	appHome := writeClashVergeHome(t, true, true, "utun9")
	configBody := strings.Replace(
		validConfig(server.URL, "203.0.113.10", 10),
		"  clash_verge:\n",
		fmt.Sprintf("  clash_verge:\n    app_home: %q\n", appHome),
		1,
	)
	configPath := writeTestConfig(t, configBody)
	logPath := filepath.Join(t.TempDir(), "sbx.log")
	fakeBin := writeFakeSystemCommands(t, "utun9", false)
	fakeSBX := writeFakeSBX(t, fakeSBXOptions{EgressIP: "203.0.113.10", LogPath: logPath, BlockRun: true})

	cmd := exec.Command("go", "run", ".", "--config", configPath)
	cmd.Dir = "."
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+fakeSBX+string(os.PathListSeparator)+os.Getenv("PATH"))
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if err := cmd.Start(); err != nil {
		t.Fatalf("start launch: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			select {
			case <-done:
			case <-time.After(time.Second):
			}
		}
	})

	waitForLogLine(t, logPath, "run --name claude-sbx")
	time.Sleep(300 * time.Millisecond)
	runtimeMismatch.Store(true)
	writeFile(t, filepath.Join(appHome, "clash-verge.yaml"), strings.TrimSpace(`
tun:
  enable: true
  device: utun9
profile: redacted-fixture
`))

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("launch unexpectedly succeeded after Clash app-home metadata change:\n%s", output.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("launch did not stop after Clash app-home metadata change:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "clash-app-home runtime check failed") {
		t.Fatalf("expected Clash app-home event source in watchdog output, got:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "host egress drift") {
		t.Fatalf("expected host egress drift, got:\n%s", output.String())
	}
	for _, forbidden := range []string{"sandbox egress invalid", "sandbox egress mismatch", "sandbox-egress"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("runtime Clash event should not report sandbox egress failure %q:\n%s", forbidden, output.String())
		}
	}
	log := readFile(t, logPath)
	if !containsLogLine(log, "stop claude-sbx") {
		t.Fatalf("expected watchdog cleanup to stop main sandbox, got:\n%s", log)
	}
	if containsLogLine(log, "exec claude-sbx curl -fsS https://api.ipify.org") {
		t.Fatalf("runtime Clash event should not run sandbox egress curl, got:\n%s", log)
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
  template: "safe-claude-sbx-herdr:latest"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: false
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
      install_if_missing: false
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
      install_if_missing: false
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
      install_if_missing: false
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
      install_if_missing: false
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
      install_if_missing: false
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
		{
			name: "sibling project file readable",
			fake: fakeSBXOptions{
				VisibilityOutput: "sibling-readable=/Users/alice/work/other-project/config.yaml\n",
			},
			wantError: "workspace.inspection.visibility.sibling",
			notLeak:   "TOP_SECRET_SIBLING_CONFIG",
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
  template: "safe-claude-sbx-herdr:latest"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: false
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
	EgressIP              string
	VersionOutput         string
	EnvOutput             string
	MountOutput           string
	VisibilityOutput      string
	MainVisibilityOutput  string
	LogPath               string
	LogRunEnvironment     bool
	FailCreate            bool
	FailCurl              bool
	FailRun               bool
	FailHerdrHook         bool
	FailHerdrServer       bool
	FailHerdrTUI          bool
	MissingHerdr          bool
	MissingCC             bool
	BlockRun              bool
	ExistingMainStatus    string
	ExistingMainWorkspace string
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
	visibilityOutput := opts.VisibilityOutput
	if visibilityOutput == "" {
		visibilityOutput = "ok\n"
	}
	mainVisibilityOutput := opts.MainVisibilityOutput
	if mainVisibilityOutput == "" {
		mainVisibilityOutput = visibilityOutput
	}
	existingMainWorkspace := opts.ExistingMainWorkspace
	if existingMainWorkspace == "" {
		existingMainWorkspace = "."
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
    if [ -n %q ]; then
      printf 'SANDBOX                       AGENT   STATUS    PORTS   WORKSPACE\n'
      printf 'claude-sbx                     claude  %%s             %%s\n' %q %q
    else
      printf 'No sandboxes found.\n'
    fi
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
      *" claude-sbx sh -lc workspace="*)
        printf '%%b' %q
        ;;
      *" sh -lc workspace="*)
        printf '%%b' %q
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
        if [ %q = "true" ]; then
          printf 'herdr not found\n' >&2
          exit 1
        fi
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
      *" command -v cc"*)
        if [ %q = "true" ]; then
          printf 'cc not found\n' >&2
          exit 1
        fi
        printf '/usr/local/bin/cc\n'
        ;;
      *" cc --version")
        printf 'claude 1.0.0\n'
        ;;
      *" herdr status server --json")
        printf '{"running":true,"socket":"/home/agent/.config/herdr/herdr.sock"}\n'
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
      *"-it "*" herdr")
        if [ %q = "true" ]; then
          printf 'herdr tui failed\n' >&2
          exit 1
        fi
        printf 'herdr tui\n'
        if [ %q = "true" ]; then
          while [ ! -f %q ]; do
            sleep 0.05
          done
        fi
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
		opts.ExistingMainStatus,
		opts.ExistingMainStatus,
		existingMainWorkspace,
		shellBool(opts.FailCreate),
		shellBool(opts.FailCurl),
		egressIP,
		mainVisibilityOutput,
		visibilityOutput,
		envOutput,
		mountOutput,
		shellBool(opts.MissingHerdr),
		shellBool(opts.FailHerdrHook),
		shellBool(opts.MissingCC),
		herdrStopFile,
		shellBool(opts.FailHerdrServer),
		herdrStopFile,
		shellBool(opts.FailHerdrTUI),
		shellBool(opts.BlockRun),
		stopFile,
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

func waitForLogLine(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(readOptionalFile(t, path), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for log line %q; log:\n%s", want, readOptionalFile(t, path))
}

func containsLogLine(log, line string) bool {
	for _, got := range strings.Split(log, "\n") {
		if got == line {
			return true
		}
	}
	return false
}

func assertNoParentGuidanceMutation(t *testing.T, log string) {
	t.Helper()

	for _, line := range strings.Split(log, "\n") {
		if !strings.Contains(line, "CLAUDE.md") {
			continue
		}
		for _, forbidden := range []string{"rm ", "mv ", "chmod ", "truncate ", "tee ", ">"} {
			if strings.Contains(line, forbidden) {
				t.Fatalf("command must not mutate parent guidance paths using %q:\n%s", forbidden, log)
			}
		}
	}
}

func assertLogLineOrder(t *testing.T, log string, want []string) {
	t.Helper()

	lines := strings.Split(log, "\n")
	next := 0
	for _, got := range lines {
		if next < len(want) && got == want[next] {
			next++
		}
	}
	if next != len(want) {
		t.Fatalf("expected log lines in order %q, got:\n%s", want, log)
	}
}

func shellBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
