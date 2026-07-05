# Decision Map: safe-claude-sbx

This decision map tracks unresolved design work for the macOS Docker Sandbox launcher. The initial PRD is in `docs/prds/safe-claude-sbx-prd.md`.

## #1: Confirm exact `sbx` command contract

Blocked by: none
Type: Research

### Question

Which `sbx` commands and flags should the launcher depend on for creating, naming, probing, stopping, and optionally removing Claude Code sandboxes?

### Answer

Unresolved. Initial assumptions are based on Docker's public docs for `sbx run claude`, `sbx ls`, `sbx stop`, and `sbx rm`. Before implementation, verify the installed `sbx` version's actual help output on the target macOS host.

## #2: Define probe sandbox execution shape

Blocked by: #1
Type: Research

### Question

How should the preflight create or reuse a temporary sandbox to run `env` and `curl` checks without leaking proxy environment variables or persistent credentials?

### Answer

Unresolved. The implementation should prefer the smallest available `sbx` command surface that can run a one-shot command in an isolated sandbox and then remove it.

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

Unresolved. Initial shape: check availability, probe, run, stop, remove, and exec. Avoid committing to equivalent security guarantees for non-`sbx` backends until separately researched.
