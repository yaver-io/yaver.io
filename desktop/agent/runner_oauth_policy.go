package main

import (
	"sync/atomic"
)

var remoteRunnerOAuthPolicy atomic.Bool

// Remote runner OAuth is an optional compatibility feature controlled by
// Convex platformConfig. It defaults off until a verified config fetch says
// otherwise, so backend/config outages fail closed.
func remoteRunnerOAuthEnabled() bool {
	return remoteRunnerOAuthPolicy.Load()
}

func applyProductPolicy(policy PlatformProductPolicy) {
	remoteRunnerOAuthPolicy.Store(policy.RemoteRunnerOAuthEnabled)
}
