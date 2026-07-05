# CLI vs MCP

## Decision table

| Need | Prefer |
| --- | --- |
| Install/setup/config visibility | CLI |
| Index freshness, registry, WAL/database issues | CLI |
| Reproducible transcript in a report | CLI |
| Web UI server | CLI (`gitnexus serve`) |
| Agent-native repo context in Codex | MCP |
| Context/impact/cypher during planning | MCP when exposed, CLI otherwise |
| Final correctness | Direct source + tests |

## Local experiment findings

During the cpilot-web-lucyna Lucyna/OpenClaw media-display investigation:

- CLI `status` and `list` were best for index health.
- CLI `context`, `impact`, and `cypher` gave useful symbol/call-chain evidence.
- CLI `query` returned empty results and emitted read-only DB FTS warnings in that environment.
- MCP `list_repos`, `context`, `impact`, and `cypher` were useful once symbols/files were known.
- Broad natural-language query was weak in both CLI and MCP for that fresh bug chain.
- Source reads and tests remained necessary to prove the root cause.

## Recommended sequence

1. CLI `gitnexus status` / `gitnexus list`.
2. MCP repo context if available.
3. `context` / `impact` / `cypher` for known symbols.
4. Direct source confirmation with file/line references.
5. Tests or targeted runtime checks before claiming completion.
