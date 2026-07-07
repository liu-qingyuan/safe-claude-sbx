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
- Launcher startup should create a fresh clone-mode main sandbox when needed,
  validate main sandbox workspace visibility without modifying parent guidance
  paths, and then attach with `sbx run --name <main-name>`.
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

## 15. Workspace parent and sibling visibility

Prerequisites:

- Complete scenario 2 with a disposable workspace under a disposable parent
  directory.
- Put a harmless marker file at the workspace parent path named `CLAUDE.md`.
- Put a harmless marker file under a sibling directory, for example
  `<parent>/sibling-project/config.yaml`.
- Do not use real secrets or real project configuration as marker contents.

Steps:

1. Point `workspace.mount` at the disposable workspace.
2. Run `safe-claude-sbx doctor --config config.yaml`.
3. Remove the parent `CLAUDE.md` marker and rerun `doctor`.
4. Remove the sibling marker or switch to a backend/workspace strategy that
   preserves the required `workspace.use_clone_mode: true` isolation, then rerun
   `doctor`.
5. With markers restored, run `safe-claude-sbx --config config.yaml`.
6. For sandbox-local Herdr, run `safe-herdr --config herdr-config.yaml` against
   a disposable existing main sandbox.
7. Run `sbx ls` after each failure.

Expected CLI output:

- When the parent guidance marker is readable, `doctor` fails with
  `workspace.inspection.visibility.parent_guidance`.
- When the sibling marker is readable, `doctor` fails with
  `workspace.inspection.visibility.sibling`.
- During launcher startup, a newly created Claude template sandbox should check
  parent guidance visibility before Claude, Herdr, or `cc` attaches. If
  visibility inspection reports a parent guidance file, startup fails closed
  and stops the main sandbox without modifying the reported path.
- The diagnostic may name the readable path, but it must not print marker file
  contents.
- Once only the configured workspace is readable, inspection can continue to the
  normal egress and environment checks.

Expected sandbox stop/cleanup behavior:

- On inspection failure, the probe sandbox is removed when
  `cleanup.remove_probe_sandbox` is `true`.
- When the probe visibility inspection fails, the main sandbox is not started.
- When the real main sandbox visibility inspection fails after launcher start
  or `safe-herdr` attach, the launcher stops the main sandbox and does not enter
  watchdog supervision.

## 16. Credential placeholders and SSH agent forwarding in probe

Prerequisites:

- Use a shell or Docker Sandbox environment where the probe can observe
  Docker-managed credential placeholders, for example `OPENAI_API_KEY` or
  `ANTHROPIC_API_KEY` with value class `proxy-managed`.
- If the host has an SSH agent, observe whether Docker Sandbox forwards
  `SSH_AUTH_SOCK` or `SSH_AUTH_SOCK_GATEWAY`. Do not print or paste any
  credential, socket, or gateway value.
- Clash Verge TUN and host egress otherwise pass policy.

Steps:

1. Run `safe-claude-sbx doctor --config config.yaml` with
   `environment.allow_ssh_agent_forwarding: false`.
2. If `SSH_AUTH_SOCK` or `SSH_AUTH_SOCK_GATEWAY` is observed, confirm `doctor`
   fails and note only the variable name in the error.
3. Set `environment.allow_ssh_agent_forwarding: true` only if this workflow
   accepts sandbox processes requesting SSH agent signatures, then rerun
   `doctor`.
4. Run `sbx ls` after the command exits.

Expected CLI output:

- Docker-managed credential placeholders pass inspection.
- Raw credential values fail with `environment.inspection.env.<NAME>` and do not
  include the value.
- `SSH_AUTH_SOCK` and `SSH_AUTH_SOCK_GATEWAY` fail by default and pass only
  when explicitly allowed with Docker-managed socket and gateway shapes.

Expected sandbox stop/cleanup behavior:

- On inspection failure, the probe sandbox is removed when
  `cleanup.remove_probe_sandbox` is `true`.
- The launcher must not start the main sandbox after the failed probe.

## 17. Sandbox-local Herdr mode successful startup

Prerequisites:

- Complete scenario 2 with the default direct Claude config first.
- Copy `config.yaml` to `herdr-config.yaml`.
- Keep all existing network, workspace, environment, watchdog, and cleanup
  policy from the working config.
- Change only the sandbox supervision block:

```yaml
sandbox:
  backend: "docker-sandbox"
  main_name: "claude-sbx"
  probe_name: "claude-sbx-probe"
  agent: "claude"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: true
      socket_path: "/home/agent/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"
```

The normal direct Claude path remains the default when
`sandbox.supervision.mode` is omitted or set to `direct-claude`.

Steps:

1. Run `safe-claude-sbx doctor --config herdr-config.yaml`.
2. Run `safe-claude-sbx --config herdr-config.yaml`.
3. In the attached terminal, confirm Claude Code has a real interactive TTY by
   typing a harmless command such as `/status`, then return to the prompt.
4. In a second terminal, set the sandbox name and inspect Herdr inside the
   running sandbox:

```bash
MAIN=claude-sbx
sbx exec "$MAIN" sh -lc 'command -v herdr && herdr --version'
sbx exec "$MAIN" sh -lc \
  'test -x /home/agent/.claude/hooks/herdr-agent-state.sh && echo hook-installed'
sbx exec "$MAIN" herdr status server --json
sbx exec "$MAIN" herdr session list --json
```

5. Confirm host Herdr isolation from the same host terminal:

```bash
env HERDR_SOCKET_PATH=/tmp/host-herdr.sock HERDR_PANE_ID=host-pane HERDR_ENV=1 \
  sbx exec "$MAIN" sh -lc 'env | grep "^HERDR_" || true'
```

6. Exit the attached Claude session or press Ctrl+C in the launcher terminal.
7. Run `sbx ls` after the launcher exits.

Expected CLI output:

- `doctor` prints `configuration ok`, `host egress ok`,
  `sandbox backend ok`, `sandbox egress ok`, and `sandbox inspection ok`.
- Launcher startup prints `configuration ok`,
  `TUN preflight ok: startup interface utunX`,
  `host egress ok: observed IP <expected-ip>`,
  `sandbox backend ok: sbx version: ...`,
  `sandbox egress ok: observed IP <expected-ip>`,
  `sandbox inspection ok`, and `sandbox started: <main-name>`.
- The in-sandbox Herdr checks print a Herdr binary path, `herdr <version>`,
  `hook-installed`, server status reporting a running server with the configured
  socket path, and a JSON session list.
- The host Herdr isolation command prints no `HERDR_*` values from inside the
  sandbox unless they are the sandbox-local values injected by the launcher for
  the Claude process itself.
- Host Herdr does not show sandbox Claude state. Sandbox-local Herdr state is
  visible only from inside the Docker Sandbox. This is the intended isolation
  boundary, not a bug.

Expected sandbox stop/cleanup behavior:

- The probe sandbox is removed after preflight when
  `cleanup.remove_probe_sandbox` is `true`.
- On normal exit or Ctrl+C, cleanup stops the sandbox-local Herdr server before
  stopping the main sandbox.
- The main sandbox remains listed as stopped unless
  `cleanup.remove_main_sandbox` is `true`.

Known limits for this mode:

- There is no host-to-sandbox Herdr bridge.
- The launcher does not expose or reuse the host Herdr socket.
- The launcher does not pass host `HERDR_*` environment into Docker Sandbox.
- The watchdog remains route-event based; this mode does not add periodic
  polling.

## 18. Sandbox-local Herdr mode fail-closed checks

Run these with `herdr-config.yaml` from scenario 17 and a disposable sandbox
name when the test can mutate sandbox state:

```yaml
sandbox:
  main_name: "claude-sbx-herdr-negative"
  probe_name: "claude-sbx-herdr-negative-probe"
```

### TUN disabled

Steps:

1. Turn Clash Verge TUN mode off.
2. Run `safe-claude-sbx --config herdr-config.yaml`.
3. Run `sbx ls`.

Expected result:

- Startup fails before `sandbox backend ok`.
- The error starts with `TUN preflight invalid:`.
- No probe sandbox is created and no main sandbox is started.

### Host egress mismatch

Steps:

1. Turn TUN mode back on.
2. Set `network.egress_ip.expected_ip` to an intentionally wrong IP.
3. Run `safe-claude-sbx doctor --config herdr-config.yaml`.
4. Run `safe-claude-sbx --config herdr-config.yaml`.

Expected result:

- Both commands fail with `host egress invalid: host-egress-mismatch`.
- The launcher fails before creating the probe or main sandbox.

### Sandbox egress or inspection failure

Steps:

1. Restore the correct host expected IP.
2. Force a sandbox probe failure by using a sandbox check endpoint that returns
   a different IP, or by setting `workspace.mount` to a forbidden path such as
   `~/.ssh` in a disposable config.
3. Run `safe-claude-sbx doctor --config herdr-config.yaml`.
4. Run `safe-claude-sbx --config herdr-config.yaml`.

Expected result:

- Sandbox egress mismatch fails with
  `sandbox probe invalid: sandbox-egress-mismatch`.
- Forbidden mount inspection fails with
  `configuration invalid: workspace.mount: path is forbidden by workspace policy`.
- When a probe sandbox was created before the failure, it is removed when
  `cleanup.remove_probe_sandbox` is `true`.
- The main sandbox is not started after the failed probe or inspection.

### Herdr unavailable or startup failure

Steps:

1. Use a fresh disposable `sandbox.main_name`.
2. Set `sandbox.supervision.herdr.install_if_missing: false`.
3. Run `safe-claude-sbx --config herdr-config.yaml`.
4. Run `sbx ls`.

Expected result:

- If the fresh Claude template does not already contain Herdr, startup fails
  after `sandbox inspection ok` with `sandbox start invalid:` and a reason that
  includes `sandbox-local Herdr unavailable`.
- If a future Claude template includes Herdr by default, record that this
  negative precondition is unavailable and instead validate a Herdr startup
  failure by blocking the sandbox from reaching the Herdr installer endpoint
  before step 3 with `install_if_missing: true`.
- The launcher does not fall back to direct Claude mode.
- Cleanup stops the disposable main sandbox. The main sandbox remains listed as
  stopped unless `cleanup.remove_main_sandbox` is `true`.

### Runtime Google connectivity failure is diagnostic only

Steps:

1. Start a disposable sandbox with a config whose
   `network.egress_ip.sandbox_check_url` returns the expected public IP.
2. In the running main sandbox, run
   `sbx exec <main-name> curl -fsS https://www.google.com`.
3. In the same sandbox, run
   `sbx exec <main-name> curl -fsS <network.egress_ip.sandbox_check_url>`.
4. Trigger a route event without changing away from the startup TUN interface.

Expected result:

- If Google fails but the configured sandbox check URL returns the expected IP,
  record the result as `Google connectivity failed`; the watchdog should keep
  the main sandbox running.
- If the configured sandbox check URL times out and no observed IP mismatch is
  returned, the watchdog reports `sandbox-egress-indeterminate`, retries once,
  and only then fails closed if the result remains indeterminate.
- If the configured sandbox check URL returns a different observed IP, the
  watchdog reports `sandbox-egress-mismatch` and stops the main sandbox without
  treating the result as a transient Google or proxy failure.
