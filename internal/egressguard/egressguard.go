package egressguard

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/watchdog"
)

type Result struct {
	Messages           []string
	CleanupCreatedMain bool
}

type EgressGuard interface {
	Acquire(ctx context.Context) (Result, error)
	ValidateMain(ctx context.Context) (Result, error)
	Watch(ctx context.Context, input WatchInput) RuntimeWatch
	Fence(ctx context.Context) (Result, error)
	Recover(ctx context.Context) (Result, error)
}

type WatchInput struct {
	StartupTUNInterface string
}

type RuntimeWatch struct {
	Events      <-chan watchdog.Event
	EventErrors <-chan error
	Checker     watchdog.Checker
}

type MainSandbox interface {
	CheckMainEndpointIsolation(ctx context.Context, sandboxName, endpoint string) error
	CheckRuntimeEgress(ctx context.Context, cfg config.Config) (network.EgressResult, error)
}

func New(cfg config.Config, main MainSandbox) (EgressGuard, error) {
	switch cfg.Network.Egress.Mode {
	case "host-inherited":
		return hostInheritedAdapter{cfg: cfg}, nil
	case "dedicated-gateway":
		client := &http.Client{Timeout: time.Duration(cfg.Network.EgressIP.TimeoutSeconds) * time.Second}
		return newDedicatedGatewayAdapter(cfg, main, osCommandExecutor{}, client), nil
	default:
		return nil, fmt.Errorf("unsupported egress mode %q", cfg.Network.Egress.Mode)
	}
}

// NewWithProtocolCheck constructs the real dedicated Adapter with an injected
// capability check. It exists for cross-package lifecycle tests; production
// callers must use New so the release support matrix remains authoritative.
func NewWithProtocolCheck(cfg config.Config, main MainSandbox, check func(context.Context) error) (EgressGuard, error) {
	if cfg.Network.Egress.Mode != "dedicated-gateway" {
		return nil, fmt.Errorf("protocol check injection requires dedicated-gateway mode")
	}
	if check == nil {
		return nil, fmt.Errorf("protocol capability check is required")
	}
	client := &http.Client{Timeout: time.Duration(cfg.Network.EgressIP.TimeoutSeconds) * time.Second}
	return newDedicatedGatewayAdapterWithProtocolCheck(cfg, main, osCommandExecutor{}, client, check), nil
}

type hostInheritedAdapter struct {
	cfg         config.Config
	routeEvents runtimeEventSource
	clashEvents runtimeEventSource
	routeRunner network.CommandRunner
	hostEgress  watchdog.HostEgressChecker
}

func (a hostInheritedAdapter) Acquire(context.Context) (Result, error) {
	observed, err := network.CheckHostEgress(a.cfg.Network.EgressIP)
	if err != nil {
		return Result{}, fmt.Errorf("host egress invalid: %w", err)
	}
	return Result{Messages: []string{fmt.Sprintf("host egress ok: observed IP %s", observed.ObservedIP)}}, nil
}

func (hostInheritedAdapter) ValidateMain(context.Context) (Result, error) {
	return Result{}, nil
}

func (a hostInheritedAdapter) Watch(ctx context.Context, input WatchInput) RuntimeWatch {
	routeSource := a.routeEvents
	if routeSource == nil {
		routeSource = watchdog.RouteMonitor{}
	}
	clashSource := a.clashEvents
	if clashSource == nil {
		clashSource = watchdog.ClashAppHomeMonitor{Policy: a.cfg.Network.ClashVerge}
	}
	routeEvents, routeErrors := routeSource.Start(ctx)
	clashEvents, clashErrors := clashSource.Start(ctx)
	events, eventErrors := watchdog.MergeEventStreams(
		ctx,
		[]<-chan watchdog.Event{routeEvents, clashEvents},
		[]<-chan error{routeErrors, clashErrors},
	)
	routeRunner := a.routeRunner
	if routeRunner == nil {
		routeRunner = network.ExecRunner{}
	}
	return RuntimeWatch{
		Events:      events,
		EventErrors: eventErrors,
		Checker: watchdog.RuntimeChecker{
			Config:              a.cfg,
			StartupTUNInterface: input.StartupTUNInterface,
			RouteRunner:         routeRunner,
			HostEgress:          a.hostEgress,
		},
	}
}

func (hostInheritedAdapter) Fence(context.Context) (Result, error) {
	return Result{}, nil
}

func (hostInheritedAdapter) Recover(context.Context) (Result, error) {
	return Result{}, nil
}

type runtimeEventSource interface {
	Start(ctx context.Context) (<-chan watchdog.Event, <-chan error)
}
