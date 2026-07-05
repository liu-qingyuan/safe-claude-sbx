# Sandbox Notes

The MVP should prefer Docker's official Claude Code sandbox template instead of maintaining a custom image.

Do not store credentials, tokens, OAuth sessions, SSH keys, Claude configuration, Clash configuration, or Keychain material in this directory.

Future custom templates must use a non-root user, fixed locale/timezone settings, and must not inject explicit proxy environment variables.
