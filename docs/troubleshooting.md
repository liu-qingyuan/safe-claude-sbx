# Troubleshooting

## Route does not use `utunX`

Clash Verge TUN mode may be off, another VPN may own the route, or macOS may have selected another interface.

## Host IP mismatch

The configured `expected_egress_ip` does not match the IP returned by the configured IP check URL.

## Sandbox IP mismatch

The host and sandbox may not be using the same network path, or Docker Sandbox egress may differ from the host route.

## Forbidden proxy variable detected

The sandbox environment contains a proxy variable such as `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`, or `NO_PROXY`. The launcher should reject startup.

## Sensitive mount rejected

The configured workspace path resolves to Home, SSH, Claude config, Clash config, Keychain, or another forbidden path.
