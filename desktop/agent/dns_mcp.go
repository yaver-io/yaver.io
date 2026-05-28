package main

// dns_mcp.go — MCP-level CRUD for DNS records + Let's Encrypt SSL
// provisioning, on top of the existing DNSProvider abstraction
// (dns_provider.go) and the cloudflare helpers in domain.go.
//
// Until this file existed, `dns_lookup` was the only DNS-related MCP
// tool. Web-only devs who buy a domain at Namecheap + DNS at
// Cloudflare had no in-yaver path to add the A / TXT records the
// platform-init wizard tells them to add — they had to alt-tab to
// the CF dashboard. The new tools close that gap:
//
//   dns_add        — create a record (CF auto if CF_API_TOKEN, else
//                    returns paste-at-your-registrar instructions).
//   dns_remove     — delete a record by id (CF only; manual returns
//                    a "remove this record at your registrar" hint).
//   ssl_provision  — Let's Encrypt cert via the bundled certbot for
//                    self-hosted yaver boxes. ACME http-01 challenge.
//
// All three are owner-only — none of them are AllowGuest because
// touching DNS / SSL changes are a footgun if scripted by a malicious
// guest agent loop.

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

func dnsMCPTools() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name": "dns_add",
			"description": "Create a DNS record at the user's DNS provider. Uses Cloudflare API when CF_API_TOKEN is set; otherwise returns paste-at-your-registrar instructions verbatim. " +
				"Owner-only; never reachable from a guest token. Returns {ok, recordId, manual, instruction?}.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"zone", "type", "name", "content"},
				"properties": map[string]interface{}{
					"zone":     map[string]interface{}{"type": "string", "description": "Apex domain — e.g. myapp.com"},
					"type":     map[string]interface{}{"type": "string", "enum": []string{"A", "AAAA", "CNAME", "TXT", "MX"}},
					"name":     map[string]interface{}{"type": "string", "description": "Record name relative to zone (\"@\" or \"sub\")"},
					"content":  map[string]interface{}{"type": "string", "description": "IP for A/AAAA, hostname for CNAME, value for TXT"},
					"ttl":      map[string]interface{}{"type": "integer", "description": "TTL seconds. 0 = auto."},
					"proxied":  map[string]interface{}{"type": "boolean", "description": "Cloudflare-only: route through CF proxy."},
					"provider": map[string]interface{}{"type": "string", "enum": []string{"", "cloudflare", "manual"}, "description": "Force a provider. Default auto."},
				},
			},
		},
		{
			"name":        "dns_remove",
			"description": "Delete a DNS record by id (Cloudflare only — returns a manual removal hint otherwise). Owner-only.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"zone", "recordId"},
				"properties": map[string]interface{}{
					"zone":     map[string]interface{}{"type": "string"},
					"recordId": map[string]interface{}{"type": "string"},
				},
			},
		},
		{
			"name": "ssl_provision",
			"description": "Provision a Let's Encrypt certificate for a domain via the installed `certbot` (http-01 challenge, standalone mode). " +
				"Used on self-hosted yaver boxes that terminate TLS themselves. The yaver-managed-cloud SKU does this automatically via the platform's traefik; this MCP exists for BYO-host users. " +
				"Owner-only; requires certbot present on PATH.",
			"inputSchema": map[string]interface{}{
				"type":     "object",
				"required": []string{"domain", "email"},
				"properties": map[string]interface{}{
					"domain":  map[string]interface{}{"type": "string", "description": "FQDN to issue a cert for (e.g. app.example.com)"},
					"email":   map[string]interface{}{"type": "string", "description": "ACME account email (Let's Encrypt notification address)"},
					"staging": map[string]interface{}{"type": "boolean", "description": "Use Let's Encrypt staging endpoint (avoids rate limits during iteration)"},
				},
			},
		},
	}
}

func dispatchDnsMCP(_ *HTTPServer, name string, arguments json.RawMessage) (bool, interface{}) {
	switch name {
	case "dns_add":
		return true, dnsMCPAdd(arguments)
	case "dns_remove":
		return true, dnsMCPRemove(arguments)
	case "ssl_provision":
		return true, sslMCPProvision(arguments)
	}
	return false, nil
}

func dnsMCPAdd(raw json.RawMessage) interface{} {
	var args struct {
		Zone     string `json:"zone"`
		Type     string `json:"type"`
		Name     string `json:"name"`
		Content  string `json:"content"`
		TTL      int    `json:"ttl"`
		Proxied  bool   `json:"proxied"`
		Provider string `json:"provider"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return mcpToolError("invalid args: " + err.Error())
	}
	if args.Zone == "" || args.Type == "" || args.Name == "" || args.Content == "" {
		return mcpToolError("zone, type, name, content are required")
	}
	rec := DNSRecord{
		Type:    strings.ToUpper(args.Type),
		Name:    args.Name,
		Content: args.Content,
		TTL:     args.TTL,
		Proxied: args.Proxied,
	}
	p := GetDNSProvider(args.Provider)
	id, manual, instr, err := p.CreateRecord(args.Zone, rec)
	if err != nil {
		return mcpToolError("create record: " + err.Error())
	}
	out := map[string]interface{}{
		"ok":       true,
		"provider": p.Name(),
		"manual":   manual,
		"recordId": id,
	}
	if instr != nil {
		out["instruction"] = instr
	}
	return mcpToolJSON(out)
}

func dnsMCPRemove(raw json.RawMessage) interface{} {
	var args struct {
		Zone     string `json:"zone"`
		RecordID string `json:"recordId"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return mcpToolError("invalid args: " + err.Error())
	}
	if args.Zone == "" || args.RecordID == "" {
		return mcpToolError("zone and recordId are required")
	}
	p := GetDNSProvider("")
	manual, instr, err := p.DeleteRecord(args.Zone, args.RecordID)
	if err != nil {
		return mcpToolError("delete record: " + err.Error())
	}
	out := map[string]interface{}{
		"ok":       true,
		"provider": p.Name(),
		"manual":   manual,
	}
	if instr != nil {
		out["instruction"] = instr
	}
	return mcpToolJSON(out)
}

func sslMCPProvision(raw json.RawMessage) interface{} {
	var args struct {
		Domain  string `json:"domain"`
		Email   string `json:"email"`
		Staging bool   `json:"staging"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return mcpToolError("invalid args: " + err.Error())
	}
	if args.Domain == "" || args.Email == "" {
		return mcpToolError("domain and email are required")
	}
	if _, err := exec.LookPath("certbot"); err != nil {
		return mcpToolError("certbot not installed — `sudo apt install certbot` or use the managed-cloud SKU which auto-provisions via traefik")
	}
	cmdArgs := []string{
		"certonly",
		"--standalone",
		"--non-interactive",
		"--agree-tos",
		"--email", args.Email,
		"-d", args.Domain,
	}
	if args.Staging {
		cmdArgs = append(cmdArgs, "--staging")
	}
	out, err := exec.Command("certbot", cmdArgs...).CombinedOutput()
	if err != nil {
		return mcpToolError(fmt.Sprintf("certbot: %v\n%s", err, string(out)))
	}
	// certbot writes the cert to /etc/letsencrypt/live/<domain>/{fullchain.pem, privkey.pem}.
	return mcpToolJSON(map[string]interface{}{
		"ok":          true,
		"domain":      args.Domain,
		"certPath":    "/etc/letsencrypt/live/" + args.Domain + "/fullchain.pem",
		"keyPath":     "/etc/letsencrypt/live/" + args.Domain + "/privkey.pem",
		"renewHint":   "certbot renew runs daily via systemd timer on most distros — `systemctl status certbot.timer` to verify.",
		"certbotOut":  string(out),
	})
}
