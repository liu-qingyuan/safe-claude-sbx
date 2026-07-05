# Docker Sandbox Backend

The first backend is Docker Sandbox / `sbx`.

Planned behavior:

- Check that `sbx` exists and can run.
- Check that the user is authenticated or otherwise able to list and run sandboxes.
- Use a temporary probe sandbox to validate sandbox egress IP and forbidden environment variables.
- Start Claude Code through the `claude` agent backend.
- Stop the named sandbox on watchdog failure or Ctrl+C.
- Remove the probe sandbox after preflight.

The exact command contract must be verified against the installed `sbx` version before implementation.
