# Sandbox Notes

The default sandbox-local Herdr flow uses `claude-herdr-template`, a Docker
Sandbox custom template based on Docker's Claude Code sandbox template.

Do not store credentials, tokens, OAuth sessions, SSH keys, Claude configuration, Clash configuration, or Keychain material in this directory.

Custom templates must use a non-root runtime user, fixed locale/timezone
settings when needed, and must not inject explicit proxy environment variables.
