package launcher

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/backend"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/egressguard"
)

func TestDoctorAcquireSuccessAvailabilityFailureStillStopsMainAfterFenceFailure(t *testing.T) {
	configPath := writeLauncherDedicatedDoctorConfig(t)
	setLauncherConfigBool(t, configPath, "stop_main_sandbox", false)
	setLauncherConfigBool(t, configPath, "remove_main_sandbox", true)
	log := make([]string, 0, 16)
	baseRunner := &doctorTestRunner{
		log:        &log,
		mainExists: true,
		mainStatus: "running",
	}
	failingRunner := &failCommandOccurrenceRunner{
		Runner:     baseRunner,
		command:    "sbx version",
		occurrence: 1,
	}
	guard := &failingFenceGuard{log: &log}
	launcher := Runner{
		sandbox: backend.DockerSandbox{Runner: failingRunner, Binary: "sbx"},
		newGuard: func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error) {
			return guard, nil
		},
	}
	var stdout, stderr bytes.Buffer

	code := launcher.Run(Request{
		Target:     DoctorTarget,
		ConfigPath: configPath,
		Stdout:     &stdout,
		Stderr:     &stderr,
	})

	if code == 0 || !strings.Contains(stderr.String(), "sandbox backend invalid") || !strings.Contains(stderr.String(), "sandboxd lease fence invalid") {
		t.Fatalf("expected availability and fence failure, got code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	if countLogEntry(log, "guard recover") != 0 {
		t.Fatalf("recovery ran after failed fence:\n%s", strings.Join(log, "\n"))
	}
	if countLogEntry(log, "sbx stop claude-sbx") != 1 {
		t.Fatalf("main was not stopped exactly once after availability failure:\n%s", strings.Join(log, "\n"))
	}
	if containsLogEntry(log, "sbx rm --force claude-sbx") {
		t.Fatalf("unknown-ownership main was removed:\n%s", strings.Join(log, "\n"))
	}
	assertLogOrder(t, log, "guard acquire", "sbx version", "guard fence", "sbx stop claude-sbx")
}

func TestLaunchAcquireSuccessInitialMainInspectionFailureStillStopsMainAfterFenceFailure(t *testing.T) {
	configPath := writeLauncherDedicatedDirectConfig(t)
	setLauncherConfigBool(t, configPath, "stop_main_sandbox", false)
	setLauncherConfigBool(t, configPath, "remove_main_sandbox", true)
	log := make([]string, 0, 16)
	baseRunner := &launchTestRunner{
		log:        &log,
		mainExists: true,
		mainStatus: "running",
	}
	failingRunner := &failCommandOccurrenceRunner{
		Runner:     baseRunner,
		command:    "sbx ls",
		occurrence: 2,
	}
	guard := &failingFenceGuard{log: &log}
	launcher := Runner{
		sandbox: backend.DockerSandbox{Runner: failingRunner, Binary: "sbx"},
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

	if code == 0 || !strings.Contains(stderr.String(), "main sandbox preflight invalid") || !strings.Contains(stderr.String(), "sandboxd lease fence invalid") {
		t.Fatalf("expected initial main inspection and fence failure, got code %d\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", code, stdout.String(), stderr.String(), strings.Join(log, "\n"))
	}
	if countLogEntry(log, "sbx ls") != 2 {
		t.Fatalf("expected availability and initial inspection list calls:\n%s", strings.Join(log, "\n"))
	}
	if countLogEntry(log, "guard recover") != 0 {
		t.Fatalf("recovery ran after failed fence:\n%s", strings.Join(log, "\n"))
	}
	if countLogEntry(log, "sbx stop claude-sbx") != 1 {
		t.Fatalf("main was not stopped exactly once after initial inspection failure:\n%s", strings.Join(log, "\n"))
	}
	if containsLogEntry(log, "sbx rm --force claude-sbx") {
		t.Fatalf("unknown-ownership main was removed:\n%s", strings.Join(log, "\n"))
	}
	assertLogOrder(t, log, "guard acquire", "sbx version", "sbx ls", "guard fence", "sbx stop claude-sbx")
}

type failCommandOccurrenceRunner struct {
	backend.Runner
	command    string
	occurrence int
	seen       int
}

func (r *failCommandOccurrenceRunner) Run(ctx context.Context, name string, args ...string) (backend.CommandResult, error) {
	// Delegate first so the stateful fake records the attempted command.
	result, err := r.Runner.Run(ctx, name, args...)
	entry := strings.TrimSpace(name + " " + strings.Join(args, " "))
	if entry != r.command {
		return result, err
	}
	r.seen++
	if r.seen != r.occurrence {
		return result, err
	}
	return backend.CommandResult{ExitCode: 1, Stderr: "injected failure"}, fmt.Errorf("injected failure")
}
