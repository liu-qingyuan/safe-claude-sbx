package watchdog

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/backend"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
)

type SandboxRuntimeProbe interface {
	CheckSandboxEgress(ctx context.Context, cfg config.Config) (backend.ProbeResult, error)
	Probe(ctx context.Context, cfg config.Config) (backend.ProbeResult, error)
}

type RuntimeChecker struct {
	Config              config.Config
	StartupTUNInterface string
	RouteRunner         network.CommandRunner
	Sandbox             SandboxRuntimeProbe
}

var runtimeProbeSequence atomic.Uint64

func (c RuntimeChecker) Check(ctx context.Context, event Event) (CheckResult, error) {
	runner := c.RouteRunner
	if runner == nil {
		runner = network.ExecRunner{}
	}

	routeInterface, err := network.RouteInterface(runner, c.Config.Network.ClashVerge.RouteCheckTarget)
	if err != nil {
		return runtimeFail("route validation failed: %v", err)
	}
	if routeInterface != c.StartupTUNInterface {
		return runtimeFail("default route changed from startup interface %s to %s", c.StartupTUNInterface, routeInterface)
	}
	if err := network.InterfaceExists(runner, c.StartupTUNInterface); err != nil {
		return runtimeFail("TUN interface %s missing: %v", c.StartupTUNInterface, err)
	}

	if c.Sandbox == nil {
		return runtimeFail("sandbox probe unavailable")
	}
	runtimeConfig := c.runtimeProbeConfig()
	egress, err := c.Sandbox.CheckSandboxEgress(ctx, runtimeConfig)
	if err != nil {
		return runtimeFail("sandbox egress invalid: %v", err)
	}
	if !egress.Egress.OK {
		return runtimeFail("sandbox egress invalid: %s", egress.Egress.FailureReason)
	}
	if _, err := c.Sandbox.Probe(ctx, runtimeConfig); err != nil {
		return runtimeFail("sandbox inspection invalid: %v", err)
	}
	return CheckResult{OK: true}, nil
}

func (c RuntimeChecker) runtimeProbeConfig() config.Config {
	cfg := c.Config
	if cfg.Sandbox.ProbeName == "" {
		return cfg
	}
	sequence := runtimeProbeSequence.Add(1)
	cfg.Sandbox.ProbeName = fmt.Sprintf("%s-runtime-%d", cfg.Sandbox.ProbeName, sequence)
	return cfg
}

func runtimeFail(format string, args ...any) (CheckResult, error) {
	reason := fmt.Sprintf(format, args...)
	return CheckResult{OK: false, Reason: reason}, fmt.Errorf("%s", reason)
}
