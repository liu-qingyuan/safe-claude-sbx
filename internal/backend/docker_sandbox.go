package backend

import (
	"context"
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
}

func NewStartPlan(cfg config.Config) StartPlan {
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
		Timezone:         cfg.Environment.Timezone,
		Locale:           cfg.Environment.Locale,
		ForbiddenEnvVars: cfg.Environment.ForbiddenEnvVars,
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

	if cfg.Cleanup.StopMainSandbox {
		if result, err := runner.Run(ctx, binary, "stop", mainName); err != nil && !isNotFoundCleanup(result) {
			return fmt.Errorf("stop main sandbox %q failed: %s", mainName, commandText(result, err))
		}
	}
	if cfg.Cleanup.RemoveMainSandbox {
		if result, err := runner.Run(ctx, binary, "rm", "--force", mainName); err != nil && !isNotFoundCleanup(result) {
			return fmt.Errorf("remove main sandbox %q failed: %s", mainName, commandText(result, err))
		}
	}
	return nil
}

func (b DockerSandbox) StartMain(ctx context.Context, plan StartPlan) (StartResult, error) {
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

func (b DockerSandbox) StartMainAttached(ctx context.Context, plan StartPlan, stdout, stderr io.Writer) (StartResult, <-chan error, error) {
	args := startMainArgs(plan)
	cmd := exec.CommandContext(ctx, b.binary(), args...)
	out := new(strings.Builder)
	errOut := new(strings.Builder)
	if stdout != nil {
		cmd.Stdout = io.MultiWriter(stdout, out)
	} else {
		cmd.Stdout = out
	}
	if stderr != nil {
		cmd.Stderr = io.MultiWriter(stderr, errOut)
	} else {
		cmd.Stderr = errOut
	}
	cmd.Env = sbxProcessEnv()

	start := StartResult{
		SandboxName: plan.SandboxName,
		Agent:       plan.Agent,
		Workspace:   plan.Workspace,
		Timezone:    plan.Timezone,
		Locale:      plan.Locale,
	}
	if err := cmd.Start(); err != nil {
		return start, nil, fmt.Errorf("start main sandbox: %w", err)
	}

	wait := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		if err != nil {
			result := CommandResult{ExitCode: exitCode(err), Stdout: out.String(), Stderr: errOut.String()}
			wait <- fmt.Errorf("start main sandbox: %s", commandText(result, err))
			return
		}
		wait <- nil
	}()

	select {
	case err := <-wait:
		if err != nil {
			return start, nil, err
		}
		done := make(chan error, 1)
		done <- nil
		return start, done, nil
	case <-time.After(100 * time.Millisecond):
		return start, wait, nil
	}
}

func startMainArgs(plan StartPlan) []string {
	args := []string{"run", plan.Agent, "--name", plan.SandboxName, plan.Workspace}
	if plan.UseCloneMode {
		args = []string{"run", "--clone", plan.Agent, "--name", plan.SandboxName, plan.Workspace}
	}
	return args
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
