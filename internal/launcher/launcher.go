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

func RunSafeClaudeSBX(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 3 && args[0] == "doctor" && args[1] == "--config" {
		return runDoctor(args[2], stdout, stderr)
	}
	if len(args) == 2 && args[0] == "--config" {
		return runLaunch(args[1], mainSandboxTarget, stdin, stdout, stderr)
	}

	fmt.Fprintln(stderr, "usage: safe-claude-sbx [doctor] --config <file>")
	return 2
}

func RunSafeHerdr(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 2 && args[0] == "--config" {
		return runLaunch(args[1], herdrTUITarget, stdin, stdout, stderr)
	}

	fmt.Fprintln(stderr, "usage: safe-herdr --config <file>")
	return 2
}

type doctorGuardFactory func(config.Config, egressguard.MainSandbox) (egressguard.EgressGuard, error)

func runDoctor(configPath string, stdout, stderr io.Writer) int {
	sandbox := backend.NewDockerSandbox()
	return runDoctorWithAdapters(configPath, stdout, stderr, sandbox, egressguard.New)
}

func runDoctorWithAdapters(
	configPath string,
	stdout, stderr io.Writer,
	sandbox backend.DockerSandbox,
	newGuard doctorGuardFactory,
) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "configuration invalid: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "configuration ok")

	if cfg.Sandbox.Backend != "docker-sandbox" {
		fmt.Fprintf(stderr, "sandbox backend invalid: unsupported backend %q\n", cfg.Sandbox.Backend)
		return 1
	}
	guard, err := newGuard(cfg, sandbox)
	if err != nil {
		fmt.Fprintf(stderr, "egress guard invalid: %v\n", err)
		return 1
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
	cleanupCtx, cancel := backend.TimeoutContext(cfg.Network.EgressIP.TimeoutSeconds)
	defer cancel()
	revoked, revokeErr := guard.Revoke(cleanupCtx)
	printEgressGuardMessages(output, revoked)
	if !cleanupMain {
		return revokeErr
	}
	return errors.Join(revokeErr, sandbox.CleanupMain(context.Background(), cfg))
}

func runLaunch(configPath string, target launchTarget, stdin io.Reader, stdout, stderr io.Writer) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "configuration invalid: %v\n", err)
		return 1
	}
	if cfg.Network.Egress.Mode == "dedicated-gateway" {
		fmt.Fprintln(stderr, "configuration invalid: dedicated-gateway launch is not available until dedicated runtime supervision is configured")
		return 1
	}
	if target == herdrTUITarget && cfg.Sandbox.Supervision.Mode != "sandbox-local-herdr" {
		fmt.Fprintln(stderr, "configuration invalid: safe-herdr requires sandbox.supervision.mode \"sandbox-local-herdr\"")
		return 1
	}
	fmt.Fprintln(stdout, "configuration ok")

	tun, err := network.NewInspector().CheckClashVergeTUN(cfg.Network.ClashVerge)
	if err != nil {
		fmt.Fprintf(stderr, "TUN preflight invalid: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "TUN preflight ok: startup interface %s\n", tun.StartupTUNInterface)

	result, err := network.CheckHostEgress(cfg.Network.EgressIP)
	if err != nil {
		fmt.Fprintf(stderr, "host egress invalid: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "host egress ok: observed IP %s\n", result.ObservedIP)

	if cfg.Sandbox.Backend != "docker-sandbox" {
		fmt.Fprintf(stderr, "sandbox backend invalid: unsupported backend %q\n", cfg.Sandbox.Backend)
		return 1
	}
	ctx, cancel := backend.TimeoutContext(cfg.Network.EgressIP.TimeoutSeconds)
	defer cancel()

	sandbox := backend.NewDockerSandbox()
	availability, err := sandbox.CheckAvailability(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "sandbox backend invalid: %s\n", backendAvailabilityError(availability, err))
		return 1
	}
	fmt.Fprintf(stdout, "sandbox backend ok: %s\n", availability.Version)

	mainCtx, stopMainCommand := context.WithCancel(context.Background())
	defer stopMainCommand()

	plan := backend.NewStartPlan(cfg)
	mainCreatedByCommand := false
	if target == herdrTUITarget {
		preflight, err := sandbox.PreflightMainSandbox(ctx, cfg)
		mainCreatedByCommand = preflight.CreatedByCommand
		if err != nil {
			if backend.ShouldCleanupMainAfterStartError(err) {
				if cleanupErr := sandbox.CleanupMain(context.Background(), cfg); cleanupErr != nil {
					fmt.Fprintf(stderr, "main sandbox preflight invalid: %v; cleanup main sandbox failed: %v\n", err, cleanupErr)
					return 1
				}
			}
			fmt.Fprintf(stderr, "main sandbox preflight invalid: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "sandbox egress ok: observed IP %s\n", preflight.Egress.ObservedIP)
		fmt.Fprintln(stdout, "sandbox inspection ok")
	} else {
		probe, err := sandbox.Probe(ctx, cfg)
		if err != nil {
			fmt.Fprintf(stderr, "sandbox probe invalid: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "sandbox egress ok: observed IP %s\n", probe.Egress.ObservedIP)
		fmt.Fprintln(stdout, "sandbox inspection ok")
	}

	start, backendExit, err := startAttachedTarget(sandbox, target, mainCtx, cfg, plan, stdin, stdout, stderr)
	if err != nil {
		if backend.ShouldCleanupMainAfterStartError(err) || mainCreatedByCommand {
			stopMainCommand()
			if cleanupErr := sandbox.CleanupMain(context.Background(), cfg); cleanupErr != nil {
				fmt.Fprintf(stderr, "%s invalid: %v; cleanup main sandbox failed: %v\n", targetStartLabel(target), err, cleanupErr)
				return 1
			}
		}
		fmt.Fprintf(stderr, "%s invalid: %v\n", targetStartLabel(target), err)
		return 1
	}
	fmt.Fprintf(stdout, "%s started: %s\n", targetStartedLabel(target), start.SandboxName)

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	routeEvents, routeErrors := watchdog.RouteMonitor{}.Start(signalCtx)
	clashEvents, clashErrors := watchdog.ClashAppHomeMonitor{Policy: cfg.Network.ClashVerge}.Start(signalCtx)
	watchdogEvents, watchdogErrors := watchdog.MergeEventStreams(
		signalCtx,
		[]<-chan watchdog.Event{routeEvents, clashEvents},
		[]<-chan error{routeErrors, clashErrors},
	)
	cleanup := watchdog.CleanupFunc(func(ctx context.Context) error {
		stopMainCommand()
		if target == herdrTUITarget {
			return sandbox.CleanupMain(ctx, cfg)
		}
		return errors.Join(sandbox.CleanupMain(ctx, cfg), sandbox.CleanupProbe(ctx, cfg))
	})
	supervisor := watchdog.Supervisor{
		Events:        watchdogEvents,
		EventErrors:   watchdogErrors,
		BackendExit:   backendExit,
		EventDebounce: watchdog.DefaultEventDebounce,
		Check: watchdog.RuntimeChecker{
			Config:              cfg,
			StartupTUNInterface: tun.StartupTUNInterface,
			RouteRunner:         network.ExecRunner{},
		},
		Cleanup: cleanup,
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

func startAttachedTarget(sandbox backend.DockerSandbox, target launchTarget, ctx context.Context, cfg config.Config, plan backend.StartPlan, stdin io.Reader, stdout, stderr io.Writer) (backend.StartResult, <-chan error, error) {
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
