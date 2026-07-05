package network

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
)

type EgressFailureKind string

const (
	EgressFailureNone            EgressFailureKind = ""
	EgressFailureMismatch        EgressFailureKind = "host-egress-mismatch"
	EgressFailureEndpointFailure EgressFailureKind = "endpoint-failure"
	EgressFailureResponseParse   EgressFailureKind = "response-parse-failure"
)

type EgressResult struct {
	OK            bool
	ExpectedIP    string
	ObservedIP    string
	FailureKind   EgressFailureKind
	FailureReason string
}

type EgressValidator struct {
	Client *http.Client
}

func CheckHostEgress(policy config.EgressIP) (EgressResult, error) {
	return EgressValidator{}.CheckHost(policy)
}

func (v EgressValidator) CheckHost(policy config.EgressIP) (EgressResult, error) {
	timeout := time.Duration(policy.TimeoutSeconds) * time.Second
	client := v.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, policy.HostCheckURL, nil)
	if err != nil {
		return egressFail(policy, EgressFailureEndpointFailure, "", "create host egress request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return egressFail(policy, EgressFailureEndpointFailure, "", "host egress endpoint request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return egressFail(policy, EgressFailureEndpointFailure, "", "host egress endpoint returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return egressFail(policy, EgressFailureEndpointFailure, "", "read host egress response: %v", err)
	}
	observedText := strings.TrimSpace(string(body))
	observed, err := netip.ParseAddr(observedText)
	if err != nil {
		return egressFail(policy, EgressFailureResponseParse, observedText, "host egress response is not an IP address")
	}
	expected, err := netip.ParseAddr(strings.TrimSpace(policy.ExpectedIP))
	if err != nil {
		return egressFail(policy, EgressFailureResponseParse, observedText, "configured expected host egress IP is not an IP address")
	}
	if observed != expected {
		return egressFail(policy, EgressFailureMismatch, observed.String(), "host egress observed IP %s does not match expected IP %s", observed.String(), expected.String())
	}

	return EgressResult{
		OK:         true,
		ExpectedIP: expected.String(),
		ObservedIP: observed.String(),
	}, nil
}

func egressFail(policy config.EgressIP, kind EgressFailureKind, observed, format string, args ...any) (EgressResult, error) {
	result := EgressResult{
		OK:            false,
		ExpectedIP:    strings.TrimSpace(policy.ExpectedIP),
		ObservedIP:    observed,
		FailureKind:   kind,
		FailureReason: fmt.Sprintf(format, args...),
	}
	return result, fmt.Errorf("%s: %s", kind, result.FailureReason)
}
