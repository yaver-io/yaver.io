package main

// Yaver transports a native PTY; it does not broker a coding runner's OAuth.
// Keep the policy function while older clients roll forward, but deliberately
// make it impossible for a server-side flag to re-enable credential capture,
// callback replay, or cross-device runner credential transfer.
func remoteRunnerOAuthEnabled() bool {
	return false
}

func applyProductPolicy(_ PlatformProductPolicy) {
}
