# Configuration

The CLI reads a structured YAML file with separate objects for network policy,
sandbox behavior, workspace mount policy, runtime environment, watchdog logging,
and cleanup behavior. See `config.example.yaml` for a complete example.

Run the doctor before starting a sandbox. It validates the configuration and
checks that the host public egress IP matches `network.egress_ip.expected_ip`:

```bash
safe-claude-sbx doctor --config config.yaml
```

## Object Model

- `network.clash_verge`
  - `app_home`: Optional Clash Verge Rev app-home override. Empty string uses the normal macOS app-home path.
  - `route_check_target`: IP used by `route get` to inspect the outbound route.
  - `tun_interface_prefix`: Expected macOS TUN interface prefix, normally `utun`.
- `network.egress_ip`
  - `expected_ip`: Public IP that both host and sandbox must observe.
  - `host_check_url`: Endpoint the host uses to read its public IP.
  - `sandbox_check_url`: Endpoint the sandbox uses to read its public IP.
  - `timeout_seconds`: Timeout for backend commands, sandbox probe commands,
    cleanup attempts, and egress IP checks. Use at least `30` for real Docker
    Sandbox probes because first-run image and daemon startup can be slow.
- `sandbox`
  - `backend`: Runtime backend. MVP value: `docker-sandbox`.
  - `main_name`: Main Docker Sandbox name.
  - `probe_name`: Temporary probe sandbox name.
  - `agent`: Agent command to run, normally `claude`.
- `workspace`
  - `mount`: Host project directory mounted into the sandbox.
  - `use_clone_mode`: Whether a copied workspace mode is requested.
  - `forbidden_paths`: Host paths that must never be used as workspace mounts.
    The policy expands `~`, rejects sensitive paths such as SSH, Claude config,
    Clash config, and Keychain paths recursively, and fails before backend
    commands run.
- `environment`
  - `timezone`: Sandbox timezone.
  - `locale`: Sandbox `LANG` and `LC_ALL`.
  - `forbidden_env_vars`: Host environment variables that must not appear
    inside the sandbox, such as `OPENAI_API_KEY`, Claude API keys, and
    `SSH_AUTH_SOCK`. Docker-managed proxy values on
    `gateway.docker.internal:3128` are allowed; host or unknown proxy targets
    fail closed.
- `watchdog`
  - `enabled`: Whether runtime supervision is enabled.
  - `log_level`: Launcher log level.
  - `log_file`: Optional log file path. Empty string means stderr/stdout only.
- `cleanup`
  - `stop_main_sandbox`: Stop the main sandbox on normal shutdown or watchdog failure.
  - `remove_probe_sandbox`: Remove temporary probe sandboxes after preflight.
  - `remove_main_sandbox`: Remove the main sandbox on exit. The default is `false`.

## Validation

`doctor --config` fails closed when required objects or fields are missing, when
`workspace.mount` resolves to a forbidden mount, when
`network.egress_ip.expected_ip` is not an IP address, or when the host egress
check cannot prove that the observed public IP matches the configured expected
IP. Error messages include object paths such as `network.clash_verge`,
`workspace.mount`, or `network.egress_ip.expected_ip`, and host egress failures
distinguish `host-egress-mismatch`, `endpoint-failure`, and
`response-parse-failure`.

After the Docker Sandbox probe runs, `doctor` validates the sandbox observation
before printing `sandbox inspection ok`. It rejects visible sensitive mounts,
host secrets such as `SSH_AUTH_SOCK`, token or credential-like env vars such as
`OPENAI_API_KEY`, host proxy values such as `127.0.0.1:7897`, and unknown proxy
targets. These errors name the policy object and env variable but do not print
captured secret values.

Legacy flat fields such as `expected_egress_ip`, `sandbox_name`,
`workspace_mount`, `timezone`, and `cleanup.stop_on_exit` are not accepted by the
MVP CLI. The doctor reports a migration message with the new object path, for
example `expected_egress_ip -> network.egress_ip.expected_ip`.
