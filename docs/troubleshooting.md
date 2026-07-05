# Troubleshooting

## Route does not use `utunX`

Clash Verge TUN mode may be off, another VPN may own the route, or macOS may have selected another interface.

## Host IP mismatch

The configured `network.egress_ip.expected_ip` does not match the IP returned by `network.egress_ip.host_check_url`. Confirm the endpoint returns a plain IP address and that the host is using the expected network path.

## Sandbox IP mismatch

The host and sandbox may not be using the same network path, or Docker Sandbox egress may differ from the host route.

## Proxy environment rejected

Docker Sandbox may inject proxy variables that point to
`gateway.docker.internal:3128`; those Docker-managed values are allowed. The
launcher rejects host proxy values such as `127.0.0.1:7897`, `localhost`, or
unknown proxy targets. The error names the proxy variable but does not print the
captured value.

## Forbidden environment detected

The sandbox environment contains a host-sensitive variable such as
`SSH_AUTH_SOCK`, a token, a password, a credential, a Claude config path, a
Clash config path, or a Keychain-related variable. The launcher rejects startup
and redacts the captured value.

## Sensitive mount rejected

The configured workspace path resolves to Home, SSH, Claude config, Clash
config, Keychain, or another forbidden path. The sandbox inspection also fails
closed if the mount observation shows one of those sensitive host paths.
