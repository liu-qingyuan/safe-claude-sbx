package main

import (
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
	if len(args) != 3 || args[0] != "doctor" || args[1] != "--config" {
		fmt.Fprintln(stderr, "usage: safe-claude-sbx doctor --config <file>")
		return 2
	}

	cfg, err := config.Load(args[2])
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
