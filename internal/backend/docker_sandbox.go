package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/policy"
)

type AvailabilityKind string

const (
	AvailabilityAvailable    AvailabilityKind = "available"
	AvailabilityUnavailable  AvailabilityKind = "unavailable"
	AvailabilityIncompatible AvailabilityKind = "version-incompatible"
)

const sandboxLocalHerdrInstallAttempts = 2

type Availability struct {
	OK         bool
	Kind       AvailabilityKind
	Path       string
	Version    string
	Diagnostic string
}

type InspectionObservation struct {
	Environment      map[string]string
	WorkingDirectory string
	Mounts           string
	Date             string
	Locale           string
	EgressIP         string
}

type ProbeResult struct {
	Egress      network.EgressResult
	Inspection  InspectionObservation
	CleanupDone bool
}

type StartResult struct {
	SandboxName string
	Agent       string
	Workspace   string
	Timezone    string
	Locale      string
}

type StartPlan struct {
	SandboxName  string
	Agent        string
	Workspace    string
	UseCloneMode bool
	Timezone     string
	Locale       string
	Environment  map[string]string
	Supervision  SupervisionPlan
}

type SupervisionPlan struct {
	Mode  string
	Herdr HerdrPlan
}

type HerdrPlan struct {
	InstallIfMissing bool
	SocketPath       string
	PaneID           string
	ReadinessTimeout time.Duration
	InstallTimeout   time.Duration
	InstallAttempts  int
}

func NewStartPlan(cfg config.Config) StartPlan {
	mode := cfg.Sandbox.Supervision.Mode
	if mode == "" {
		mode = "direct-claude"
	}
	var herdr HerdrPlan
	if cfg.Sandbox.Supervision.Herdr != nil {
		herdr = HerdrPlan{
			InstallIfMissing: cfg.Sandbox.Supervision.Herdr.InstallIfMissing != nil && *cfg.Sandbox.Supervision.Herdr.InstallIfMissing,
			SocketPath:       strings.TrimSpace(cfg.Sandbox.Supervision.Herdr.SocketPath),
			PaneID:           strings.TrimSpace(cfg.Sandbox.Supervision.Herdr.PaneID),
			ReadinessTimeout: time.Duration(cfg.Network.EgressIP.TimeoutSeconds) * time.Second,
			InstallTimeout:   time.Duration(cfg.Network.EgressIP.TimeoutSeconds) * time.Second,
			InstallAttempts:  sandboxLocalHerdrInstallAttempts,
		}
	}
	return StartPlan{
		SandboxName:  cfg.Sandbox.MainName,
		Agent:        cfg.Sandbox.Agent,
		Workspace:    cfg.Workspace.Mount,
		UseCloneMode: cfg.Workspace.UseCloneMode,
		Timezone:     cfg.Environment.Timezone,
		Locale:       cfg.Environment.Locale,
		Environment: map[string]string{
			"TZ":     cfg.Environment.Timezone,
			"LANG":   cfg.Environment.Locale,
			"LC_ALL": cfg.Environment.Locale,
		},
		Supervision: SupervisionPlan{
			Mode:  mode,
			Herdr: herdr,
		},
	}
}

type CommandResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type Runner interface {
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
	LookPath(file string) (string, error)
}

type DockerSandbox struct {
	Runner Runner
	Binary string
}

func NewDockerSandbox() DockerSandbox {
	return DockerSandbox{Runner: ExecRunner{}, Binary: "sbx"}
}

func (b DockerSandbox) CheckAvailability(ctx context.Context) (Availability, error) {
	runner := b.runner()
	binary := b.binary()

	path, err := runner.LookPath(binary)
	if err != nil {
		status := Availability{Kind: AvailabilityUnavailable, Diagnostic: err.Error()}
		return status, fmt.Errorf("%s: %s not found", status.Kind, binary)
	}

	version, err := runner.Run(ctx, binary, "version")
	if err != nil {
		status := Availability{Kind: AvailabilityIncompatible, Path: path, Diagnostic: commandText(version, err)}
		return status, fmt.Errorf("%s: sbx version failed: %s", status.Kind, status.Diagnostic)
	}
	versionText := strings.TrimSpace(version.Stdout)
	if !strings.HasPrefix(versionText, "sbx version:") {
		status := Availability{Kind: AvailabilityIncompatible, Path: path, Version: versionText, Diagnostic: versionText}
		return status, fmt.Errorf("%s: unexpected sbx version output", status.Kind)
	}

	diagnose, err := runner.Run(ctx, binary, "ls")
	if err != nil {
		status := Availability{
			Kind:       AvailabilityUnavailable,
			Path:       path,
			Version:    versionText,
			Diagnostic: commandText(diagnose, err),
		}
		return status, fmt.Errorf("%s: sbx list failed: %s", status.Kind, status.Diagnostic)
	}

	return Availability{
		OK:         true,
		Kind:       AvailabilityAvailable,
		Path:       path,
		Version:    versionText,
		Diagnostic: strings.TrimSpace(diagnose.Stdout),
	}, nil
}

func (b DockerSandbox) Probe(ctx context.Context, cfg config.Config) (ProbeResult, error) {
	runner := b.runner()
	binary := b.binary()
	probeName := cfg.Sandbox.ProbeName

	create := []string{"create", "--name", probeName, "shell", cfg.Workspace.Mount}
	if cfg.Workspace.UseCloneMode {
		create = []string{"create", "--clone", "--name", probeName, "shell", cfg.Workspace.Mount}
	}
	if result, err := runner.Run(ctx, binary, create...); err != nil {
		return b.finishProbe(ctx, cfg, ProbeResult{}, fmt.Errorf("create probe sandbox: %s", commandText(result, err)))
	}

	env, err := b.execProbe(ctx, probeName, "-e", "TZ="+cfg.Environment.Timezone, "-e", "LANG="+cfg.Environment.Locale, "-e", "LC_ALL="+cfg.Environment.Locale, "env")
	if err != nil {
		return b.finishProbe(ctx, cfg, ProbeResult{}, err)
	}
	pwd, err := b.execProbe(ctx, probeName, "pwd")
	if err != nil {
		return b.finishProbe(ctx, cfg, ProbeResult{}, err)
	}
	mounts, err := b.execProbe(ctx, probeName, "mount")
	if err != nil {
		return b.finishProbe(ctx, cfg, ProbeResult{}, err)
	}
	date, err := b.execProbe(ctx, probeName, "-e", "TZ="+cfg.Environment.Timezone, "date")
	if err != nil {
		return b.finishProbe(ctx, cfg, ProbeResult{}, err)
	}
	locale, err := b.execProbe(ctx, probeName, "-e", "LANG="+cfg.Environment.Locale, "-e", "LC_ALL="+cfg.Environment.Locale, "locale")
	if err != nil {
		return b.finishProbe(ctx, cfg, ProbeResult{}, err)
	}
	egressText, err := b.execProbe(ctx, probeName, "curl", "-fsS", cfg.Network.EgressIP.SandboxCheckURL)
	if err != nil {
		return b.finishProbe(ctx, cfg, ProbeResult{}, err)
	}

	egress, err := compareSandboxEgress(cfg.Network.EgressIP, egressText)
	if err != nil {
		return b.finishProbe(ctx, cfg, ProbeResult{Egress: egress}, err)
	}

	inspection := InspectionObservation{
		Environment:      parseEnv(env),
		WorkingDirectory: strings.TrimSpace(pwd),
		Mounts:           strings.TrimSpace(mounts),
		Date:             strings.TrimSpace(date),
		Locale:           strings.TrimSpace(locale),
		EgressIP:         egress.ObservedIP,
	}
	result := ProbeResult{
		Egress:     egress,
		Inspection: inspection,
	}
	if err := policy.ValidateInspection(policy.InspectionPolicy{
		Workspace: policy.WorkspacePolicy{
			Mount:          cfg.Workspace.Mount,
			ForbiddenPaths: cfg.Workspace.ForbiddenPaths,
		},
		Timezone:                cfg.Environment.Timezone,
		Locale:                  cfg.Environment.Locale,
		AllowSSHAgentForwarding: cfg.Environment.AllowSSHAgentForwarding,
		ForbiddenEnvVars:        cfg.Environment.ForbiddenEnvVars,
		HerdrRuntime:            herdrRuntimePolicy(cfg),
	}, policy.InspectionObservation{
		Environment:      inspection.Environment,
		WorkingDirectory: inspection.WorkingDirectory,
		Mounts:           inspection.Mounts,
		Date:             inspection.Date,
		Locale:           inspection.Locale,
	}); err != nil {
		return b.finishProbe(ctx, cfg, result, fmt.Errorf("sandbox inspection invalid: %w", err))
	}

	return b.finishProbe(ctx, cfg, result, nil)
}

func herdrRuntimePolicy(cfg config.Config) policy.HerdrRuntimePolicy {
	if cfg.Sandbox.Supervision.Mode != "sandbox-local-herdr" || cfg.Sandbox.Supervision.Herdr == nil {
		return policy.HerdrRuntimePolicy{}
	}
	return policy.HerdrRuntimePolicy{
		Enabled:    true,
		SocketPath: strings.TrimSpace(cfg.Sandbox.Supervision.Herdr.SocketPath),
		PaneID:     strings.TrimSpace(cfg.Sandbox.Supervision.Herdr.PaneID),
	}
}

func (b DockerSandbox) CleanupProbe(ctx context.Context, cfg config.Config) error {
	if !b.cleanupProbe(ctx, cfg) {
		return fmt.Errorf("cleanup probe sandbox %q failed", cfg.Sandbox.ProbeName)
	}
	return nil
}

func (b DockerSandbox) CleanupMain(ctx context.Context, cfg config.Config) error {
	runner := b.runner()
	binary := b.binary()
	mainName := cfg.Sandbox.MainName
	var cleanupErr error

	if cfg.Sandbox.Supervision.Mode == "sandbox-local-herdr" {
		if result, err := runner.Run(ctx, binary, "exec", mainName, "herdr", "server", "stop"); err != nil && !isBenignHerdrCleanup(result) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stop sandbox-local Herdr server in %q failed: %s", mainName, commandText(result, err)))
		}
	}

	if cfg.Cleanup.StopMainSandbox {
		if result, err := runner.Run(ctx, binary, "stop", mainName); err != nil && !isNotFoundCleanup(result) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stop main sandbox %q failed: %s", mainName, commandText(result, err)))
		}
	}
	if cfg.Cleanup.RemoveMainSandbox {
		if result, err := runner.Run(ctx, binary, "rm", "--force", mainName); err != nil && !isNotFoundCleanup(result) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove main sandbox %q failed: %s", mainName, commandText(result, err)))
		}
	}
	return cleanupErr
}

func (b DockerSandbox) StartMain(ctx context.Context, plan StartPlan) (StartResult, error) {
	if plan.Supervision.Mode == "sandbox-local-herdr" {
		return b.startSandboxLocalHerdr(ctx, plan)
	}
	args := startMainArgs(plan)
	result, err := b.runner().Run(ctx, b.binary(), args...)
	start := StartResult{
		SandboxName: plan.SandboxName,
		Agent:       plan.Agent,
		Workspace:   plan.Workspace,
		Timezone:    plan.Timezone,
		Locale:      plan.Locale,
	}
	if err != nil {
		return start, fmt.Errorf("start main sandbox: %s", commandText(result, err))
	}
	return start, nil
}

func (b DockerSandbox) StartMainAttached(ctx context.Context, plan StartPlan, stdin io.Reader, stdout, stderr io.Writer) (StartResult, <-chan error, error) {
	start := StartResult{
		SandboxName: plan.SandboxName,
		Agent:       plan.Agent,
		Workspace:   plan.Workspace,
		Timezone:    plan.Timezone,
		Locale:      plan.Locale,
	}
	if plan.Supervision.Mode == "sandbox-local-herdr" {
		wait, err := b.startSandboxLocalHerdrAttached(ctx, plan, stdin, stdout, stderr)
		if err != nil {
			if shouldCleanupStartedMain(err) {
				b.cleanupStartedMain(context.Background(), plan)
			}
			return start, nil, err
		}
		return start, wait, nil
	}

	wait, err := b.startAttachedCommand(ctx, startMainArgs(plan), "start main sandbox", stdin, stdout, stderr)
	if err != nil {
		return start, nil, err
	}
	return start, wait, nil
}

func (b DockerSandbox) StartHerdrTUIAttached(ctx context.Context, plan StartPlan, stdin io.Reader, stdout, stderr io.Writer) (StartResult, <-chan error, error) {
	start := StartResult{
		SandboxName: plan.SandboxName,
		Agent:       plan.Agent,
		Workspace:   plan.Workspace,
		Timezone:    plan.Timezone,
		Locale:      plan.Locale,
	}
	if plan.Supervision.Mode != "sandbox-local-herdr" {
		return start, nil, fmt.Errorf("start Herdr TUI: sandbox-local-herdr supervision required")
	}
	if err := b.prepareSandboxLocalHerdr(ctx, plan); err != nil {
		if shouldCleanupStartedMain(err) {
			b.cleanupStartedMain(context.Background(), plan)
		}
		return start, nil, err
	}
	if err := b.ensureSandboxLocalCC(ctx, plan); err != nil {
		b.cleanupStartedMain(context.Background(), plan)
		return start, nil, err
	}
	wait, err := b.startAttachedCommand(ctx, []string{"exec", "-it", plan.SandboxName, "herdr"}, "start sandbox-local Herdr TUI", stdin, stdout, stderr)
	if err != nil {
		b.cleanupStartedMain(context.Background(), plan)
		return start, nil, err
	}
	return start, wait, nil
}

func startMainArgs(plan StartPlan) []string {
	args := []string{"run", plan.Agent, "--name", plan.SandboxName, plan.Workspace}
	if plan.UseCloneMode {
		args = []string{"run", "--clone", plan.Agent, "--name", plan.SandboxName, plan.Workspace}
	}
	return args
}

func (b DockerSandbox) startSandboxLocalHerdr(ctx context.Context, plan StartPlan) (StartResult, error) {
	start := StartResult{
		SandboxName: plan.SandboxName,
		Agent:       plan.Agent,
		Workspace:   plan.Workspace,
		Timezone:    plan.Timezone,
		Locale:      plan.Locale,
	}
	if err := b.prepareSandboxLocalHerdr(ctx, plan); err != nil {
		if shouldCleanupStartedMain(err) {
			b.cleanupStartedMain(context.Background(), plan)
		}
		return start, err
	}
	for _, args := range [][]string{
		{"exec", plan.SandboxName, "herdr", "server"},
	} {
		result, err := b.runner().Run(ctx, b.binary(), args...)
		if err != nil {
			b.cleanupStartedMain(context.Background(), plan)
			return start, fmt.Errorf("start main sandbox: %s", commandText(result, err))
		}
	}
	if err := b.waitSandboxLocalHerdrReady(ctx, plan); err != nil {
		b.cleanupStartedMain(context.Background(), plan)
		return start, err
	}
	result, err := b.runner().Run(ctx, b.binary(), herdrClaudeArgs(plan)...)
	if err != nil {
		b.cleanupStartedMain(context.Background(), plan)
		return start, fmt.Errorf("start main sandbox: %s", commandText(result, err))
	}
	return start, nil
}

func (b DockerSandbox) startSandboxLocalHerdrAttached(ctx context.Context, plan StartPlan, stdin io.Reader, stdout, stderr io.Writer) (<-chan error, error) {
	if err := b.prepareSandboxLocalHerdr(ctx, plan); err != nil {
		return nil, err
	}
	herdrWait, err := b.startAttachedCommand(ctx, []string{"exec", plan.SandboxName, "herdr", "server"}, "start sandbox-local Herdr server", nil, stdout, stderr)
	if err != nil {
		return nil, err
	}
	if err := b.waitSandboxLocalHerdrReady(ctx, plan); err != nil {
		b.stopSandboxLocalHerdr(context.Background(), plan.SandboxName)
		return nil, err
	}
	claudeWait, err := b.startAttachedCommand(ctx, herdrClaudeArgs(plan), "start main sandbox", stdin, stdout, stderr)
	if err != nil {
		b.stopSandboxLocalHerdr(context.Background(), plan.SandboxName)
		return nil, err
	}
	return combineHerdrAndClaudeWait(herdrWait, claudeWait), nil
}

func (b DockerSandbox) prepareSandboxLocalHerdr(ctx context.Context, plan StartPlan) error {
	runner := b.runner()
	binary := b.binary()
	if err := b.ensureFreshMainSandbox(ctx, plan); err != nil {
		return err
	}
	if result, err := runner.Run(ctx, binary, "exec", plan.SandboxName, "command", "-v", "herdr"); err != nil {
		if !plan.Supervision.Herdr.InstallIfMissing {
			return fmt.Errorf("sandbox-local Herdr unavailable: %s", commandText(result, err))
		}
		if installErr := b.installSandboxLocalHerdr(ctx, plan); installErr != nil {
			return installErr
		}
	}
	if result, err := runner.Run(ctx, binary, "exec", plan.SandboxName, "herdr", "--version"); err != nil {
		return fmt.Errorf("verify sandbox-local Herdr: %s", commandText(result, err))
	}
	if result, err := runner.Run(ctx, binary, "exec", plan.SandboxName, "herdr", "integration", "install", "claude"); err != nil {
		return fmt.Errorf("install Claude Herdr integration: %s", commandText(result, err))
	}
	return nil
}

func (b DockerSandbox) ensureSandboxLocalCC(ctx context.Context, plan StartPlan) error {
	script := `command -v cc >/dev/null 2>&1 || { printf '%s\n' '#!/bin/sh' 'exec claude "$@"' > /usr/local/bin/cc && chmod +x /usr/local/bin/cc; }; command -v cc`
	result, err := b.runner().Run(ctx, b.binary(), "exec", plan.SandboxName, "sh", "-lc", script)
	if err != nil {
		return fmt.Errorf("ensure sandbox-local cc: %s", commandText(result, err))
	}
	return nil
}

func (b DockerSandbox) installSandboxLocalHerdr(ctx context.Context, plan StartPlan) error {
	timeout := plan.Supervision.Herdr.InstallTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	attempts := plan.Supervision.Herdr.InstallAttempts
	if attempts <= 0 {
		attempts = sandboxLocalHerdrInstallAttempts
	}

	var last string
	for attempt := 1; attempt <= attempts; attempt++ {
		installCtx, cancel := context.WithTimeout(ctx, timeout)
		result, err := b.runner().Run(installCtx, b.binary(), "exec", plan.SandboxName, "sh", "-lc", "curl -fsSL https://herdr.dev/install.sh | sh")
		cancel()
		if err == nil {
			return nil
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(installCtx.Err(), context.DeadlineExceeded) {
			last = fmt.Sprintf("attempt %d/%d timed out after %s: %s", attempt, attempts, timeout, commandText(result, err))
		} else {
			last = fmt.Sprintf("attempt %d/%d failed: %s", attempt, attempts, commandText(result, err))
		}
		if ctx.Err() != nil {
			break
		}
	}
	return fmt.Errorf("install sandbox-local Herdr failed after %d attempt(s); last error: %s", attempts, last)
}

type herdrServerStatus struct {
	Running bool
	Socket  string
}

func (b DockerSandbox) waitSandboxLocalHerdrReady(ctx context.Context, plan StartPlan) error {
	timeout := plan.Supervision.Herdr.ReadinessTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastNotReady error
	for {
		result, err := b.runner().Run(readyCtx, b.binary(), "exec", plan.SandboxName, "herdr", "status", "server", "--json")
		if err != nil {
			return fmt.Errorf("wait for sandbox-local Herdr readiness: %s", commandText(result, err))
		}
		status, err := parseHerdrServerStatus(result.Stdout)
		if err != nil {
			return fmt.Errorf("wait for sandbox-local Herdr readiness: %w", err)
		}
		if status.Running {
			if status.Socket != plan.Supervision.Herdr.SocketPath {
				return fmt.Errorf("wait for sandbox-local Herdr readiness: socket path mismatch")
			}
			return nil
		}
		lastNotReady = errors.New("server is not running")

		select {
		case <-readyCtx.Done():
			return fmt.Errorf("wait for sandbox-local Herdr readiness timed out: %w", lastNotReady)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func parseHerdrServerStatus(output string) (herdrServerStatus, error) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		return herdrServerStatus{}, fmt.Errorf("parse status JSON: %w", err)
	}

	status := herdrServerStatus{}
	if running, ok := raw["running"].(bool); ok {
		status.Running = running
	} else if text, ok := raw["status"].(string); ok {
		status.Running = strings.EqualFold(strings.TrimSpace(text), "running")
	}
	for _, key := range []string{"socket", "socket_path"} {
		if socket, ok := raw[key].(string); ok {
			status.Socket = strings.TrimSpace(socket)
			break
		}
	}
	if status.Socket == "" {
		return herdrServerStatus{}, fmt.Errorf("status JSON missing socket path")
	}
	return status, nil
}

func (b DockerSandbox) ensureFreshMainSandbox(ctx context.Context, plan StartPlan) error {
	runner := b.runner()
	binary := b.binary()

	state, err := b.inspectMainSandbox(ctx, plan.SandboxName)
	if err != nil {
		return preserveExistingMainError{err: err}
	}
	if state.Exists {
		switch state.Status {
		case "stopped":
			if result, err := runner.Run(ctx, binary, "stop", plan.SandboxName); err != nil && !isNotFoundCleanup(result) {
				return preserveExistingMainError{err: fmt.Errorf("prepare existing main sandbox: stop %q: %s", plan.SandboxName, commandText(result, err))}
			}
			if result, err := runner.Run(ctx, binary, "rm", "--force", plan.SandboxName); err != nil && !isNotFoundCleanup(result) {
				return preserveExistingMainError{err: fmt.Errorf("prepare existing main sandbox: remove %q: %s", plan.SandboxName, commandText(result, err))}
			}
		default:
			return preserveExistingMainError{err: fmt.Errorf("prepare existing main sandbox: %q has unsafe status %q", plan.SandboxName, state.Status)}
		}
	}

	if result, err := runner.Run(ctx, binary, createMainArgs(plan)...); err != nil {
		return fmt.Errorf("create main sandbox: %s", commandText(result, err))
	}
	return nil
}

type mainSandboxState struct {
	Exists bool
	Status string
}

type preserveExistingMainError struct {
	err error
}

func (e preserveExistingMainError) Error() string {
	return e.err.Error()
}

func (e preserveExistingMainError) Unwrap() error {
	return e.err
}

func shouldCleanupStartedMain(err error) bool {
	if err == nil {
		return false
	}
	var preserve preserveExistingMainError
	return !errors.As(err, &preserve)
}

// ShouldCleanupMainAfterStartError reports whether a failed StartMainAttached
// attempt created or started main sandbox state that the launcher should clean up.
func ShouldCleanupMainAfterStartError(err error) bool {
	return shouldCleanupStartedMain(err)
}

func (b DockerSandbox) inspectMainSandbox(ctx context.Context, sandboxName string) (mainSandboxState, error) {
	result, err := b.runner().Run(ctx, b.binary(), "ls")
	if err != nil {
		return mainSandboxState{}, fmt.Errorf("inspect existing main sandbox: %s", commandText(result, err))
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != sandboxName {
			continue
		}
		if len(fields) < 3 {
			return mainSandboxState{Exists: true}, fmt.Errorf("inspect existing main sandbox: %q status unavailable", sandboxName)
		}
		return mainSandboxState{Exists: true, Status: strings.ToLower(fields[2])}, nil
	}
	return mainSandboxState{}, nil
}

func createMainArgs(plan StartPlan) []string {
	args := []string{"create", "--name", plan.SandboxName, "claude", plan.Workspace}
	if plan.UseCloneMode {
		args = []string{"create", "--clone", "--name", plan.SandboxName, "claude", plan.Workspace}
	}
	return args
}

func herdrClaudeArgs(plan StartPlan) []string {
	return []string{
		"exec",
		"-e", "HERDR_ENV=1",
		"-e", "HERDR_SOCKET_PATH=" + plan.Supervision.Herdr.SocketPath,
		"-e", "HERDR_PANE_ID=" + plan.Supervision.Herdr.PaneID,
		plan.SandboxName,
		plan.Agent,
	}
}

func (b DockerSandbox) startAttachedCommand(ctx context.Context, args []string, label string, stdin io.Reader, stdout, stderr io.Writer) (<-chan error, error) {
	cmd := exec.CommandContext(ctx, b.binary(), args...)
	out := new(strings.Builder)
	errOut := new(strings.Builder)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	if stdinFile, stdinOK := stdin.(*os.File); stdinOK {
		if stdoutFile, stdoutOK := stdout.(*os.File); stdoutOK {
			if stderrFile, stderrOK := stderr.(*os.File); stderrOK {
				cmd.Stdin = stdinFile
				cmd.Stdout = stdoutFile
				cmd.Stderr = stderrFile
			}
		}
	}
	if cmd.Stdout == nil {
		if stdout != nil {
			cmd.Stdout = io.MultiWriter(stdout, out)
		} else {
			cmd.Stdout = out
		}
	}
	if cmd.Stderr == nil {
		if stderr != nil {
			cmd.Stderr = io.MultiWriter(stderr, errOut)
		} else {
			cmd.Stderr = errOut
		}
	}
	cmd.Env = sbxProcessEnv()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}

	wait := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if err != nil {
			result := CommandResult{ExitCode: exitCode(err), Stdout: out.String(), Stderr: errOut.String()}
			wait <- fmt.Errorf("%s: %s", label, commandText(result, err))
			return
		}
		wait <- nil
	}()

	select {
	case err := <-wait:
		if err != nil {
			return nil, err
		}
		done := make(chan error, 1)
		done <- nil
		return done, nil
	case <-time.After(100 * time.Millisecond):
		return wait, nil
	}
}

func combineHerdrAndClaudeWait(herdrWait, claudeWait <-chan error) <-chan error {
	wait := make(chan error, 1)
	go func() {
		select {
		case err := <-claudeWait:
			wait <- err
		case err := <-herdrWait:
			if err != nil {
				wait <- err
				return
			}
			wait <- fmt.Errorf("sandbox-local Herdr server exited before Claude")
		}
	}()
	return wait
}

func (b DockerSandbox) cleanupStartedMain(ctx context.Context, plan StartPlan) {
	b.stopSandboxLocalHerdr(ctx, plan.SandboxName)
	_, _ = b.runner().Run(ctx, b.binary(), "stop", plan.SandboxName)
}

func (b DockerSandbox) stopSandboxLocalHerdr(ctx context.Context, sandboxName string) {
	_, _ = b.runner().Run(ctx, b.binary(), "exec", sandboxName, "herdr", "server", "stop")
}

func (b DockerSandbox) finishProbe(ctx context.Context, cfg config.Config, result ProbeResult, err error) (ProbeResult, error) {
	if !cfg.Cleanup.RemoveProbeSandbox {
		return result, err
	}
	cleanupCtx, cancel := CleanupTimeoutContext(cfg.Network.EgressIP.TimeoutSeconds)
	defer cancel()

	result.CleanupDone = b.cleanupProbe(cleanupCtx, cfg)
	if result.CleanupDone {
		return result, err
	}
	if err != nil {
		return result, fmt.Errorf("%w; cleanup probe sandbox failed", err)
	}
	return result, fmt.Errorf("cleanup probe sandbox failed")
}

func (b DockerSandbox) cleanupProbe(ctx context.Context, cfg config.Config) bool {
	if !cfg.Cleanup.RemoveProbeSandbox {
		return true
	}
	probeName := cfg.Sandbox.ProbeName
	runner := b.runner()
	binary := b.binary()

	if result, err := runner.Run(ctx, binary, "stop", probeName); err != nil && !isNotFoundCleanup(result) {
		return false
	}
	if result, err := runner.Run(ctx, binary, "rm", "--force", probeName); err != nil && !isNotFoundCleanup(result) {
		return false
	}
	return true
}

func (b DockerSandbox) execProbe(ctx context.Context, probeName string, args ...string) (string, error) {
	command := []string{"exec"}
	commandNameIndex := 0
	for commandNameIndex < len(args) && strings.HasPrefix(args[commandNameIndex], "-") {
		command = append(command, args[commandNameIndex])
		commandNameIndex++
		if commandNameIndex < len(args) {
			command = append(command, args[commandNameIndex])
			commandNameIndex++
		}
	}
	command = append(command, probeName)
	command = append(command, args[commandNameIndex:]...)

	result, err := b.runner().Run(ctx, b.binary(), command...)
	if err != nil {
		if commandName(args) == "env" {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return "", fmt.Errorf("probe command failed: env: %w", err)
			}
			return "", fmt.Errorf("probe command failed: env: %s", commandErrorText(result, err))
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("probe command failed: %w", err)
		}
		return "", fmt.Errorf("probe command failed: %s", commandText(result, err))
	}
	return result.Stdout, nil
}

func (b DockerSandbox) runner() Runner {
	if b.Runner != nil {
		return b.Runner
	}
	return ExecRunner{}
}

func (b DockerSandbox) binary() string {
	if b.Binary != "" {
		return b.Binary
	}
	return "sbx"
}

type ExecRunner struct{}

func (ExecRunner) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout := new(strings.Builder)
	stderr := new(strings.Builder)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = sbxProcessEnv()

	err := cmd.Run()
	result := CommandResult{
		ExitCode: exitCode(err),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
	return result, err
}

func compareSandboxEgress(policy config.EgressIP, observedText string) (network.EgressResult, error) {
	observedRaw := strings.TrimSpace(observedText)
	observed, err := netip.ParseAddr(observedRaw)
	if err != nil {
		return sandboxEgressFail(policy, "sandbox-egress-response-parse-failure", observedRaw, "sandbox egress response is not an IP address")
	}
	expected, err := netip.ParseAddr(strings.TrimSpace(policy.ExpectedIP))
	if err != nil {
		return sandboxEgressFail(policy, "sandbox-egress-response-parse-failure", observedRaw, "configured expected sandbox egress IP is not an IP address")
	}
	if observed != expected {
		return sandboxEgressFail(policy, "sandbox-egress-mismatch", observed.String(), "sandbox egress observed IP %s does not match expected IP %s", observed.String(), expected.String())
	}
	return network.EgressResult{
		OK:         true,
		ExpectedIP: expected.String(),
		ObservedIP: observed.String(),
	}, nil
}

func sandboxEgressFail(policy config.EgressIP, kind network.EgressFailureKind, observed, reason string, args ...any) (network.EgressResult, error) {
	result := network.EgressResult{
		OK:            false,
		ExpectedIP:    strings.TrimSpace(policy.ExpectedIP),
		ObservedIP:    observed,
		FailureKind:   kind,
		FailureReason: fmt.Sprintf(reason, args...),
	}
	return result, fmt.Errorf("%s: %s", kind, result.FailureReason)
}

func parseEnv(output string) map[string]string {
	env := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		name, value, ok := strings.Cut(line, "=")
		if ok && name != "" {
			env[name] = value
		}
	}
	return env
}

func isNotFoundCleanup(result CommandResult) bool {
	text := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	return strings.Contains(text, "not found") || strings.Contains(text, "no such sandbox")
}

func isBenignHerdrCleanup(result CommandResult) bool {
	text := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	return strings.Contains(text, "not found") ||
		strings.Contains(text, "no such sandbox") ||
		strings.Contains(text, "not running") ||
		strings.Contains(text, "command not found")
}

func commandText(result CommandResult, err error) string {
	parts := []string{}
	if err != nil {
		parts = append(parts, err.Error())
	}
	if strings.TrimSpace(result.Stdout) != "" {
		parts = append(parts, strings.TrimSpace(result.Stdout))
	}
	if strings.TrimSpace(result.Stderr) != "" {
		parts = append(parts, strings.TrimSpace(result.Stderr))
	}
	return strings.Join(parts, ": ")
}

func commandErrorText(result CommandResult, err error) string {
	parts := []string{}
	if err != nil {
		parts = append(parts, err.Error())
	}
	if strings.TrimSpace(result.Stderr) != "" {
		parts = append(parts, strings.TrimSpace(result.Stderr))
	}
	return strings.Join(parts, ": ")
}

func commandName(args []string) string {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		i++
		if i < len(args) {
			i++
		}
	}
	if i >= len(args) {
		return ""
	}
	return args[i]
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return 124
	}
	return 1
}

func TimeoutContext(seconds int) (context.Context, context.CancelFunc) {
	if seconds <= 0 {
		seconds = 30
	}
	return context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
}

func CleanupTimeoutContext(seconds int) (context.Context, context.CancelFunc) {
	if seconds <= 0 {
		seconds = 30
	}
	return context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
}

func sbxProcessEnv() []string {
	allowed := map[string]bool{
		"HOME":    true,
		"LOGNAME": true,
		"PATH":    true,
		"SHELL":   true,
		"TERM":    true,
		"TMPDIR":  true,
		"USER":    true,
	}
	env := make([]string, 0, len(allowed))
	seen := make(map[string]bool, len(allowed))
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || !allowed[name] || seen[name] {
			continue
		}
		seen[name] = true
		env = append(env, entry)
	}
	return env
}
