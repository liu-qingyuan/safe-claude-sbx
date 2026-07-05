---
name: simple
description: Simplify artifacts while preserving intent and required behavior. Use when the user asks to make something simpler, shorter, clearer, leaner, more efficient, less complex, or easier to understand across code, Markdown, docs, prompts, configs, flowcharts, Mermaid diagrams, architecture notes, plans, specs, or process descriptions.
---

# simple

Use `$simple` to reduce complexity without losing the user's intent. Apply it to code, Markdown, documentation, prompts, configs, flowcharts, Mermaid diagrams, plans, specs, and process descriptions.

## Default output

Keep the response short:

- `Simplified`：the simplified version, patch, or concise recommendation.
- `Kept`：what meaning/behavior/contract was preserved.
- `Removed`：what complexity was deleted or merged.
- `Validation`：tests/checks/manual review done or still needed.

Omit sections that add no value. For direct edits, summarize changed files and validation evidence.


## Progressive loading / Occam

Keep `SKILL.md` as the small interface. Do not add references, scripts, assets, templates, diagrams, tools, abstractions, or process unless repeated use proves the detail is optional, reusable, and better loaded only for that artifact type.

## Simplification rules

- Preserve observable behavior, user intent, public contracts, required data, and safety constraints.
- Delete redundancy first; merge shallow layers second; rename for clarity third; add abstraction last.
- Prefer fewer concepts, fewer branches, fewer headings, fewer nodes, fewer options, and fewer special cases.
- Replace vague or clever wording with direct, task-oriented language.
- Keep one idea per paragraph/node/function when it improves reading; do not split into tiny fragments.
- Prefer existing project style and tools over new dependencies, new frameworks, new registries, or new processes.

## Artifact-specific guidance

- Code：remove dead code, duplicate paths, needless wrappers, over-abstracted helpers, deep nesting, and speculative extension points; verify behavior with existing tests or focused smoke checks.
- Markdown/docs：lead with the answer, remove repetition, collapse duplicate headings, keep only audience-needed context, and prefer examples over long explanation when useful.
- Flowcharts/Mermaid：keep the main path visible, merge trivial nodes, remove decorative styling, split only when one diagram becomes unreadable, and validate syntax if possible.
- Prompts/skills/plans：turn long procedures into purpose, defaults, output contract, boundaries, and stop rules.
- Config/process：remove unused options, duplicate sources of truth, and manual steps that can be safely replaced by existing commands or conventions.

## Stop rules

Stop and report before changing public APIs, persisted formats, security/privacy behavior, business rules, external contracts, destructive operations, or broad architecture ownership. If simplification would hide necessary nuance, keep the nuance and explain why.

## Maintenance validation

After editing this skill, run `quick_validate.py`. A real `$simple ...` smoke test is stronger; if skipped, say so.
