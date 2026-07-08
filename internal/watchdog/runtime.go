package watchdog

import (
	"context"
	"fmt"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
)

type HostEgressChecker interface {
	CheckHostContext(ctx context.Context, policy config.EgressIP) (network.EgressResult, error)
}

type RuntimeChecker struct {
	Config              config.Config
	StartupTUNInterface string
	RouteRunner         network.CommandRunner
	HostEgress          HostEgressChecker
}

func (c RuntimeChecker) Check(ctx context.Context, _ Event) (CheckResult, error) {
	runner := c.RouteRunner
	if runner == nil {
		runner = network.ExecRunner{}
	}

	if result, err := c.validateRuntimeRoute(runner); err != nil {
		return result, err
	}

	egress, err := c.checkHostEgress(ctx)
	if err != nil {
		return CheckResult{OK: false, Reason: egress}, err
	}
	return CheckResult{OK: true}, nil
}

func (c RuntimeChecker) validateRuntimeRoute(runner network.CommandRunner) (CheckResult, error) {
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
	return CheckResult{OK: true}, nil
}

func (c RuntimeChecker) checkHostEgress(ctx context.Context) (string, error) {
	checker := c.HostEgress
	if checker == nil {
		checker = network.EgressValidator{}
	}
	result, err := checker.CheckHostContext(ctx, c.Config.Network.EgressIP)
	if err == nil && result.OK {
		return "", nil
	}
	reason := result.FailureReason
	if reason == "" && err != nil {
		reason = err.Error()
	}
	if reason == "" {
		reason = "unknown host egress failure"
	}
	if result.FailureKind == network.EgressFailureMismatch {
		return runtimeFailure("host egress drift: %s", reason)
	}
	return runtimeFailure("host egress check failed: %s", reason)
}

func runtimeFail(format string, args ...any) (CheckResult, error) {
	reason, err := runtimeFailure(format, args...)
	return CheckResult{OK: false, Reason: reason}, err
}

func runtimeFailure(format string, args ...any) (string, error) {
	reason := fmt.Sprintf(format, args...)
	return reason, fmt.Errorf("%s", reason)
}
