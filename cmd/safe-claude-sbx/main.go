package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/backend"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 3 && args[0] == "doctor" && args[1] == "--config" {
		return runDoctor(args[2], stdout, stderr)
	}
	if len(args) == 2 && args[0] == "--config" {
		return runLaunch(args[1], stdout, stderr)
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

func runLaunch(configPath string, stdout, stderr io.Writer) int {
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

	start, err := sandbox.StartMain(context.Background(), backend.NewStartPlan(cfg))
	if err != nil {
		if cleanupErr := sandbox.CleanupMain(context.Background(), cfg); cleanupErr != nil {
			fmt.Fprintf(stderr, "sandbox start invalid: %v; cleanup main sandbox failed: %v\n", err, cleanupErr)
			return 1
		}
		fmt.Fprintf(stderr, "sandbox start invalid: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "sandbox started: %s\n", start.SandboxName)
	return 0
}
