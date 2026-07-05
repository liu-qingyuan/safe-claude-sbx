# Agent Instructions

## Agent skills

### Issue tracker

Issues are tracked in GitHub Issues for this repository. External PRs are not treated as the default request intake surface. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the default triage label vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, and `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

This is a single-context repository with `CONTEXT.md` at the root and ADRs under `docs/adr/`. See `docs/agents/domain.md`.

## Project constraints

- Keep this project focused on compliant network consistency checks, privacy isolation, mount safety, and operational guardrails.
- Do not add instructions for bypassing platform rules, account controls, payment controls, or service-side risk systems.
- Prefer explicit contracts around backend, route inspection, egress validation, mount policy, and cleanup behavior.
