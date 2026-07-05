package main

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
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/watchdog"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 3 && args[0] == "doctor" && args[1] == "--config" {
		return runDoctor(args[2], stdout, stderr)
	}
	if len(args) == 2 && args[0] == "--config" {
		return runLaunch(args[1], stdin, stdout, stderr)
	}

	fmt.Fprintln(stderr, "usage: safe-claude-sbx [doctor] --config <file>")
	return 2
}

func runDoctor(configPath string, stdout, stderr io.Writer) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "configuration invalid: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "configuration ok")

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
		fmt.Fprintf(stderr, "sandbox backend invalid: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "sandbox backend ok: %s\n", availability.Version)

	probe, err := sandbox.Probe(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "sandbox probe invalid: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "sandbox egress ok: observed IP %s\n", probe.Egress.ObservedIP)
	fmt.Fprintln(stdout, "sandbox inspection ok")
	return 0
}

func runLaunch(configPath string, stdin io.Reader, stdout, stderr io.Writer) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "configuration invalid: %v\n", err)
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
		fmt.Fprintf(stderr, "sandbox backend invalid: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "sandbox backend ok: %s\n", availability.Version)

	probe, err := sandbox.Probe(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "sandbox probe invalid: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "sandbox egress ok: observed IP %s\n", probe.Egress.ObservedIP)
	fmt.Fprintln(stdout, "sandbox inspection ok")

	mainCtx, stopMainCommand := context.WithCancel(context.Background())
	defer stopMainCommand()

	start, backendExit, err := sandbox.StartMainAttached(mainCtx, backend.NewStartPlan(cfg), stdin, stdout, stderr)
	if err != nil {
		if cleanupErr := sandbox.CleanupMain(context.Background(), cfg); cleanupErr != nil {
			fmt.Fprintf(stderr, "sandbox start invalid: %v; cleanup main sandbox failed: %v\n", err, cleanupErr)
			return 1
		}
		fmt.Fprintf(stderr, "sandbox start invalid: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "sandbox started: %s\n", start.SandboxName)

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	routeEvents, routeErrors := watchdog.RouteMonitor{}.Start(signalCtx)
	cleanup := watchdog.CleanupFunc(func(ctx context.Context) error {
		stopMainCommand()
		return errors.Join(
			sandbox.CleanupMain(ctx, cfg),
			sandbox.CleanupProbe(ctx, cfg),
		)
	})
	supervisor := watchdog.Supervisor{
		Events:      routeEvents,
		EventErrors: routeErrors,
		BackendExit: backendExit,
		Check: watchdog.RuntimeChecker{
			Config:              cfg,
			StartupTUNInterface: tun.StartupTUNInterface,
			RouteRunner:         network.ExecRunner{},
			Sandbox:             sandbox,
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
