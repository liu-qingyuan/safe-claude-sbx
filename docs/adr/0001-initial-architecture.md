# ADR 0001: macOS-first launcher with Docker Sandbox backend

## Status

Accepted

## Context

The first project goal is to safely start Claude Code in Docker Sandbox on a macOS workstation when Clash Verge TUN mode is active and both host and sandbox egress match a configured IP.

## Decision

The project will start as a macOS-first CLI with a Docker Sandbox / `sbx` backend. Core policy logic will be separated from platform route inspection and backend process control so future backends can be added without rewriting the safety policy.

The first version will be event-driven for route changes using `route -n monitor` and will not use a 5 to 10 second fallback polling loop by default.

## Consequences

- macOS route monitoring and `utunX` checks are explicitly platform-specific.
- Backend behavior is isolated behind an adapter contract.
- Manual integration testing is required because TUN routing and public egress IPs depend on the user's local environment.
