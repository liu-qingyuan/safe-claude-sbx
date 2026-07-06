package watchdog

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	RetryPolicy         RuntimeEgressRetryPolicy
}

const runtimeEgressCheckAttempts = 10

var (
	defaultRuntimeEgressInitialBackoff = time.Second
	defaultRuntimeEgressMaxBackoff     = 30 * time.Second
)

type RuntimeEgressRetryPolicy struct {
	Attempts       int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Sleeper        Sleeper
}

type Sleeper interface {
	Sleep(ctx context.Context, duration time.Duration) error
}

type SleepFunc func(context.Context, time.Duration) error

func (f SleepFunc) Sleep(ctx context.Context, duration time.Duration) error {
	return f(ctx, duration)
}

func (c RuntimeChecker) Check(ctx context.Context, event Event) (CheckResult, error) {
	runner := c.RouteRunner
	if runner == nil {
		runner = network.ExecRunner{}
	}

	if result, err := c.validateRuntimeRoute(runner); err != nil {
		return result, err
	}

	if c.Sandbox == nil {
		return runtimeFail("sandbox probe unavailable")
	}
	egress, err := c.checkRuntimeEgress(ctx, runner)
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

func (c RuntimeChecker) checkRuntimeEgress(ctx context.Context, runner network.CommandRunner) (string, error) {
	policy := c.retryPolicy()
	var lastErr error
	var lastResult backend.ProbeResult
	for attempt := 1; attempt <= policy.Attempts; attempt++ {
		if attempt > 1 {
			if routeResult, routeErr := c.validateRuntimeRoute(runner); routeErr != nil {
				return routeResult.Reason, routeErr
			}
		}
		result, err := c.Sandbox.CheckRuntimeEgress(ctx, c.Config)
		if err == nil && result.Egress.OK {
			return "", nil
		}
		lastErr = err
		lastResult = result
		if !isIndeterminateRuntimeEgress(result, err) {
			break
		}
		if attempt == policy.Attempts {
			break
		}
		if routeResult, routeErr := c.validateRuntimeRoute(runner); routeErr != nil {
			return routeResult.Reason, routeErr
		}
		if err := policy.Sleeper.Sleep(ctx, policy.backoff(attempt)); err != nil {
			return runtimeFailure("runtime egress indeterminate retry %d/%d interrupted: %v", attempt, policy.Attempts, err)
		}
	}
	if isIndeterminateRuntimeEgress(lastResult, lastErr) {
		return runtimeFailure("indeterminate runtime egress check failed after %d attempt(s): runtime egress indeterminate retry %d/%d: %s", policy.Attempts, policy.Attempts, policy.Attempts, runtimeEgressDiagnostic(lastResult, lastErr))
	}
	if lastErr != nil {
		return runtimeFailure("sandbox egress invalid: %v", lastErr)
	}
	return runtimeFailure("sandbox egress invalid: %s", lastResult.Egress.FailureReason)
}

func (c RuntimeChecker) retryPolicy() RuntimeEgressRetryPolicy {
	policy := c.RetryPolicy
	if policy.Attempts <= 0 {
		policy.Attempts = runtimeEgressCheckAttempts
	}
	if policy.InitialBackoff <= 0 {
		policy.InitialBackoff = defaultRuntimeEgressInitialBackoff
	}
	if policy.MaxBackoff <= 0 {
		policy.MaxBackoff = defaultRuntimeEgressMaxBackoff
	}
	if policy.Sleeper == nil {
		policy.Sleeper = SleepFunc(func(ctx context.Context, duration time.Duration) error {
			timer := time.NewTimer(duration)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		})
	}
	return policy
}

func (p RuntimeEgressRetryPolicy) backoff(completedAttempt int) time.Duration {
	backoff := p.InitialBackoff
	for range completedAttempt - 1 {
		if backoff >= p.MaxBackoff/2 {
			return p.MaxBackoff
		}
		backoff *= 2
	}
	if backoff > p.MaxBackoff {
		return p.MaxBackoff
	}
	return backoff
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
