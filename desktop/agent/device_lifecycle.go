package main

// AgentLifecycleState is the canonical device lifecycle that the agent exposes
// to web/mobile clients. Clients still own the final "connected" state because
// that reflects an active client session, not just box-level reachability.
type AgentLifecycleState string

// AgentLifecycleRecoveryMode is the typed string for the recoveryMode field
// on AgentLifecycleInfo. Tightening from a raw string prevents typos at the
// (few) callsites that build lifecycle info — the compiler now catches a
// stray "boostrap-reclaim" the way it always caught stray State values.
type AgentLifecycleRecoveryMode string

const (
	AgentLifecycleBootstrap      AgentLifecycleState = "bootstrap"
	AgentLifecycleAuthExpired    AgentLifecycleState = "yaver-auth-expired"
	AgentLifecycleReadyToConnect AgentLifecycleState = "ready-to-connect"

	AgentLifecycleFreshBootstrap   AgentLifecycleRecoveryMode = "first-pair"
	AgentLifecycleBootstrapRecover AgentLifecycleRecoveryMode = "bootstrap-reclaim"
	AgentLifecycleReauthRecover    AgentLifecycleRecoveryMode = "reauth"
	AgentLifecycleNoRecovery       AgentLifecycleRecoveryMode = "none"
)

type AgentLifecycleInfo struct {
	State              AgentLifecycleState        `json:"state"`
	Usable             bool                       `json:"usable"`
	Recoverable        bool                       `json:"recoverable"`
	RecoveryMode       AgentLifecycleRecoveryMode `json:"recoveryMode,omitempty"`
	SupportsOwnerClaim bool                       `json:"supportsOwnerClaim,omitempty"`
	OwnerClaimReady    bool                       `json:"ownerClaimReady,omitempty"`
	RequiresFirstPair  bool                       `json:"requiresFirstPair,omitempty"`
}

func bootstrapLifecycleInfo(cfg *Config) AgentLifecycleInfo {
	hasIdentity := cfg != nil && cfg.DeviceID != "" && cfg.ConvexSiteURL != ""
	ownerClaimReady := hasIdentity && activePairingSnapshot() != nil
	mode := AgentLifecycleFreshBootstrap
	if hasIdentity {
		mode = AgentLifecycleBootstrapRecover
	}
	return AgentLifecycleInfo{
		State:              AgentLifecycleBootstrap,
		Usable:             false,
		Recoverable:        true,
		RecoveryMode:       mode,
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
