# Configuration

The CLI reads a structured YAML file with separate objects for network policy,
sandbox behavior, workspace mount policy, runtime environment, watchdog logging,
and cleanup behavior. See `config.example.yaml` for a complete example.

Run the configuration doctor before starting a sandbox:

```bash
safe-claude-sbx doctor --config config.yaml
```

## Object Model

- `network.clash_verge`
  - `route_check_target`: IP used by `route get` to inspect the outbound route.
  - `tun_interface_prefix`: Expected macOS TUN interface prefix, normally `utun`.
- `network.egress_ip`
  - `expected_ip`: Public IP that both host and sandbox must observe.
  - `host_check_url`: Endpoint the host uses to read its public IP.
  - `sandbox_check_url`: Endpoint the sandbox uses to read its public IP.
  - `timeout_seconds`: Timeout for egress IP checks.
- `sandbox`
  - `backend`: Runtime backend. MVP value: `docker-sandbox`.
  - `main_name`: Main Docker Sandbox name.
  - `probe_name`: Temporary probe sandbox name.
  - `agent`: Agent command to run, normally `claude`.
- `workspace`
  - `mount`: Host project directory mounted into the sandbox.
  - `use_clone_mode`: Whether a copied workspace mode is requested.
  - `forbidden_paths`: Host paths that must never be used as workspace mounts.
- `environment`
  - `timezone`: Sandbox timezone.
  - `locale`: Sandbox `LANG` and `LC_ALL`.
  - `forbidden_env_vars`: Environment variables that must not appear inside the sandbox.
- `watchdog`
  - `enabled`: Whether runtime supervision is enabled.
  - `log_level`: Launcher log level.
  - `log_file`: Optional log file path. Empty string means stderr/stdout only.
- `cleanup`
  - `stop_main_sandbox`: Stop the main sandbox on normal shutdown or watchdog failure.
  - `remove_probe_sandbox`: Remove temporary probe sandboxes after preflight.
  - `remove_main_sandbox`: Remove the main sandbox on exit. The default is `false`.

## Validation

`doctor --config` fails closed when required objects or fields are missing. Error
messages include object paths such as `network.clash_verge` or
`network.egress_ip.expected_ip`.

Legacy flat fields such as `expected_egress_ip`, `sandbox_name`,
`workspace_mount`, `timezone`, and `cleanup.stop_on_exit` are not accepted by the
MVP CLI. The doctor reports a migration message with the new object path, for
example `expected_egress_ip -> network.egress_ip.expected_ip`.
