# Backend Extension

The MVP supports only Docker Sandbox / `sbx`.

Future backends should implement the same behavioral contract:

- Check availability.
- Probe egress and environment.
- Run the agent.
- Stop the runtime.
- Remove temporary probe resources.
- Execute a command inside the runtime when needed for validation.

Adding a backend does not imply it has the same isolation properties as Docker Sandbox. Each backend needs its own documented security boundary.
