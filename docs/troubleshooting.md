# Troubleshooting

## Route does not use `utunX`

Clash Verge TUN mode may be off, another VPN may own the route, or macOS may have selected another interface.

## Host IP mismatch

The configured `network.egress_ip.expected_ip` does not match the IP returned by `network.egress_ip.host_check_url`. Confirm the endpoint returns a plain IP address and that the host is using the expected network path.

## Sandbox IP mismatch

The host and sandbox may not be using the same network path, or Docker Sandbox egress may differ from the host route.

## Forbidden proxy variable detected

The sandbox environment contains a proxy variable such as `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`, or `NO_PROXY`. The launcher should reject startup.

## Sensitive mount rejected

The configured workspace path resolves to Home, SSH, Claude config, Clash config, Keychain, or another forbidden path.
