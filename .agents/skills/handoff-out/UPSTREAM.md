# Upstream link

This skill started from Matt Pocock's `handoff` skill and is now the local `handoff-out` customized variant for `skills-sync-lqy`.

- Original repository: https://github.com/mattpocock/skills
- Original upstream skill path: `skills/productivity/handoff`
- Original upstream file: https://github.com/mattpocock/skills/blob/main/skills/productivity/handoff/SKILL.md
- Local customized install path: `skills/handoff-out`
- Customized on: 2026-06-04

## Local behavior change

The original skill wrote a handoff document to the OS temporary directory. This customized version does **not** write files. It outputs a copy-paste prompt directly in chat so the user can send it to another AI or fresh session.

The local prompt contract is also stricter: it favors task-type work contracts with objective, boundaries, acceptance criteria, validation evidence, and stop conditions instead of long narrative recaps or rigid implementation scripts.

The upstream `handoff` name is reserved for a future upstream-aligned copy if this repository chooses to mirror it. Other Matt Pocock skills remain documented as external links in `docs/external-skill-links/mattpocock-skills.md` and should not be mirrored unless explicitly selected.
