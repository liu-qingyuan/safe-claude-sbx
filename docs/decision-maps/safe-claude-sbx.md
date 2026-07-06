# Decision Map: safe-claude-sbx

This decision map tracks unresolved design work for the macOS Docker Sandbox launcher. The initial PRD is in `docs/prds/safe-claude-sbx-prd.md`.

## #1: Confirm exact `sbx` command contract

Blocked by: none
Type: Research

### Question

Which `sbx` commands and flags should the launcher depend on for creating, naming, probing, stopping, and optionally removing Claude Code sandboxes?

### Answer

Partially confirmed on the target macOS host with `sbx v0.34.0`.

Confirmed:

- `sbx` can be installed with `brew trust docker/tap` and
  `brew install docker/tap/sbx`.
- `command -v sbx` resolves to `/opt/homebrew/bin/sbx`.
- `sbx create --name <name> claude <workspace>` is the clearest creation
  command for the main Claude sandbox.
- `sbx run claude --name <name> <workspace> -- <agent-args>` creates or
  reattaches a main sandbox and passes agent arguments after `--`.
- `sbx create --name <probe-name> shell <workspace>` plus `sbx exec` is the
  confirmed probe shape.
- `sbx stop <name>` stops without removing state.
- `sbx rm --force <name>` removes a sandbox without an interactive confirmation.
- `sbx login` completed through Docker browser device-code authentication.
- The target host's global Docker Sandbox policy was initialized with
  `sbx policy init allow-all`.
- After authentication, `sbx ls` succeeds with exit code `0` on an empty machine.
- After authentication, `sbx stop <missing-name>` and
  `sbx rm --force <missing-name>` return exit code `1` with a not-found error;
  the launcher should treat this specific cleanup case as non-fatal.

Blocked:

- `sbx daemon start` is a foreground daemon process; `sbx ls` can start the
  daemon when needed, but no one-shot startup command has been confirmed.
- Docker Sandbox injects proxy env vars into the sandbox by default, pointing
  at `gateway.docker.internal:3128`; #8 must allow Docker-managed proxy values
  while rejecting host/Clash or unknown proxy targets.
- Main-agent timezone/locale injection still requires runtime validation. Host
  or daemon logs using `+08:00` are not proof that sandbox agent timezone is
  configured.

Details and diagrams are recorded in `docs/docker-sandbox-backend.md`.

## #2: Define probe sandbox execution shape

Blocked by: #1
Type: Research

### Question

How should the preflight create or reuse a temporary sandbox to run `env` and `curl` checks without leaking proxy environment variables or persistent credentials?

### Answer

Confirmed after Docker login and `sbx policy init allow-all`. The
implementation should create a named `shell` probe sandbox and execute
validation commands inside it:

```bash
sbx create --name <probe-name> shell <workspace>
sbx exec <probe-name> env
sbx exec <probe-name> curl -fsS <sandbox-check-url>
sbx rm --force <probe-name>
```

The first image pull failed twice with Docker registry/CDN `EOF` /
`unexpected EOF` errors, then succeeded after switching Clash nodes. The `shell`
probe includes `env`, `sh`, and `/usr/bin/curl`.

Observed egress behavior:

- `https://icanhazip.com` returned the same IP on host and inside the probe:
  `123.116.44.34`.
- `https://api.ipify.org` failed on both host and probe with TLS EOF on the
  current node, so it should not be the only default check URL.

## #3: Decide first implementation language and packaging

Blocked by: none
Type: Grilling

### Question

Should the MVP be a Go CLI, a shell-first tool, or another packaging format?

### Answer

Initial default: Go CLI. The project needs reliable signal handling, child process supervision, YAML parsing, route event streaming, and testable policy modules. Shell scripts remain as diagnostics, not the main state machine.

## #4: Validate macOS route event coverage

Blocked by: none
Type: Research

### Question

Which real Clash Verge TUN state changes produce `route -n monitor` events, and which changes require documentation as known limitations?

### Answer

Unresolved. Manual tests must cover TUN off, node switch, Wi-Fi switch, sleep/wake, VPN conflict, and Docker Sandbox already running.

## #5: Define backend extension contract

Blocked by: #1, #2
Type: Grilling

### Question

What is the minimal backend interface needed for Docker Sandbox now while leaving room for ordinary Docker, Apple container, or microVM backends later?

### Answer

Initial shape confirmed for the Docker Sandbox adapter:

- Check availability and diagnostics.
- Ensure authentication, or return a browser/account blocker.
- Create a named probe sandbox.
- Execute `env` and `curl` inside the probe.
- Create or run a named main sandbox.
- Stop the main sandbox.
- Remove the probe sandbox.

Avoid committing to equivalent security guarantees for non-`sbx` backends until
separately researched.

## #6: Decide sandbox-local Herdr supervision model

Blocked by: #1, #2
Type: Prototype

### Question

Should `safe-claude-sbx` support running a sandbox-local Herdr instance that
supervises Claude Code inside the Docker Sandbox, without exposing the host
Herdr socket or host `HERDR_*` environment variables?

### Answer

Chosen direction: prototype option B only. Host Herdr does not need to display
the sandbox Claude state. Do not expose the host `HERDR_SOCKET_PATH`,
`HERDR_PANE_ID`, or other host `HERDR_*` values into the sandbox.

Confirmed in the target `claude-sbx` sandbox:

- Herdr can be installed inside the Claude Docker Sandbox:
  `/home/agent/.local/bin/herdr`, version `0.7.1`.
- `herdr integration install claude` succeeds inside the `claude` template
  sandbox because `/home/agent/.claude` exists.
- The hook is installed at
  `/home/agent/.claude/hooks/herdr-agent-state.sh`.
- The hook expects `HERDR_ENV`, `HERDR_SOCKET_PATH`, and `HERDR_PANE_ID`.
- Without sandbox-local `HERDR_*` values, the hook exits and reports nothing.
- The shell probe template is not sufficient for this experiment because it does
  not contain Claude Code or `/home/agent/.claude`.

Next prototype should answer:

- How to start sandbox-local Herdr server/session before Claude Code starts.
- Which sandbox-local `HERDR_*` values are required for the Claude hook.
- Whether `sbx run shell` plus an in-sandbox launcher can start Herdr and Claude
  in the same TTY without weakening TUN, egress, mount, cleanup, or watchdog
  guarantees.
- Whether Herdr state remains entirely inside the sandbox when no host Herdr
  socket is mounted or passed through.
