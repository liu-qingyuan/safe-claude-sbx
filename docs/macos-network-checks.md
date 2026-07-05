# macOS Network Checks

The MVP is macOS-specific.

Planned checks:

- Use `route get <route_check_target>` to identify the interface used for default external traffic.
- Require the interface to match `utunX`.
- Record the startup TUN interface and require runtime route checks to keep using the same interface.
- Use `ifconfig <utunX>` to verify the startup TUN interface still exists.
- Use `route -n monitor` as the primary runtime event source.

Known limitation: not every observable network condition is guaranteed to emit a route event in the same way across macOS, Clash Verge, and Docker Sandbox versions. Manual testing must document observed behavior.
