# Configuration

The CLI reads a structured YAML file with separate objects for network policy,
sandbox behavior, workspace mount policy, runtime environment, watchdog logging,
and cleanup behavior. See `config.example.yaml` for a complete example.

Run the doctor before starting a sandbox. It validates the configuration and
checks the configured main sandbox directly after host policy checks pass:

```bash
safe-claude-sbx doctor --config config.yaml
```

For the sandbox-local Herdr daily flow, keep the default team egress IP
`34.68.40.236` unless your approved route differs, build the Docker Sandbox
template, then start the TUI through the guarded entrypoint:

```bash
docker build -t safe-claude-sbx-herdr:latest sandbox/claude-herdr-template
docker image save safe-claude-sbx-herdr:latest -o safe-claude-sbx-herdr.tar
sbx template load safe-claude-sbx-herdr.tar
```

```bash
safe-herdr --config config.yaml
```

`safe-herdr` creates the configured main sandbox from `sandbox.template` when it
is missing, verifies `herdr`, `herdr --version`,
`herdr integration install claude`, `cc`, and `cc --version` inside the sandbox,
then attaches the Herdr TUI. It does not download Herdr during startup or
rewrite `/usr/local/bin/cc`, and it does not create a temporary probe sandbox on
the hot path.

Inside the Herdr TUI, run `cc` to start Claude. The `cc` command is baked into
the Docker Sandbox template at `/usr/local/bin/cc`.

## Egress Modes

If `network.egress.mode` is omitted, the launcher uses `host-inherited`. This
keeps the existing Clash Verge TUN, host public IP, doctor, launch, and watchdog
behavior unchanged. The explicit form is:

```yaml
network:
  egress:
    mode: "host-inherited"
  clash_verge:
    route_check_target: "1.1.1.1"
    tun_interface_prefix: "utun"
  egress_ip:
    expected_ip: "34.68.40.236"
    host_check_url: "https://ipv4.icanhazip.com"
    sandbox_check_url: "https://ipv4.icanhazip.com"
    timeout_seconds: 60
```

`dedicated-gateway` is a capability-gated research mode. Mihomo and MetaCubeXD
remain operator-managed external processes; the launcher does not install,
start, stop, or update them. The configuration is accepted so operators can
record the intended gateway contract, but startup proceeds only when the Docker
Sandbox backend has an explicitly validated protocol-complete upstream for
managed HTTP(S), generic TCP, and DNS:

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

Set the referenced controller secret in the doctor process environment. The
secret itself does not belong in YAML. Both URLs must be credential-free
loopback HTTP endpoints with explicit ports.

`sbx v0.34.0` and `sbx v0.35.0` are not protocol-complete. The former provides
an HTTP-only `DOCKER_SANDBOXES_PROXY` contract. The latter adds the documented
SOCKS5/SOCKS5h transport, but disposable validation still found generic TCP and
Docker-internal DNS available after gateway loss. Neither release can preserve
managed HTTP(S) while making those other protocols fail closed. Dedicated
doctor therefore fails with `dedicated protocol isolation unsupported` after
reading only `sbx version` and before controller access, daemon stop/start,
sandbox creation, or main attach. Unknown Docker Sandbox versions also fail
closed until their protocol contract is explicitly validated and added to the
Adapter.

The existing controller, exclusive lease, main preflight, and ordered cleanup
implementation remains behind this capability gate for a future supported
backend. It never sets generic `HTTP_PROXY`, `HTTPS_PROXY`, or `ALL_PROXY` on
sandboxd. Doctor, direct `safe-claude-sbx --config`, and `safe-herdr` all enter
the same capability gate; `sbx v0.34.0`, `sbx v0.35.0`, other unknown
versions, and capability inspection failures are rejected before controller,
daemon, main, or attach operations.
See `docs/dedicated-gateway-operations.md` for the ownership boundary, startup
evidence, failure handling, host-inherited recovery, and disposable acceptance
procedure.

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
  template: "safe-claude-sbx-herdr:latest"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: false
      socket_path: "/home/agent/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"
```

This declares sandbox-local Herdr startup inputs only. It does not expose host
Herdr state, host Herdr sockets, or host `HERDR_*` values to Docker Sandbox.

## Object Model

- `network.egress`
  - `mode`: `host-inherited` or `dedicated-gateway`. If omitted, defaults to
    `host-inherited`.
  - `dedicated_gateway`: Required only for `dedicated-gateway`.
    - `upstream_url`: Credential-free loopback HTTP upstream URL with an
      explicit port.
    - `controller_url`: Credential-free loopback HTTP Mihomo controller base
      URL with an explicit port.
    - `controller_secret_env`: Name of the host environment variable containing
      the controller secret. The value is never passed to sandboxd or main.
- `network.clash_verge`
  - `app_home`: Optional Clash Verge Rev app-home override. Empty string uses the normal macOS app-home path.
  - `route_check_target`: IP used by `route get` to inspect the outbound route.
  - `tun_interface_prefix`: Expected macOS TUN interface prefix, normally `utun`.
- `network.egress_ip`
  - `expected_ip`: Public IP the sandbox must observe. In `host-inherited`, the
    host must also observe it. The example default is the team egress IP
    `34.68.40.236`; change it only when the approved route uses a different
    public IP.
  - `host_check_url`: Endpoint the host uses to read its public IP. Required
    only for `host-inherited`.
  - `sandbox_check_url`: Endpoint the sandbox uses to read its public IP.
  - `timeout_seconds`: Timeout for backend commands, configured main sandbox
    inspection commands, sandbox-local Herdr checks, cleanup attempts, and
    egress IP checks. Use at least `30` for real Docker Sandbox startup because
    first-run image and daemon startup can be slow.
- `sandbox`
  - `backend`: Runtime backend. MVP value: `docker-sandbox`.
  - `main_name`: Main Docker Sandbox name.
  - `probe_name`: Legacy compatibility name for temporary probe diagnostics.
    It no longer affects the `doctor` or `safe-herdr` hot path, which inspects
    the configured main sandbox directly.
  - `agent`: Agent command to run, normally `claude`.
  - `template`: Optional Docker Sandbox template image for direct Claude mode.
    Required for `sandbox-local-herdr`; the launcher passes it as
    `sbx create --template <template>` when creating the main sandbox.
  - `supervision.mode`: Agent startup supervision mode. Supported values are
    `direct-claude` and `sandbox-local-herdr`. If omitted, the launcher uses
    `direct-claude`, creating the main sandbox first, validating workspace
    visibility, and only then attaching Claude Code.
  - `supervision.herdr`: Required only when `supervision.mode` is
    `sandbox-local-herdr`. This object declares sandbox-local Herdr startup
    inputs, not host Herdr state.
    - `install_if_missing`: Must be `false`. Runtime Herdr installation is
      disabled; build/load/use a Docker Sandbox template that already contains
      `herdr` and `/usr/local/bin/cc`.
    - `socket_path`: Sandbox-local Herdr socket path. It must point inside the
      sandbox user's home, such as `/home/agent/.config/herdr/herdr.sock`.
    - `pane_id`: Non-empty sandbox-local pane identity used by the Herdr
      integration.
- `workspace`
  - `mount`: Host project directory mounted into the sandbox.
  - `use_clone_mode`: Optional. Defaults to `false` in the example config. When
    `true`, the launcher adds `--clone` to Docker Sandbox create commands.
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
  - `stop_main_sandbox`: Stop the main sandbox on normal shutdown or watchdog
    failure. Dedicated teardown always leaves main stopped even when this value
    is `false`, because a main cannot outlive its validated egress lease.
  - `remove_probe_sandbox`: Legacy compatibility cleanup flag for temporary
    probe diagnostics. It no longer affects the `doctor` or `safe-herdr` hot
    path.
  - `remove_main_sandbox`: Remove a launcher-created main sandbox on exit. The
    default is `false`; dedicated teardown never removes a preexisting main.

## Validation

`doctor --config` fails closed when required objects or fields are missing, when
`workspace.mount` resolves to a forbidden mount, or when
`network.egress_ip.expected_ip` is not an IP address. `host-inherited` also
requires Clash Verge policy and a host check URL, and fails when the host egress
check cannot prove the expected IP. Dedicated mode rejects non-loopback,
credentialed, path-bearing, or non-HTTP endpoints and invalid controller secret
references before backend mutation. Error messages include object paths such as
`network.clash_verge`, `network.egress.dedicated_gateway.upstream_url`,
`workspace.mount`, or `network.egress_ip.expected_ip`.

Supervision config also fails closed. `sandbox.supervision.mode` must be either
`direct-claude` or `sandbox-local-herdr`. Herdr mode requires
`sandbox.template`, the nested `sandbox.supervision.herdr` object,
`install_if_missing: false`, a sandbox-home socket path, and a non-empty pane
id. Host-looking Herdr socket paths and top-level `HERDR_*` config are rejected
without printing raw socket or pane values.

`doctor` validates the configured main sandbox before printing
`sandbox inspection ok`; it does not create a temporary probe sandbox. Timezone
and locale validation use sandbox-internal `TZ`, `date`, and `locale`
observations; host or daemon log timestamps are not accepted as proof of sandbox
runtime settings. The same inspection rejects visible sensitive mounts, raw
token or credential-like env values such as `OPENAI_API_KEY`, unexpected SSH
agent forwarding env such as `SSH_AUTH_SOCK` and `SSH_AUTH_SOCK_GATEWAY`, host
proxy values such as `127.0.0.1:7897`, and unknown proxy targets.
Docker-managed credential placeholders are allowed. These errors name the
policy object and env variable but do not print captured secret values.

The configured main sandbox checks workspace visibility. The configured
workspace is the only project tree expected to be readable by Herdr, Claude
Code, and the `cc` process started inside Herdr because they share the same
Docker Sandbox filesystem view. Current Docker Sandbox behavior can expose the
configured workspace and its parent path as host-style paths; a parent
`CLAUDE.md` guidance file is therefore treated as Docker Sandbox/Claude template
workspace metadata, not as a policy failure. If the main sandbox can read a file
under a sibling project directory, `doctor` or launcher startup fails closed
with `workspace.inspection.visibility.sibling`. The diagnostic names the
readable non-workspace path but does not print file contents. The current
default uses normal Docker Sandbox create mode, not `--clone`; set
`workspace.use_clone_mode: true` only when you explicitly want Docker Sandbox's
private clone behavior. During `safe-herdr`, a missing or stopped main sandbox
is created from `sandbox.template`; an existing running main sandbox is
inspected and must already contain valid template-provided Herdr and `cc`.

Docker Sandbox may still report the configured workspace path in `pwd`, `sbx`
status output, or source-mount metadata. Treat those path strings as expected
backend metadata exposure. The enforced boundary is that unrelated sensitive
paths and sibling project files are not readable by Claude, Herdr, or `cc`. Do
not depend on Herdr or `cc` to provide another filesystem isolation layer inside
the same sandbox.

Legacy flat fields such as `expected_egress_ip`, `sandbox_name`,
`workspace_mount`, `timezone`, and `cleanup.stop_on_exit` are not accepted by the
MVP CLI. The doctor reports a migration message with the new object path, for
example `expected_egress_ip -> network.egress_ip.expected_ip`.

## Runtime Watchdog

Startup remains the deep validation point. In `host-inherited`, before the main
sandbox is attached, the launcher validates Clash Verge TUN declarations, the
live macOS route and startup TUN interface, host egress, Docker Sandbox
availability, sandbox egress, workspace visibility, and sandbox environment
policy. Dedicated doctor is capability-gated and rejects the current
`sbx v0.34.0` and `sbx v0.35.0` backends before mutation. The future-supported
doctor, direct Claude, and Herdr paths share main preflight, controller
isolation, the dedicated watchdog, and `Fence`/`Recover` teardown, but no
released backend is enabled in the production support matrix.

Runtime supervision is intentionally lighter. The watchdog merges macOS route
monitor events and Clash Verge app-home file metadata events, debounces the
burst, and then checks host-observable policy facts:

- `route get <network.clash_verge.route_check_target>` still resolves to the
  startup TUN interface.
- `ifconfig <startup-utun>` still proves that interface exists.
- `network.egress_ip.host_check_url` still returns
  `network.egress_ip.expected_ip`.

The runtime watchdog does not continuously poll the public IP endpoint. It also
does not use `sbx exec <main-name> curl ...` as the synchronous route-event
gate. A Docker Sandbox control-plane stall should therefore be diagnosed as
backend health or cleanup trouble, not as runtime sandbox egress drift.

The Clash Verge app-home event source observes metadata only for stable paths
such as `verge.yaml`, `config.yaml`, `clash-verge.yaml`, `profiles.yaml`,
`profiles`, `rules`, and `providers`. It does not read, print, or copy Clash
configuration contents, subscriptions, node definitions, controller secrets, or
Claude credentials.
