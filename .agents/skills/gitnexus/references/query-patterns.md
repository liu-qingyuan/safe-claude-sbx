# Query Patterns

## Basic CLI patterns

```bash
gitnexus status
gitnexus list
gitnexus context <symbol-or-file>
gitnexus impact <symbol-or-file>
gitnexus query "short concept"
```

## Cypher patterns

Prefer relationship property filters observed to work in local CLI:

```cypher
MATCH (a)-[r]->(b)
WHERE r.type = "CALLS"
RETURN a.name, b.name
LIMIT 50
```

Avoid assuming `:CALLS` shorthand or `type(r)` works in every GitNexus/LadybugDB adapter version; verify against `gitnexus://repo/{name}/schema` or a small query first.

## Planning/debugging prompts

Use graph queries to answer:

- What files/symbols participate in this user-facing flow?
- What calls this function and what does it call?
- What breaks if this symbol changes?
- Which process or cluster owns the path?
- Is the index stale relative to HEAD?

## Validation pattern

For each important graph claim:

1. Record GitNexus command/tool output.
2. Open the cited source file.
3. Capture file:line evidence.
4. If modifying code, run focused tests plus typecheck/lint where relevant.
