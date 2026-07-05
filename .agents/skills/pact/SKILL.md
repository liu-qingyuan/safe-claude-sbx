---
name: pact
description: Open-source Pact contract testing assistant for consumer-driven contracts, provider verification, self-hosted Pact Broker, can-i-deploy, pacticipant/version setup, publishing pacts, provider states, message pacts, and CI gates. Use when Codex needs to add, review, debug, or explain Pact tests or Pact Broker workflows.
---

# pact

Use this skill for **open-source Pact + self-hosted Pact Broker**. Default to the free path: consumer tests generate Pact JSON, provider verification checks the real provider, Pact Broker stores compatibility, and `can-i-deploy` gates release safety.

## Default rule

- Contract definition stays in the project’s existing format: OpenAPI/AsyncAPI YAML, JSON Schema, schema YAML, typed contract, custom JSON/YAML, or existing contract files.
- Pact is a **contract testing tool**, not the default contract definition format.
- Do not handwrite Pact JSON/YAML by default; generate Pact files from consumer tests.
- Do not replace existing contract files with Pact unless the user explicitly asks.

## Short workflow

1. **Inspect contract boundary**: identify consumer, provider, API/event/tool/provider boundary, existing contract files, and CI commands.
2. **Write consumer test**: use Pact mock provider/message pact to capture only behavior the consumer actually uses.
3. **Publish pact**: publish generated Pact JSON to self-hosted Pact Broker with consumer version and branch.
4. **Verify provider**: run provider verification against real provider implementation, with provider states and published verification result.
5. **Gate release**: use `pact-broker can-i-deploy` before deploy; record deployment/release when applicable.

```text
consumer test -> generated pact JSON -> self-hosted Pact Broker -> provider verification -> can-i-deploy
```

## What to read

- Concepts and fit: `references/pact-concepts.md`, `references/pact-faq.md`
- Consumer tests: `references/pact-consumer.md`
- Provider verification: `references/pact-provider.md`
- Message/event pacts: `references/pact-messages.md`
- Broker setup and CLI: `references/pact-broker-setup.md`, `references/pact-broker-advanced.md`
- CI/CD: `references/pact-cicd.md`
- Language examples: `references/pact-implementations.md` and `references/dsl.*.md`
- Recipes/plugins: `references/pact-recipes.md`, `references/pact-plugins.md`

Read only the needed references; do not load every file by default.

## Pact Broker defaults

- Use a self-hosted Pact Broker by default.
- Use basic auth or the project’s existing broker auth; keep secrets in CI secrets/env vars.
- Publish verification results only in CI unless the project already does otherwise.
- Use branch-aware selectors where supported: main branch, matching branch, and deployed/released versions.
- Use `can-i-deploy` as a compatibility gate, not as a replacement for tests.

## Provider verification must include

- Provider base URL or provider app bootstrap.
- Provider states for data preconditions.
- Consumer version selectors.
- Provider version and branch.
- Published verification results in CI.

## Do not

- Do not mock the real provider during provider verification.
- Do not test provider internals; verify the public contract.
- Do not use Pact to cover every API field “just in case”; test consumer-used behavior.
- Do not make E2E tests replace Pact verification.
