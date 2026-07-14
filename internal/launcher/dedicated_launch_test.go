package launcher

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/backend"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/egressguard"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/watchdog"
)

type synchronizedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func TestDedicatedDirectClaudeCapabilityFailureStopsBeforeMainAndAttach(t *testing.T) {
	configPath := writeLauncherDedicatedDirectConfig(t)
	log := make([]string, 0, 8)
	runner := &launchTestRunner{log: &log}
	guard := &launchTestGuard{
		log:        &log,
		acquireErr: fmt.Errorf("dedicated protocol isolation unsupported: sbx v0.34.0 provides HTTP upstream only"),
	}
	launcher := Runner{
		sandbox: backend.DockerSandbox{Runner: runner, Binary: "sbx"},
		newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
	}
	var stdout, stderr bytes.Buffer

	code := launcher.Run(Request{
		Target:     DirectClaudeTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code == 0 || !strings.Contains(stderr.String(), "dedicated protocol isolation unsupported") {
		t.Fatalf("expected dedicated capability rejection, got code %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	assertLogOrder(t, log, "guard acquire", "guard fence", "guard recover")
	if containsLogEntry(log, "sbx") {
		t.Fatalf("capability rejection reached backend side effects:\n%s", strings.Join(log, "\n"))
	}
}

func TestDedicatedDirectClaudeUsesValidatedMainAndSharedTeardown(t *testing.T) {
	configPath := writeLauncherDedicatedDirectConfig(t)
	log := make([]string, 0, 32)
	writeAttachedSBX(t)
	runner := &launchTestRunner{
		log:        &log,
		egressIP:   "203.0.113.10",
		mainExists: true,
		mainStatus: "running",
	}
	sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
	guard := &launchTestGuard{log: &log}
	var stdout synchronizedBuffer
	var stderr bytes.Buffer

	launcher := Runner{
		sandbox: sandbox,
		newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
	}
	code := launcher.Run(Request{
		Target:     DirectClaudeTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code != 0 {
		t.Fatalf("dedicated direct Claude failed with code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	for _, want := range []string{
		"sandbox egress ok: observed IP 203.0.113.10",
		"controller isolation ok: endpoint unreachable from main sandbox",
		"sandbox started: claude-sbx",
		"attached argv:exec -it claude-sbx claude",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output %q, got:\n%s", want, stdout.String())
		}
	}
	assertLogOrder(t, log,
		"guard acquire",
		"guard validate",
		"guard fence",
		"guard recover",
		"sbx stop claude-sbx",
	)
	for _, forbidden := range []string{"sbx create", "sbx rm", "sbx create --name claude-sbx-probe"} {
		if containsLogEntry(log, forbidden) {
			t.Fatalf("dedicated direct Claude used forbidden command %q:\n%s", forbidden, strings.Join(log, "\n"))
		}
	}
}

func TestRunnerUsesRealDedicatedAdapterToFenceRecoverAndStopMain(t *testing.T) {
	const secret = "controller-secret-value"
	t.Setenv("SAFE_CLAUDE_SBX_MIHOMO_SECRET", secret)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		fmt.Fprintln(w, `{"version":"v1.19.28"}`)
	}))
	t.Cleanup(controller.Close)

	configPath := writeLauncherDedicatedDirectConfig(t)
	setLauncherConfigText(t, configPath, "http://127.0.0.1:19090", controller.URL)
	setLauncherConfigBool(t, configPath, "stop_main_sandbox", false)
	commandLog := writeStatefulDedicatedSBX(t)
	var stdout synchronizedBuffer
	var stderr bytes.Buffer
	launcher := Runner{
		sandbox: backend.DockerSandbox{Runner: backend.ExecRunner{}, Binary: "sbx"},
		newGuard: func(cfg config.Config, main egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return egressguard.NewWithProtocolCheck(cfg, main, func(context.Context) error { return nil })
		},
	}

	code := launcher.Run(Request{
		Target:     DirectClaudeTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code != 0 {
		t.Fatalf("real dedicated Adapter launch failed with code %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	logText, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	log := strings.Split(strings.TrimSpace(string(logText)), "\n")
	lastDaemonStop := lastLogIndex(log, "daemon stop")
	recoveryList := firstLogIndexAfter(log, "ls", lastDaemonStop)
	mainStop := firstLogIndexAfter(log, "stop claude-sbx", recoveryList)
	if lastDaemonStop < 0 || recoveryList < 0 || mainStop < 0 {
		t.Fatalf("expected real Adapter fence, recovery, and main stop order:\n%s", string(logText))
	}
	if firstLogIndexAfter(log, "exec claude-sbx true", lastDaemonStop) >= 0 {
		t.Fatalf("real Adapter restarted main after fence:\n%s", string(logText))
	}
	if !strings.Contains(stdout.String(), "sandboxd lease recovered: normal daemon restored with main stopped") {
		t.Fatalf("expected real Adapter recovery output, got:\n%s", stdout.String())
	}
}

func TestDedicatedDirectClaudeCleanupHonorsOwnershipWithoutLeavingMainRunning(t *testing.T) {
	tests := []struct {
		name              string
		mainExists        bool
		stopMainSandbox   bool
		removeMainSandbox bool
		wantCreate        bool
		wantRemove        bool
	}{
		{
			name:            "preexisting main is stopped even when stop policy is false",
			mainExists:      true,
			stopMainSandbox: false,
		},
		{
			name:              "preexisting main is retained when remove policy is true",
			mainExists:        true,
			stopMainSandbox:   true,
			removeMainSandbox: true,
		},
		{
			name:              "launcher-created main follows remove policy after stop",
			stopMainSandbox:   true,
			removeMainSandbox: true,
			wantCreate:        true,
			wantRemove:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := writeLauncherDedicatedDirectConfig(t)
			setLauncherConfigBool(t, configPath, "stop_main_sandbox", tt.stopMainSandbox)
			setLauncherConfigBool(t, configPath, "remove_main_sandbox", tt.removeMainSandbox)
			log := make([]string, 0, 32)
			writeAttachedSBX(t)
			runner := &launchTestRunner{
				log:        &log,
				egressIP:   "203.0.113.10",
				mainExists: tt.mainExists,
				mainStatus: "running",
			}
			guard := &launchTestGuard{log: &log}
			launcher := Runner{
				sandbox: backend.DockerSandbox{Runner: runner, Binary: "sbx"},
				newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
					return guard, nil
				},
			}
			var stdout synchronizedBuffer
			var stderr bytes.Buffer

			code := launcher.Run(Request{
				Target:     DirectClaudeTarget,
				ConfigPath: configPath,
				Stdout:     &stdout,
				Stderr:     &stderr,
			})

			if code != 0 {
				t.Fatalf("dedicated direct Claude failed with code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
			}
			if containsLogEntry(log, "sbx create") != tt.wantCreate {
				t.Fatalf("unexpected main creation:\n%s", strings.Join(log, "\n"))
			}
			if countLogEntry(log, "sbx stop claude-sbx") != 1 {
				t.Fatalf("dedicated main was not stopped exactly once:\n%s", strings.Join(log, "\n"))
			}
			if containsLogEntry(log, "sbx rm --force claude-sbx") != tt.wantRemove {
				t.Fatalf("unexpected main removal behavior:\n%s", strings.Join(log, "\n"))
			}
			assertLogOrder(t, log, "guard fence", "guard recover", "sbx stop claude-sbx")
		})
	}
}

func TestDedicatedDirectClaudeCleanupFailureStillFencesAndRecoversOnce(t *testing.T) {
	configPath := writeLauncherDedicatedDirectConfig(t)
	log := make([]string, 0, 32)
	writeAttachedSBX(t)
	runner := &launchTestRunner{
		log:         &log,
		egressIP:    "203.0.113.10",
		mainExists:  true,
		mainStatus:  "running",
		failCommand: "sbx stop claude-sbx",
	}
	guard := &launchTestGuard{log: &log}
	launcher := Runner{
		sandbox: backend.DockerSandbox{Runner: runner, Binary: "sbx"},
		newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
	}
	var stdout synchronizedBuffer
	var stderr bytes.Buffer

	code := launcher.Run(Request{
		Target:     DirectClaudeTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code == 0 || !strings.Contains(stderr.String(), "stop main sandbox") {
		t.Fatalf("expected cleanup failure, got code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	if countLogEntry(log, "guard fence") != 1 || countLogEntry(log, "guard recover") != 1 {
		t.Fatalf("cleanup failure repeated guard finalization:\n%s", strings.Join(log, "\n"))
	}
	assertLogOrder(t, log, "guard fence", "guard recover", "sbx stop claude-sbx")
}

func TestDedicatedDirectClaudeFenceFailureSkipsRecoveryButStillStopsMain(t *testing.T) {
	configPath := writeLauncherDedicatedDirectConfig(t)
	log := make([]string, 0, 32)
	writeAttachedSBX(t)
	runner := &launchTestRunner{
		log:        &log,
		egressIP:   "203.0.113.10",
		mainExists: true,
		mainStatus: "running",
	}
	guard := &failingFenceGuard{log: &log}
	launcher := Runner{
		sandbox: backend.DockerSandbox{Runner: runner, Binary: "sbx"},
		newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
	}
	var stdout synchronizedBuffer
	var stderr bytes.Buffer

	code := launcher.Run(Request{
		Target:     DirectClaudeTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code == 0 || !strings.Contains(stderr.String(), "sandboxd lease fence invalid") {
		t.Fatalf("expected fence failure, got code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	if countLogEntry(log, "guard recover") != 0 {
		t.Fatalf("recovery ran after failed fence:\n%s", strings.Join(log, "\n"))
	}
	assertLogOrder(t, log, "guard fence", "sbx stop claude-sbx")
}

func TestDedicatedSafeHerdrRevalidatesExistingMainAndFencesBeforeCleanup(t *testing.T) {
	configPath := writeLauncherDedicatedLaunchConfig(t)
	log := make([]string, 0, 32)
	writeAttachedSBX(t)
	runner := &launchTestRunner{
		log:        &log,
		egressIP:   "203.0.113.10",
		mainExists: true,
		mainStatus: "running",
	}
	sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
	guard := &launchTestGuard{log: &log}
	var stdout synchronizedBuffer
	var stderr bytes.Buffer

	launcher := Runner{
		sandbox: sandbox,
		newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
	}
	code := launcher.Run(Request{
		Target:     HerdrTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code != 0 {
		t.Fatalf("dedicated safe-herdr failed with code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	for _, want := range []string{
		"sandbox egress ok: observed IP 203.0.113.10",
		"controller isolation ok: endpoint unreachable from main sandbox",
		"Herdr TUI started: claude-sbx",
		"sandbox exited; cleanup complete",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected output %q, got:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "TUN preflight") || strings.Contains(stdout.String(), "host egress") {
		t.Fatalf("dedicated launch unexpectedly ran host-inherited checks:\n%s", stdout.String())
	}
	assertLogOrder(t, log,
		"guard acquire",
		"sbx version",
		"guard validate",
		"guard fence",
		"guard recover",
		"sbx stop claude-sbx",
	)
	for _, forbidden := range []string{"sbx create", "sbx rm"} {
		for _, entry := range log {
			if strings.HasPrefix(entry, forbidden) {
				t.Fatalf("reuse path mutated existing main with %q:\n%s", entry, strings.Join(log, "\n"))
			}
		}
	}
}

func TestDedicatedSafeHerdrCreatesNewMainAndFencesBeforeOwnedCleanup(t *testing.T) {
	configPath := writeLauncherDedicatedLaunchConfig(t)
	log := make([]string, 0, 32)
	writeAttachedSBX(t)
	runner := &launchTestRunner{log: &log, egressIP: "203.0.113.10"}
	sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
	guard := &launchTestGuard{log: &log}
	var stdout synchronizedBuffer
	var stderr bytes.Buffer

	launcher := Runner{
		sandbox: sandbox,
		newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
	}
	code := launcher.Run(Request{
		Target:     HerdrTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code != 0 {
		t.Fatalf("dedicated safe-herdr failed with code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	assertLogOrder(t, log,
		"guard acquire",
		"sbx create --name claude-sbx --template safe-claude-sbx-herdr:latest claude .",
		"guard validate",
		"guard fence",
		"guard recover",
		"sbx stop claude-sbx",
	)
}

func TestDedicatedSafeHerdrAttachesExpectedCommand(t *testing.T) {
	configPath := writeLauncherDedicatedLaunchConfig(t)
	log := make([]string, 0, 32)
	writeAttachedSBX(t)
	runner := &launchTestRunner{
		log:        &log,
		egressIP:   "203.0.113.10",
		mainExists: true,
		mainStatus: "running",
	}
	sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
	guard := &launchTestGuard{log: &log}
	var stdout synchronizedBuffer
	var stderr bytes.Buffer

	launcher := Runner{
		sandbox: sandbox,
		newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
	}
	code := launcher.Run(Request{
		Target:     HerdrTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code != 0 {
		t.Fatalf("dedicated safe-herdr failed with code %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "attached argv:exec -it claude-sbx herdr") {
		t.Fatalf("expected exact Herdr attach command, got:\n%s", stdout.String())
	}
}

func TestDedicatedSafeHerdrFailsClosedBeforeAttach(t *testing.T) {
	tests := []struct {
		name          string
		mainExists    bool
		egressIP      string
		acquireErr    error
		validateErr   error
		failCommand   string
		attachFailure bool
		wantError     string
		wantCreate    bool
		wantValidate  bool
		wantMainClean bool
	}{
		{
			name:       "acquire failure",
			acquireErr: fmt.Errorf("sandboxd lease invalid: unrelated sandbox conflict: other-sbx"),
			wantError:  "sandboxd lease invalid: unrelated sandbox conflict: other-sbx",
		},
		{
			name:       "existing main preflight failure",
			mainExists: true,
			egressIP:   "198.51.100.20",
			wantError:  "sandbox-egress-mismatch",
		},
		{
			name:          "new main preflight failure",
			egressIP:      "198.51.100.20",
			wantError:     "sandbox-egress-mismatch",
			wantCreate:    true,
			wantMainClean: true,
		},
		{
			name:         "existing main validation failure",
			mainExists:   true,
			egressIP:     "203.0.113.10",
			validateErr:  fmt.Errorf("controller isolation invalid: controller endpoint is reachable from main sandbox"),
			wantError:    "controller isolation invalid",
			wantValidate: true,
		},
		{
			name:         "existing main Herdr prepare failure",
			mainExists:   true,
			egressIP:     "203.0.113.10",
			failCommand:  "herdr --version",
			wantError:    "verify sandbox-local Herdr",
			wantValidate: true,
		},
		{
			name:          "existing main Herdr attach failure",
			mainExists:    true,
			egressIP:      "203.0.113.10",
			attachFailure: true,
			wantError:     "start sandbox-local Herdr TUI",
			wantValidate:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := writeLauncherDedicatedLaunchConfig(t)
			log := make([]string, 0, 32)
			if tt.attachFailure {
				writeUnstartableSBX(t)
			} else {
				writeAttachedSBX(t)
			}
			runner := &launchTestRunner{
				log:         &log,
				egressIP:    tt.egressIP,
				mainExists:  tt.mainExists,
				mainStatus:  "running",
				failCommand: tt.failCommand,
			}
			sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
			guard := &launchTestGuard{log: &log, acquireErr: tt.acquireErr, validateErr: tt.validateErr}
			var stdout, stderr bytes.Buffer

			launcher := Runner{
				sandbox: sandbox,
				newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
					return guard, nil
				},
			}
			code := launcher.Run(Request{
				Target:     HerdrTarget,
				ConfigPath: configPath,
				Stdout:     &stdout,
				Stderr:     &stderr,
			})

			if code == 0 || !strings.Contains(stderr.String(), tt.wantError) {
				t.Fatalf("expected %q failure, got code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", tt.wantError, code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
			}
			if strings.Contains(stdout.String(), "Herdr TUI started") {
				t.Fatalf("failure entered attached Herdr lifecycle:\n%s", stdout.String())
			}
			if containsLogEntry(log, "sbx create") != tt.wantCreate {
				t.Fatalf("unexpected create behavior:\n%s", strings.Join(log, "\n"))
			}
			if containsLogEntry(log, "guard validate") != tt.wantValidate {
				t.Fatalf("unexpected validation behavior:\n%s", strings.Join(log, "\n"))
			}
			if containsLogEntry(log, "sbx stop claude-sbx") != tt.wantMainClean {
				t.Fatalf("unexpected main cleanup behavior:\n%s", strings.Join(log, "\n"))
			}
			assertLogOrder(t, log, "guard acquire", "guard fence", "guard recover")
			if tt.wantMainClean {
				assertLogOrder(t, log, "guard recover", "sbx stop claude-sbx")
			}
		})
	}
}

func TestDedicatedSafeHerdrCancelsAttachedProcessBeforeFenceAndRecover(t *testing.T) {
	configPath := writeLauncherDedicatedLaunchConfig(t)
	log := make([]string, 0, 32)
	pidPath := writeBlockingSBX(t)
	runner := &launchTestRunner{
		log:        &log,
		egressIP:   "203.0.113.10",
		mainExists: true,
		mainStatus: "running",
	}
	sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
	guard := &fenceOrderingGuard{log: &log, attachedPIDPath: pidPath}
	var stdout, stderr bytes.Buffer

	launcher := Runner{
		sandbox: sandbox,
		newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
		platform: launchPlatform{
			signalContext: func() (context.Context, context.CancelFunc) {
				waitForFile(t, pidPath)
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
		},
	}
	code := launcher.Run(Request{
		Target:     HerdrTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code != 130 {
		t.Fatalf("expected signal exit 130, got %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	assertLogOrder(t, log,
		"guard fence",
		"guard recover",
		"sbx stop claude-sbx",
	)
	if containsLogEntry(log, "sbx exec claude-sbx herdr server stop") {
		t.Fatalf("dedicated cleanup restarted Herdr after recovery:\n%s", strings.Join(log, "\n"))
	}
}

func TestHostInheritedSafeHerdrKeepsPlatformPreflightAndRuntimeWatchdog(t *testing.T) {
	configPath := writeLauncherHostLaunchConfig(t)
	log := make([]string, 0, 32)
	writeBlockingSBX(t)
	runner := &launchTestRunner{
		log:        &log,
		egressIP:   "203.0.113.10",
		mainExists: true,
		mainStatus: "running",
	}
	sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
	guard := &hostLaunchGuard{log: &log}
	var stdout, stderr bytes.Buffer

	launcher := Runner{
		sandbox: sandbox,
		newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
		platform: launchPlatform{
			checkTUN: func(config.ClashVerge) (network.TUNPreflightResult, error) {
				log = append(log, "platform TUN preflight")
				return network.TUNPreflightResult{StartupTUNInterface: "utun9"}, nil
			},
			signalContext: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
		},
	}
	code := launcher.Run(Request{
		Target:     HerdrTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code == 0 || !strings.Contains(stderr.String(), "host-route runtime policy failed: host route drift") {
		t.Fatalf("expected host runtime failure, got code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	for _, want := range []string{
		"TUN preflight ok: startup interface utun9",
		"host egress ok: observed IP 203.0.113.10",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected host-inherited output %q, got:\n%s", want, stdout.String())
		}
	}
	assertLogOrder(t, log,
		"platform TUN preflight",
		"guard acquire",
		"guard validate",
		"guard runtime start",
		"guard runtime check",
		"guard fence",
		"guard recover",
		"sbx exec claude-sbx herdr server stop",
		"sbx stop claude-sbx",
	)
}

func TestDedicatedSafeHerdrRuntimeFailureFencesAndRecoversBeforeCleanupOnce(t *testing.T) {
	configPath := writeLauncherDedicatedLaunchConfig(t)
	log := make([]string, 0, 32)
	writeBlockingSBX(t)
	runner := &launchTestRunner{
		log:        &log,
		egressIP:   "203.0.113.10",
		mainExists: true,
		mainStatus: "running",
	}
	sandbox := backend.DockerSandbox{Runner: runner, Binary: "sbx"}
	event := watchdog.Event{Source: egressguard.DedicatedHealthEventSource, Detail: "scheduled runtime validation"}
	guard := &launchTestGuard{
		log:         &log,
		watchEvent:  &event,
		watchResult: watchdog.CheckResult{OK: false, Reason: "dedicated egress drift"},
	}
	var stdout, stderr bytes.Buffer

	launcher := Runner{
		sandbox: sandbox,
		newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
		platform: launchPlatform{signalContext: func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		}},
	}
	code := launcher.Run(Request{
		Target:     HerdrTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code == 0 || !strings.Contains(stderr.String(), "dedicated-health runtime policy failed: dedicated egress drift") {
		t.Fatalf("expected dedicated runtime failure, got code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	if countLogEntry(log, "guard fence") != 1 || countLogEntry(log, "guard recover") != 1 {
		t.Fatalf("expected one egress fence and recovery, got:\n%s", strings.Join(log, "\n"))
	}
	if countLogEntry(log, "sbx stop claude-sbx") != 1 {
		t.Fatalf("expected one main cleanup, got:\n%s", strings.Join(log, "\n"))
	}
	assertLogOrder(t, log,
		"guard runtime start",
		"guard runtime check",
		"guard fence",
		"guard recover",
		"sbx stop claude-sbx",
	)
}

type fenceOrderingGuard struct {
	log             *[]string
	attachedPIDPath string
}

type failingFenceGuard struct {
	log *[]string
}

type hostLaunchGuard struct {
	log *[]string
}

func (g *hostLaunchGuard) Acquire(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard acquire")
	return egressguard.Result{Messages: []string{"host egress ok: observed IP 203.0.113.10"}}, nil
}

func (g *hostLaunchGuard) ValidateMain(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard validate")
	return egressguard.Result{}, nil
}

func (g *hostLaunchGuard) Watch(context.Context, egressguard.WatchInput) egressguard.RuntimeWatch {
	*g.log = append(*g.log, "guard runtime start")
	events := make(chan watchdog.Event, 1)
	events <- watchdog.Event{Source: "host-route"}
	return egressguard.RuntimeWatch{
		Events: events,
		Checker: watchdog.CheckFunc(func(context.Context, watchdog.Event) (watchdog.CheckResult, error) {
			*g.log = append(*g.log, "guard runtime check")
			return watchdog.CheckResult{OK: false, Reason: "host route drift"}, nil
		}),
	}
}

func (g *hostLaunchGuard) Fence(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard fence")
	return egressguard.Result{}, nil
}

func (g *hostLaunchGuard) Recover(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard recover")
	return egressguard.Result{}, nil
}

func (g *fenceOrderingGuard) Acquire(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard acquire")
	return egressguard.Result{}, nil
}

func (g *fenceOrderingGuard) ValidateMain(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard validate")
	return egressguard.Result{}, nil
}

func (*fenceOrderingGuard) Watch(context.Context, egressguard.WatchInput) egressguard.RuntimeWatch {
	return egressguard.RuntimeWatch{}
}

func (g *fenceOrderingGuard) Fence(context.Context) (egressguard.Result, error) {
	pidText, err := os.ReadFile(g.attachedPIDPath)
	if err != nil {
		return egressguard.Result{}, fmt.Errorf("read attached process PID: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidText)))
	if err != nil {
		return egressguard.Result{}, fmt.Errorf("parse attached process PID: %w", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			*g.log = append(*g.log, "guard fence")
			return egressguard.Result{}, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return egressguard.Result{}, fmt.Errorf("attached Herdr still running before fence")
}

func (g *fenceOrderingGuard) Recover(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard recover")
	return egressguard.Result{}, nil
}

func (g *failingFenceGuard) Acquire(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard acquire")
	return egressguard.Result{}, nil
}

func (g *failingFenceGuard) ValidateMain(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard validate")
	return egressguard.Result{}, nil
}

func (*failingFenceGuard) Watch(context.Context, egressguard.WatchInput) egressguard.RuntimeWatch {
	return egressguard.RuntimeWatch{}
}

func (g *failingFenceGuard) Fence(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard fence")
	return egressguard.Result{}, fmt.Errorf("sandboxd lease fence invalid: injected failure")
}

func (g *failingFenceGuard) Recover(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard recover")
	return egressguard.Result{}, nil
}

type launchTestGuard struct {
	log         *[]string
	acquireErr  error
	validateErr error
	watchEvent  *watchdog.Event
	watchResult watchdog.CheckResult
	watchErr    error
}

func (g *launchTestGuard) Acquire(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard acquire")
	if g.acquireErr != nil {
		return egressguard.Result{}, g.acquireErr
	}
	return egressguard.Result{Messages: []string{
		"gateway controller ok: authenticated loopback controller",
		"sandboxd lease ok: exclusive command-scoped upstream active",
	}}, nil
}

func (g *launchTestGuard) ValidateMain(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard validate")
	if g.validateErr != nil {
		return egressguard.Result{}, g.validateErr
	}
	return egressguard.Result{Messages: []string{"controller isolation ok: endpoint unreachable from main sandbox"}}, nil
}

func (g *launchTestGuard) Watch(context.Context, egressguard.WatchInput) egressguard.RuntimeWatch {
	if g.watchEvent == nil {
		return egressguard.RuntimeWatch{}
	}
	*g.log = append(*g.log, "guard runtime start")
	events := make(chan watchdog.Event, 1)
	events <- *g.watchEvent
	return egressguard.RuntimeWatch{
		Events: events,
		Checker: watchdog.CheckFunc(func(context.Context, watchdog.Event) (watchdog.CheckResult, error) {
			*g.log = append(*g.log, "guard runtime check")
			return g.watchResult, g.watchErr
		}),
	}
}

func (g *launchTestGuard) Fence(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard fence")
	return egressguard.Result{Messages: []string{"sandboxd lease fenced: dedicated egress stopped"}}, nil
}

func (g *launchTestGuard) Recover(context.Context) (egressguard.Result, error) {
	*g.log = append(*g.log, "guard recover")
	return egressguard.Result{Messages: []string{"sandboxd lease recovered: normal daemon restored with main stopped"}}, nil
}

type launchTestRunner struct {
	log         *[]string
	egressIP    string
	mainExists  bool
	mainStatus  string
	failCommand string
}

func (r *launchTestRunner) LookPath(string) (string, error) {
	return "/fake/sbx", nil
}

func (r *launchTestRunner) Run(_ context.Context, name string, args ...string) (backend.CommandResult, error) {
	entry := strings.TrimSpace(name + " " + strings.Join(args, " "))
	*r.log = append(*r.log, entry)
	if name != "sbx" || len(args) == 0 {
		return backend.CommandResult{ExitCode: 1, Stderr: "unexpected command"}, fmt.Errorf("unexpected command")
	}
	if r.failCommand != "" && strings.Contains(entry, r.failCommand) {
		return backend.CommandResult{ExitCode: 1, Stderr: "injected failure"}, fmt.Errorf("injected failure")
	}
	switch args[0] {
	case "version":
		return backend.CommandResult{Stdout: "sbx version: v0.34.0 fake\n"}, nil
	case "ls":
		if !r.mainExists {
			return backend.CommandResult{Stdout: "No sandboxes found.\n"}, nil
		}
		status := r.mainStatus
		if status == "" {
			status = "running"
		}
		return backend.CommandResult{Stdout: fmt.Sprintf("SANDBOX AGENT STATUS PORTS WORKSPACE\nclaude-sbx claude %s - .\n", status)}, nil
	case "create":
		r.mainExists = true
		r.mainStatus = "running"
		return backend.CommandResult{Stdout: "created\n"}, nil
	case "exec":
		return r.exec(args)
	case "stop":
		r.mainStatus = "stopped"
		return backend.CommandResult{Stdout: "stopped\n"}, nil
	case "rm":
		r.mainExists = false
		return backend.CommandResult{Stdout: "removed\n"}, nil
	default:
		return backend.CommandResult{ExitCode: 1, Stderr: "unexpected command"}, fmt.Errorf("unexpected command")
	}
}

func containsLogEntry(log []string, prefix string) bool {
	for _, entry := range log {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func countLogEntry(log []string, want string) int {
	count := 0
	for _, entry := range log {
		if entry == want {
			count++
		}
	}
	return count
}

func (r *launchTestRunner) exec(args []string) (backend.CommandResult, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "sh -lc workspace="):
		return backend.CommandResult{Stdout: "ok\n"}, nil
	case strings.Contains(joined, "sh -lc command -v"):
		return backend.CommandResult{Stdout: "/usr/local/bin/tool\n"}, nil
	case strings.HasSuffix(joined, " herdr --version"):
		return backend.CommandResult{Stdout: "herdr 1.0.0\n"}, nil
	case strings.HasSuffix(joined, " herdr integration install claude"):
		return backend.CommandResult{Stdout: "installed\n"}, nil
	case strings.HasSuffix(joined, " cc --version"):
		return backend.CommandResult{Stdout: "cc 1.0.0\n"}, nil
	case strings.HasSuffix(joined, " herdr server stop"):
		return backend.CommandResult{Stdout: "stopped\n"}, nil
	case args[len(args)-1] == "env":
		return backend.CommandResult{Stdout: "PATH=/usr/bin\nTZ=America/Chicago\nLANG=en_US.UTF-8\nLC_ALL=en_US.UTF-8\nHTTP_PROXY=http://gateway.docker.internal:3128\nHTTPS_PROXY=http://gateway.docker.internal:3128\nNO_PROXY=localhost,127.0.0.1,gateway.docker.internal\n"}, nil
	case args[len(args)-1] == "pwd":
		return backend.CommandResult{Stdout: "/workspace\n"}, nil
	case args[len(args)-1] == "mount":
		return backend.CommandResult{Stdout: "/dev/disk1 on /workspace type virtiofs\n"}, nil
	case args[len(args)-1] == "date":
		return backend.CommandResult{Stdout: "Sun Jul  5 12:00:00 UTC 2026\n"}, nil
	case args[len(args)-1] == "locale":
		return backend.CommandResult{Stdout: "LANG=en_US.UTF-8\nLC_ALL=en_US.UTF-8\n"}, nil
	case strings.Contains(joined, "curl -fsS"):
		return backend.CommandResult{Stdout: r.egressIP + "\n"}, nil
	default:
		return backend.CommandResult{ExitCode: 1, Stderr: "unexpected exec"}, fmt.Errorf("unexpected exec")
	}
}

func writeAttachedSBX(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sbx")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'attached argv:%s\\n' \"$*\"\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write attached sbx: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeUnstartableSBX(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sbx")
	if err := os.WriteFile(path, []byte("not executable\n"), 0o600); err != nil {
		t.Fatalf("write unstartable sbx: %v", err)
	}
	t.Setenv("PATH", dir)
}

func writeBlockingSBX(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "attached.pid")
	path := filepath.Join(dir, "sbx")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' \"$$\" > %s\nwhile :; do sleep 1; done\n", strconv.Quote(pidPath))
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write blocking sbx: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return pidPath
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func writeLauncherDedicatedLaunchConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `network:
  egress:
    mode: "dedicated-gateway"
    dedicated_gateway:
      upstream_url: "http://127.0.0.1:17890"
      controller_url: "http://127.0.0.1:19090"
      controller_secret_env: "SAFE_CLAUDE_SBX_MIHOMO_SECRET"
  egress_ip:
    expected_ip: "203.0.113.10"
    sandbox_check_url: "https://api.ipify.org"
    timeout_seconds: 3
sandbox:
  backend: "docker-sandbox"
  main_name: "claude-sbx"
  probe_name: "claude-sbx-probe"
  agent: "claude"
  template: "safe-claude-sbx-herdr:latest"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: false
      socket_path: "/home/agent/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"
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
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func writeLauncherDedicatedDirectConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `network:
  egress:
    mode: "dedicated-gateway"
    dedicated_gateway:
      upstream_url: "http://127.0.0.1:17890"
      controller_url: "http://127.0.0.1:19090"
      controller_secret_env: "SAFE_CLAUDE_SBX_MIHOMO_SECRET"
  egress_ip:
    expected_ip: "203.0.113.10"
    sandbox_check_url: "https://api.ipify.org"
    timeout_seconds: 3
sandbox:
  backend: "docker-sandbox"
  main_name: "claude-sbx"
  probe_name: "claude-sbx-probe"
  agent: "claude"
  supervision:
    mode: "direct-claude"
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
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func setLauncherConfigBool(t *testing.T, path, key string, value bool) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	newValue := key + ": " + strconv.FormatBool(value)
	if strings.Contains(string(body), newValue) {
		return
	}
	oldValue := key + ": " + strconv.FormatBool(!value)
	if !strings.Contains(string(body), oldValue) {
		t.Fatalf("config key %s not found", key)
	}
	updated := strings.Replace(string(body), oldValue, newValue, 1)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func setLauncherConfigText(t *testing.T, path, oldValue, newValue string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	updated := strings.Replace(string(body), oldValue, newValue, 1)
	if updated == string(body) {
		t.Fatalf("config value %q not found", oldValue)
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func writeStatefulDedicatedSBX(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	commandLog := filepath.Join(dir, "commands.log")
	statePath := filepath.Join(dir, "main.state")
	pidPath := filepath.Join(dir, "daemon.pid")
	if err := os.WriteFile(statePath, []byte("running\n"), 0o600); err != nil {
		t.Fatalf("write main state: %v", err)
	}
	script := fmt.Sprintf(`#!/bin/sh
log_path=%s
state_path=%s
pid_path=%s
printf '%%s\n' "$*" >> "$log_path"
case "$1 $2" in
  "daemon start")
    printf '%%s' "$$" > "$pid_path"
    trap 'exit 0' TERM INT
    while :; do sleep 1; done
    ;;
  "daemon stop")
    if [ -f "$pid_path" ]; then
      pid=$(cat "$pid_path")
      kill "$pid" 2>/dev/null || true
      rm -f "$pid_path"
    fi
    printf 'stopped\n' > "$state_path"
    exit 0
    ;;
  "daemon status")
    exit 0
    ;;
esac
case "$1" in
  version)
    printf 'sbx version: v0.34.0 fake\n'
    ;;
  ls)
    state=$(cat "$state_path")
    printf 'SANDBOX AGENT STATUS PORTS WORKSPACE\nclaude-sbx claude %%s - .\n' "$state"
    ;;
  create)
    printf 'running\n' > "$state_path"
    ;;
  stop)
    printf 'stopped\n' > "$state_path"
    ;;
  rm)
    rm -f "$state_path"
    ;;
  exec)
    case " $* " in
      *" exec -it claude-sbx claude "*) printf 'attached argv:%%s\n' "$*" ;;
      *" workspace="*) printf 'ok\n' ;;
      *" curl -fsS "*) printf '203.0.113.10\n' ;;
      *" env "*) printf 'PATH=/usr/bin\nTZ=America/Chicago\nLANG=en_US.UTF-8\nLC_ALL=en_US.UTF-8\nHTTP_PROXY=http://gateway.docker.internal:3128\nHTTPS_PROXY=http://gateway.docker.internal:3128\nNO_PROXY=localhost,127.0.0.1,gateway.docker.internal\n' ;;
      *" pwd "*) printf '/workspace\n' ;;
      *" mount "*) printf '/dev/disk1 on /workspace type virtiofs\n' ;;
      *" date "*) printf 'Sun Jul  5 12:00:00 UTC 2026\n' ;;
      *" locale "*) printf 'LANG=en_US.UTF-8\nLC_ALL=en_US.UTF-8\n' ;;
      *" true "*) printf 'running\n' > "$state_path" ;;
      *" sh -lc "*) exit 0 ;;
    esac
    ;;
esac
exit 0
`, strconv.Quote(commandLog), strconv.Quote(statePath), strconv.Quote(pidPath))
	path := filepath.Join(dir, "sbx")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write stateful sbx: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return commandLog
}

func lastLogIndex(log []string, command string) int {
	for i := len(log) - 1; i >= 0; i-- {
		if log[i] == command {
			return i
		}
	}
	return -1
}

func firstLogIndexAfter(log []string, command string, after int) int {
	for i := after + 1; i < len(log); i++ {
		if log[i] == command {
			return i
		}
	}
	return -1
}

func writeLauncherHostLaunchConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := fmt.Sprintf(`network:
  clash_verge:
    app_home: %q
    route_check_target: "1.1.1.1"
    tun_interface_prefix: "utun"
  egress_ip:
    expected_ip: "203.0.113.10"
    host_check_url: "https://api.ipify.org"
    sandbox_check_url: "https://api.ipify.org"
    timeout_seconds: 3
sandbox:
  backend: "docker-sandbox"
  main_name: "claude-sbx"
  probe_name: "claude-sbx-probe"
  agent: "claude"
  template: "safe-claude-sbx-herdr:latest"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: false
      socket_path: "/home/agent/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"
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
`, t.TempDir())
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
