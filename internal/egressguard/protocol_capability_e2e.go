//go:build safe_claude_sbx_e2e

package egressguard

// dockerSandboxProtocolComplete enables the supported-path CLI fixture only in
// explicitly tagged test binaries. Production builds keep an empty matrix.
func dockerSandboxProtocolComplete(version string) bool {
	return version == "v0.34.0"
}
