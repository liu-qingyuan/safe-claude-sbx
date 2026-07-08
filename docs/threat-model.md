# Threat Model

## Protected Against

- Accidentally starting Claude Code when Clash Verge TUN mode is not active.
- Continuing to run after the startup TUN route disappears or changes.
- Starting when sandbox egress IP does not match policy, and continuing to run
  after event-triggered host route or host egress drift is detected.
- Accidentally exposing explicit proxy environment variables inside the sandbox.
- Accidentally mounting host-sensitive paths into the sandbox.
- Accidentally allowing the sandbox to read sibling project files or configured
  host-sensitive paths during preflight inspection.

## Not Protected Against

- Remote services identifying network type, proxy characteristics, device characteristics, account state, organization policy, or billing state.
- Users placing secrets inside the project workspace.
- Vulnerabilities in macOS, Clash Verge, Docker Sandbox, Docker Desktop, or Claude Code.
- All possible network changes if macOS and Clash Verge do not emit a useful
  route or app-home metadata event for a given transition.

## Compliance Rule

This project must not contain instructions, code, or workflows intended to bypass platform terms, account limits, payment requirements, organization controls, or service-side risk systems.
