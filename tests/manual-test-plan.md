# Manual Test Plan

These tests require a macOS host, Clash Verge TUN mode, Docker Sandbox / `sbx`, and a known expected egress IP.

## 1. TUN closed at startup

- Turn off Clash Verge TUN mode.
- Run the launcher.
- Expected: startup is rejected because `route get 1.1.1.1` does not resolve to `utunX`.
- Expected: no main sandbox is created.

## 2. TUN enabled but host IP mismatch

- Enable TUN.
- Configure an intentionally wrong `expected_egress_ip`.
- Run the launcher.
- Expected: startup is rejected with expected and actual host IP shown.

## 3. TUN enabled and IP matches

- Enable TUN.
- Configure the correct `expected_egress_ip`.
- Run the launcher.
- Expected: preflight succeeds and Docker Sandbox starts Claude Code.

## 4. TUN closed while running

- Start the sandbox successfully.
- Turn off TUN.
- Expected: route watcher observes a route change and stops the sandbox.

## 5. Exit IP changes while running

- Start the sandbox successfully.
- Switch Clash Verge node so the public egress IP changes.
- Expected: route-triggered validation detects sandbox IP mismatch and stops the sandbox.

## 6. Proxy env var appears inside sandbox

- Force a probe sandbox to contain `HTTP_PROXY` or another forbidden proxy variable.
- Expected: preflight rejects startup.

## 7. `sbx` not installed

- Remove `sbx` from `PATH` for the test shell.
- Run the launcher.
- Expected: startup is rejected with a clear installation or availability message.

## 8. Docker Sandbox backend unavailable

- Make `sbx ls` or `sbx run` fail.
- Run the launcher.
- Expected: startup is rejected with a clear backend diagnostic.

## 9. Ctrl+C cleanup

- Start the sandbox successfully.
- Press Ctrl+C.
- Expected: route watcher stops, sandbox is stopped, probe sandbox is removed, and the process exits cleanly.

## 10. Sensitive mount path

- Set `workspace_mount` to `~`, `~/.ssh`, `~/.claude`, a Clash config directory, or a Keychain path.
- Run the launcher.
- Expected: preflight rejects startup before creating a sandbox.
