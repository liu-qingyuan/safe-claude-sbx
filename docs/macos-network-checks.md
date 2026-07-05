# macOS Network Checks

The MVP is macOS-specific.

Planned checks:

- Use `route get <route_check_target>` to identify the interface used for default external traffic.
- Require the interface to match `utunX`.
- Record the startup TUN interface and require runtime route checks to keep using the same interface.
- Use `ifconfig <utunX>` to verify the startup TUN interface still exists.
- Use `route -n monitor` as the primary runtime event source.

Known limitation: not every observable network condition is guaranteed to emit a route event in the same way across macOS, Clash Verge, and Docker Sandbox versions. Manual testing must document observed behavior.

## Clash Verge Rev TUN State Sources

This research is based on Clash Verge Rev source commit `3da6d7e466c37753f0dbe55bbbaf291692750374` and MetaCubeX/mihomo dashboard source commit `1aaff032d4c0928d1a55db434304cdf5f77890e9`.

Primary source anchors:

- Clash Verge Rev app id, app-home, portable-mode, and config file names: https://github.com/clash-verge-rev/clash-verge-rev/blob/3da6d7e466c37753f0dbe55bbbaf291692750374/src-tauri/src/utils/dirs.rs#L10-L138
- Clash Verge Rev `enable_tun_mode` schema field: https://github.com/clash-verge-rev/clash-verge-rev/blob/3da6d7e466c37753f0dbe55bbbaf291692750374/src-tauri/src/config/verge.rs#L82-L86
- Clash Verge Rev generated runtime config path and write: https://github.com/clash-verge-rev/clash-verge-rev/blob/3da6d7e466c37753f0dbe55bbbaf291692750374/src-tauri/src/config/config.rs#L183-L200
- Clash Verge Rev TUN merge function: https://github.com/clash-verge-rev/clash-verge-rev/blob/3da6d7e466c37753f0dbe55bbbaf291692750374/src-tauri/src/enhance/tun.rs#L24-L84
- MetaCubeX/mihomo dashboard TUN builder showing common fields: https://github.com/MetaCubeX/metacubexd/blob/1aaff032d4c0928d1a55db434304cdf5f77890e9/packages/agent/src/tun.ts#L1-L24

Clash Verge Rev computes its app home with Tauri's data directory joined with the app id `io.github.clash-verge-rev.clash-verge-rev`. On a normal macOS install, the expected local directory is:

```text
~/Library/Application Support/io.github.clash-verge-rev.clash-verge-rev/
```

Portable mode is different: Clash Verge Rev uses `.config/io.github.clash-verge-rev.clash-verge-rev` beside the app executable when its portable flag exists. The detector should therefore support an explicit configured app-home override and use the normal macOS path only as the default.

Relevant files under the app home:

- `verge.yaml`: app settings. `enable_tun_mode` is the Clash Verge Rev TUN toggle declaration.
- `config.yaml`: Clash Verge Rev's base Clash/mihomo config template. It contains a `tun` section, but its default may be `enable: false` even when the runtime state later changes.
- `clash-verge.yaml`: generated runtime config written by Clash Verge Rev for the running mihomo core. This is the useful file for `tun.enable`, `tun.device`, `tun.auto-route`, `tun.auto-detect-interface`, and `tun.strict-route` when it exists.
- `profiles.yaml` and `profiles/`: profile metadata and profile YAML files that can contribute to the final runtime config.

The safe-claude-sbx detector must not treat `verge.yaml` as sufficient proof that traffic is routed through TUN. In Clash Verge Rev, `enable_tun_mode` is consumed while generating the runtime config, and `use_tun` writes the final `tun.enable` value into the generated mapping. The TUN toggle is therefore a declaration, not a system fact.

The detector should treat `clash-verge.yaml` as an optional second-level declaration. When present, inspect:

```yaml
tun:
  enable: true
  device: "utun9"
  auto-route: true
  auto-detect-interface: true
  strict-route: true
```

`device` may be missing if mihomo chooses the device automatically. `strict-route` may be false in Clash Verge Rev's base template, while other mihomo frontends may default it to true. For this MVP, absence or mismatch in `clash-verge.yaml` should be diagnostic context, not the final authority.

## Detection Priority Contract

Future implementation should evaluate TUN readiness in layers and record each layer separately:

1. `verge.yaml` declares `enable_tun_mode: true`.
2. Generated runtime config, if available, declares `tun.enable: true` and captures `device`, `auto-route`, `auto-detect-interface`, and `strict-route`.
3. macOS has a live `utunX` interface, preferably matching `tun.device` when a device is declared.
4. `route get <route_check_target>` resolves through the startup `utunX` interface.
5. Host egress IP and sandbox egress IP match the configured expected value.

Fail closed when the system-fact layers fail. Use the configuration-declaration layers to produce better diagnostics and to distinguish "Clash Verge says TUN is disabled" from "Clash Verge says TUN is enabled, but macOS is not routing through it."

The redacted fixtures in `tests/fixtures/clash-verge-tun-enabled.yaml`, `tests/fixtures/clash-verge-tun-disabled.yaml`, and `tests/fixtures/clash-verge-tun-missing.yaml` define the three states later parser tests should cover.
