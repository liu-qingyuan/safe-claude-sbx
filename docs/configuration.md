# Configuration

The future CLI will read `config.yaml`. `config.example.yaml` documents the initial shape.

## Required policy fields

- `expected_egress_ip`: Public IP that both host and sandbox must observe.
- `route_check_target`: IP used by `route get` to inspect the outbound route.
- `ip_check_url`: Endpoint that returns the caller public IP as plain text.
- `sandbox_name`: Main Docker Sandbox name.
- `backend`: Runtime backend. MVP value: `docker-sandbox`.
- `workspace_mount`: Host project directory mounted into the sandbox.

## Runtime consistency fields

- `timezone`: Sandbox timezone.
- `locale`: Sandbox `LANG` and `LC_ALL`.
- `require_tun_interface_prefix`: Expected macOS TUN interface prefix, normally `utun`.

## Safety fields

- `forbidden_env_vars`: Environment variables that must not appear inside the sandbox.
- `forbidden_mount_paths`: Host paths that must never be used as workspace mounts.
- `cleanup`: Stop/remove behavior for main and probe sandboxes.
