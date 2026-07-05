# Manual Test Plan

These tests require a macOS host, Clash Verge TUN mode, Docker Sandbox / `sbx`,
and a known expected egress IP. Use a local `config.yaml` copied from
`config.example.yaml`; do not put tokens, Claude configuration, SSH keys, Clash
configuration, Keychain material, or other credentials in this repository or in
any mounted workspace.

Run `safe-claude-sbx doctor --config config.yaml` before launcher tests when
you need to isolate preflight failures from runtime watchdog behavior.

Default successful startup output should include:

```text
configuration ok
TUN preflight ok: startup interface utunX
host egress ok: observed IP <expected-ip>
sandbox backend ok: sbx version: ...
sandbox egress ok: observed IP <expected-ip>
sandbox inspection ok
sandbox started: <main-name>
```

Default cleanup behavior is:

- Stop the main sandbox when `cleanup.stop_main_sandbox` is `true`.
- Remove the probe sandbox when `cleanup.remove_probe_sandbox` is `true`.
- Leave the main sandbox state present when `cleanup.remove_main_sandbox` is
  `false`.
- Treat already-missing cleanup targets as non-fatal.

`route -n monitor` is the first MVP runtime event source. Record the observed
route event lines for each runtime scenario, but do not assume every macOS,
Clash Verge, Docker Sandbox, Wi-Fi, sleep/wake, or VPN state transition emits
the same event sequence on every host.

## 1. TUN off at startup

Prerequisites:

- Clash Verge is running.
- Clash Verge TUN mode is off.
- `config.yaml` has the expected egress IP for the network path you intend to
  validate.
- No main sandbox named by `sandbox.main_name` is required.

Steps:

1. Confirm `route get <network.clash_verge.route_check_target>` resolves to a
   non-`utun` interface such as `en0`, or confirm the Clash Verge runtime config
   has `tun.enable: false`.
2. Run `safe-claude-sbx --config config.yaml`.
3. Run `sbx ls` after the command exits.

Expected CLI output:

- Startup fails before `sandbox backend ok`.
- The error starts with `TUN preflight invalid:`.
- The reason should identify either
  `configuration-declaration: verge.yaml enable_tun_mode is not true`,
  `configuration-declaration: clash-verge.yaml tun.enable is not true`, or
  `system-route: default route interface <iface> does not match TUN prefix utun`.

Expected sandbox stop/cleanup behavior:

- No probe sandbox is created.
- No main sandbox is started.
- No `sbx stop <main-name>` is needed.

## 2. TUN on and default route is stable

Prerequisites:

- Clash Verge TUN mode is on.
- `route get <network.clash_verge.route_check_target>` resolves to a live
  `utunX` interface.
- `ifconfig utunX` succeeds.
- Host and sandbox egress should both match
  `network.egress_ip.expected_ip`.

Steps:

1. Run `safe-claude-sbx doctor --config config.yaml`.
2. Run `safe-claude-sbx --config config.yaml`.
3. Leave the attached sandbox running for at least one minute without changing
   Wi-Fi, VPN, or Clash node state.

Expected CLI output:

- `doctor` prints `configuration ok`, `host egress ok`, `sandbox backend ok`,
  `sandbox egress ok`, and `sandbox inspection ok`.
- Launcher startup prints `TUN preflight ok: startup interface utunX`.
- Launcher startup prints `sandbox started: <main-name>`.
- No watchdog failure is printed while the route and egress stay stable.

Expected sandbox stop/cleanup behavior:

- The probe sandbox is removed after preflight.
- The main sandbox remains running while the attached agent is running.
- The main sandbox is stopped only when the agent exits, a signal is received,
  or a later watchdog failure occurs.

## 3. Default route switches away from startup `utunX`

Prerequisites:

- Complete scenario 2 and keep the launcher running.
- Know the startup interface printed in
  `TUN preflight ok: startup interface utunX`.

Steps:

1. In another terminal, run `route -n monitor` and save the observed lines.
2. Change network state so `route get <route_check_target>` resolves to a
   different interface, for example by disabling TUN, changing macOS network
   service priority, or enabling a VPN that takes the default route.
3. Wait for the launcher watchdog to process route events.

Expected CLI output:

- The launcher exits non-zero.
- The error includes `watchdog stopped sandbox: route-monitor runtime policy failed`.
- The reason includes either
  `default route changed from startup interface utunX to <iface>` or another
  runtime check failure naming the route/interface problem.

Expected sandbox stop/cleanup behavior:

- `sbx stop <main-name>` is attempted when `cleanup.stop_main_sandbox` is
  `true`.
- `sbx rm --force <probe-name>` is attempted when
  `cleanup.remove_probe_sandbox` is `true`.
- The main sandbox is not removed unless `cleanup.remove_main_sandbox` is
  explicitly `true`.

## 4. Clash node switch changes egress IP

Prerequisites:

- Complete scenario 2 and keep the launcher running.
- Have two Clash nodes whose public egress IPs differ.
- `network.egress_ip.expected_ip` is set to the first node's IP.

Steps:

1. In another terminal, run `route -n monitor` and save the observed lines.
2. Switch Clash Verge to the second node.
3. Confirm the host egress endpoint now returns a different IP.
4. Wait for the launcher watchdog to process route events.

Expected CLI output:

- If the node switch emits a route event, the launcher exits non-zero.
- The error includes `watchdog stopped sandbox: route-monitor runtime policy failed`
  or `route-monitor runtime check failed`.
- The reason should include `host-egress-mismatch`,
  `sandbox-egress-mismatch`, or `sandbox egress invalid`.

Expected sandbox stop/cleanup behavior:

- On detection, the main sandbox is stopped and the probe sandbox is removed
  according to cleanup policy.
- If the node switch does not emit a route event on the test host, record that
  limitation and validate manually with `safe-claude-sbx doctor --config
  config.yaml`; the MVP does not guarantee polling without a route event.

## 5. Wi-Fi switch

Prerequisites:

- Complete scenario 2 and keep the launcher running.
- Have a second Wi-Fi network available.
- Know whether the second Wi-Fi network should preserve or change the expected
  public egress IP.

Steps:

1. In another terminal, run `route -n monitor` and save the observed lines.
2. Switch macOS from the current Wi-Fi network to the second Wi-Fi network.
3. Run `route get <route_check_target>` and the host egress check endpoint.
4. Wait for the launcher watchdog to process route events.

Expected CLI output:

- If the route remains on the same `utunX` and egress still matches,
  no watchdog failure is expected.
- If the default route changes away from the startup `utunX`, expect
  `watchdog stopped sandbox: route-monitor runtime policy failed` with a
  default-route reason.
- If the route remains on TUN but egress changes, expect a host or sandbox
  egress mismatch reason after a route event.

Expected sandbox stop/cleanup behavior:

- The main sandbox is stopped only when a policy failure is detected.
- The probe sandbox is removed on failure cleanup.
- If macOS changes Wi-Fi without producing route monitor events, record the
  observation and verify the current state with `doctor`.

## 6. Sleep/wake

Prerequisites:

- Complete scenario 2 and keep the launcher running.
- The machine is on AC power or otherwise configured so the test can be
  observed after wake.

Steps:

1. In another terminal, run `route -n monitor` and save the observed lines.
2. Put the Mac to sleep for at least 30 seconds.
3. Wake the Mac and wait for network connectivity to return.
4. Run `route get <route_check_target>` and the host egress check endpoint.
5. Observe the launcher.

Expected CLI output:

- If wake preserves the startup `utunX` route and egress IP, the launcher should
  continue running.
- If wake removes `utunX`, changes the default route, or changes egress, the
  launcher should stop the sandbox with a route-monitor runtime failure after an
  event is observed.
- If `route -n monitor` exits unexpectedly, the launcher should print
  `watchdog stopped sandbox: watchdog event source failed`.

Expected sandbox stop/cleanup behavior:

- On watchdog failure or route monitor failure, cleanup stops the main sandbox
  and removes the probe sandbox according to policy.
- If sleep/wake produces no route event, record the observation and validate
  manually with `doctor`.

## 7. VPN conflict

Prerequisites:

- Complete scenario 2 and keep the launcher running.
- Have a VPN client available that can take the default route or create another
  `utun` route.

Steps:

1. In another terminal, run `route -n monitor` and save the observed lines.
2. Enable the VPN.
3. Run `route get <route_check_target>`.
4. Run the host egress check endpoint.
5. Observe the launcher.

Expected CLI output:

- If the VPN changes the default route to a non-startup interface, expect a
  route-monitor runtime policy failure.
- If the VPN still uses a `utun` interface but not the startup interface, expect
  a failure naming the default route change from the startup interface.
- If the route stays the same but egress changes, expect host or sandbox egress
  mismatch after a route event.

Expected sandbox stop/cleanup behavior:

- On detection, the main sandbox is stopped and the probe sandbox is removed.
- Do not continue using the running sandbox after a VPN conflict unless a fresh
  launcher run passes preflight.

## 8. Docker Sandbox already running

Prerequisites:

- Clash Verge TUN and expected egress are valid.
- A sandbox with `sandbox.main_name` already exists. Test both stopped and
  running states if possible.

Steps:

1. Run `sbx ls` and record the existing sandbox state.
2. Run `safe-claude-sbx doctor --config config.yaml`.
3. Run `safe-claude-sbx --config config.yaml`.

Expected CLI output:

- `doctor` should still validate backend availability, probe creation,
  sandbox egress, and sandbox inspection.
- Launcher startup should either attach to or start the named sandbox according
  to the observed `sbx run claude --name <main-name> <workspace>` behavior.
- If `sbx run` rejects the existing sandbox state, the launcher should fail with
  `sandbox start invalid: start main sandbox: ...`.

Expected sandbox stop/cleanup behavior:

- If the main sandbox starts or attaches successfully, cleanup uses
  `sbx stop <main-name>` when the launcher exits or fails at runtime.
- The launcher must not remove the main sandbox unless
  `cleanup.remove_main_sandbox` is `true`.

## 9. Backend exits by itself

Prerequisites:

- Complete scenario 2 and keep the launcher running.
- Know how to make the attached `sbx run` process exit, for example by exiting
  the agent session or stopping the sandbox from another terminal.

Steps:

1. Exit the attached agent cleanly, or run `sbx stop <main-name>` from another
   terminal.
2. Observe the launcher process.

Expected CLI output:

- Clean backend exit prints `sandbox exited; cleanup complete`.
- Backend exit with an error prints `watchdog stopped sandbox: backend exited:
  start main sandbox: ...`.

Expected sandbox stop/cleanup behavior:

- Cleanup runs once even if the backend already stopped.
- Already-stopped or missing cleanup targets are non-fatal when `sbx` reports a
  not-found result.

## 10. Ctrl+C cleanup

Prerequisites:

- Complete scenario 2 and keep the launcher running.

Steps:

1. Press Ctrl+C in the launcher terminal.
2. Run `sbx ls`.

Expected CLI output:

- The launcher prints `watchdog stopped: signal received; cleanup complete`.
- The process exits with signal-style status `130`.

Expected sandbox stop/cleanup behavior:

- The route watcher stops.
- `sbx stop <main-name>` is attempted when `cleanup.stop_main_sandbox` is
  `true`.
- `sbx rm --force <probe-name>` is attempted when
  `cleanup.remove_probe_sandbox` is `true`.
- The main sandbox remains listed as stopped unless
  `cleanup.remove_main_sandbox` is `true`.

## 11. Host IP mismatch at startup

Prerequisites:

- Clash Verge TUN is on and `route get <route_check_target>` resolves through a
  live `utunX`.
- Set `network.egress_ip.expected_ip` to an intentionally wrong IP.

Steps:

1. Run `safe-claude-sbx doctor --config config.yaml`.
2. Run `safe-claude-sbx --config config.yaml`.

Expected CLI output:

- Both commands fail with `host egress invalid: host-egress-mismatch`.
- The output names the observed and expected IPs.

Expected sandbox stop/cleanup behavior:

- `doctor` fails before probe creation.
- Launcher fails before creating or starting the main sandbox.

## 12. Sandbox IP mismatch at startup

Prerequisites:

- Clash Verge TUN is on and host egress matches the configured expected IP.
- Configure the sandbox check endpoint or network state so the sandbox observes
  a different public IP than `network.egress_ip.expected_ip`.

Steps:

1. Run `safe-claude-sbx doctor --config config.yaml`.
2. Run `safe-claude-sbx --config config.yaml`.
3. Run `sbx ls` after the command exits.

Expected CLI output:

- Startup fails after `sandbox backend ok`.
- The error includes `sandbox probe invalid: sandbox-egress-mismatch` or
  `sandbox probe invalid: sandbox egress response is not an IP address`.

Expected sandbox stop/cleanup behavior:

- The probe sandbox is removed when `cleanup.remove_probe_sandbox` is `true`.
- The main sandbox is not started.

## 13. `sbx` unavailable or unauthenticated

Prerequisites:

- Use a shell where `sbx` is not on `PATH`, or sign out of Docker Sandbox, or
  stop the backend so `sbx ls` cannot succeed.

Steps:

1. Run `safe-claude-sbx doctor --config config.yaml`.
2. Restore `sbx` and authentication.
3. Run `safe-claude-sbx doctor --config config.yaml` again.

Expected CLI output:

- When `sbx` is missing, output includes `sandbox backend invalid: unavailable:
  sbx not found`.
- When authentication or backend reachability fails, output includes
  `sandbox backend invalid: unavailable: sbx list failed: ...`.
- After restoring the backend, output includes `sandbox backend ok: sbx version:
  ...`.

Expected sandbox stop/cleanup behavior:

- No probe sandbox is created when availability fails.
- No main sandbox is started.

## 14. Forbidden mount path

Prerequisites:

- Create a temporary config copy.
- Set `workspace.mount` to a forbidden path such as the home directory, an SSH
  directory, a Claude configuration directory, a Clash configuration directory,
  or a Keychain path. Do not copy real secret material into the repository.

Steps:

1. Run `safe-claude-sbx doctor --config forbidden-mount.yaml`.
2. Run `safe-claude-sbx --config forbidden-mount.yaml`.

Expected CLI output:

- Both commands fail with `configuration invalid: workspace.mount: path is
  forbidden by workspace policy`.

Expected sandbox stop/cleanup behavior:

- Failure occurs before any backend command.
- No probe or main sandbox is created.

## 15. Credential placeholders and SSH agent forwarding in probe

Prerequisites:

- Use a shell or Docker Sandbox environment where the probe can observe
  Docker-managed credential placeholders, for example `OPENAI_API_KEY` or
  `ANTHROPIC_API_KEY` with value class `proxy-managed`.
- If the host has an SSH agent, observe whether Docker Sandbox forwards
  `SSH_AUTH_SOCK`. Do not print or paste any credential or socket value.
- Clash Verge TUN and host egress otherwise pass policy.

Steps:

1. Run `safe-claude-sbx doctor --config config.yaml` with
   `environment.allow_ssh_agent_forwarding: false`.
2. If `SSH_AUTH_SOCK` is observed, confirm `doctor` fails and note only the
   variable name in the error.
3. Set `environment.allow_ssh_agent_forwarding: true` only if this workflow
   accepts sandbox processes requesting SSH agent signatures, then rerun
   `doctor`.
4. Run `sbx ls` after the command exits.

Expected CLI output:

- Docker-managed credential placeholders pass inspection.
- Raw credential values fail with `environment.inspection.env.<NAME>` and do not
  include the value.
- `SSH_AUTH_SOCK` fails by default and passes only when explicitly allowed.

Expected sandbox stop/cleanup behavior:

- On inspection failure, the probe sandbox is removed when
  `cleanup.remove_probe_sandbox` is `true`.
- The launcher must not start the main sandbox after the failed probe.
