# safe-claude-sbx

`safe-claude-sbx` is a macOS-first launcher and watchdog for running Claude Code inside Docker Sandbox only when the local network and sandbox egress match the configured safety policy.

The project is intended for compliant local development workflows. It does not bypass platform rules, account controls, payment requirements, organization policies, or service-side risk systems.

## Goals

- Refuse to start Claude Code when the macOS default route is not going through a TUN interface.
- Verify that both the host and Docker Sandbox egress IP match the configured expected IP.
- Allow Docker-managed sandbox proxy values and credential placeholders, while
  rejecting host/unknown proxy targets and raw sensitive environment values.
- Require explicit configuration before accepting Docker Sandbox SSH agent
  forwarding.
- Restrict the sandbox workspace mount to the current project directory.
- Stop the sandbox when runtime route or Clash Verge app-home metadata events
  show that the startup TUN interface disappeared, the default route changed,
  or host egress drifted from the configured expected IP.

## Non-Goals

- Bypassing any platform policy or risk control.
- Hiding from remote service network classification.
- Managing Claude, Anthropic, Docker, or organization credentials.
- Providing a complete security boundary beyond what Docker Sandbox and macOS networking actually enforce.

## Current Status

This repository is implementing the Docker Sandbox / `sbx` MVP described in:

- `docs/prds/safe-claude-sbx-prd.md`
- `docs/decision-maps/safe-claude-sbx.md`
- `tests/manual-test-plan.md`

## Daily Commands

Before starting work, update `network.egress_ip.expected_ip` in `config.yaml`
for the current approved egress IP.

Build the Docker Sandbox template that contains sandbox-local Herdr and
`/usr/local/bin/cc`:

```bash
docker build -t safe-claude-sbx-herdr:latest sandbox/claude-herdr-template
docker image save safe-claude-sbx-herdr:latest -o safe-claude-sbx-herdr.tar
sbx template load safe-claude-sbx-herdr.tar
```

Set `sandbox.template: "safe-claude-sbx-herdr:latest"` and keep
`sandbox.supervision.herdr.install_if_missing: false`.

Validate the current network and sandbox policy:

```bash
safe-claude-sbx doctor --config config.yaml
```

Use the policy-gated sandbox-local Herdr TUI as the daily entry point:

```bash
safe-herdr --config config.yaml
```

`safe-herdr` validates policy, creates the configured main sandbox from
`sandbox.template` when needed, verifies `herdr` and `cc` inside that sandbox,
and then attaches with `sbx exec -it <main_name> herdr`. It does not download
Herdr during startup or rewrite sandbox-local wrappers.

Inside the Herdr TUI, start Claude with the sandbox-local shortcut:

```bash
cc
```

`cc` is a sandbox-local wrapper at `/usr/local/bin/cc`; it should not be
installed on the host or read host Claude credentials.

For plain Docker Sandbox usage without Herdr, set
`sandbox.supervision.mode: "direct-claude"` and start Claude through the guarded
launcher:

```bash
safe-claude-sbx --config config.yaml
```

## Configuration

Copy `config.example.yaml` to `config.yaml`, then set
`network.egress_ip.expected_ip` and `network.egress_ip.host_check_url` for your
local network policy before running `doctor` or `safe-herdr`.

## Safety Notice

Do not commit tokens, OAuth sessions, API keys, SSH keys, Claude user configuration, Clash configuration, or Keychain material into this repository or into any workspace mounted into a sandbox.
