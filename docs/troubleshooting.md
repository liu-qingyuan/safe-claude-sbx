# Troubleshooting

The launcher fails closed. Start with the first error prefix printed by the CLI,
then use the matching section below. Error messages should name policy fields
and failure kinds without printing captured secret values.

Do not paste tokens, OAuth sessions, SSH keys, Claude user configuration, Clash
configuration, Keychain material, or complete sandbox environment dumps into
issues. Redact hostnames, usernames, IPs, and paths when they are not needed for
the diagnosis.

## Clash Verge TUN is not enabled

Typical output:

```text
TUN preflight invalid: configuration-declaration: verge.yaml enable_tun_mode is not true
TUN preflight invalid: configuration-declaration: clash-verge.yaml tun.enable is not true
```

Meaning:

- Clash Verge's app setting or generated runtime config does not declare TUN as
  enabled.
- The launcher treats Clash Verge config as a declaration only; it still
  requires live macOS route and interface checks before starting a sandbox.

Checks:

- Enable TUN in Clash Verge and wait for the core to reload.
- Confirm `network.clash_verge.app_home` points at the active Clash Verge Rev
  app home if you use portable mode or a non-default app location.
- Re-run `safe-herdr --config config.yaml` for the Herdr TUI flow, or
  `safe-claude-sbx --config config.yaml` for direct Claude mode.

Do not attach the local Clash config file to an issue. If needed, report only
whether `enable_tun_mode` and generated `tun.enable` are true after redaction.

## `utunX` does not exist

Typical output:

```text
TUN preflight invalid: system-interface: TUN interface utun9 missing: ...
```

Meaning:

- `route get <network.clash_verge.route_check_target>` resolved to `utun9`, but
  `ifconfig utun9` failed.
- The route may be stale, Clash Verge may have restarted, or macOS may have
  removed the interface during a network transition.

Checks:

- Run `ifconfig utun9`.
- Toggle Clash Verge TUN off and on, then re-run the launcher.
- If the generated Clash runtime config declares a specific `tun.device`, check
  whether it still matches the live route interface.

Expected behavior:

- Startup is rejected before Docker Sandbox preflight.
- No main sandbox should be created.

## Default route does not match TUN policy

Typical output:

```text
TUN preflight invalid: system-route: default route interface en0 does not match TUN prefix utun
TUN preflight invalid: system-route: default route interface utun10 does not match mihomo tun.device utun9
watchdog stopped sandbox: route-monitor runtime policy failed: default route changed from startup interface utun9 to en0
```

Meaning:

- At startup, default external traffic is not routed through the expected TUN
  interface, or it does not match the configured mihomo device.
- At runtime, the route changed away from the startup interface.

Checks:

- Run `route get <network.clash_verge.route_check_target>` and inspect the
  `interface:` line.
- Check for Wi-Fi changes, sleep/wake, VPN clients, or macOS network service
  priority changes.
- Use `route -n monitor` while reproducing the issue and record only the
  relevant event lines.

Expected behavior:

- Startup route failures happen before sandbox creation.
- Runtime route failures stop the main sandbox and remove the probe sandbox
  according to cleanup policy.

## Host egress mismatch

Typical output:

```text
host egress invalid: host-egress-mismatch: host egress observed IP <observed> does not match expected IP <expected>
```

Meaning:

- The host's public IP from `network.egress_ip.host_check_url` differs from
  `network.egress_ip.expected_ip`.

Checks:

- Confirm the configured expected IP is the policy-approved public IP for the
  current Clash node and network.
- Confirm the host check endpoint returns a plain IP address.
- Try a known-good endpoint if the current endpoint has TLS, DNS, captive
  portal, or response-format problems.

Expected behavior:

- `doctor` and launcher startup fail before sandbox creation.

## Sandbox egress mismatch

Typical output:

```text
sandbox probe invalid: sandbox-egress-mismatch: sandbox egress observed IP <observed> does not match expected IP <expected>
```

Meaning:

- During startup preflight, Docker Sandbox egress does not match the configured
  expected IP even though the host may have passed its own egress check.
- Docker Sandbox normally uses an internal proxy at
  `gateway.docker.internal:3128`; this is allowed when the observed public IP
  still matches policy.
- Runtime route and Clash app-home events no longer run sandbox egress curl as
  the watchdog gate. Runtime IP drift is reported through host egress failures.

Checks:

- Run `safe-claude-sbx doctor --config config.yaml`.
- Confirm `network.egress_ip.sandbox_check_url` returns a plain IP from inside
  the sandbox.
- Check whether a Clash node switch, VPN, Wi-Fi switch, or Docker Sandbox
  backend state changed startup sandbox egress.

Expected behavior:

- Startup mismatch removes the probe sandbox and does not start the main
  sandbox.
- At runtime, the watchdog does not report `sandbox-egress-mismatch`; it stops
  the main sandbox when a route or Clash app-home metadata event leads to a
  route/interface failure or a `host-egress-mismatch`.

## Runtime host egress drift

Typical output:

```text
watchdog stopped sandbox: route-monitor runtime check failed: host egress drift: host egress observed IP <observed> does not match expected IP <expected>
watchdog stopped sandbox: clash-app-home runtime check failed: host egress drift: host egress observed IP <observed> does not match expected IP <expected>
```

Meaning:

- A route monitor event or Clash Verge app-home metadata event triggered a
  runtime check.
- The startup TUN route and interface were still safe enough to reach the host
  egress check, but `network.egress_ip.host_check_url` no longer returned
  `network.egress_ip.expected_ip`.
- The watchdog does not continuously poll the host egress endpoint; it checks
  only when a supported event source fires.

Checks:

- Confirm the configured expected IP is still the approved IP for the active
  Clash node.
- Run the host check endpoint locally and confirm it returns a plain IP.
- If the event source was `clash-app-home`, inspect only whether relevant files
  changed; do not paste Clash configs, subscriptions, nodes, secrets, or logs
  into issues.

Expected behavior:

- Host egress mismatch fails closed and stops the main sandbox.
- Endpoint errors are reported as `host egress check failed` rather than as
  sandbox egress failures. Runtime endpoint failures are retried up to five
  attempts with short backoff before the watchdog fails closed.
- Host egress mismatch, TUN route changes, and missing TUN interfaces are not
  retried.
- The cleanup path stops the main sandbox according to cleanup policy; if that
  cleanup hangs or fails, diagnose Docker Sandbox control-plane health.

## Runtime Docker Sandbox control-plane stall

Typical output:

```text
watchdog stopped sandbox: ... cleanup failed: sbx control-plane failure: stop main sandbox "<main-name>" failed: ...
sbx exec <main-name> curl -fsS <network.egress_ip.sandbox_check_url>   # manual diagnostic hangs or times out
```

Meaning:

- `sbx exec` or `sbx stop` is slow, hung, unauthenticated, or unable to reach
  the Docker Sandbox control plane.
- This should not be interpreted as a runtime sandbox egress mismatch. Runtime
  watchdog policy no longer depends on repeated `sbx exec` egress probes.
- Manual `sbx exec <main-name> curl ...` remains useful as a backend diagnostic
  after the fact, but it is not the runtime safety gate.

Checks:

- Run `sbx version` and `sbx ls`.
- If `sbx ls` hangs, treat it as Docker Sandbox control-plane unavailability.
- Run `safe-claude-sbx doctor --config config.yaml` after recovering the Docker
  Sandbox control plane to revalidate startup sandbox egress.

Expected behavior:

- Host route or host egress failures are reported separately from cleanup
  failures.
- A cleanup control-plane failure may leave the main sandbox in Docker Sandbox
  until local recovery commands succeed.

## `sbx` is unavailable

Typical output:

```text
sandbox backend invalid: sbx binary missing: sbx not found
sandbox backend invalid: version-incompatible: sbx version failed: ...
sandbox backend invalid: sbx control-plane unavailable: sbx ls failed: command was canceled; run `sbx diagnose` and `sbx ls`, then restart the sbx daemon or Docker Desktop
sandbox backend invalid: unavailable: sbx list failed: ERROR: Not authenticated to Docker
watchdog stopped sandbox: ... cleanup failed: sbx control-plane failure: stop main sandbox "claude-sbx" failed: ...
```

Meaning:

- Startup backend readiness distinguishes a missing `sbx` binary, incompatible
  `sbx version`, Docker Sandbox control-plane/listing unavailability, and other
  `sbx ls` backend errors such as authentication failures.
- If cleanup reports `sbx control-plane failure`, the launcher already decided
  to fail closed, but Docker Sandbox's local control plane did not complete the
  stop or remove request. This is distinct from a TUN, route, or egress policy
  failure.
- If startup reports `sbx control-plane unavailable`, treat it as local Docker
  Sandbox control-plane health, not as evidence that the network safety policy
  passed or failed.

Checks:

- Run `command -v sbx`.
- Run `sbx version`.
- Run `sbx ls`. If it hangs, stop waiting and use the recovery checks below.
- If unauthenticated, complete Docker Sandbox login in your local environment.
- If policy is not initialized, follow Docker Sandbox's own `sbx policy init`
  instructions for the host policy you intend to use.

Expected behavior:

- Availability failure happens before probe creation and before main sandbox
  startup.
- Cleanup uses bounded, independent `sbx` operations. A sandbox-local Herdr
  stop timeout should not prevent the main sandbox stop attempt.

Recovery checks when `sbx ls` or `sbx stop <main-name>` hangs:

```bash
pgrep -af 'safe-claude-sbx|safe-herdr|sbx stop|sbx daemon|sandboxd|containerd-shim'
sbx version
sbx ls
```

If `sbx ls` is still stuck after terminating stale `safe-claude-sbx`,
`safe-herdr`, or manual `sbx stop` processes, restart the Docker Sandbox daemon
from a local terminal:

```bash
pkill -f 'sbx daemon start'
sbx daemon start
```

Keep `sbx daemon start` running in that terminal, then in a second terminal run:

```bash
sbx ls
sbx stop <main-name>
safe-claude-sbx doctor --config config.yaml
```

If `sbx daemon start` cannot recover `sbx ls`, restart Docker Desktop and rerun
`safe-claude-sbx doctor --config config.yaml` before launching another sandbox.

## Docker Sandbox already running

Typical output:

```text
sandbox started: <main-name>
sandbox start invalid: start main sandbox: ...
```

Meaning:

- A sandbox with `sandbox.main_name` already exists. `sbx run` may attach,
  restart, or reject depending on the current Docker Sandbox state and version.

Checks:

- Run `sbx ls` and inspect the state of `<main-name>`.
- If the existing sandbox is stale, stop it with `sbx stop <main-name>` and
  remove it only when you intentionally want to discard its state.
- Re-run `safe-claude-sbx doctor --config config.yaml` before starting again.

Expected behavior:

- In `sandbox-local-herdr` mode, a stopped sandbox with the configured
  `sandbox.main_name` is treated as stale startup state. The launcher stops it
  idempotently, removes it, and creates a fresh `claude` template sandbox after
  the usual preflight and probe inspection pass.
- If the existing main sandbox is running or has an unrecognized status, startup
  fails closed and reports the sandbox name and status. It does not stop or
  remove that existing sandbox.
- On successful attach/start, launcher cleanup stops the main sandbox when
  configured.
- The launcher does not remove the main sandbox unless
  `cleanup.remove_main_sandbox` is `true`.

## Forbidden mount path

Typical output:

```text
configuration invalid: workspace.mount: path is forbidden by workspace policy
sandbox probe invalid: sandbox inspection invalid: workspace.inspection.mounts: forbidden host path visible
sandbox probe invalid: sandbox inspection invalid: workspace.inspection.visibility.sibling: non-workspace path readable: /Users/alice/work/other-project/config.yaml
```

Meaning:

- The configured workspace mount resolves to a sensitive path, or sandbox mount
  inspection exposed a forbidden host path.
- Forbidden paths include Home, SSH, Claude config, Clash config, Keychain, and
  any extra paths configured under `workspace.forbidden_paths`.
- The sandbox can read a sibling project file outside the configured workspace.
  Herdr TUI and `cc` share that same sandbox filesystem view.
- A parent `CLAUDE.md` may be visible through Docker Sandbox/Claude template
  workspace guidance handling. That is expected workspace metadata, not a policy
  failure by itself.

Checks:

- Set `workspace.mount` to the project directory only.
- Check symlinks and relative paths; the policy resolves them before backend
  commands run.
- Do not mount home directories or credential/config directories.
- The current default is `workspace.use_clone_mode: false`. New main sandbox
  startup checks sibling visibility before attaching Claude, Herdr, or `cc`; if
  visibility inspection reports a sibling file, startup fails closed without
  modifying the reported path. Set `workspace.use_clone_mode: true` only when
  you explicitly want Docker Sandbox's private clone behavior.
- Do not paste the readable file contents into issues or logs; the diagnostic
  path is enough to debug the mount boundary.

Expected behavior:

- Configuration failures happen before backend commands.
- Inspection failures clean up the probe and prevent main sandbox startup.

## Proxy environment rejected

Typical output:

```text
sandbox probe invalid: sandbox inspection invalid: environment.inspection.env.HTTP_PROXY: proxy target is not Docker-managed
sandbox probe invalid: sandbox inspection invalid: environment.inspection.env.HTTPS_PROXY: proxy target is not Docker-managed
sandbox probe invalid: sandbox inspection invalid: environment.inspection.env.NO_PROXY: unknown proxy bypass policy
```

Meaning:

- Docker Sandbox may inject proxy variables pointing to
  `gateway.docker.internal:3128`; those Docker-managed values are allowed.
- Host Clash proxy values such as `127.0.0.1:7897`, `localhost`, or unknown
  proxy targets are rejected.

Checks:

- Verify the launcher is not adding host proxy variables to `sbx`.
- Check shell startup files if host proxy variables are being inherited
  unexpectedly.
- Share only the variable names and target class in issues, not full environment
  dumps.

Expected behavior:

- Probe cleanup runs.
- Main sandbox startup is blocked.

## Forbidden environment detected

Typical output:

```text
sandbox probe invalid: sandbox inspection invalid: environment.inspection.env.OPENAI_API_KEY: raw credential value visible
sandbox probe invalid: sandbox inspection invalid: environment.inspection.env.SSH_AUTH_SOCK: ssh agent forwarding is not allowed
sandbox probe invalid: sandbox inspection invalid: environment.inspection.env.SSH_AUTH_SOCK_GATEWAY: ssh agent forwarding is not allowed
```

Meaning:

- The sandbox environment contains a host-sensitive variable such as an OpenAI
  API key, token, password, credential, Claude config path, Clash config path,
  or Keychain-related variable.
- Docker-managed credential placeholders such as `proxy-managed` are allowed;
  raw credential values are rejected.
- Docker Sandbox may forward the host SSH agent as SSH forwarding environment
  such as `SSH_AUTH_SOCK` and `SSH_AUTH_SOCK_GATEWAY`. This is rejected unless
  `environment.allow_ssh_agent_forwarding` is explicitly `true`. When enabled,
  sandbox processes can request SSH signatures from the host agent.
- The launcher output intentionally names only the variable, not its value.
- The launcher also starts `sbx` subprocesses with a small host environment
  allowlist so `OPENAI_API_KEY`, `SSH_AUTH_SOCK`, `SSH_AUTH_SOCK_GATEWAY`, and
  host proxy variables are not passed to the main sandbox command.

Checks:

- Run `safe-claude-sbx doctor --config config.yaml`.
- Remove the variable from shell startup files, terminal profiles, or wrapper
  scripts used to start the launcher.
- If Docker Sandbox exposes API credential names as `proxy-managed`, no action is
  required.
- If `SSH_AUTH_SOCK` or `SSH_AUTH_SOCK_GATEWAY` appears and this workflow needs
  Git-over-SSH, set `environment.allow_ssh_agent_forwarding: true`; otherwise
  leave it disabled.
- Do not paste the variable value into issues.

Expected behavior:

- Probe cleanup runs.
- Main sandbox startup is blocked.

## `route -n monitor` exits or misses an event

Typical output:

```text
watchdog stopped sandbox: watchdog event source failed: route monitor exited
watchdog stopped sandbox: watchdog event source failed: start route monitor: ...
```

Meaning:

- `route -n monitor` is one runtime event source; Clash app-home metadata is
  the other current runtime event source.
- If it exits, the launcher treats the event source as failed and stops the
  sandbox.
- Real macOS environments may not emit identical route events for every Clash
  node switch, Wi-Fi switch, sleep/wake, or VPN transition.

Checks:

- Reproduce with a separate `route -n monitor` terminal and record the event
  lines around the transition.
- After any transition that did not stop the sandbox, run
  `safe-claude-sbx doctor --config config.yaml` to validate the current state.
- Record macOS version, Clash Verge version, Docker Sandbox version, and the
  transition type in the issue.

Expected behavior:

- Event source failure stops the main sandbox and removes the probe sandbox
  according to cleanup policy.
- Missing route events are a known MVP limitation; the launcher does not claim
  polling coverage without an event.

## Clash app-home event source exits or misses an event

Typical output:

```text
watchdog stopped sandbox: watchdog event source failed: clash app-home event source unavailable: network.clash_verge.app_home does not exist or is not accessible
watchdog stopped sandbox: clash-app-home runtime check failed: host egress drift: ...
```

Meaning:

- The runtime watchdog also watches Clash Verge app-home metadata for configured
  policy files and directories.
- Metadata changes trigger the same lightweight runtime check as route events:
  startup TUN route, startup TUN interface existence, and host egress.
- The watcher observes file metadata only. It does not read or print Clash file
  contents.

Checks:

- Confirm `network.clash_verge.app_home` points to the active Clash Verge Rev
  app home, especially for portable mode.
- Check that the app-home directory exists and is readable by the launcher.
- Reproduce the node or profile switch while recording only changed path names,
  not file contents.

Expected behavior:

- If app-home metadata changes and host egress drifts, the main sandbox stops.
- If the app-home event source cannot start, the watchdog treats that event
  source as failed and stops the main sandbox.
- If a Clash node switch emits no route event and changes no watched app-home
  metadata, immediate detection is not guaranteed.
