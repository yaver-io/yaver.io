package main

// ops_cert.go — verb "cert": TLS cert status + renewal. Today we
// report status via ssl_check / domain_ssl_status and hand back a
// renew hint pointing at the domain_setup flow. Automated renewals
// via ACME live in nginx+certbot on the user's relay/web server and
// aren't directly MCP-invocable yet; this verb is a checkpoint so
// agents can monitor expiry without learning three domain tools.

import "encoding/json"

type opsCertPayload struct {
	Op     string `json:"op"`               // status | renew
	Domain string `json:"domain"`
	Port   int    `json:"port,omitempty"`   // defaults 443
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "cert",
		Description: "TLS certificate status + renewal. op=status returns expiry/chain info via ssl_check; op=renew returns the exact command to run on the host (certbot/acme.sh) since renewal is environment-specific.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"op", "domain"},
			"properties": map[string]interface{}{
				"op":     map[string]interface{}{"type": "string", "enum": []string{"status", "renew"}},
				"domain": map[string]interface{}{"type": "string"},
				"port":   map[string]interface{}{"type": "integer", "default": 443},
			},
			"additionalProperties": false,
		},
		Handler:    opsCertHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsCertHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p opsCertPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Domain == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "domain is required"}
	}
	port := p.Port
	if port == 0 {
		port = 443
	}
	switch p.Op {
	case "status":
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"hint":    "call the ssl_check MCP tool with this domain to get full cert details + days-until-expiry",
			"mcpTool": "ssl_check",
			"args":    map[string]interface{}{"domain": p.Domain, "port": port},
		}}
	case "renew":
		// Renewal is environment-specific. Return the commands for
		// both certbot + acme.sh so the agent can pick whichever
		// tool the host actually has.
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"domain": p.Domain,
			"commands": []string{
				"sudo certbot renew --cert-name " + p.Domain,
				"acme.sh --renew -d " + p.Domain,
			},
			"hint": "automated renewal is delegated to the host's ACME client. certbot is the default on Yaver-managed relay deployments; acme.sh lives on self-hosted machines.",
		}}
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "unknown op: " + p.Op}
	}
}
