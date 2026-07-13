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
- `doctor` and `safe-herdr` do not create a temporary probe sandbox on the hot
  path. Legacy direct-Claude probe diagnostics clean up their temporary sandbox
  before attach/start behavior.
- Leave the main sandbox state present when `cleanup.remove_main_sandbox` is
  `false`.
- Treat already-missing cleanup targets as non-fatal.

Runtime watchdog checks are event-triggered. The current event sources are
`route -n monitor` and Clash Verge app-home file metadata changes. Record the
observed route event lines and changed app-home path names for runtime
scenarios, but do not paste Clash configuration contents, subscriptions, node
names, secrets, Claude tokens, SSH keys, Docker credentials, or Herdr
socket/env values into issues. Do not assume every macOS, Clash Verge, Docker
Sandbox, Wi-Fi, sleep/wake, VPN, or node transition emits the same event
sequence on every host.

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

- `doctor` inspects the configured main sandbox directly without a temporary
  probe. The direct Claude launcher may still clean up its legacy probe after
  preflight.
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
- The error includes `watchdog stopped sandbox: route-monitor runtime check failed`.
- The reason includes either
  `default route changed from startup interface utunX to <iface>` or another
  runtime check failure naming the route/interface problem.

Expected sandbox stop/cleanup behavior:

- `sbx stop <main-name>` is attempted when `cleanup.stop_main_sandbox` is
  `true`.
- The main sandbox is not removed unless `cleanup.remove_main_sandbox` is
  explicitly `true`.

## 4. Clash node switch changes host egress IP

Prerequisites:

- Complete scenario 2 and keep the launcher running.
- Have two Clash nodes whose public egress IPs differ.
- `network.egress_ip.expected_ip` is set to the first node's IP.
- Confirm `network.clash_verge.app_home` points at the active Clash Verge Rev
  app home.

Steps:

1. In another terminal, run `route -n monitor` and save the observed lines.
2. Switch Clash Verge to the second node.
3. Confirm the host egress endpoint now returns a different IP.
4. Note only which watched app-home paths changed, if any, for example
   `verge.yaml`, `clash-verge.yaml`, `profiles.yaml`, or `profiles`.
5. Wait for the launcher watchdog to process route or Clash app-home metadata
   events.

Expected CLI output:

- If the node switch emits a route event or app-home metadata event, the
  launcher exits non-zero.
- The error includes either
  `watchdog stopped sandbox: route-monitor runtime check failed` or
  `watchdog stopped sandbox: clash-app-home runtime check failed`.
- The reason includes `host egress drift` and `host-egress-mismatch`.
- The runtime failure must not report `sandbox-egress-mismatch`,
  `sandbox egress invalid`, or `sandbox-egress-indeterminate`.

Expected sandbox stop/cleanup behavior:

- On detection, the main sandbox is stopped according to cleanup policy.
- If the node switch emits neither a route event nor a watched app-home metadata
  change on the test host, record that limitation and validate manually with
  `safe-claude-sbx doctor --config config.yaml`; the watchdog does not
  continuously poll the public IP endpoint without an event.

## 5. Clash app-home metadata changes while host egress stays valid

Prerequisites:

- Complete scenario 2 and keep the launcher running.
- `network.clash_verge.app_home` points at a disposable or active Clash Verge
  Rev app home that the launcher can read.
- Host egress currently matches `network.egress_ip.expected_ip`.

Steps:

1. Record the active app-home path without copying file contents.
2. Change metadata for a watched path, for example by switching a harmless
   profile setting in Clash Verge or touching a disposable copied app-home file
   during a controlled local test.
3. Confirm the host egress endpoint still returns the expected IP.
4. Wait for the launcher watchdog to process the Clash app-home event.

Expected CLI output:

- No watchdog failure is printed while the startup TUN route, startup TUN
  interface, and host egress remain valid.
- If a failure occurs, it should name `clash-app-home` as the event source and
  report the concrete route/interface/host-egress reason.

Expected sandbox stop/cleanup behavior:

- The main sandbox continues running when policy remains valid.
- No probe sandbox is created for this runtime check.

## 6. Wi-Fi switch

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
  `watchdog stopped sandbox: route-monitor runtime check failed` with a
  default-route reason.
- If the route remains on TUN but host egress changes, expect a host egress
  drift reason after a route or app-home metadata event.

Expected sandbox stop/cleanup behavior:

- The main sandbox is stopped only when a policy failure is detected.
- If macOS changes Wi-Fi without producing route monitor events, record the
  observation and verify the current state with `doctor`.

## 7. Sleep/wake

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
  according to policy.
- If sleep/wake produces no route event, record the observation and validate
  manually with `doctor`.

## 8. VPN conflict

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
- If the route stays the same but host egress changes, expect host egress drift
  after a route or app-home metadata event.

Expected sandbox stop/cleanup behavior:

- On detection, the main sandbox is stopped according to cleanup policy.
- Do not continue using the running sandbox after a VPN conflict unless a fresh
  launcher run passes preflight.

## 9. Docker Sandbox already running

Prerequisites:

- Clash Verge TUN and expected egress are valid.
- A sandbox with `sandbox.main_name` already exists. Test both stopped and
  running states if possible.

Steps:

1. Run `sbx ls` and record the existing sandbox state.
2. Run `safe-claude-sbx doctor --config config.yaml`.
3. Run `safe-claude-sbx --config config.yaml`.

Expected CLI output:

- `doctor` should validate backend availability, configured main sandbox
  egress, and configured main sandbox inspection without creating a temporary
  probe sandbox.
- Launcher startup should create a fresh main sandbox when needed, validate main
  sandbox workspace visibility without modifying parent guidance paths, and then
  attach with `sbx run --name <main-name>`.
- If `sbx run` rejects the existing sandbox state, the launcher should fail with
  `sandbox start invalid: start main sandbox: ...`.

Expected sandbox stop/cleanup behavior:

- If the main sandbox starts or attaches successfully, cleanup uses
  `sbx stop <main-name>` when the launcher exits or fails at runtime.
- The launcher must not remove the main sandbox unless
  `cleanup.remove_main_sandbox` is `true`.

## 10. Backend exits by itself

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

## 11. Ctrl+C cleanup

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
- The main sandbox remains listed as stopped unless
  `cleanup.remove_main_sandbox` is `true`.

## 12. Host IP mismatch at startup

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

- `doctor` fails before configured main sandbox inspection.
- Launcher fails before creating or starting the main sandbox.

## 13. Sandbox IP mismatch at startup

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
- For `doctor` and `safe-herdr`, the error includes
  `main sandbox preflight invalid: sandbox-egress-mismatch` or a main sandbox
  egress parse/command diagnostic. For the direct Claude launcher, the legacy
  probe path may still report `sandbox probe invalid: sandbox-egress-mismatch`.

Expected sandbox stop/cleanup behavior:

- `doctor` and `safe-herdr` clean up only main sandbox state created by the
  current command. The direct Claude launcher cleans up its temporary probe
  before refusing to start the main sandbox.

## 14. `sbx` unavailable or unauthenticated

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

## 15. Forbidden mount path

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

## 16. Workspace parent metadata and sibling visibility

Prerequisites:

- Complete scenario 2 with a disposable workspace under a disposable parent
  directory.
- Optionally put a harmless marker file at the workspace parent path named
  `CLAUDE.md` to confirm parent guidance metadata behavior.
- Put a harmless marker file under a sibling directory, for example
  `<parent>/sibling-project/config.yaml`.
- Do not use real secrets or real project configuration as marker contents.

Steps:

1. Point `workspace.mount` at the disposable workspace.
2. Run `safe-claude-sbx doctor --config config.yaml`.
3. Confirm a parent `CLAUDE.md` marker alone does not fail `doctor`.
4. Remove the sibling marker or switch to a backend/workspace strategy that
   prevents sibling visibility, then rerun `doctor`.
5. With markers restored, run `safe-claude-sbx --config config.yaml`.
6. For sandbox-local Herdr, run `safe-herdr --config herdr-config.yaml` against
   a disposable existing main sandbox.
7. Run `sbx ls` after each failure.

Expected CLI output:

- Parent guidance visibility is treated as Docker Sandbox/Claude template
  workspace metadata and does not fail by itself.
- When the sibling marker is readable, `doctor` fails with
  `workspace.inspection.visibility.sibling`.
- During launcher startup, a newly created Claude template sandbox should check
  sibling visibility before Claude, Herdr, or `cc` attaches. If visibility
  inspection reports a sibling project file, startup fails closed and stops the
  main sandbox without modifying the reported path.
- The diagnostic may name the readable path, but it must not print marker file
  contents.
- Once only the configured workspace is readable, inspection can continue to the
  normal egress and environment checks.

Expected sandbox stop/cleanup behavior:

- On `doctor` or `safe-herdr` inspection failure, only main sandbox state
  created by the current command is eligible for cleanup.
- When the main sandbox visibility inspection fails before attach/start, the
  launcher stops the newly created main sandbox and does not enter watchdog
  supervision.

## 17. Credential placeholders and SSH agent forwarding in main inspection

Prerequisites:

- Use a shell or Docker Sandbox environment where the configured main sandbox can observe
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

- On `doctor` or `safe-herdr` inspection failure, only main sandbox state
  created by the current command is eligible for cleanup.
- The launcher must not attach Herdr or start Claude after failed main sandbox
  inspection.

## 18. Sandbox-local Herdr mode successful startup

Prerequisites:

- Complete scenario 2 with the default direct Claude config first.
- Build the sandbox-local Herdr template:

```bash
docker build -t safe-claude-sbx-herdr:latest sandbox/claude-herdr-template
docker image save safe-claude-sbx-herdr:latest -o safe-claude-sbx-herdr.tar
sbx template load safe-claude-sbx-herdr.tar
```

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
  template: "safe-claude-sbx-herdr:latest"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: false
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

- No temporary probe sandbox is created by `doctor` or `safe-herdr`.
- On normal exit or Ctrl+C, cleanup stops the sandbox-local Herdr server before
  stopping the main sandbox.
- The main sandbox remains listed as stopped unless
  `cleanup.remove_main_sandbox` is `true`.

Known limits for this mode:

- There is no host-to-sandbox Herdr bridge.
- The launcher does not expose or reuse the host Herdr socket.
- The launcher does not pass host `HERDR_*` environment into Docker Sandbox.
- The watchdog remains event-triggered from route monitor and Clash app-home
  metadata sources; this mode does not add periodic polling.

## 19. Sandbox-local Herdr mode fail-closed checks

Run these with `herdr-config.yaml` from scenario 18 and a disposable sandbox
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
- The launcher fails before creating or inspecting the main sandbox.

### Sandbox egress or inspection failure

Steps:

1. Restore the correct host expected IP.
2. Force a main sandbox preflight failure by using a sandbox check endpoint that
   returns a different IP, or by setting `workspace.mount` to a forbidden path
   such as `~/.ssh` in a disposable config.
3. Run `safe-claude-sbx doctor --config herdr-config.yaml`.
4. Run `safe-claude-sbx --config herdr-config.yaml`.

Expected result:

- Sandbox egress mismatch fails with
  `main sandbox preflight invalid: sandbox-egress-mismatch`.
- Forbidden mount inspection fails with
  `configuration invalid: workspace.mount: path is forbidden by workspace policy`.
- Main sandbox state created by the current command is cleaned up after failed
  preflight or inspection.
- Herdr is not attached after failed main sandbox preflight or inspection.

### Herdr unavailable or startup failure

Steps:

1. Use a fresh disposable `sandbox.main_name`.
2. Point `sandbox.template` at a disposable template image that intentionally
   does not contain Herdr or `/usr/local/bin/cc`.
3. Keep `sandbox.supervision.herdr.install_if_missing: false`.
4. Run `safe-claude-sbx --config herdr-config.yaml`.
5. Run `sbx ls`.

Expected result:

- Startup fails after `sandbox inspection ok` with `sandbox start invalid:` and
  a reason that includes `sandbox-local Herdr unavailable` or
  `sandbox-local cc unavailable`.
- The launcher does not run a Herdr installer in the running sandbox.
- The launcher does not fall back to direct Claude mode.
- Cleanup stops the disposable main sandbox. The main sandbox remains listed as
  stopped unless `cleanup.remove_main_sandbox` is `true`.

### Docker Sandbox control-plane stall is diagnostic only

Steps:

1. Start a disposable sandbox with a config whose
   `network.egress_ip.sandbox_check_url` returns the expected public IP.
2. Simulate or observe a Docker Sandbox control-plane stall, for example an
   `sbx exec <main-name> ...` command that hangs in a separate diagnostic
   terminal.
3. Trigger a route or Clash app-home metadata event without changing away from
   the startup TUN interface and without changing host egress.
4. If cleanup needs to stop a sandbox while the control plane is unhealthy,
   record only the `sbx` error class and sandbox name.

Expected result:

- Runtime events do not run `sbx exec <main-name> curl ...` as the watchdog
  gate.
- If the route, startup TUN interface, and host egress remain valid, the
  watchdog should keep the main sandbox running even if a separate manual
  `sbx exec` diagnostic is stuck.
- If cleanup later fails because `sbx stop <main-name>` cannot reach the Docker
  Sandbox control plane, the error should be recorded as `sbx control-plane
  failure`, not as `sandbox-egress-mismatch`,
  `sandbox-egress-indeterminate`, or `sandbox egress invalid`.

## 20. Dedicated private Docker Engine and protocol acceptance

This scenario is a strict gate for `dedicated-gateway`; it is not a routine
launcher smoke test. Run it only with a disposable sandbox name, a cached
`shell-docker` template, an operator-managed disposable gateway, and an approved
public test endpoint. Record hostnames, status classes, and expected public IPs
only. Do not record proxy configuration, subscriptions, node names, credentials,
controller secrets, or response bodies other than the public IP result.

Prerequisites:

- `sbx version` and the Docker Sandbox version under test are recorded.
- `sbx ls` is saved so preexisting sandboxes can be distinguished from the
  disposable sandbox. No preexisting sandbox may be removed.
- The `shell-docker` template is already cached. Template transfer is a host
  sandboxd control-plane path and must not be attempted after acquiring the
  dedicated lease.
- The disposable gateway is healthy, loopback-only, credential-free at the
  sandboxd upstream endpoint, and has connection logging enabled without
  sensitive configuration output.
- The normal sandboxd daemon can be restored after the experiment. Do not
  modify host routes or reset/change global Docker Sandbox policy.

Steps:

1. Start sandboxd with the command-scoped
   `DOCKER_SANDBOXES_PROXY=<loopback-http-upstream>` environment described in
   `docs/docker-sandbox-backend.md`. Do not add generic proxy environment
   variables.
2. Create one uniquely named disposable `shell-docker` sandbox and verify its
   private Docker Engine with `docker version`.
3. From the agent, request the approved IP endpoint over HTTP and HTTPS. From an
   `alpine` container, repeat both requests without injecting proxy variables.
4. Pull one uncached, disposable container image with the private Docker Engine.
   Confirm registry, authentication, and layer-download hosts appear in gateway
   logs. Do not confuse this with `sbx create` template transfer.
5. From agent and container, resolve a unique harmless hostname so cached DNS
   cannot hide the path. Confirm the query uses a documented, dedicated-aware
   resolver path.
6. From the container, connect to an approved non-HTTP TCP endpoint, for example
   the SSH banner endpoint at `ssh.github.com:443`. Confirm the connection is
   either recorded by the dedicated gateway or explicitly denied by policy.
7. Confirm direct external UDP DNS and ICMP are blocked. Check `sbx policy log`
   for `<udp proxy policy>` and `<icmp proxy policy>` without changing policy.
8. Stop only the disposable gateway while leaving the dedicated sandboxd lease
   active. Repeat agent/container HTTP and HTTPS, the unique DNS lookup, the
   non-HTTP TCP connection, and an uncached private Engine image pull.
9. Stop and remove only the disposable sandbox, stop the dedicated daemon,
   restore normal sandboxd without `DOCKER_SANDBOXES_PROXY`, and verify the
   preexisting sandbox list and states are unchanged.

Pass conditions:

- Agent and container HTTP/HTTPS return the configured expected dedicated IP
  while the gateway is healthy and fail without an IP after gateway loss.
- Private Engine image pull hosts appear in gateway logs while healthy; an
  uncached pull fails after gateway loss without host-direct fallback.
- Allowed non-HTTP TCP is observable on the dedicated path. If the backend
  cannot route it through that path, it must be denied.
- Agent and container DNS use a documented dedicated-aware path and cannot keep
  producing external resolutions after gateway loss.
- Direct UDP and ICMP remain blocked by Docker Sandbox policy.
- Any raw TCP connection or unique external DNS lookup that succeeds after
  gateway loss fails this scenario. Keep the validation Ticket open and create
  or retain a blocker rather than weakening the pass condition.

Observed `sbx v0.34.0` result on 2026-07-13:

- HTTP/HTTPS and private Engine pulls met the fail-closed conditions.
- Direct external UDP DNS and ICMP were blocked.
- Agent/container unique DNS lookups and raw TCP remained available after
  gateway loss, so the overall scenario failed. See blocker #51.
