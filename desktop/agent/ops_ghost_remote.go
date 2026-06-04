package main

// ops_ghost_remote.go — generic remote-view verbs over the abstract RemoteView
// layer (remoteview.go). Provider defaults to "rustdesk"; "anydesk"/"vnc" also
// register. The customer installs ONLY the remote-view tool on their Logo PC;
// this agent connects a client and the ghost_* verbs operate the window.
//
// Heavy management is in Yaver (remoteview.go); these verbs are thin dispatch.

import (
	"encoding/json"
)

func ghostEnabledGate(c OpsContext) *OpsResult {
	if c.Server == nil {
		return &OpsResult{OK: false, Code: "unavailable", Error: "no server context"}
	}
	if !c.Server.ghostEnabled {
		return &OpsResult{OK: false, Code: "unauthorized", Error: "GUI ghost is disabled on this agent; start it with `yaver serve --ghost`"}
	}
	return nil
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_remote_providers",
		Description: "List remote-view providers (rustdesk/anydesk/vnc) and whether each is installed. Requires --ghost.",
		Schema:      ghostJSONSchema(map[string]interface{}{}),
		Handler:     ghostRemoteProvidersHandler,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_remote_connect",
		Description: "Connect a remote-view client to the customer's PC so the ghost can drive it (blackbox model — only the remote-view tool is installed there). provider defaults to rustdesk; peerId = its remote ID; password = its unattended password. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"provider": map[string]interface{}{"type": "string", "enum": []string{"rustdesk", "anydesk", "vnc"}},
			"peerId":   map[string]interface{}{"type": "string"},
			"password": map[string]interface{}{"type": "string"},
		}, "peerId"),
		Handler:    ghostRemoteConnectHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_remote_disconnect",
		Description: "Disconnect the remote-view client (provider defaults to rustdesk). Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"provider": map[string]interface{}{"type": "string"},
		}),
		Handler:    ghostRemoteDisconnectHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "ghost_remote_status",
		Description: "Remote-view client status (installed, connected, peer, pid). provider defaults to rustdesk. Requires --ghost.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"provider": map[string]interface{}{"type": "string"},
		}),
		Handler:    ghostRemoteStatusHandler,
		AllowGuest: false,
	})
}

func ghostRemoteProvidersHandler(c OpsContext, _ json.RawMessage) OpsResult {
	if deny := ghostEnabledGate(c); deny != nil {
		return *deny
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"providers": listRemoteViews()}}
}

func ghostRemoteConnectHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if deny := ghostEnabledGate(c); deny != nil {
		return *deny
	}
	var p struct {
		Provider string            `json:"provider"`
		PeerID   string            `json:"peerId"`
		Password string            `json:"password"`
		Opts     map[string]string `json:"opts"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if p.PeerID == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "peerId is required"}
	}
	rv, ok := getRemoteView(p.Provider)
	if !ok {
		return OpsResult{OK: false, Code: "unknown_provider", Error: "unknown remote-view provider: " + p.Provider}
	}
	if err := rv.Connect(p.PeerID, p.Password, p.Opts); err != nil {
		return OpsResult{OK: false, Code: "remote_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: rv.Status()}
}

func ghostRemoteDisconnectHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if deny := ghostEnabledGate(c); deny != nil {
		return *deny
	}
	var p struct {
		Provider string `json:"provider"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	rv, ok := getRemoteView(p.Provider)
	if !ok {
		return OpsResult{OK: false, Code: "unknown_provider", Error: "unknown remote-view provider: " + p.Provider}
	}
	_ = rv.Disconnect()
	return OpsResult{OK: true, Initial: rv.Status()}
}

func ghostRemoteStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if deny := ghostEnabledGate(c); deny != nil {
		return *deny
	}
	var p struct {
		Provider string `json:"provider"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	rv, ok := getRemoteView(p.Provider)
	if !ok {
		return OpsResult{OK: false, Code: "unknown_provider", Error: "unknown remote-view provider: " + p.Provider}
	}
	return OpsResult{OK: true, Initial: rv.Status()}
}
