# Dedicated Gateway Operations

This runbook is for operators evaluating `network.egress.mode:
dedicated-gateway`. It covers launcher-owned egress guard behavior, external
Mihomo/MetaCubeXD responsibilities, failure recovery, and acceptance evidence.

## Current Support Boundary

Dedicated mode is capability-gated. No released Docker Sandbox version is in
the production support matrix yet. Local validation found that `sbx v0.34.0`
provides an HTTP upstream and `sbx v0.35.0` adds the documented SOCKS5/SOCKS5h
transport. Neither release makes generic TCP and internal DNS fail closed.
The production Adapter therefore rejects both before controller access, daemon
mutation, sandbox creation, main inspection, attach, or watchdog entry.

For `sbx v0.34.0`, a non-zero result containing the following text is the
passing production acceptance result:

```text
dedicated protocol isolation unsupported: sbx v0.34.0 provides HTTP upstream only; generic TCP and DNS are not fail closed
```

For the installed `sbx v0.35.0`, the passing production acceptance result is:

```text
dedicated protocol isolation unsupported: sbx v0.35.0 has no validated generic TCP and DNS contract
```

Do not bypass this gate. The supported-path launcher and EgressGuard tests
exercise the future lifecycle contract, but they are not evidence that the
installed Docker Sandbox release is protocol-complete.

## Ownership Boundary

The operator owns:

- Mihomo installation, process lifecycle, provider and node configuration,
  subscriptions, and upgrade policy.
- MetaCubeXD installation, UI assets, and access policy.
- The dedicated upstream route and its approved public egress.
- Supplying the Mihomo controller secret through the configured host
  environment variable.

The launcher owns:

- validating the Docker Sandbox protocol capability before side effects;
- checking the authenticated loopback controller without printing its secret;
- acquiring and supervising the exclusive sandboxd upstream lease;
- validating the main sandbox egress and controller isolation;
- attaching direct Claude or sandbox-local Herdr only after startup validation;
- canceling active work, fencing dedicated egress, and recovering normal
  sandboxd without restarting main before main cleanup.

The launcher never installs, starts, stops, updates, or reconfigures Mihomo or
MetaCubeXD. The dashboard is an operator UI, not startup evidence. A green
dashboard does not replace controller health, exclusive lease, controller
isolation, or observed sandbox egress checks.

## Network Boundary

`DOCKER_SANDBOXES_PROXY` is a command-scoped sandboxd upstream setting. On the
validated release it selects one daemon-global upstream that Docker exposes to
individual runtimes through managed listeners. It is not a per-sandbox route
selector.

Do not set generic `HTTP_PROXY`, `HTTPS_PROXY`, or `ALL_PROXY` variables on
sandboxd. Do not inject the dedicated upstream URL into the sandbox. Workloads
continue to see Docker-managed proxy values such as
`gateway.docker.internal:3128`; sandboxd connects those managed listeners to the
dedicated upstream while the lease is active.

The dedicated workload guarantee does not include Docker control-plane
traffic. These operations continue to use host networking:

- Docker login and credential exchange;
- Docker Sandbox updates and CLI release checks;
- template pull, build, save, and `sbx template load` traffic;
- sandboxd control commands themselves.

An image pull made by the private Docker Engine inside a sandbox is workload
traffic and must be validated separately. Do not confuse it with a host-side
template pull.

## Configuration Contract

Keep project configuration limited to launcher-required facts:

```yaml
network:
  egress:
    mode: "dedicated-gateway"
    dedicated_gateway:
      upstream_url: "http://127.0.0.1:17890"
      controller_url: "http://127.0.0.1:19090"
      controller_secret_env: "SAFE_CLAUDE_SBX_MIHOMO_SECRET"
  egress_ip:
    expected_ip: "203.0.113.10"
    sandbox_check_url: "https://ipv4.icanhazip.com"
    timeout_seconds: 60
```

Both URLs must be credential-free loopback HTTP URLs with explicit ports. The
secret value belongs only in the named host environment variable, not in YAML,
shell history, logs, issue comments, or the mounted workspace. Mihomo providers,
nodes, subscriptions, rules, and MetaCubeXD settings stay in external gateway
configuration.

The controller must not be reachable from the main sandbox. Loopback binding on
the host is necessary but not sufficient; startup validation also probes the
configured endpoint from main and requires it to be unreachable.

## Unified EgressGuard Flow

For a future explicitly supported backend, `doctor`, direct
`safe-claude-sbx --config`, and `safe-herdr` share this flow:

1. `Acquire` checks backend capability, controller health, and exclusive
   sandboxd scope, then starts launcher-owned sandboxd with the command-scoped
   upstream.
2. The backend preflights the configured main sandbox and observes its current
   egress. Existing main state is revalidated rather than trusted.
3. `ValidateMain` rechecks daemon scope and controller health, then proves that
   the controller is unreachable from main.
4. `doctor` immediately finalizes the lease. Direct Claude attaches the agent
   to the validated main; `safe-herdr` prepares and attaches Herdr. Both launch
   targets then enter the existing Watchdog Supervisor.
5. Dedicated `Watch` observes owned sandboxd exit and scheduled health checks.
   Health checks validate daemon status, controller health, exclusive scope,
   and current main egress in that order.
6. Every exit path cancels the active attach and runtime check, calls `Fence`
   so main cannot continue to egress, calls `Recover` to start normal sandboxd
   without an upstream or main restart, and only then retains or removes the
   stopped main according to ownership and cleanup policy.

`host-inherited` uses the same Interface and Supervisor, but its Adapter keeps
the existing macOS TUN, host egress, route-event, and Clash app-home behavior.

## Startup Procedure

1. Record `sbx version`. Continue only if that exact release is in the Adapter's
   explicit support matrix.
2. Confirm no unrelated sandbox is running. The daemon-global lease cannot be
   shared with an unrelated workload.
3. Start and validate the operator-managed Mihomo/MetaCubeXD stack outside the
   workspace. Do not expose configuration contents as evidence.
4. Export only the controller secret variable referenced by config. Disable
   shell tracing before handling it.
5. Run the doctor:

   ```bash
   safe-claude-sbx doctor --config config.yaml
   ```

6. Run one guarded launch target only after doctor succeeds:

   ```bash
   safe-claude-sbx --config config.yaml
   # or
   safe-herdr --config config.yaml
   ```

Both commands use the same dedicated capability, main preflight, controller
isolation, watchdog, and teardown contract. The supervision choice does not
change the egress guarantee.

On the validated `sbx v0.34.0` and `sbx v0.35.0` candidates, stop routine
startup after the expected capability rejection. Do not start a dedicated
daemon manually to make launcher startup continue. Only an explicitly scoped
candidate-validation Ticket may run the disposable matrix before enablement.

## Runtime Evidence

A healthy supported session must provide all of these observations:

- authenticated controller health on host loopback;
- no unrelated running sandbox in the daemon-global lease scope;
- controller endpoint unreachable from main;
- main observed egress equals `network.egress_ip.expected_ip`;
- scheduled dedicated health checks continue to pass;
- gateway logs show only the approved, redacted connection metadata needed to
  prove traversal.

Dashboard status, browser-side IP widgets, persisted main metadata, and public
IP equality by themselves are insufficient. Dedicated and host paths can share
the same public IP, and a restarted normal daemon can reuse existing sandbox
metadata without the dedicated upstream.

## Failure Handling

| Failure | Launcher behavior | Operator action |
| --- | --- | --- |
| Backend capability unsupported or unknown | Reject before controller, lease, or main operations | Keep dedicated mode disabled; validate a newer release through the full disposable protocol matrix before adding support |
| Gateway or controller loss | Runtime check fails closed, then fences and recovers before main cleanup | Repair the external gateway, verify loopback controller health, and start a new guarded session |
| Lease conflict | `Acquire` rejects an unrelated running sandbox | Preserve preexisting sandboxes; stop only an operator-confirmed disposable conflict or use `host-inherited` |
| Lease drift or owned sandboxd exit | Watchdog fails closed and starts ordered cleanup | Do not reuse the session; restore normal sandboxd, then rerun doctor |
| Startup or runtime egress mismatch | Reject or stop without accepting the observed IP | Fix the gateway route or the approved expected IP source; do not change policy merely to match an unexpected observation |
| Controller reachable from main | Reject before attach | Correct host binding and Docker exposure; never mount controller sockets or config into main |
| Fence failure | Report `sandboxd lease fence invalid`; skip automatic recovery but still attempt main cleanup | Treat dedicated daemon state as unknown and follow the explicit host-inherited recovery below |
| Recover failure | Report `sandboxd lease recovery invalid`; main cleanup is still attempted | Treat normal daemon state as unknown and follow the explicit host-inherited recovery below |
| Main cleanup failure | Report cleanup failure after fence/recovery attempts | Verify main state by name; stop it, but do not remove a preexisting main |

Do not restart Mihomo from launcher recovery logic. It remains operator-owned
even when gateway loss triggered the failure.

## Recover to Host-Inherited Mode

Use this procedure when dedicated startup fails, lease state is uncertain, or
the operator intentionally returns to the normal host path:

1. Stop the launcher session and wait for its fence/recovery/cleanup result. Do not run
   `sbx reset`.
2. Record the configured main sandbox's pre-recovery existence and state. Do
   not delete it.
3. If `Recover` succeeded, keep the restored normal sandboxd and continue to
   step 5. If `Fence` or `Recover` failed, treat daemon state as unknown and stop it:

   ```bash
   sbx daemon stop
   ```

4. After the failure-path stop, start normal sandboxd in a dedicated terminal
   from an environment that removes the dedicated upstream and generic proxy
   variables:

   ```bash
   env -u DOCKER_SANDBOXES_PROXY \
     -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
     -u http_proxy -u https_proxy -u all_proxy \
     sbx daemon start
   ```

   `sbx daemon start` is a foreground process. Keep that terminal running; use
   a second terminal for steps 5-7. Stopping the command stops sandboxd.

5. Change only `network.egress.mode` to `host-inherited` and restore the normal
   `network.clash_verge` plus host egress settings.
6. Run `safe-claude-sbx doctor --config config.yaml`. Let the normal launcher
   revalidate the existing main before reuse.
7. Confirm the preexisting main still exists. Restore its previous running
   state only through the normal guarded workflow.

This procedure restores daemon configuration; it does not remove templates,
reset global policy, modify host routes, or delete preexisting sandboxes.

## Disposable Acceptance

Routine release acceptance may run the strict matrix in
`tests/manual-test-plan.md` only after production code explicitly recognizes
the exact Docker Sandbox release. An explicitly scoped candidate-validation
Ticket may run it before enablement only when it names the exact version, owns
one disposable sandbox, preserves all preexisting state, and keeps the
production support matrix unchanged until a PASS. The matrix must cover
startup, HTTP/HTTPS no-fallback, generic TCP, DNS, private Docker Engine pulls,
policy evidence, daemon recovery, and cleanup. A later enablement Ticket must
separately cover doctor, direct Claude, `safe-herdr`, watchdog failures, and
`Fence`/`Recover` ordering.

Acceptance is incomplete if generic TCP or external DNS remains available after
gateway loss, even when HTTP/HTTPS and image pulls fail closed. Keep the backend
unsupported rather than weakening the pass condition.

After every disposable run, verify:

- no disposable sandbox remains;
- normal sandboxd is running without `DOCKER_SANDBOXES_PROXY`;
- no launcher-owned lease or temporary config remains;
- the operator has stopped any gateway created only for the test;
- every main used by the dedicated lease is stopped before Runner returns;
- a preexisting main is retained, while a launcher-created main is removed only
  when `cleanup.remove_main_sandbox` requests it;
- shared evidence contains no controller secret, provider, node, subscription,
  credential, raw environment, response body, or sensitive configuration.
