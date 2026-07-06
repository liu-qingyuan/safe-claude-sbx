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
watchdog stopped sandbox: route-monitor runtime policy failed: sandbox egress invalid: sandbox-egress-mismatch: ...
```

Meaning:

- Docker Sandbox egress does not match the configured expected IP even though
  the host may have passed its own egress check.
- Docker Sandbox normally uses an internal proxy at
  `gateway.docker.internal:3128`; this is allowed when the observed public IP
  still matches policy.

Checks:

- Run `safe-claude-sbx doctor --config config.yaml`.
- Confirm `network.egress_ip.sandbox_check_url` returns a plain IP from inside
  the sandbox.
- Check whether a Clash node switch, VPN, Wi-Fi switch, or Docker Sandbox
  backend state changed host or sandbox egress.

Expected behavior:

- Startup mismatch removes the probe sandbox and does not start the main
  sandbox.
- Runtime mismatch stops the main sandbox and removes the probe sandbox
  according to cleanup policy after a route event triggers validation.

## Runtime sandbox egress indeterminate

Typical output:

```text
watchdog stopped sandbox: route-monitor runtime check failed: indeterminate runtime egress check failed after 10 attempt(s): runtime egress indeterminate retry 10/10: runtime sandbox egress command failed against configured network.egress_ip.sandbox_check_url ...
```

Meaning:

- The watchdog could not prove the main sandbox egress IP from
  `network.egress_ip.sandbox_check_url`, but it also did not observe a different
  public IP.
- Common causes include a curl timeout, Docker Sandbox gateway proxy failure,
  Docker registry auth/token timeout in older probe-based paths, DNS/TLS
  endpoint failure, or transient network loss.
- This is distinct from `sandbox-egress-mismatch`, where the sandbox returned a
  concrete observed IP that differs from the configured expected IP.

Checks:

- Run `sbx exec <main-name> curl -fsS <network.egress_ip.sandbox_check_url>`.
- If Google is the only failing destination, also run
  `sbx exec <main-name> curl -fsS https://www.google.com` and record it as
  Google connectivity failure, not as the policy check.
- Prefer a `sandbox_check_url` endpoint that returns only a plain IP and is
  reliable from both host and Docker Sandbox.

Expected behavior:

- Runtime checks retry indeterminate failures up to 10 attempts with capped
  exponential backoff before failing closed.
- Before each retry, the watchdog revalidates the startup TUN route and fails
  closed immediately if the route or startup TUN interface is explicitly unsafe.
- If the configured sandbox check URL later returns the expected IP, the
  watchdog keeps the main sandbox running even when Google connectivity fails.
- Explicit TUN mismatch, TUN missing, or observed IP mismatch still fail closed
  immediately.

## `sbx` is unavailable

Typical output:

```text
sandbox backend invalid: unavailable: sbx not found
sandbox backend invalid: unavailable: sbx list failed: ERROR: Not authenticated to Docker
sandbox backend invalid: unavailable: sbx list failed: ...
watchdog stopped sandbox: ... cleanup failed: sbx control-plane failure: stop main sandbox "claude-sbx" failed: ...
```

Meaning:

- The `sbx` CLI is missing, incompatible, unauthenticated, or unable to reach
  its backend.
- If cleanup reports `sbx control-plane failure`, the launcher already decided
  to fail closed, but Docker Sandbox's local control plane did not complete the
  stop or remove request. This is distinct from a TUN, route, or egress policy
  failure.
- If `sbx ls` hangs, treat it as Docker Sandbox control plane unavailability,
  not as evidence that the network safety policy passed or failed.

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
sandbox probe invalid: sandbox inspection invalid: workspace.inspection.visibility.parent_guidance: non-workspace path readable: /Users/alice/work/CLAUDE.md
sandbox probe invalid: sandbox inspection invalid: workspace.inspection.visibility.sibling: non-workspace path readable: /Users/alice/work/other-project/config.yaml
```

Meaning:

- The configured workspace mount resolves to a sensitive path, or sandbox mount
  inspection exposed a forbidden host path.
- Forbidden paths include Home, SSH, Claude config, Clash config, Keychain, and
  any extra paths configured under `workspace.forbidden_paths`.
- The sandbox can read outside the configured workspace, such as a parent
  `CLAUDE.md` guidance file or a sibling project file. Herdr TUI and `cc` share
  that same sandbox filesystem view.

Checks:

- Set `workspace.mount` to the project directory only.
- Check symlinks and relative paths; the policy resolves them before backend
  commands run.
- Do not mount home directories or credential/config directories.
- Try `workspace.use_clone_mode: true` if the Docker Sandbox backend supports it
  for the workflow, or copy only the required project files to a disposable
  temporary workspace and mount that path.
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

- `route -n monitor` is the MVP's primary runtime event source.
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
