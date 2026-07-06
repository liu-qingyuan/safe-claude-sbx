package watchdog

import (
	"context"
	"fmt"
	"strings"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/backend"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
)

type SandboxRuntimeProbe interface {
	CheckRuntimeEgress(ctx context.Context, cfg config.Config) (backend.ProbeResult, error)
}

type RuntimeChecker struct {
	Config              config.Config
	StartupTUNInterface string
	RouteRunner         network.CommandRunner
	Sandbox             SandboxRuntimeProbe
}

const runtimeEgressCheckAttempts = 2

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
	egress, err := c.checkRuntimeEgress(ctx)
	if err != nil {
		return CheckResult{OK: false, Reason: egress}, err
	}
	return CheckResult{OK: true}, nil
}

func (c RuntimeChecker) checkRuntimeEgress(ctx context.Context) (string, error) {
	var lastErr error
	var lastResult backend.ProbeResult
	for attempt := 1; attempt <= runtimeEgressCheckAttempts; attempt++ {
		result, err := c.Sandbox.CheckRuntimeEgress(ctx, c.Config)
		if err == nil && result.Egress.OK {
			return "", nil
		}
		lastErr = err
		lastResult = result
		if isIndeterminateRuntimeEgress(result, err) && attempt < runtimeEgressCheckAttempts {
			continue
		}
		break
	}
	if isIndeterminateRuntimeEgress(lastResult, lastErr) {
		return runtimeFailure("indeterminate runtime egress check failed after %d attempt(s): %s", runtimeEgressCheckAttempts, runtimeEgressDiagnostic(lastResult, lastErr))
	}
	if lastErr != nil {
		return runtimeFailure("sandbox egress invalid: %v", lastErr)
	}
	return runtimeFailure("sandbox egress invalid: %s", lastResult.Egress.FailureReason)
}

func isIndeterminateRuntimeEgress(result backend.ProbeResult, err error) bool {
	if result.Egress.FailureKind == network.EgressFailureIndeterminate {
		return true
	}
	return err != nil && result.Egress.FailureKind == network.EgressFailureNone && containsFailureKind(err, network.EgressFailureIndeterminate)
}

func containsFailureKind(err error, kind network.EgressFailureKind) bool {
	return err != nil && strings.Contains(err.Error(), string(kind))
}

func runtimeEgressDiagnostic(result backend.ProbeResult, err error) string {
	if result.Egress.FailureReason != "" {
		return result.Egress.FailureReason
	}
	if err != nil {
		return err.Error()
	}
	return "unknown runtime egress failure"
}

func runtimeFail(format string, args ...any) (CheckResult, error) {
	reason, err := runtimeFailure(format, args...)
	return CheckResult{OK: false, Reason: reason}, err
}

func runtimeFailure(format string, args ...any) (string, error) {
	reason := fmt.Sprintf(format, args...)
	return reason, fmt.Errorf("%s", reason)
}
