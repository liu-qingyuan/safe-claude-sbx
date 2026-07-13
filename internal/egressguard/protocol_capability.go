//go:build !safe_claude_sbx_e2e

package egressguard

func dockerSandboxProtocolComplete(string) bool {
	return false
}
