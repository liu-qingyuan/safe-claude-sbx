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
- Stop the sandbox when route events indicate the TUN interface disappeared, the default route changed, or sandbox egress changed.

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

Validate the current network and sandbox policy before starting work:

```bash
safe-claude-sbx doctor --config config.yaml
```

Use sandbox-local Herdr as the daily entry point:

```bash
sbx exec -it claude-sbx herdr
```

Inside the Herdr TUI, start Claude with the sandbox-local shortcut:

```bash
cc
```

`cc` is a sandbox-local wrapper at `/usr/local/bin/cc`; it should not be
installed on the host or read host Claude credentials.

For plain Docker Sandbox usage without Herdr, start Claude directly:

```bash
sbx run claude --name claude-sbx .
```

For an existing sandbox, open Claude interactively:

```bash
sbx exec -it claude-sbx claude
```

## Configuration

Copy `config.example.yaml` to `config.yaml`, then set `network.egress_ip.expected_ip` and `network.egress_ip.host_check_url` for your local network policy before running `doctor`.

## Safety Notice

Do not commit tokens, OAuth sessions, API keys, SSH keys, Claude user configuration, Clash configuration, or Keychain material into this repository or into any workspace mounted into a sandbox.
