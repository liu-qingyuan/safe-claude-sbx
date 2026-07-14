# ADR 0003: Dedicated egress runtime watchdog

## Status

Accepted

## Context

ADR 0002 makes `host-inherited` runtime supervision host-centered so route or
Clash events do not depend on repeated Docker Sandbox control-plane probes.
That indirect model is not sufficient for `dedicated-gateway`: host route and
host public egress do not prove that the sandboxd upstream lease, the external
Mihomo gateway, or the main sandbox public egress still match policy.

The dedicated mode therefore has three runtime invariants that must remain
observable after startup: the authenticated loopback controller is healthy,
the launcher-owned sandboxd lease is active and exclusive, and main sandbox
egress matches the configured expected IP.

## Decision

Keep ADR 0002 unchanged for `host-inherited`. For `dedicated-gateway`, use the
same `EgressGuard.Watch` Interface and existing Watchdog Supervisor with these
mode-specific rules:

- emit an immediate event when the launcher-owned sandboxd process exits;
- emit a dedicated health event every 5 seconds;
- ignore host route and Clash app-home events in dedicated mode;
- on a health event, check launcher-owned daemon status, authenticated Mihomo
  controller health, and exclusive sandbox scope before checking main egress;
- only after those host-observable checks pass, run one
  `CheckRuntimeEgress` probe against the configured sandbox egress endpoint;
- bound the entire health check, including sandboxd commands and the egress
  probe, by `network.egress_ip.timeout_seconds`;
- let the Supervisor cancel any active check before cleanup, then revoke the
  sandboxd lease before stopping or cleaning the main sandbox;
- observe the operator-managed Mihomo process only through its controller and
  never start, stop, or reconfigure it.

This is a narrow exception to ADR 0002's repeated `sbx exec` prohibition. It
applies only because dedicated sandbox egress cannot be inferred from host
route or host egress. It does not restore sandbox probes to host route or Clash
event handling.

## Consequences

- Dedicated egress drift is detected without treating host Clash activity as a
  dedicated-mode failure signal.
- A healthy dedicated session makes one bounded sandbox egress request per
  health interval.
- Controller or lease failures avoid the sandbox egress probe and fail closed
  from host-observable evidence first.
- A stalled sandboxd command or sandbox egress probe is canceled before
  `Revoke`, so cleanup does not wait indefinitely for the runtime checker lock.
- The existing Supervisor continues to own debounce, backend exit,
  cancellation, error reporting, and cleanup-once behavior for both modes.
