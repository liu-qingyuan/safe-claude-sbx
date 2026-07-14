package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/backend"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/egressguard"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/watchdog"
)

type launchTarget int

const (
	mainSandboxTarget launchTarget = iota
	herdrTUITarget
)

type Target int

const (
	DoctorTarget Target = iota
	DirectClaudeTarget
	HerdrTarget
)

type Request struct {
	Target     Target
	ConfigPath string
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

type Runner struct {
	sandbox  backend.DockerSandbox
	newGuard doctorGuardFactory
	platform launchPlatform
}

func NewRunner() Runner {
	return Runner{
		sandbox:  backend.NewDockerSandbox(),
		newGuard: egressguard.New,
		platform: defaultLaunchPlatform(),
	}
}

func (r Runner) Run(request Request) int {
	stdout := request.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := request.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	cfg, err := config.Load(request.ConfigPath)
	if err != nil {
		fmt.Fprintf(stderr, "configuration invalid: %v\n", err)
		return 1
	}

	isDoctor := false
	var target launchTarget
	switch request.Target {
	case DoctorTarget:
		isDoctor = true
	case DirectClaudeTarget:
		target = mainSandboxTarget
	case HerdrTarget:
		if cfg.Sandbox.Supervision.Mode != "sandbox-local-herdr" {
			fmt.Fprintln(stderr, "configuration invalid: safe-herdr requires sandbox.supervision.mode \"sandbox-local-herdr\"")
			return 1
		}
		target = herdrTUITarget
	default:
		fmt.Fprintln(stderr, "configuration invalid: unsupported launcher target")
		return 1
	}
	fmt.Fprintln(stdout, "configuration ok")
	if cfg.Sandbox.Backend != "docker-sandbox" {
		fmt.Fprintf(stderr, "sandbox backend invalid: unsupported backend %q\n", cfg.Sandbox.Backend)
		return 1
	}

	newGuard := r.newGuard
	if newGuard == nil {
		newGuard = egressguard.New
	}
	guard, err := newGuard(cfg, r.sandbox)
	if err != nil {
		fmt.Fprintf(stderr, "egress guard invalid: %v\n", err)
		return 1
	}

	if isDoctor {
		return r.runDoctor(cfg, guard, stdout, stderr)
	}
	return r.runLaunch(cfg, target, guard, request.Stdin, stdout, stderr)
}

func RunSafeClaudeSBX(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	runner := NewRunner()
	if len(args) == 3 && args[0] == "doctor" && args[1] == "--config" {
		return runner.Run(Request{Target: DoctorTarget, ConfigPath: args[2], Stdin: stdin, Stdout: stdout, Stderr: stderr})
	}
	if len(args) == 2 && args[0] == "--config" {
		return runner.Run(Request{Target: DirectClaudeTarget, ConfigPath: args[1], Stdin: stdin, Stdout: stdout, Stderr: stderr})
	}

	fmt.Fprintln(stderr, "usage: safe-claude-sbx [doctor] --config <file>")
	return 2
}

func RunSafeHerdr(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 2 && args[0] == "--config" {
		return NewRunner().Run(Request{Target: HerdrTarget, ConfigPath: args[1], Stdin: stdin, Stdout: stdout, Stderr: stderr})
	}

	fmt.Fprintln(stderr, "usage: safe-herdr --config <file>")
	return 2
}

type doctorGuardFactory func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error)

func (r Runner) runDoctor(cfg config.Config, guard egressguard.EgressGuard, stdout, stderr io.Writer) int {
	sandbox := r.sandbox
	acquireCtx, cancelAcquire := backend.TimeoutContext(cfg.Network.EgressIP.TimeoutSeconds)
	acquisition, err := guard.Acquire(acquireCtx)
	cancelAcquire()
	if err != nil {
		if cleanupErr := finalizeDoctorGuard(guard, sandbox, cfg, false, stdout); cleanupErr != nil {
			fmt.Fprintf(stderr, "%v; cleanup failed: %v\n", err, cleanupErr)
			return 1
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	printEgressGuardMessages(stdout, acquisition)

	ctx, cancel := backend.TimeoutContext(cfg.Network.EgressIP.TimeoutSeconds)
	defer cancel()

	availability, err := sandbox.CheckAvailability(ctx)
	if err != nil {
		cleanupErr := finalizeDoctorGuard(guard, sandbox, cfg, false, stdout)
		if cleanupErr != nil {
			fmt.Fprintf(stderr, "sandbox backend invalid: %s; cleanup failed: %v\n", backendAvailabilityError(availability, err), cleanupErr)
			return 1
		}
		fmt.Fprintf(stderr, "sandbox backend invalid: %s\n", backendAvailabilityError(availability, err))
		return 1
	}
	fmt.Fprintf(stdout, "sandbox backend ok: %s\n", availability.Version)

	preflight, err := sandbox.PreflightMainSandbox(ctx, cfg)
	if err != nil {
		cleanupMain := backend.ShouldCleanupMainAfterStartError(err)
		if cleanupErr := finalizeDoctorGuard(guard, sandbox, cfg, cleanupMain, stdout); cleanupErr != nil {
			fmt.Fprintf(stderr, "main sandbox preflight invalid: %v; cleanup failed: %v\n", err, cleanupErr)
			return 1
		}
		fmt.Fprintf(stderr, "main sandbox preflight invalid: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "sandbox egress ok: observed IP %s\n", preflight.Egress.ObservedIP)
	validation, err := guard.ValidateMain(ctx)
	if err != nil {
		if cleanupErr := finalizeDoctorGuard(guard, sandbox, cfg, preflight.CreatedByCommand, stdout); cleanupErr != nil {
			fmt.Fprintf(stderr, "%v; cleanup failed: %v\n", err, cleanupErr)
			return 1
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	printEgressGuardMessages(stdout, validation)
	cleanupMain := acquisition.CleanupCreatedMain && preflight.CreatedByCommand
	if err := finalizeDoctorGuard(guard, sandbox, cfg, cleanupMain, stdout); err != nil {
		fmt.Fprintf(stderr, "doctor cleanup invalid: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "sandbox inspection ok")
	return 0
}

func printEgressGuardMessages(output io.Writer, result egressguard.Result) {
	for _, message := range result.Messages {
		fmt.Fprintln(output, message)
	}
}

func finalizeDoctorGuard(guard egressguard.EgressGuard, sandbox backend.DockerSandbox, cfg config.Config, cleanupMain bool, output io.Writer) error {
	guardErr := finalizeEgressGuard(guard, cfg, output)
	if !cleanupMain {
		return guardErr
	}
	return errors.Join(guardErr, sandbox.CleanupMain(context.Background(), mainCleanupConfig(cfg, true)))
}

func finalizeEgressGuard(guard egressguard.EgressGuard, cfg config.Config, output io.Writer) error {
	fenceCtx, cancelFence := backend.TimeoutContext(cfg.Network.EgressIP.TimeoutSeconds)
	fenced, fenceErr := guard.Fence(fenceCtx)
	cancelFence()
	printEgressGuardMessages(output, fenced)

	recoverCtx, cancelRecover := backend.TimeoutContext(cfg.Network.EgressIP.TimeoutSeconds)
	recovered, recoverErr := guard.Recover(recoverCtx)
	cancelRecover()
	printEgressGuardMessages(output, recovered)
	return errors.Join(fenceErr, recoverErr)
}

func mainCleanupConfig(cfg config.Config, mainCreatedByCommand bool) config.Config {
	if cfg.Network.Egress.Mode != "dedicated-gateway" {
		return cfg
	}
	cfg.Cleanup.StopMainSandbox = true
	if !mainCreatedByCommand {
		cfg.Cleanup.RemoveMainSandbox = false
	}
	// Fence already stopped the dedicated daemon. Do not restart main through
	// an in-sandbox Herdr cleanup command after normal sandboxd is recovered.
	cfg.Sandbox.Supervision.Mode = "direct-claude"
	return cfg
}

type launchPlatform struct {
	checkTUN      func(config.ClashVerge) (network.TUNPreflightResult, error)
	signalContext func() (context.Context, context.CancelFunc)
}

func defaultLaunchPlatform() launchPlatform {
	return launchPlatform{
		checkTUN: func(policy config.ClashVerge) (network.TUNPreflightResult, error) {
			return network.NewInspector().CheckClashVergeTUN(policy)
		},
		signalContext: func() (context.Context, context.CancelFunc) {
			return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		},
	}
}

func (r Runner) runLaunch(
	cfg config.Config,
	target launchTarget,
	guard egressguard.EgressGuard,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int {
	sandbox := r.sandbox
	platform := r.platform
	var tun network.TUNPreflightResult
	if cfg.Network.Egress.Mode == "host-inherited" {
		checkTUN := platform.checkTUN
		if checkTUN == nil {
			checkTUN = defaultLaunchPlatform().checkTUN
		}
		checkedTUN, err := checkTUN(cfg.Network.ClashVerge)
		if err != nil {
			fmt.Fprintf(stderr, "TUN preflight invalid: %v\n", err)
			return 1
		}
		tun = checkedTUN
		fmt.Fprintf(stdout, "TUN preflight ok: startup interface %s\n", tun.StartupTUNInterface)
	}

	acquireCtx, cancelAcquire := backend.TimeoutContext(cfg.Network.EgressIP.TimeoutSeconds)
	acquisition, err := guard.Acquire(acquireCtx)
	cancelAcquire()
	if err != nil {
		if cleanupErr := finalizeDoctorGuard(guard, sandbox, cfg, false, stdout); cleanupErr != nil {
			fmt.Fprintf(stderr, "%v; cleanup failed: %v\n", err, cleanupErr)
			return 1
		}
		fmt.Fprintln(stderr, err)
		return 1
	}
	printEgressGuardMessages(stdout, acquisition)

	ctx, cancel := backend.TimeoutContext(cfg.Network.EgressIP.TimeoutSeconds)
	defer cancel()

	availability, err := sandbox.CheckAvailability(ctx)
	if err != nil {
		cleanupErr := finalizeDoctorGuard(guard, sandbox, cfg, false, stdout)
		if cleanupErr != nil {
			fmt.Fprintf(stderr, "sandbox backend invalid: %s; cleanup failed: %v\n", backendAvailabilityError(availability, err), cleanupErr)
			return 1
		}
		fmt.Fprintf(stderr, "sandbox backend invalid: %s\n", backendAvailabilityError(availability, err))
		return 1
	}
	fmt.Fprintf(stdout, "sandbox backend ok: %s\n", availability.Version)

	mainCtx, stopMainCommand := context.WithCancel(context.Background())
	defer stopMainCommand()

	plan := backend.NewStartPlan(cfg)
	mainCreatedByCommand := false
	mainPreflightRequired := target == herdrTUITarget || cfg.Network.Egress.Mode == "dedicated-gateway"
	if mainPreflightRequired {
		preflight, err := sandbox.PreflightMainSandbox(ctx, cfg)
		mainCreatedByCommand = preflight.CreatedByCommand
		if err != nil {
			cleanupMain := backend.ShouldCleanupMainAfterStartError(err)
			if cleanupErr := finalizeDoctorGuard(guard, sandbox, cfg, cleanupMain, stdout); cleanupErr != nil {
				fmt.Fprintf(stderr, "main sandbox preflight invalid: %v; cleanup failed: %v\n", err, cleanupErr)
				return 1
			}
			fmt.Fprintf(stderr, "main sandbox preflight invalid: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "sandbox egress ok: observed IP %s\n", preflight.Egress.ObservedIP)
		validation, err := guard.ValidateMain(ctx)
		if err != nil {
			if cleanupErr := finalizeDoctorGuard(guard, sandbox, cfg, preflight.CreatedByCommand, stdout); cleanupErr != nil {
				fmt.Fprintf(stderr, "%v; cleanup failed: %v\n", err, cleanupErr)
				return 1
			}
			fmt.Fprintln(stderr, err)
			return 1
		}
		printEgressGuardMessages(stdout, validation)
		fmt.Fprintln(stdout, "sandbox inspection ok")
	} else {
		probe, err := sandbox.Probe(ctx, cfg)
		if err != nil {
			if cleanupErr := finalizeDoctorGuard(guard, sandbox, cfg, false, stdout); cleanupErr != nil {
				fmt.Fprintf(stderr, "sandbox probe invalid: %v; cleanup failed: %v\n", err, cleanupErr)
				return 1
			}
			fmt.Fprintf(stderr, "sandbox probe invalid: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "sandbox egress ok: observed IP %s\n", probe.Egress.ObservedIP)
		fmt.Fprintln(stdout, "sandbox inspection ok")
	}

	start, backendExit, err := startAttachedTarget(sandbox, target, mainPreflightRequired, mainCtx, cfg, plan, stdin, stdout, stderr)
	if err != nil {
		stopMainCommand()
		cleanupMain := backend.ShouldCleanupMainAfterStartError(err) || mainCreatedByCommand
		if cleanupErr := finalizeDoctorGuard(guard, sandbox, cfg, cleanupMain, stdout); cleanupErr != nil {
			fmt.Fprintf(stderr, "%s invalid: %v; cleanup failed: %v\n", targetStartLabel(target), err, cleanupErr)
			return 1
		}
		fmt.Fprintf(stderr, "%s invalid: %v\n", targetStartLabel(target), err)
		return 1
	}
	fmt.Fprintf(stdout, "%s started: %s\n", targetStartedLabel(target), start.SandboxName)

	newSignalContext := platform.signalContext
	if newSignalContext == nil {
		newSignalContext = defaultLaunchPlatform().signalContext
	}
	signalCtx, stopSignals := newSignalContext()
	defer stopSignals()

	runtime := guard.Watch(signalCtx, egressguard.WatchInput{StartupTUNInterface: tun.StartupTUNInterface})
	cleanup := watchdog.CleanupFunc(func(ctx context.Context) error {
		stopMainCommand()
		guardErr := finalizeEgressGuard(guard, cfg, stdout)
		cleanupErr := sandbox.CleanupMain(ctx, mainCleanupConfig(cfg, mainCreatedByCommand))
		if target == herdrTUITarget || cfg.Network.Egress.Mode == "dedicated-gateway" {
			return errors.Join(guardErr, cleanupErr)
		}
		return errors.Join(guardErr, cleanupErr, sandbox.CleanupProbe(ctx, cfg))
	})
	supervisor := watchdog.Supervisor{
		Events:        runtime.Events,
		EventErrors:   runtime.EventErrors,
		BackendExit:   backendExit,
		EventDebounce: watchdog.DefaultEventDebounce,
		Check:         runtime.Checker,
		Cleanup:       cleanup,
	}
	if err := supervisor.Run(signalCtx); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(stdout, "watchdog stopped: signal received; cleanup complete")
			return 130
		}
		fmt.Fprintf(stderr, "watchdog stopped sandbox: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "sandbox exited; cleanup complete")
	return 0
}

func startAttachedTarget(sandbox backend.DockerSandbox, target launchTarget, attachValidatedMain bool, ctx context.Context, cfg config.Config, plan backend.StartPlan, stdin io.Reader, stdout, stderr io.Writer) (backend.StartResult, <-chan error, error) {
	if target == herdrTUITarget {
		start := backend.StartResult{
			SandboxName: plan.SandboxName,
			Agent:       plan.Agent,
			Workspace:   plan.Workspace,
			Timezone:    plan.Timezone,
			Locale:      plan.Locale,
		}
		if err := sandbox.PrepareHerdrTUI(ctx, plan); err != nil {
			return start, nil, err
		}
		fmt.Fprintln(stdout, "main sandbox inspection ok")
		wait, err := sandbox.AttachHerdrTUI(ctx, plan, stdin, stdout, stderr)
		if err != nil {
			return start, nil, err
		}
		return start, wait, nil
	}
	if attachValidatedMain {
		start := backend.StartResult{
			SandboxName: plan.SandboxName,
			Agent:       plan.Agent,
			Workspace:   plan.Workspace,
			Timezone:    plan.Timezone,
			Locale:      plan.Locale,
		}
		wait, err := sandbox.AttachMain(ctx, plan, stdin, stdout, stderr)
		if err != nil {
			return start, nil, err
		}
		fmt.Fprintln(stdout, "main sandbox inspection ok")
		return start, wait, nil
	}
	start, wait, err := sandbox.StartMainAttached(ctx, plan, stdin, stdout, stderr)
	if err != nil {
		return start, wait, err
	}
	if err := checkMainWorkspaceVisibility(sandbox, cfg); err != nil {
		return start, nil, err
	}
	fmt.Fprintln(stdout, "main sandbox inspection ok")
	return start, wait, nil
}

func checkMainWorkspaceVisibility(sandbox backend.DockerSandbox, cfg config.Config) error {
	mainInspectionCtx, cancelMainInspection := backend.TimeoutContext(cfg.Network.EgressIP.TimeoutSeconds)
	defer cancelMainInspection()
	_, err := sandbox.CheckMainWorkspaceVisibility(mainInspectionCtx, cfg)
	return err
}

func targetStartLabel(target launchTarget) string {
	if target == herdrTUITarget {
		return "Herdr TUI start"
	}
	return "sandbox start"
}

func targetStartedLabel(target launchTarget) string {
	if target == herdrTUITarget {
		return "Herdr TUI"
	}
	return "sandbox"
}

func backendAvailabilityError(availability backend.Availability, err error) string {
	if availability.Kind == backend.AvailabilityControlPlaneUnavailable {
		return fmt.Sprintf("%v; run `sbx diagnose` and `sbx ls`, then restart the sbx daemon or Docker Desktop", err)
	}
	return err.Error()
}
