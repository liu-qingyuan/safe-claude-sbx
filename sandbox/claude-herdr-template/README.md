# Claude Herdr Template

This Docker Sandbox custom template preinstalls sandbox-local Herdr and
`/usr/local/bin/cc` for the `sandbox-local-herdr` supervision mode.

Build and use a local template image:

```bash
docker build -t safe-claude-sbx-herdr:latest sandbox/claude-herdr-template
docker image save safe-claude-sbx-herdr:latest -o safe-claude-sbx-herdr.tar
sbx template load safe-claude-sbx-herdr.tar
```

Configure:

```yaml
sandbox:
  template: "safe-claude-sbx-herdr:latest"
  supervision:
    mode: "sandbox-local-herdr"
    herdr:
      install_if_missing: false
      socket_path: "/home/agent/.config/herdr/herdr.sock"
      pane_id: "sandbox-claude"
```

The template must not include host Claude tokens, OAuth state, cookies, API
keys, SSH keys, Docker credentials, Herdr host sockets, host `HERDR_*` values,
or proxy environment variables.
