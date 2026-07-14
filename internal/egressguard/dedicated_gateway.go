package egressguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/policy"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/watchdog"
)

const (
	DedicatedDaemonEventSource      = "dedicated-daemon"
	DedicatedHealthEventSource      = "dedicated-health"
	defaultDedicatedRuntimeInterval = 5 * time.Second
)

type commandResult struct {
	stdout string
}

type commandExecutor interface {
	Run(ctx context.Context, env []string, name string, args ...string) (commandResult, error)
	Start(env []string, name string, args ...string) (runningProcess, error)
}

type runningProcess interface {
	Wait() error
	Kill() error
}

type protocolCapabilityCheck func(context.Context) error

type dedicatedGatewayAdapter struct {
	cfg                     config.Config
	main                    MainSandbox
	executor                commandExecutor
	client                  *http.Client
	checkProtocolCapability protocolCapabilityCheck

	mu              sync.Mutex
	process         runningProcess
	processExit     <-chan error
	acquired        bool
	fenced          bool
	runtimeInterval time.Duration
}

func newDedicatedGatewayAdapter(cfg config.Config, main MainSandbox, executor commandExecutor, client *http.Client) EgressGuard {
	return newDedicatedGatewayAdapterWithProtocolCheck(
		cfg,
		main,
		executor,
		client,
		dockerSandboxProtocolCheck(executor),
	)
}

func newDedicatedGatewayAdapterWithProtocolCheck(
	cfg config.Config,
	main MainSandbox,
	executor commandExecutor,
	client *http.Client,
	check protocolCapabilityCheck,
) EgressGuard {
	return &dedicatedGatewayAdapter{
		cfg:                     cfg,
		main:                    main,
		executor:                executor,
		client:                  client,
		checkProtocolCapability: check,
	}
}

func (a *dedicatedGatewayAdapter) Acquire(ctx context.Context) (Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.acquired {
		return Result{}, fmt.Errorf("sandboxd lease invalid: lease already acquired")
	}
	if err := a.checkProtocolCapability(ctx); err != nil {
		return Result{}, err
	}
	if err := a.checkController(ctx); err != nil {
		return Result{}, err
	}

	list, err := a.executor.Run(ctx, commandEnvironment(""), "sbx", "ls")
	if err != nil {
		return Result{}, fmt.Errorf("sandboxd lease invalid: inspect running sandboxes failed")
	}
	scope, err := inspectLeaseScope(list.stdout, a.cfg.Sandbox.MainName, a.cfg.Sandbox.Agent, a.cfg.Workspace.Mount)
	if err != nil {
		return Result{}, fmt.Errorf("sandboxd lease invalid: %w", err)
	}
	if len(scope.conflicts) > 0 {
		return Result{}, fmt.Errorf("sandboxd lease invalid: unrelated sandbox conflict: %s", strings.Join(scope.conflicts, ", "))
	}
	if _, err := a.executor.Run(ctx, commandEnvironment(""), "sbx", "daemon", "stop"); err != nil {
		return Result{}, fmt.Errorf("sandboxd lease invalid: stop existing daemon failed")
	}
	a.acquired = true
	a.fenced = false

	upstream := a.cfg.Network.Egress.DedicatedGateway.UpstreamURL
	process, err := a.executor.Start(commandEnvironment(upstream), "sbx", "daemon", "start")
	if err != nil {
		restoreErr := a.restoreAfterFailedAcquire()
		return Result{}, errors.Join(fmt.Errorf("sandboxd lease invalid: start dedicated daemon failed"), restoreErr)
	}
	exit := make(chan error, 1)
	go func() {
		exit <- process.Wait()
		close(exit)
	}()
	a.process = process
	a.processExit = exit
	if err := a.waitForDaemon(ctx); err != nil {
		return Result{}, errors.Join(err, a.restoreAfterFailedAcquire())
	}
	if scope.mainWasRunning {
		if _, err := a.executor.Run(ctx, commandEnvironment(""), "sbx", "exec", a.cfg.Sandbox.MainName, "true"); err != nil {
			return Result{}, errors.Join(
				fmt.Errorf("sandboxd lease invalid: restore configured main under dedicated daemon failed"),
				a.restoreAfterFailedAcquire(),
			)
		}
	}
	a.acquired = true
	return Result{
		Messages: []string{
			"gateway controller ok: authenticated loopback controller",
			"sandboxd lease ok: exclusive command-scoped upstream active",
		},
		CleanupCreatedMain: true,
	}, nil
}

var sbxVersionPattern = regexp.MustCompile(`(?m)\bsbx version:\s+(v[0-9]+\.[0-9]+\.[0-9]+)\b`)

func dockerSandboxProtocolCheck(executor commandExecutor) protocolCapabilityCheck {
	return func(ctx context.Context) error {
		result, err := executor.Run(ctx, commandEnvironment(""), "sbx", "version")
		if err != nil {
			return fmt.Errorf("dedicated protocol isolation unsupported: cannot inspect Docker Sandbox version")
		}
		matches := sbxVersionPattern.FindStringSubmatch(result.stdout)
		if len(matches) != 2 {
			return fmt.Errorf("dedicated protocol isolation unsupported: unrecognized Docker Sandbox version output")
		}

		return validateDockerSandboxProtocolSupport(matches[1])
	}
}

func validateDockerSandboxProtocolSupport(version string) error {
	// No released Docker Sandbox version has a validated upstream contract for
	// managed HTTP(S), generic TCP, and DNS together.
	switch version {
	case "v0.34.0":
		return fmt.Errorf(
			"dedicated protocol isolation unsupported: sbx %s provides HTTP upstream only; generic TCP and DNS are not fail closed",
			version,
		)
	default:
		return fmt.Errorf(
			"dedicated protocol isolation unsupported: sbx %s has no validated generic TCP and DNS contract",
			version,
		)
	}
}

func (a *dedicatedGatewayAdapter) ValidateMain(ctx context.Context) (Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.acquired {
		return Result{}, fmt.Errorf("sandboxd lease invalid: lease is not active")
	}
	select {
	case err := <-a.processExit:
		return Result{}, fmt.Errorf("sandboxd lease invalid: dedicated daemon exited: %v", err)
	default:
	}
	if _, err := a.executor.Run(ctx, commandEnvironment(""), "sbx", "daemon", "status"); err != nil {
		return Result{}, fmt.Errorf("sandboxd lease invalid: dedicated daemon status failed")
	}
	list, err := a.executor.Run(ctx, commandEnvironment(""), "sbx", "ls")
	if err != nil {
		return Result{}, fmt.Errorf("sandboxd lease invalid: inspect running sandboxes failed")
	}
	scope, err := inspectLeaseScope(list.stdout, a.cfg.Sandbox.MainName, a.cfg.Sandbox.Agent, a.cfg.Workspace.Mount)
	if err != nil {
		return Result{}, fmt.Errorf("sandboxd lease invalid: %w", err)
	}
	if len(scope.conflicts) > 0 {
		return Result{}, fmt.Errorf("sandboxd lease invalid: unrelated sandbox conflict: %s", strings.Join(scope.conflicts, ", "))
	}
	if err := a.checkController(ctx); err != nil {
		return Result{}, err
	}
	if a.main == nil {
		return Result{}, fmt.Errorf("controller isolation invalid: main sandbox inspector unavailable")
	}
	endpoint := a.cfg.Network.Egress.DedicatedGateway.ControllerURL
	if err := a.main.CheckMainEndpointIsolation(ctx, a.cfg.Sandbox.MainName, endpoint); err != nil {
		return Result{}, fmt.Errorf("controller isolation invalid: %w", err)
	}
	return Result{Messages: []string{"controller isolation ok: endpoint unreachable from main sandbox"}}, nil
}

func (a *dedicatedGatewayAdapter) Watch(ctx context.Context, _ WatchInput) RuntimeWatch {
	a.mu.Lock()
	processExit := a.processExit
	interval := a.runtimeInterval
	a.mu.Unlock()
	if interval <= 0 {
		interval = defaultDedicatedRuntimeInterval
	}
	events := make(chan watchdog.Event, 1)
	go func() {
		defer close(events)
		if processExit == nil {
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case err := <-processExit:
				detail := "dedicated daemon exited"
				if err != nil {
					detail = err.Error()
				}
				select {
				case <-ctx.Done():
				case events <- watchdog.Event{Source: DedicatedDaemonEventSource, Detail: detail}:
				}
				return
			case <-ticker.C:
				select {
				case <-ctx.Done():
					return
				case events <- watchdog.Event{Source: DedicatedHealthEventSource, Detail: "scheduled runtime validation"}:
				}
			}
		}
	}()
	return RuntimeWatch{
		Events:  events,
		Checker: watchdog.CheckFunc(a.checkRuntimeEvent),
	}
}

func (a *dedicatedGatewayAdapter) checkRuntimeEvent(ctx context.Context, event watchdog.Event) (watchdog.CheckResult, error) {
	if event.Source == DedicatedDaemonEventSource {
		return dedicatedRuntimeFailure("sandboxd lease invalid: dedicated daemon exited")
	}
	checkCtx, cancel := context.WithTimeout(ctx, a.cleanupTimeout())
	defer cancel()
	return a.checkRuntimeHealth(checkCtx)
}

func (a *dedicatedGatewayAdapter) checkRuntimeHealth(ctx context.Context) (watchdog.CheckResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.acquired {
		return dedicatedRuntimeFailure("sandboxd lease invalid: lease is not active")
	}
	if _, err := a.executor.Run(ctx, commandEnvironment(""), "sbx", "daemon", "status"); err != nil {
		return dedicatedRuntimeFailure("sandboxd lease invalid: dedicated daemon status failed")
	}
	if err := a.checkController(ctx); err != nil {
		return dedicatedRuntimeFailure("%v", err)
	}
	list, err := a.executor.Run(ctx, commandEnvironment(""), "sbx", "ls")
	if err != nil {
		return dedicatedRuntimeFailure("sandboxd lease invalid: inspect running sandboxes failed")
	}
	scope, err := inspectLeaseScope(list.stdout, a.cfg.Sandbox.MainName, a.cfg.Sandbox.Agent, a.cfg.Workspace.Mount)
	if err != nil {
		return dedicatedRuntimeFailure("sandboxd lease invalid: %v", err)
	}
	if len(scope.conflicts) > 0 {
		return dedicatedRuntimeFailure("sandboxd lease invalid: unrelated sandbox conflict: %s", strings.Join(scope.conflicts, ", "))
	}
	if a.main == nil {
		return dedicatedRuntimeFailure("dedicated egress invalid: main sandbox checker unavailable")
	}
	egress, err := a.main.CheckRuntimeEgress(ctx, a.cfg)
	if err != nil || !egress.OK {
		reason := strings.TrimSpace(egress.FailureReason)
		if reason == "" && err != nil {
			reason = err.Error()
		}
		if reason == "" {
			reason = "unknown dedicated egress failure"
		}
		if egress.FailureKind == "sandbox-egress-mismatch" {
			return dedicatedRuntimeFailure("dedicated egress drift: %s", reason)
		}
		return dedicatedRuntimeFailure("dedicated egress check failed: %s", reason)
	}
	return watchdog.CheckResult{OK: true}, nil
}

func dedicatedRuntimeFailure(format string, args ...any) (watchdog.CheckResult, error) {
	reason := fmt.Sprintf(format, args...)
	return watchdog.CheckResult{OK: false, Reason: reason}, fmt.Errorf("%s", reason)
}

func (a *dedicatedGatewayAdapter) Fence(ctx context.Context) (Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.acquired {
		return Result{}, nil
	}
	if a.fenced {
		return Result{Messages: []string{"sandboxd lease fenced: dedicated egress stopped"}}, nil
	}
	if err := a.stopOwnedDaemon(ctx); err != nil {
		return Result{}, fmt.Errorf("sandboxd lease fence invalid: %w", err)
	}
	a.fenced = true
	return Result{Messages: []string{"sandboxd lease fenced: dedicated egress stopped"}}, nil
}

func (a *dedicatedGatewayAdapter) Recover(ctx context.Context) (Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.acquired {
		return Result{}, nil
	}
	if !a.fenced {
		return Result{}, fmt.Errorf("sandboxd lease recovery invalid: dedicated egress is not fenced")
	}
	if err := a.restoreNormalDaemon(ctx); err != nil {
		return Result{}, fmt.Errorf("sandboxd lease recovery invalid: %w", err)
	}
	a.acquired = false
	a.process = nil
	a.processExit = nil
	a.fenced = false
	return Result{Messages: []string{"sandboxd lease recovered: normal daemon restored with main stopped"}}, nil
}

func (a *dedicatedGatewayAdapter) waitForDaemon(ctx context.Context) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-a.processExit:
			return fmt.Errorf("sandboxd lease invalid: dedicated daemon exited before readiness: %v", err)
		case <-ctx.Done():
			return fmt.Errorf("sandboxd lease invalid: dedicated daemon readiness: %w", ctx.Err())
		case <-ticker.C:
			if _, err := a.executor.Run(ctx, commandEnvironment(""), "sbx", "daemon", "status"); err == nil {
				return nil
			}
		}
	}
}

func (a *dedicatedGatewayAdapter) stopOwnedDaemon(ctx context.Context) error {
	if a.process == nil {
		return nil
	}
	select {
	case <-a.processExit:
		return nil
	default:
	}
	_, stopErr := a.executor.Run(ctx, commandEnvironment(""), "sbx", "daemon", "stop")
	select {
	case <-a.processExit:
		return nil
	case <-ctx.Done():
		killErr := a.process.Kill()
		return errors.Join(stopErr, ctx.Err(), killErr)
	case <-time.After(2 * time.Second):
		killErr := a.process.Kill()
		return errors.Join(stopErr, fmt.Errorf("dedicated daemon did not stop"), killErr)
	}
}

func (a *dedicatedGatewayAdapter) restoreAfterFailedAcquire() error {
	ctx, cancel := context.WithTimeout(context.Background(), a.cleanupTimeout())
	defer cancel()
	stopErr := a.stopOwnedDaemon(ctx)
	if stopErr != nil {
		return fmt.Errorf("restore normal daemon failed: %w", stopErr)
	}
	a.fenced = true
	restoreErr := a.restoreNormalDaemon(ctx)
	if restoreErr == nil {
		a.acquired = false
		a.process = nil
		a.processExit = nil
		a.fenced = false
		return nil
	}
	return fmt.Errorf("restore normal daemon failed: %w", restoreErr)
}

func (a *dedicatedGatewayAdapter) restoreNormalDaemon(ctx context.Context) error {
	if _, err := a.executor.Run(ctx, commandEnvironment(""), "sbx", "ls"); err != nil {
		return fmt.Errorf("start normal daemon: %w", err)
	}
	return nil
}

func (a *dedicatedGatewayAdapter) cleanupTimeout() time.Duration {
	seconds := a.cfg.Network.EgressIP.TimeoutSeconds
	if seconds <= 0 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}

func (a *dedicatedGatewayAdapter) checkController(ctx context.Context) error {
	secretRef := strings.TrimSpace(a.cfg.Network.Egress.DedicatedGateway.ControllerSecretEnv)
	secret := os.Getenv(secretRef)
	if secret == "" {
		return fmt.Errorf("gateway controller invalid: secret reference %s is unset or empty", secretRef)
	}
	endpoint := strings.TrimRight(a.cfg.Network.Egress.DedicatedGateway.ControllerURL, "/") + "/version"

	unauthenticated, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("gateway controller invalid: build health request")
	}
	response, err := a.client.Do(unauthenticated)
	if err != nil {
		return fmt.Errorf("gateway controller invalid: loopback controller unavailable")
	}
	io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("gateway controller invalid: authentication is not enforced")
	}

	authenticated, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("gateway controller invalid: build authenticated request")
	}
	authenticated.Header.Set("Authorization", "Bearer "+secret)
	response, err = a.client.Do(authenticated)
	if err != nil {
		return fmt.Errorf("gateway controller invalid: authenticated health request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("gateway controller invalid: authentication failed")
	}
	var version struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&version); err != nil || strings.TrimSpace(version.Version) == "" {
		return fmt.Errorf("gateway controller invalid: version response is invalid")
	}
	return nil
}

type leaseScope struct {
	mainWasRunning bool
	conflicts      []string
}

func inspectLeaseScope(output, allowedMain, expectedAgent, expectedWorkspace string) (leaseScope, error) {
	var scope leaseScope
	seenMain := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "No sandboxes found." || strings.HasPrefix(line, "SANDBOX ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return leaseScope{}, fmt.Errorf("unrecognized sbx ls output")
		}
		status := strings.ToLower(fields[2])
		if status != "running" && status != "stopped" {
			return leaseScope{}, fmt.Errorf("sandbox %q has unrecognized status %q", fields[0], fields[2])
		}
		if fields[0] == allowedMain {
			if seenMain {
				return leaseScope{}, fmt.Errorf("configured main sandbox %q appears more than once", allowedMain)
			}
			seenMain = true
			if status == "running" {
				if expectedAgent != "" && fields[1] != expectedAgent {
					return leaseScope{}, fmt.Errorf("configured main sandbox %q agent mismatch", allowedMain)
				}
				if len(fields) < 4 || !sameWorkspace(fields[len(fields)-1], expectedWorkspace) {
					return leaseScope{}, fmt.Errorf("configured main sandbox %q workspace mismatch", allowedMain)
				}
				scope.mainWasRunning = true
			}
			continue
		}
		scope.conflicts = append(scope.conflicts, fields[0])
	}
	sort.Strings(scope.conflicts)
	return scope, nil
}

func sameWorkspace(observed, expected string) bool {
	observedPath, observedErr := policy.NormalizeWorkspacePath(observed)
	expectedPath, expectedErr := policy.NormalizeWorkspacePath(expected)
	return observedErr == nil && expectedErr == nil && observedPath == expectedPath
}

func commandEnvironment(upstream string) []string {
	allowed := map[string]bool{
		"HOME": true, "LOGNAME": true, "PATH": true, "SHELL": true,
		"TERM": true, "TMPDIR": true, "USER": true,
	}
	environment := make([]string, 0, len(allowed)+1)
	seen := make(map[string]bool, len(allowed))
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || !allowed[name] || seen[name] {
			continue
		}
		seen[name] = true
		environment = append(environment, entry)
	}
	if upstream != "" {
		environment = append(environment, "DOCKER_SANDBOXES_PROXY="+upstream)
	}
	sort.Strings(environment)
	return environment
}

type osCommandExecutor struct{}

func (osCommandExecutor) Run(ctx context.Context, environment []string, name string, args ...string) (commandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stdout := new(strings.Builder)
	cmd.Stdout = stdout
	cmd.Stderr = io.Discard
	cmd.Env = environment
	err := cmd.Run()
	return commandResult{stdout: stdout.String()}, err
}

func (osCommandExecutor) Start(environment []string, name string, args ...string) (runningProcess, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Env = environment
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return osRunningProcess{cmd: cmd}, nil
}

type osRunningProcess struct {
	cmd *exec.Cmd
}

func (p osRunningProcess) Wait() error {
	return p.cmd.Wait()
}

func (p osRunningProcess) Kill() error {
	return p.cmd.Process.Kill()
}
