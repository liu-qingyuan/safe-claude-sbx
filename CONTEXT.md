# Project Context

`safe-claude-sbx` is a macOS-first CLI tool for safely starting and supervising Claude Code inside Docker Sandbox.

## Domain Terms

- **launcher**: The user-facing CLI that validates safety conditions before starting the sandbox.
- **preflight**: Startup checks that must pass before a sandbox may be created or used, including sandbox reachability checks that require `sbx exec`.
- **watchdog**: Lightweight runtime supervision that stops the sandbox when host route or host egress conditions no longer match policy.
- **Clash event source**: A host-side signal that may indicate route or egress drift, such as a route event or Clash Verge app-home file metadata change; it must not require reading or printing proxy credentials.
- **TUN interface**: A macOS `utunX` interface used by Clash Verge TUN mode for transparent routing.
- **egress IP**: The public IP observed by the host, or by the sandbox during startup validation, when calling a configured IP-check endpoint.
- **workspace mount**: The single project directory made visible inside the sandbox.
- **sensitive path**: A host path that must never be mounted into the sandbox, such as Home, SSH, Claude config, Clash config, or Keychain directories.
- **backend**: The sandbox runtime adapter. Phase 1 supports Docker Sandbox / `sbx`.

## Architectural Direction

- The highest testing and integration seam is the launcher behavior: given config and observable platform/backend state, it either starts and supervises a sandbox or fails closed.
- macOS route inspection is platform-specific infrastructure.
- Docker Sandbox / `sbx` is a backend adapter, not core policy logic.
- Egress validation and mount policy should remain backend-independent where possible.

## Compliance Boundary

This project must not include guidance or implementation intended to bypass platform rules, risk controls, account limits, payment systems, organization policies, or credential protections.
