# Docker Sandbox Backend

The first backend is Docker Sandbox / `sbx`.

This page records the command contract observed on the target macOS host for
`sbx v0.34.0` on 2026-07-05. Docker authentication and global network policy
setup have been validated on the target machine. Historical shell probe notes
remain below for the legacy direct-Claude diagnostic path; `doctor` and
`safe-herdr` now inspect the configured main sandbox directly.

## Availability

The launcher should treat `sbx` as available only when all required preflight
commands can run successfully.

Startup readiness reports distinct states for a missing `sbx` binary,
incompatible `sbx version` output, and Docker Sandbox control-plane/listing
unavailability from bounded `sbx ls`. A killed, canceled, timed-out, or local
socket-stalled `sbx ls` is reported as `sbx control-plane unavailable` with a
short recovery hint, not as Herdr, Clash/TUN, sandbox egress, or runtime
watchdog failure.

Observed installation flow:

```bash
brew trust docker/tap
brew install docker/tap/sbx
command -v sbx
sbx version
sbx diagnose
```

Observed result:

- `brew trust docker/tap` succeeded.
- `brew install docker/tap/sbx` installed the `sbx` cask successfully.
- `command -v sbx` returned `/opt/homebrew/bin/sbx`.
- `sbx version` returned `sbx version: v0.34.0 2eae0c4fc3894475da3318615f69783b0e7be747`.
- `sbx diagnose` found the CLI and storage directories, but reported the daemon
  as not reachable until `sbx daemon start` is running.
- `sbx ls` starts `sandboxd` when needed and returned exit code `0` after
  authentication, with `No sandboxes found` on an empty machine.

`sbx daemon start` runs as a foreground process and printed:

```text
Starting daemon at /Users/liuqingyuan/Library/Application Support/com.docker.sandboxes/sandboxes/sandboxd/sandboxd.sock (Ctrl+C to stop)...
```

The command did not exit within 45 seconds. A launcher should not assume daemon
startup is a short one-shot command unless a later manual validation proves a
background mode or service manager contract.

## Authentication And Policy

`sbx login` supports browser-based login and non-interactive username/password
input:

```bash
sbx login
sbx login --username <docker-user> --password-stdin
```

Observed `sbx login` behavior without credentials:

```text
Your one-time device confirmation code is: <code>
Open this URL to sign in: https://login.docker.com/activate?user_code=<code>
Waiting for authentication...
```

The command waits for browser/account authentication. After the user completed
the Docker device-code login, `sbx login` exited successfully:

```text
Signed in as ggboy1464.
```

After login, creating a sandbox required initializing the global network policy:

```text
ERROR: global network policy has not been initialized

Initialize it with:
  sbx policy init <allow-all|balanced|deny-all>
```

The target host was initialized with:

```bash
sbx policy init allow-all
```

Observed policy rules after initialization:

- `default-allow-all`: network allow `**`
- `default-fs-read-allow-all`: filesystem read allow `**`
- `default-fs-write-allow-all`: filesystem write allow `**`

Before authentication, these commands fail with exit code `1`:

```text
ERROR: Not authenticated to Docker

Sign in with: sbx login
```

Observed pre-auth commands with this behavior:

- `sbx ls`
- `sbx stop safe-claude-sbx-nonexistent`
- `sbx rm --force safe-claude-sbx-nonexistent`

After authentication, `sbx ls` succeeds with exit code `0` on an empty machine.

## Command Contract

### List Sandboxes

```bash
sbx ls
```

Use this as the authentication and backend reachability check. Before login it
returns exit code `1` with `Not authenticated to Docker`.

After login it returned:

```text
No sandboxes found.
Launch one: sbx run claude
```

with exit code `0`.

### Create Main Sandbox

Prefer `sbx create` when the launcher needs to create the sandbox before
attaching an agent:

```bash
sbx create --name <main-name> claude <workspace>
```

When `sandbox.template` is configured, the launcher creates the main sandbox
with:

```bash
sbx create --name <main-name> --template <template> claude <workspace>
```

Confirmed help contract:

- `sbx create [flags] AGENT PATH [PATH...]`
- `--name string` sets the sandbox name.
- The default name is `<agent>-<workdir>`.
- Names allow letters, numbers, hyphens, periods, plus signs, and minus signs.
- Additional workspace paths are accepted.
- Append `:ro` to additional workspace paths for read-only mounts.
- `--clone` requests a private in-container clone rather than the default
  workspace mode. The launcher adds this flag only when
  `workspace.use_clone_mode` is `true`.
- `--profile` assigns a governance profile.
- `--cpus`, `--memory`, `--template`, and `--kit` are available resource/image
  controls.
- In `sandbox-local-herdr` mode, `sandbox.template` is required. The template
  must provide `herdr`, `herdr --version`, `herdr integration install claude`,
  `/usr/local/bin/cc`, and `cc --version` without running the Herdr installer
  during launcher startup.

For sandbox-local Herdr mode, the adapter inspects `sbx ls` before creating the
named `claude` main sandbox. If the configured main sandbox name already exists
with status `stopped`, startup treats it as stale local state: it stops the name
idempotently, removes it with `sbx rm --force`, and then creates a fresh
`claude` template sandbox. This startup recovery is separate from exit cleanup;
normal cleanup still only removes the main sandbox when
`cleanup.remove_main_sandbox` is `true`.

If the configured main sandbox exists with any other status, the adapter fails
closed with the sandbox name and status. It does not stop or remove that existing
sandbox as part of startup failure cleanup.

After creating a fresh main sandbox, the adapter validates sibling project
visibility without modifying parent guidance paths. Docker Sandbox can expose
the configured workspace and parent guidance path as host-style metadata; that
alone is not a policy failure.

For the Herdr TUI entrypoint, `safe-herdr` creates the main sandbox from
`sandbox.template` when it is missing or stopped, verifies template-provided
Herdr and `cc`, validates main workspace visibility, and then attaches with
`sbx exec -it <main-name> herdr`. It does not run
`curl -fsSL https://herdr.dev/install.sh | sh` during startup.

### Run Or Attach Main Sandbox

Use `sbx run` to attach Claude Code to a created or existing sandbox:

```bash
sbx run --name <main-name>
```

Confirmed help contract:

- `sbx run [flags] [AGENT] [PATH...] [-- AGENT_ARGS...]`
- `--name string` names the sandbox or reattaches to an existing one.
- When reattaching by name, the agent positional argument is optional.
- Agent arguments are passed after `--`.
- Additional workspace paths are accepted, with `:ro` for read-only mounts.
- `--clone`, `--profile`, `--cpus`, `--memory`, `--template`, and `--kit` are
  supported for new sandbox creation. The launcher creates new main sandboxes
  with `sbx create`, validates sibling visibility without modifying parent
  guidance paths, and then attaches with `sbx run --name`. It adds `--clone`
  only when `workspace.use_clone_mode` is `true`.
- Help text mentions `--detached (-d)`, but the observed flag list did not show
  it. Do not depend on detached `run` until verified after login.

### Legacy Probe Sandbox

Early MVP notes used a separate probe sandbox name derived from configuration,
for example:

```bash
sbx create --name <probe-name> shell <workspace>
sbx exec <probe-name> env
sbx exec <probe-name> curl -fsS <sandbox-check-url>
sbx rm --force <probe-name>
```

The `shell` agent is listed as an available agent for `create` and `run`, but
the current `doctor` and `safe-herdr` hot paths no longer create this temporary
probe. They prepare or inspect the configured main sandbox directly so the
diagnostic target matches the runtime target.

Runtime validation attempted:

```bash
sbx create --name safe-claude-sbx-probe-check shell .
```

The first two attempts passed authentication and policy checks, then failed
during the initial shell image pull with registry/CDN read errors:

```text
failed to pull image: ... Get "https://production.cloudfront.docker.com/...": EOF
failed to pull image: short read: expected 94976614 bytes but got 4905140: unexpected EOF
```

No sandbox was left behind after the failed pull. After switching Clash nodes,
the same command succeeded and created a running probe sandbox:

```text
SANDBOX                       AGENT   STATUS    PORTS   WORKSPACE
safe-claude-sbx-probe-check   shell   running           /Users/liuqingyuan/work/safe-claude-sbx
```

The `shell` probe includes `env`, `sh`, and `curl`.

### Execute Validation Commands

```bash
sbx exec <sandbox-name> env
sbx exec <sandbox-name> curl -fsS <url>
```

Confirmed help contract:

- `sbx exec [flags] SANDBOX COMMAND [ARG...]`
- If the sandbox is stopped, `sbx exec` starts it first.
- Flags match `docker exec` behavior.
- `--env` and `--env-file` can set environment variables for the executed
  command.
- `--workdir`, `--user`, `--interactive`, `--tty`, and `--detach` are available.

Use `sbx exec <probe-name> env` to classify proxy variables. Docker-managed
proxy variables are expected; host or unknown proxy targets are not. During
startup preflight, use `sbx exec <probe-name> curl -fsS <sandbox-check-url>` to
verify the sandbox egress IP. Runtime watchdog checks do not use
`sbx exec <main-name> curl -fsS <sandbox-check-url>` as the route-event safety
gate. They validate host-observable route, interface, and host egress state
instead, so a Docker Sandbox control-plane stall is not misclassified as runtime
sandbox egress drift.

Observed runtime behavior:

- `sbx exec <probe-name> env` succeeds with exit code `0`.
- Docker's credentials documentation describes the sandbox credential proxy and
  SSH agent forwarding model:
  <https://docs.docker.com/ai/sandboxes/security/credentials/>.
- Docker Sandbox may expose built-in service credential names such as
  `OPENAI_API_KEY` and `ANTHROPIC_API_KEY` as Docker-managed placeholders such
  as `proxy-managed`. The real credential value stays outside the sandbox.
- When the host has an SSH agent, Docker Sandbox may forward it into the sandbox
  as SSH forwarding environment such as `SSH_AUTH_SOCK` and
  `SSH_AUTH_SOCK_GATEWAY`. Private keys stay on the host, but sandbox processes
  can request signatures from the forwarded agent.
- Docker Sandbox injects proxy variables by default inside the sandbox:
  `HTTP_PROXY`, `HTTPS_PROXY`, `http_proxy`, `https_proxy`, `NO_PROXY`, and
  `no_proxy`.
- These values point at Docker Sandbox's internal proxy
  `gateway.docker.internal:3128`; they are not the host Clash proxy port
  `127.0.0.1:7897` configured by the user's shell.
- `sbx exec <probe-name> sh -lc 'command -v curl'` returned `/usr/bin/curl`.
- `curl -fsS https://icanhazip.com` succeeded inside the probe and returned the
  same IP as the host: `123.116.44.34`.
- `curl -fsS https://api.ipify.org` failed both on host and inside the probe on
  the current Clash node with TLS EOF errors, so `api.ipify.org` is not a
  reliable default check URL for this environment.
- Google connectivity is diagnostic only. A failed
  `curl -fsS https://www.google.com` from inside Docker Sandbox does not by
  itself prove an egress IP mismatch. Startup policy uses the configured
  `network.egress_ip.sandbox_check_url` during probe validation. Runtime policy
  uses event-triggered host route, interface, and host egress checks.

Docker documentation describes this as the normal networking path: requests
from inside the sandbox go through a sandbox proxy on `gateway.docker.internal`.
The proxy then applies Docker Sandbox policy and forwards allowed traffic
through the host network. The MVP therefore should not require the sandbox to be
proxy-env-free. It should allow Docker-managed proxy values such as
`gateway.docker.internal:3128`, reject host/Clash proxy values such as
`127.0.0.1:7897`, and reject unknown proxy targets. The launcher itself should
not add Clash proxy ports; network consistency should be based on TUN route and
egress validation.

### Timezone, Locale, And Environment

`sbx create` and `sbx run` help output did not expose a general environment flag.
The observed environment controls are on `sbx exec`:

```bash
sbx exec --env TZ=<timezone> --env LANG=<locale> --env LC_ALL=<locale> <sandbox-name> env
```

The backend adapter should not assume main-agent timezone or locale injection is
supported by `sbx run` until runtime validation finds a supported mechanism,
such as a template, kit, profile, secret, or agent argument.

Observed `sbx exec` environment injection:

```bash
sbx exec -e TZ=America/Chicago -e LANG=en_US.UTF-8 -e LC_ALL=en_US.UTF-8 <probe-name> env
```

Inside that exec command, `TZ` and `LANG` reflected the injected values.
`LC_ALL=en_US.UTF-8` was coerced to `LC_ALL=C.UTF-8` by the probe environment.
This confirms per-command exec environment injection, not main-agent launch
environment injection.

Timezone configuration should use an IANA timezone name such as
`America/Chicago`, matching the current Claude egress region. Do not configure a
fixed offset such as `UTC-5`: in July 2026 Chicago observes daylight saving time
and displays `UTC-5`, but it shifts to `UTC-6` in winter.

### Clean Environment Research

On 2026-07-05, `sbx create --help`, `sbx run --help`, and `sbx exec --help`
were rechecked locally against `/opt/homebrew/bin/sbx`.

Observed clean-env controls:

- `sbx exec` supports `--env` and `--env-file` for one command inside an
  existing sandbox.
- `sbx create` and `sbx run` expose `--profile`, `--template`, and
  experimental `--kit`, but their help output does not document a flag to
  disable default environment inheritance or provide an environment allowlist.
- No create/run help output documented a clean-env profile, template, or kit
  contract that this launcher can safely depend on.

Current launcher behavior:

- The backend adapter runs `sbx` subprocesses with a small host environment
  allowlist: `HOME`, `LOGNAME`, `PATH`, `SHELL`, `TERM`, `TMPDIR`, and `USER`.
- The configured main sandbox is inspected after host-side checks pass.
  Docker-managed credential placeholders such as `proxy-managed` are allowed,
  raw credential values fail closed, host or unknown proxy targets fail closed, and
  SSH forwarding environment such as `SSH_AUTH_SOCK` and
  `SSH_AUTH_SOCK_GATEWAY` is allowed only when
  `environment.allow_ssh_agent_forwarding` is explicitly `true`.
- The configured main sandbox performs sibling project read checks without
  reading file contents. It fails closed if the sandbox can read a file
  under a sibling project directory. Diagnostics report the readable path only.
  Direct launcher startup creates the main sandbox first, checks visibility
  without modifying parent guidance paths, and only then attaches Claude Code or
  starts sandbox-local Herdr. Failures stop the main sandbox and do not enter
  runtime watchdog supervision. `safe-herdr` checks the existing main sandbox
  before attaching the interactive Herdr TUI.
- Docker Sandbox can still expose the configured workspace path as metadata in
  `pwd`, `sbx ls`, create output, source-mount descriptions, or parent guidance
  handling. Current policy treats this as expected backend path metadata and
  enforces that sibling project files and unrelated sensitive paths are not
  readable.
- If a future Docker Sandbox version documents a create/run clean-env,
  allowlist, profile, template, or kit contract, the backend adapter should use
  that official mechanism and keep the inspection step as verification.

Host-side `sbx` and `sandboxd` logs still use the host timezone. A timestamp
such as `time=2026-07-05T21:32:31.321+08:00` should be treated as host/daemon
log time, not proof that the sandbox main agent timezone is configured.

### Dedicated Sandboxd Upstream Research

Issue #43 validated the `DOCKER_SANDBOXES_PROXY` contract on 2026-07-13 with:

- `sbx v0.34.0` (`2eae0c4fc3894475da3318615f69783b0e7be747`);
- Mihomo Meta `v1.19.28` for Darwin arm64; and
- MetaCubeXD `v1.267.2` served by that disposable Mihomo instance.

The tested `sbx` version accepts an HTTP upstream. SOCKS5 must remain behind an
explicit Docker Sandbox version capability check; this experiment did not test
the v0.35 SOCKS5 behavior.

The test Mihomo listened only on host loopback, used no controller secret or
proxy credentials, and chained to the host's existing credential-free loopback
HTTP proxy. Direct host dialing was unavailable on the test machine, so the
test gateway and the normal host path observed the same public IP. The evidence
therefore proves upstream traversal and fail-closed behavior, not a distinct
provider or public IP.

Sandboxd was started with a minimal command-scoped environment:

```bash
env -i \
  HOME="$HOME" \
  USER="$USER" \
  LOGNAME="$LOGNAME" \
  TMPDIR="${TMPDIR:-/tmp}" \
  PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin" \
  DOCKER_SANDBOXES_PROXY="http://127.0.0.1:17890" \
  sbx daemon start
```

Do not add generic `HTTP_PROXY`, `HTTPS_PROXY`, or `ALL_PROXY` variables to the
dedicated daemon environment. Keep the upstream URL credential-free.

Observed scope and positive egress evidence:

- Two disposable shell sandboxes ran simultaneously. Both retained Docker's
  managed `gateway.docker.internal:3128` proxy environment.
- HTTP and HTTPS requests from both sandboxes returned public IP
  `144.168.60.76`. At the same timestamps, Mihomo logged two HTTP and two HTTPS
  `api.ipify.org` connections through the configured upstream.
- Sandboxd reads the upstream at daemon startup and applies it to every loaded
  runtime. Its logs created a separate managed listener for each sandbox and
  reported `sandbox-specific upstream proxy configured` with the same upstream
  for both disposable sandboxes and the preexisting stopped sandbox.
- The practical contract is daemon-global selection with per-runtime managed
  listeners. There is no observed per-sandbox API for selecting different
  upstreams. A dedicated lease must therefore exclude concurrently running
  sandboxes that are not owned by the launcher and must keep watching for new
  conflicting runtimes.

Observed fail-closed behavior:

- With sandboxd still configured for the upstream, stopping Mihomo caused HTTP
  workload requests to receive a managed-proxy `500` response.
- HTTPS CONNECT failed with `502`. Curl still reported `proxy_used=1`; neither
  protocol returned a public IP or fell back to host-direct egress.
- `sbx daemon status`, `sbx ls`, `sbx template ls`, and `sbx exec` remained
  available while the gateway was down. The exec control request succeeded and
  only the workload network request failed.

Observed restart and drift behavior:

- `sbx daemon stop` changed both running disposable sandboxes to `stopped`.
- Restarting sandboxd without `DOCKER_SANDBOXES_PROXY` left existing sandbox
  metadata intact. A later `sbx exec` restarted those sandboxes and restored the
  normal host egress path while Mihomo remained stopped.
- A launcher must not trust persisted sandbox state. The dedicated lease must
  detect at least daemon PID/liveness change and loss of the upstream contract,
  then revalidate observed egress before reuse. Public IP alone is insufficient
  when dedicated and host paths happen to share an exit.
- Recovery requires the gateway to be healthy before sandboxd starts with the
  command-scoped upstream. A foreground daemon restart re-injected the upstream
  into loaded runtimes, after which both HTTP and HTTPS workload probes again
  appeared in Mihomo logs.

Control-plane boundary and limitations:

- All positive Mihomo observations came from sandbox workload or sandbox
  bootstrap hosts such as `api.ipify.org`, `download.docker.com`, and
  `ports.ubuntu.com`.
- Pull attempts for uncached stable tags `shell-0.2.0` and
  `shell-docker-0.2.0` caused containerd to contact
  `registry-1.docker.io` directly. With the minimal dedicated daemon
  environment and no usable host-direct route, each registry HEAD timed out
  after about 30 seconds. No request traversed Mihomo, no sandbox was created,
  and no image was added to the local store. This proves template transfer is
  outside the workload upstream, but it also means operators must complete
  required pulls on a working host control-plane path before acquiring the
  dedicated lease.
- `sbx login` is a separate operator CLI action and is not run inside the
  command-scoped daemon environment. Its interactive device flow was not
  repeated because doing so would create or handle a device code. Login remains
  a host control-plane prerequisite, not part of the dedicated workload-egress
  guarantee.
- Existing authentication state, daemon control, image listing, and sandbox
  lifecycle operations remained available independently of gateway health.
  Treat login, template image transfer, and daemon APIs as host control-plane
  traffic outside the dedicated workload-egress guarantee.

Cleanup removed both disposable sandboxes, the test gateway process, the
dashboard assets, and all temporary files. The preexisting `claude-sbx`
remained present and stopped, with its original workspace unchanged. The normal
sandboxd process was restored without `DOCKER_SANDBOXES_PROXY`.

### Private Docker Engine And Protocol Validation

Issue #47 extended the dedicated upstream experiment on 2026-07-13 with the
cached `shell-docker` template and its private Docker Engine. The test used one
uniquely named disposable sandbox, the same command-scoped HTTP upstream shape,
and a disposable Mihomo process chained to the credential-free host loopback
proxy. It did not change host routes or policy and did not inspect Clash
configuration, subscriptions, nodes, or credentials.

Confirmed paths:

- Agent and private Engine container HTTP and HTTPS observed public IP
  `144.168.60.76`, matching the expected upstream egress. Mihomo recorded the
  corresponding `api.ipify.org` HTTP and HTTPS connections.
- `docker pull alpine:3.20` inside the sandbox's private Docker Engine traversed
  Mihomo. Logs included `registry-1.docker.io`, `auth.docker.io`, and
  `production.cloudfront.docker.com`.
- This differs from host sandboxd template transfer. The #43 uncached
  `shell-docker-0.2.0` template pull contacted the registry outside Mihomo,
  while the #47 private Engine pull was part of the dedicated workload path.
- Direct external UDP DNS to `1.1.1.1:53` and ICMP to `1.1.1.1` were blocked.
  `sbx policy log` classified them as `<udp proxy policy>` and
  `<icmp proxy policy>` decisions.
- With Mihomo stopped, agent and container HTTP/HTTPS failed through the
  managed proxy, and an uncached private Engine `docker pull busybox:1.36.1`
  failed with `Bad Gateway`. These paths did not fall back to host-direct
  egress.

Unresolved bypasses:

- Container raw TCP to `ssh.github.com:443` succeeded both before and after the
  disposable Mihomo process stopped. Agent raw TCP using Bash `/dev/tcp` also
  succeeded after gateway loss. Mihomo recorded neither connection.
- Agent and container lookups of unique `nip.io` names continued to resolve
  after gateway loss through Docker Sandbox's internal resolver. The successful
  queries did not appear in Mihomo logs.
- `sbx policy log` recorded the blocked UDP and ICMP requests and the managed
  HTTP paths, but it did not record the successful raw TCP connection.

The HTTP `DOCKER_SANDBOXES_PROXY` contract therefore covers managed HTTP(S),
private Engine image pulls, and their failure behavior, but it does not prove a
fail-closed workload contract for generic TCP or internal DNS. Issue #51 closes
that unsafe startup path by rejecting unsupported Docker Sandbox protocol
capabilities before a dedicated daemon lease or main sandbox operation. The
strict repeatable acceptance procedure is in `tests/manual-test-plan.md`.

Cleanup removed the disposable sandbox and its private Engine image state,
stopped the disposable gateway, restored normal sandboxd without
`DOCKER_SANDBOXES_PROXY`, and left the preexisting `claude-sbx` present and
stopped.

### `sbx v0.35.0` SOCKS5h Candidate Validation

Issue #59 validated the installed candidate on 2026-07-15:

- `sbx v0.35.0` (`01e01520456e4126a9653471e7072e4d9b280321`);
- Mihomo Meta `v1.19.25` for Darwin arm64; and
- one credential-free `socks5h://127.0.0.1:17891` disposable endpoint chained
  to the operator's credential-free host loopback HTTP proxy.

The official [v0.35.0 release notes](https://github.com/docker/sbx-releases/releases/tag/v0.35.0)
add `socks5://` and `socks5h://` upstream transport. Docker's
[architecture contract](https://docs.docker.com/ai/sandboxes/architecture/)
states that `socks5://` resolves DNS locally, while `socks5h://` delegates
hostname resolution to the proxy. It also states that
`DOCKER_SANDBOXES_PROXY` applies only to sandbox traffic and does not affect
sandboxd image pulls or the daemon's own requests. Private Engine image pulls
inside a sandbox are a separate workload path and were tested below. The test
used only command-scoped `DOCKER_SANDBOXES_PROXY`; generic proxy variables were
absent.

Healthy-gateway evidence:

- Agent and private Engine container HTTP/HTTPS all observed
  `144.168.60.76`. Mihomo recorded `api.ipify.org` by hostname, confirming
  SOCKS5h proxy-side resolution for those connections.
- An uncached private Engine pull of `alpine:3.20` succeeded. Mihomo recorded
  `registry-1.docker.io`, `auth.docker.io`, and the layer CDN.
- Agent and container generic TCP to `ssh.github.com:443` succeeded, but
  neither Mihomo nor `sbx policy log` recorded those connections. A read-only
  `sbx policy check network` reported the target allowed by `default-allow-all`.
- Unique agent and container lookups through Docker's internal resolver
  succeeded. Direct UDP DNS to `1.1.1.1:53` was blocked and logged as
  `<udp proxy policy>` with `Direct UDP connections not allowed`.

Gateway-loss evidence held the same sandboxd PID and running disposable
sandbox while the Mihomo listener was stopped:

- Agent/container HTTP and HTTPS failed without returning a public IP.
- The private Engine confirmed `busybox:1.36.1` was uncached, then its pull
  failed. Policy logs marked the registry and HTTP(S) attempts `<dial failed>`.
- Agent and container generic TCP to `ssh.github.com:443` still succeeded.
- Fresh, uncached agent and container names still resolved through Docker's
  internal resolver. Direct UDP DNS remained blocked.

This is a **FAIL** for the protocol-complete dedicated contract. SOCKS5h adds
the documented proxy transport and remote hostname resolution for proxied
connections, but generic TCP and Docker-internal DNS still have paths that do
not depend on the disposable gateway. `v0.35.0` therefore remains outside the
production support matrix and continues through unknown-version rejection.

Cleanup stopped and removed only `ralph-59-socks5h-20260715`, stopped the
disposable gateway, removed its temporary files, and restored normal sandboxd
without dedicated or generic proxy variables. The cached template list was
unchanged, and the preexisting `claude-sbx` remained present and stopped with
its original workspace.

### Dedicated Protocol Capability Gate

Issue #51 rechecked the official `sbx v0.34.0` command surface on 2026-07-13:

- `sbx daemon start` accepts only `--policy allow-all|balanced|deny-all`; it has
  no transparent upstream or DNS upstream option.
- `sbx policy allow network` and `sbx policy deny network` can be scoped to one
  sandbox, but resources are hostnames, domains, IP addresses, and optional
  ports. Rules do not select HTTP, generic TCP, or DNS as separate protocols.
- Deny rules take precedence. A scoped `deny **` would also block the managed
  HTTP(S) and private Engine paths that must remain available, so it cannot be
  combined with an allow rule to express the dedicated contract.
- Policy profiles come from remote governance; `v0.34.0` exposes no local
  profile creation mechanism that supplies a protocol-complete upstream.

The dedicated `EgressGuard` now reads `sbx version` through its existing
command seam before controller health or lease acquisition. The production
support matrix is intentionally empty because no tested release has a validated
combined contract for managed HTTP(S), generic TCP, and DNS. `v0.34.0`,
`v0.35.0`, command failure, malformed version output, and unknown versions all
fail closed without printing controller secrets or backend output. The
diagnostics for the tested releases are:

```text
dedicated protocol isolation unsupported: sbx v0.34.0 provides HTTP upstream only; generic TCP and DNS are not fail closed
dedicated protocol isolation unsupported: sbx v0.35.0 has no validated generic TCP and DNS contract
```

Doctor performs this capability check before general backend availability so
`sbx ls` cannot auto-start a normal daemon on an unsupported version. The
failure occurs before controller requests, `sbx ls`, `sbx daemon stop`,
`sbx daemon start`, `sbx create`, or `sbx exec`. The deeper
controller, exclusive lease, main validation, and cleanup implementation remains
covered through an internal test Adapter so a future explicitly supported
backend can reuse it without a parallel gateway path.

### Sandbox-Local Herdr Prototype

Issue #15 validated the sandbox-local Herdr startup contract against a real
Docker Sandbox `claude` template on 2026-07-06. That prototype proved that
sandbox-local Herdr could run inside Docker Sandbox, but it also proved that
installing Herdr in a running sandbox is not durable because normal launcher
startup prepares fresh sandbox state.

The permanent path is the custom template in
`sandbox/claude-herdr-template/`. Runtime launcher startup must verify
template-provided Herdr and `cc`; it must not install Herdr in the running main
sandbox.

The Claude integration hook is repeatable:

```bash
sbx exec safe-claude-sbx-herdr-prototype herdr integration install claude
sbx exec safe-claude-sbx-herdr-prototype sh -lc 'cat /home/agent/.claude/settings.json'
sbx exec safe-claude-sbx-herdr-prototype sh -lc 'sed -n "1,220p" /home/agent/.claude/hooks/herdr-agent-state.sh'
```

Observed result:

- `herdr integration install claude` installed
  `/home/agent/.claude/hooks/herdr-agent-state.sh`.
- It ensured Claude settings at `/home/agent/.claude/settings.json`.
- The settings hook entry calls:
  `bash '/home/agent/.claude/hooks/herdr-agent-state.sh' session`.
- The hook exits before reporting unless all of these are present:
  `HERDR_ENV=1`, non-empty `HERDR_SOCKET_PATH`, and non-empty `HERDR_PANE_ID`.
- With those values present and a session payload containing `session_id`, the
  hook attempts a Unix socket request to `pane.report_agent_session`.

The sandbox-local server/socket command shape is:

```bash
sbx exec safe-claude-sbx-herdr-prototype herdr status server
sbx exec safe-claude-sbx-herdr-prototype herdr server
sbx exec safe-claude-sbx-herdr-prototype herdr status server --json
sbx exec safe-claude-sbx-herdr-prototype herdr session list --json
```

Observed result:

- Before startup, `herdr status server` reported `status: not running` and
  socket `/home/agent/.config/herdr/herdr.sock`.
- `herdr server` runs as a foreground server process and prints:
  `api socket: /home/agent/.config/herdr/herdr.sock`,
  `client socket: /home/agent/.config/herdr/herdr-client.sock`, and
  `logs: /home/agent/.config/herdr/herdr-server.log`.
- A running server reports JSON status with `running: true`, `version: "0.7.1"`,
  `protocol: 14`, and socket `/home/agent/.config/herdr/herdr.sock`.
- The server creates a default session whose `socket_path` is
  `/home/agent/.config/herdr/herdr.sock`.
- In this prototype, `sbx exec --detach ... herdr server` still kept the host
  command attached until the server stopped. A launcher should supervise this
  foreground process explicitly or validate a stronger detached/session
  contract before depending on it.

The hook can be exercised without starting a real Claude account session:

```bash
sbx exec -e HERDR_ENV=1 -e HERDR_SOCKET_PATH=/home/agent/.config/herdr/herdr.sock -e HERDR_PANE_ID=sandbox-local:claude safe-claude-sbx-herdr-prototype sh -lc 'printf "%s\n" "{\"hook_event_name\":\"SessionStart\",\"session_id\":\"prototype-session\",\"transcript_path\":\"/home/agent/.claude/projects/prototype.jsonl\",\"source\":\"startup\"}" | /home/agent/.claude/hooks/herdr-agent-state.sh session; echo hook_exit:$?'
sbx exec safe-claude-sbx-herdr-prototype sh -lc 'tail -n 80 /home/agent/.config/herdr/herdr-server.log'
```

Observed result:

- The hook exited `0`.
- The Herdr server log recorded an API request for
  `method="pane.report_agent_session"` with a `herdr:claude` request id.
- The API request outcome was `error` because the synthetic pane id was not a
  real Herdr pane. That still confirms the hook reached the sandbox-local Herdr
  socket when given sandbox-local `HERDR_*` values.

Host Herdr isolation was checked explicitly:

```bash
env HERDR_SOCKET_PATH=/tmp/host-herdr.sock HERDR_PANE_ID=host-pane HERDR_ENV=1 sbx exec safe-claude-sbx-herdr-prototype sh -lc 'env | grep "^HERDR_" || true'
```

Observed result:

- No `HERDR_*` values were printed inside the sandbox.
- Host `HERDR_*` values did not enter `sbx exec` unless explicitly passed with
  `-e`.
- The future launcher must keep using an explicit environment allowlist for host
  `sbx` subprocesses and must only inject sandbox-local Herdr values inside the
  sandbox command that needs them.

Cleanup behavior:

```bash
sbx exec safe-claude-sbx-herdr-prototype herdr server stop
sbx stop safe-claude-sbx-herdr-prototype
sbx rm --force safe-claude-sbx-herdr-prototype
```

Observed result:

- `herdr server stop` stopped the foreground Herdr server.
- `sbx stop` stopped the prototype sandbox.
- `sbx rm --force` removed the prototype sandbox.
- After cleanup, `sbx ls` showed only the preexisting stopped `claude-sbx`.

The implementation contract for sandbox-local Herdr mode is:

- Use the `claude` template sandbox, not the `shell` probe template, for Herdr
  integration validation.
- Require template-provided `herdr` and `/usr/local/bin/cc`. The launcher checks
  inside the sandbox with `command -v herdr` and `command -v cc`; missing tools
  fail closed with an actionable template diagnostic and do not trigger runtime
  downloads.
- Fail closed if the version checks fail.
- Run `herdr integration install claude` inside the Claude template sandbox and
  verify `/home/agent/.claude/hooks/herdr-agent-state.sh`.
- Start `herdr server` inside the sandbox before starting Claude under the
  sandbox-local Herdr environment.
- Wait for `herdr status server --json` to report a running server whose socket
  path matches the configured sandbox-local socket before starting Claude.
- Use `/home/agent/.config/herdr/herdr.sock` as the sandbox-local socket path
  unless configuration supplies another path under `/home/agent`.
- Provide only sandbox-local `HERDR_ENV=1`, `HERDR_SOCKET_PATH`, and
  `HERDR_PANE_ID` to the Claude/Herdr process boundary.
- Never pass host `HERDR_SOCKET_PATH`, host pane ids, host workspace ids, or
  host Herdr sockets into the sandbox.
- Treat the Herdr TUI and any `cc` process it starts as sharing the same Docker
  Sandbox filesystem visibility. Herdr is supervision inside the sandbox, not an
  additional filesystem isolation boundary.
- On startup failure or watchdog-triggered shutdown, stop the sandbox-local
  Herdr server and then use the existing sandbox cleanup path.

#### Sandbox-Local Herdr Architecture

```mermaid
graph TB
    hostLauncher["Host launcher"]
    hostEnv["Host environment allowlist"]
    hostHerdr["Host Herdr state"]
    sbxCli["sbx CLI"]
    sandbox["Docker Sandbox claude template"]
    localHerdr["Sandbox-local Herdr server"]
    localSocket["Sandbox socket /home/agent/.config/herdr/herdr.sock"]
    claude["Sandbox-local Claude Code"]
    hook["Claude Herdr hook"]

    hostLauncher --> hostEnv
    hostLauncher --> sbxCli
    sbxCli --> sandbox
    sandbox --> localHerdr
    localHerdr --> localSocket
    sandbox --> claude
    claude --> hook
    hook --> localSocket
    hostHerdr -.->|must not pass socket or HERDR env| sandbox

    classDef hostClass fill:#e7f5ff,stroke:#1971c2,color:#0b3d66
    classDef blockedClass fill:#ffe3e3,stroke:#c92a2a,color:#5c1a1a
    classDef sandboxClass fill:#c5f6fa,stroke:#0c8599,color:#073b43
    classDef herdrClass fill:#e5dbff,stroke:#5f3dc4,color:#2b145e
    class hostLauncher,hostEnv,sbxCli hostClass
    class hostHerdr blockedClass
    class sandbox,claude,hook sandboxClass
    class localHerdr,localSocket herdrClass
```

#### Sandbox-Local Herdr Startup Sequence

```mermaid
sequenceDiagram
    participant L as Launcher
    participant S as sbx CLI
    participant X as Claude sandbox
    participant H as Sandbox Herdr
    participant C as Claude Code
    participant K as Claude hook

    L->>S: sbx create --name main --template configured-template claude workspace
    S-->>X: Claude template with Herdr and /usr/local/bin/cc
    L->>S: sbx exec main command -v herdr
    alt Herdr missing
        L->>S: sbx stop main
        S-->>L: fail closed
    else Herdr available
        S-->>L: continue startup
    end
    L->>S: sbx exec main herdr --version
    L->>S: sbx exec main herdr integration install claude
    S-->>K: Hook and settings installed
    L->>S: sbx exec main command -v cc
    alt cc missing
        L->>S: sbx stop main
        S-->>L: fail closed
    else cc available
        S-->>L: continue startup
    end
    L->>S: sbx exec main cc --version
    L->>S: sbx exec main herdr server
    S-->>H: Foreground server owns sandbox socket
    loop until ready or bounded timeout
        L->>S: sbx exec main herdr status server --json
        S-->>L: running flag and socket path
    end
    L->>S: start Claude with sandbox-local HERDR env
    S-->>C: Claude process
    C->>K: SessionStart hook
    K->>H: pane.report_agent_session over sandbox socket
    alt startup or runtime failure
        L->>S: herdr server stop
        L->>S: sbx stop main
    end
```

#### Sandbox-Local Herdr Prototype Contract

```mermaid
classDiagram
    class HerdrStartupContract {
        +sandboxName string
        +workspace string
        +installIfMissing bool
        +socketPath string
        +paneID string
    }

    class HerdrInstallResult {
        +binaryPath string
        +version string
        +installed bool
    }

    class ClaudeIntegrationResult {
        +hookPath string
        +settingsPath string
        +requiresEnv bool
    }

    class HerdrServerResult {
        +socketPath string
        +clientSocketPath string
        +logPath string
        +foregroundProcess bool
    }

    class HerdrCleanupResult {
        +serverStopped bool
        +sandboxStopped bool
        +sandboxRemoved bool
    }

    HerdrStartupContract --> HerdrInstallResult
    HerdrStartupContract --> ClaudeIntegrationResult
    HerdrStartupContract --> HerdrServerResult
    HerdrStartupContract --> HerdrCleanupResult
```

### Stop And Cleanup

Default cleanup policy:

- Stop the main sandbox.
- Remove temporary probe sandboxes only when a legacy diagnostic path created one.
- Do not remove the main sandbox.
- Treat missing or already-stopped cleanup targets as non-fatal after their
  authenticated exit behavior is confirmed.

Confirmed help contract:

```bash
sbx stop <sandbox-name>
sbx rm --force <sandbox-name>
```

- `sbx stop SANDBOX [SANDBOX...]` stops one or more running sandboxes without
  removing state.
- Stopped sandboxes can be restarted with `sbx run`.
- `sbx rm [SANDBOX...] --force` removes sandboxes and skips confirmation.
- `sbx rm --all --force` removes every sandbox and must not be used by the MVP.

Pre-auth `stop` and `rm` fail at authentication before checking whether the
named sandbox exists.

After authentication, nonexistent cleanup targets returned exit code `1`:

```text
Error: sandbox 'safe-claude-sbx-nonexistent' not found (run 'sbx ls' to see your sandboxes)
```

Observed commands:

- `sbx stop safe-claude-sbx-nonexistent`
- `sbx rm --force safe-claude-sbx-nonexistent`

The launcher should treat this specific authenticated not-found cleanup result
as non-fatal during idempotent cleanup, while still surfacing unexpected cleanup
errors.

Observed real probe cleanup:

- `sbx stop safe-claude-sbx-probe-check` returned exit code `0` and printed
  `Sandbox 'safe-claude-sbx-probe-check' stopped`.
- `sbx exec <stopped-probe> ...` restarted the stopped sandbox automatically,
  matching the help contract.
- `sbx rm --force safe-claude-sbx-probe-check` returned exit code `0` and
  removed the probe.

## Adapter Boundary

```mermaid
graph TB
    launcher["safe-claude-sbx launcher"]
    config["Structured config"]
    policy["Policy checks"]
    backend["Docker Sandbox backend adapter"]
    sbx["sbx CLI v0.34.0"]
    daemon["sandboxd daemon"]
    dockerAuth["Docker authentication"]
    sandbox["Named sandbox"]
    probe["Probe sandbox"]

    config --> launcher
    launcher --> policy
    policy --> backend
    backend --> sbx
    sbx --> daemon
    sbx --> dockerAuth
    daemon --> sandbox
    daemon --> probe
    probe --> policy
    sandbox --> launcher

    classDef appClass fill:#e7f5ff,stroke:#1971c2,color:#0b3d66
    classDef policyClass fill:#ffe3e3,stroke:#c92a2a,color:#5c1a1a
    classDef backendClass fill:#e5dbff,stroke:#5f3dc4,color:#2b145e
    classDef runtimeClass fill:#c5f6fa,stroke:#0c8599,color:#073b43
    class launcher,config appClass
    class policy policyClass
    class backend,sbx backendClass
    class daemon,dockerAuth,sandbox,probe runtimeClass
```

## Lifecycle Sequence

```mermaid
sequenceDiagram
    participant L as Launcher
    participant B as Backend adapter
    participant S as sbx CLI
    participant D as sandboxd
    participant A as Docker auth
    participant P as Probe sandbox
    participant M as Main sandbox

    L->>B: Check availability
    B->>S: command -v sbx and sbx version
    B->>S: sbx diagnose
    S-->>B: CLI ok, daemon may be stopped
    B->>S: sbx login
    S->>A: Browser device-code login
    A-->>S: Human completes login
    S-->>B: Authenticated session
    B->>S: sbx create --name probe shell workspace
    S->>D: Create probe
    D-->>P: Probe ready
    B->>S: sbx exec probe env
    B->>S: sbx exec probe curl check-url
    S-->>B: Env and egress observations
    B->>S: sbx rm --force probe
    B->>S: sbx create --name main claude workspace
    S->>D: Create main
    D-->>M: Main ready
    B->>S: sbx exec main inspect workspace visibility
    B->>S: sbx run --name main
    M-->>L: Agent session
    L->>B: Cleanup on exit or watchdog failure
    B->>S: sbx stop main
    B->>S: sbx rm --force probe
```

## Minimum Backend Interface

```mermaid
classDiagram
    class SandboxBackend {
        +CheckAvailability() BackendStatus
        +EnsureAuthenticated() AuthStatus
        +CreateProbe(config) SandboxRef
        +Exec(sandbox, command, args) CommandResult
        +CreateMain(config) SandboxRef
        +RunMain(config) ProcessHandle
        +Stop(sandbox) CleanupResult
        +Remove(sandbox) CleanupResult
    }

    class BackendStatus {
        +sbxPath string
        +version string
        +daemonReachable bool
        +diagnostic string
    }

    class AuthStatus {
        +authenticated bool
        +requiresBrowser bool
        +message string
    }

    class SandboxRef {
        +name string
        +agent string
        +workspace string
        +isProbe bool
    }

    class CommandResult {
        +exitCode int
        +stdout string
        +stderr string
    }

    class CleanupResult {
        +fatal bool
        +message string
    }

    SandboxBackend --> BackendStatus
    SandboxBackend --> AuthStatus
    SandboxBackend --> SandboxRef
    SandboxBackend --> CommandResult
    SandboxBackend --> CleanupResult
```
