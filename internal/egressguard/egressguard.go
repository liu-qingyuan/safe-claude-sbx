package egressguard

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
	"github.com/liu-qingyuan/safe-claude-sbx/internal/network"
)

type Result struct {
	Messages           []string
	CleanupCreatedMain bool
}

type EgressGuard interface {
	Acquire(ctx context.Context) (Result, error)
	ValidateMain(ctx context.Context) (Result, error)
	Revoke(ctx context.Context) (Result, error)
}

type MainSandbox interface {
	CheckMainEndpointIsolation(ctx context.Context, sandboxName, endpoint string) error
}

func New(cfg config.Config, main MainSandbox) (EgressGuard, error) {
	switch cfg.Network.Egress.Mode {
	case "host-inherited":
		return hostInheritedAdapter{policy: cfg.Network.EgressIP}, nil
	case "dedicated-gateway":
		client := &http.Client{Timeout: time.Duration(cfg.Network.EgressIP.TimeoutSeconds) * time.Second}
		return newDedicatedGatewayAdapter(cfg, main, osCommandExecutor{}, client), nil
	default:
		return nil, fmt.Errorf("unsupported egress mode %q", cfg.Network.Egress.Mode)
	}
}

type hostInheritedAdapter struct {
	policy config.EgressIP
}

func (a hostInheritedAdapter) Acquire(context.Context) (Result, error) {
	observed, err := network.CheckHostEgress(a.policy)
	if err != nil {
		return Result{}, fmt.Errorf("host egress invalid: %w", err)
	}
	return Result{Messages: []string{fmt.Sprintf("host egress ok: observed IP %s", observed.ObservedIP)}}, nil
}

func (hostInheritedAdapter) ValidateMain(context.Context) (Result, error) {
	return Result{}, nil
}

func (hostInheritedAdapter) Revoke(context.Context) (Result, error) {
	return Result{}, nil
}
