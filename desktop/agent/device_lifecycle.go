package main

// AgentLifecycleState is the canonical device lifecycle that the agent exposes
// to web/mobile clients. Clients still own the final "connected" state because
// that reflects an active client session, not just box-level reachability.
type AgentLifecycleState string

const (
	AgentLifecycleBootstrap        AgentLifecycleState = "bootstrap"
	AgentLifecycleAuthExpired      AgentLifecycleState = "yaver-auth-expired"
	AgentLifecycleReadyToConnect   AgentLifecycleState = "ready-to-connect"
	AgentLifecycleFreshBootstrap   string              = "first-pair"
	AgentLifecycleBootstrapRecover string              = "bootstrap-reclaim"
	AgentLifecycleReauthRecover    string              = "reauth"
	AgentLifecycleNoRecovery       string              = "none"
)

type AgentLifecycleInfo struct {
	State              AgentLifecycleState `json:"state"`
	Usable             bool                `json:"usable"`
	Recoverable        bool                `json:"recoverable"`
	RecoveryMode       string              `json:"recoveryMode,omitempty"`
	SupportsOwnerClaim bool                `json:"supportsOwnerClaim,omitempty"`
	OwnerClaimReady    bool                `json:"ownerClaimReady,omitempty"`
	RequiresFirstPair  bool                `json:"requiresFirstPair,omitempty"`
}

func bootstrapLifecycleInfo(cfg *Config) AgentLifecycleInfo {
	hasIdentity := cfg != nil && cfg.DeviceID != "" && cfg.ConvexSiteURL != ""
	ownerClaimReady := hasIdentity && activePairingSnapshot() != nil
	return AgentLifecycleInfo{
		State:              AgentLifecycleBootstrap,
		Usable:             false,
		Recoverable:        true,
		RecoveryMode:       map[bool]string{true: AgentLifecycleBootstrapRecover, false: AgentLifecycleFreshBootstrap}[hasIdentity],
		SupportsOwnerClaim: hasIdentity,
		OwnerClaimReady:    ownerClaimReady,
		RequiresFirstPair:  !hasIdentity,
	}
}

func (s *HTTPServer) lifecycleInfo() AgentLifecycleInfo {
	if s != nil && s.authExpired.Load() {
		return AgentLifecycleInfo{
			State:        AgentLifecycleAuthExpired,
			Usable:       false,
			Recoverable:  true,
			RecoveryMode: AgentLifecycleReauthRecover,
		}
	}
	return AgentLifecycleInfo{
		State:        AgentLifecycleReadyToConnect,
		Usable:       true,
		Recoverable:  false,
		RecoveryMode: AgentLifecycleNoRecovery,
	}
}
