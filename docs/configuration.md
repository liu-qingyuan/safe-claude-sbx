# Configuration

The CLI reads a structured YAML file with separate objects for network policy,
sandbox behavior, workspace mount policy, runtime environment, watchdog logging,
and cleanup behavior. See `config.example.yaml` for a complete example.

Run the doctor before starting a sandbox. It validates the configuration and
checks that the host public egress IP matches `network.egress_ip.expected_ip`:

```bash
safe-claude-sbx doctor --config config.yaml
```

For the sandbox-local Herdr daily flow, update
`network.egress_ip.expected_ip`, then start the TUI through the guarded
entrypoint:

```bash
safe-herdr --config config.yaml
```

`safe-herdr` is an attach-only daily entrypoint. It requires the configured main
sandbox to already exist for the configured workspace and to have Herdr available
inside the sandbox; it does not install Herdr, install the Claude integration, or
rewrite `/usr/local/bin/cc`.

Inside the Herdr TUI, run `cc` to start Claude. The `cc` command is created only
inside the Docker Sandbox when needed.

## Supervision Examples

The default supervision mode is direct Claude startup. A minimal explicit
configuration is:

```yaml
sandbox:
  backend: "docker-sandbox"
  main_name: "claude-sbx"
  probe_name: "claude-sbx-probe"
  agent: "claude"
  supervision:
    mode: "direct-claude"
```

To opt in to sandbox-local Herdr mode, keep the same network, workspace,
environment, watchdog, and cleanup policy, and change only the sandbox
supervision block:

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

This declares sandbox-local Herdr startup inputs only. It does not expose host
Herdr state, host Herdr sockets, or host `HERDR_*` values to Docker Sandbox.

## Object Model

- `network.clash_verge`
  - `app_home`: Optional Clash Verge Rev app-home override. Empty string uses the normal macOS app-home path.
  - `route_check_target`: IP used by `route get` to inspect the outbound route.
  - `tun_interface_prefix`: Expected macOS TUN interface prefix, normally `utun`.
- `network.egress_ip`
  - `expected_ip`: Public IP that both host and sandbox must observe.
  - `host_check_url`: Endpoint the host uses to read its public IP.
  - `sandbox_check_url`: Endpoint the sandbox uses to read its public IP.
  - `timeout_seconds`: Timeout for backend commands, sandbox probe commands,
    sandbox-local Herdr install attempts, cleanup attempts, and egress IP
    checks. Use at least `30` for real Docker Sandbox probes because first-run
    image and daemon startup can be slow.
- `sandbox`
  - `backend`: Runtime backend. MVP value: `docker-sandbox`.
  - `main_name`: Main Docker Sandbox name.
  - `probe_name`: Temporary probe sandbox name.
  - `agent`: Agent command to run, normally `claude`.
  - `supervision.mode`: Agent startup supervision mode. Supported values are
    `direct-claude` and `sandbox-local-herdr`. If omitted, the launcher uses
    `direct-claude`, preserving the current `sbx run claude` startup path.
  - `supervision.herdr`: Required only when `supervision.mode` is
    `sandbox-local-herdr`. This object declares sandbox-local Herdr startup
    inputs, not host Herdr state.
    - `install_if_missing`: Whether the `safe-claude-sbx` initialization/start
      path may install Herdr inside the sandbox if the sandbox-local binary is
      absent. When installation is enabled, the launcher first checks for
      `/home/agent/.local/bin/herdr` with `command -v herdr`; an already
      installed binary is reused without downloading. Missing Herdr is installed
      inside the sandbox with two attempts, each bounded by
      `network.egress_ip.timeout_seconds`. The `safe-herdr` daily entrypoint
      does not use this install path.
    - `socket_path`: Sandbox-local Herdr socket path. It must point inside the
      sandbox user's home, such as `/home/agent/.config/herdr/herdr.sock`.
    - `pane_id`: Non-empty sandbox-local pane identity used by the Herdr
      integration.
- `workspace`
  - `mount`: Host project directory mounted into the sandbox.
  - `use_clone_mode`: Whether a copied workspace mode is requested.
  - `forbidden_paths`: Host paths that must never be used as workspace mounts.
    The policy expands `~`, rejects sensitive paths such as SSH, Claude config,
    Clash config, and Keychain paths recursively, and fails before backend
    commands run.
- `environment`
  - `timezone`: Sandbox timezone as an IANA timezone name. The example uses
    `America/Chicago`; do not configure a fixed offset such as `UTC-5`,
    because Chicago observes daylight saving time and shifts to `UTC-6` in
    winter.
  - `locale`: Sandbox `LANG` and `LC_ALL`.
  - `allow_ssh_agent_forwarding`: Whether Docker Sandbox's forwarded SSH agent
    environment family, including `SSH_AUTH_SOCK` and
    `SSH_AUTH_SOCK_GATEWAY`, is allowed. The default is `false`; enabling it
    means sandbox processes can ask the host SSH agent to sign operations,
    while private keys remain on the host.
  - `forbidden_env_vars`: Host environment variables that must not appear
    inside the sandbox as raw values, such as `OPENAI_API_KEY` and Claude API
    keys. Docker-managed credential placeholders such as `proxy-managed` are
    allowed. Docker-managed proxy values on `gateway.docker.internal:3128` are
    allowed; host or unknown proxy targets fail closed.
- `watchdog`
  - `enabled`: Whether runtime supervision is enabled.
  - `log_level`: Launcher log level.
  - `log_file`: Optional log file path. Empty string means stderr/stdout only.
- `cleanup`
  - `stop_main_sandbox`: Stop the main sandbox on normal shutdown or watchdog failure.
  - `remove_probe_sandbox`: Remove temporary probe sandboxes after preflight.
  - `remove_main_sandbox`: Remove the main sandbox on exit. The default is `false`.

## Validation

`doctor --config` fails closed when required objects or fields are missing, when
`workspace.mount` resolves to a forbidden mount, when
`network.egress_ip.expected_ip` is not an IP address, or when the host egress
check cannot prove that the observed public IP matches the configured expected
IP. Error messages include object paths such as `network.clash_verge`,
`workspace.mount`, or `network.egress_ip.expected_ip`, and host egress failures
distinguish `host-egress-mismatch`, `endpoint-failure`, and
`response-parse-failure`.

Supervision config also fails closed. `sandbox.supervision.mode` must be either
`direct-claude` or `sandbox-local-herdr`. Herdr mode requires the nested
`sandbox.supervision.herdr` object, declared install behavior, a sandbox-home
socket path, and a non-empty pane id. Host-looking Herdr socket paths and
top-level `HERDR_*` config are rejected without printing raw socket or pane
values.

After the Docker Sandbox probe runs, `doctor` validates the sandbox observation
before printing `sandbox inspection ok`. Timezone and locale validation use
sandbox-internal `TZ`, `date`, and `locale` observations; host or daemon log
timestamps are not accepted as proof of sandbox runtime settings. The same
inspection rejects visible sensitive mounts, raw token or credential-like env
values such as `OPENAI_API_KEY`, unexpected SSH agent forwarding env such as
`SSH_AUTH_SOCK` and `SSH_AUTH_SOCK_GATEWAY`, host proxy values such as
`127.0.0.1:7897`, and unknown proxy targets. Docker-managed credential
placeholders are allowed. These errors name the policy object and env variable
but do not print captured secret values.

Legacy flat fields such as `expected_egress_ip`, `sandbox_name`,
`workspace_mount`, `timezone`, and `cleanup.stop_on_exit` are not accepted by the
MVP CLI. The doctor reports a migration message with the new object path, for
example `expected_egress_ip -> network.egress_ip.expected_ip`.
