# ADR 0002: Lightweight runtime watchdog

## Status

Accepted

## Context

The launcher performs deep startup validation before Claude Code runs in Docker
Sandbox. That startup validation can use `sbx exec` because the sandbox is not
yet trusted for work, and a failed check should prevent startup.

Runtime supervision has a different job. It should detect when the host leaves
the approved TUN route or approved egress IP after startup without continuously
polling an IP service. The current runtime egress check runs
`sbx exec <main> curl ...` on route-monitor events. When the Docker Sandbox
control plane or a sandbox exec session stalls, the watchdog can misclassify a
control-plane stall as indeterminate network egress and then fail closed.
Cleanup can also stall because it uses the same `sbx exec` control path.

## Decision

Keep sandbox egress validation in preflight/startup checks.

Make runtime watchdog checks lightweight and host-centered:

- verify the default route still uses the startup TUN interface;
- verify the startup TUN interface still exists;
- verify host egress still matches the configured expected IP only when a
  relevant host route, interface, or Clash app-home metadata event occurs;
- do not continuously poll the host egress endpoint;
- avoid blocking runtime safety decisions on repeated `sbx exec` probes.

Runtime sandbox egress probes may still exist as explicit doctor or manual
diagnostics, but they should not be the synchronous route-event gate.

Clash Verge Rev app-home metadata is treated as an optional event source. If a
supported, safe signal is available, such as a route event or app-home file
metadata change, the watchdog may use it to trigger a host egress recheck. The
watchdog must not depend on undocumented Clash Verge IPC or read/emit proxy
credentials.

## Consequences

- The watchdog remains focused on the safety condition it can observe reliably
  from the host: route and egress drift.
- Docker Sandbox control-plane stalls are no longer confused with egress policy
  drift.
- Startup still fails closed if the sandbox cannot prove its initial egress.
- Runtime checks become simpler, faster, and less likely to destabilize cleanup.
- The system accepts that sandbox egress inheritance is established at startup
  and monitored indirectly through host route and host egress drift afterward.
- Node switches that do not emit a route, interface, or watched app-home file
  metadata event may not be detected immediately. Operators should keep the
  configured expected IP strict and rerun startup validation after ambiguous
  network changes.
