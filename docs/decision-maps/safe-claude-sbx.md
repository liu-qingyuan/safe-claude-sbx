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
  preferred probe shape until authenticated runtime testing proves a smaller
  one-shot command.
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
- Creating the `shell` probe is currently blocked by repeated initial image
  pull failures from Docker's registry/CDN: `EOF` and `unexpected EOF`.
- Probe network access, `env`/`curl` availability, and main-agent
  timezone/locale injection still require runtime validation after image pull
  succeeds.

Details and diagrams are recorded in `docs/docker-sandbox-backend.md`.

## #2: Define probe sandbox execution shape

Blocked by: #1
Type: Research

### Question

How should the preflight create or reuse a temporary sandbox to run `env` and `curl` checks without leaking proxy environment variables or persistent credentials?

### Answer

Partially confirmed. After Docker login and `sbx policy init allow-all`, the
implementation should create a named `shell` probe sandbox and execute
validation commands inside it:

```bash
sbx create --name <probe-name> shell <workspace>
sbx exec <probe-name> env
sbx exec <probe-name> curl -fsS <sandbox-check-url>
sbx rm --force <probe-name>
```

This contract still depends on a successful initial `shell` image pull. Two
runtime attempts failed during image download with Docker registry/CDN `EOF` /
`unexpected EOF` errors. After the pull succeeds, validate that the `shell`
probe image includes `env` and `curl`, and that network policy allows the
egress check URL.

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
