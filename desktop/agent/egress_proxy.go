package main

// egress_proxy.go — peer-egress forward proxy: lend THIS machine's egress IP to
// the SAME user's OTHER machines, so a collector running on box A can be seen by
// a source as box B. This is the "use my US box's IP while I sit on my EU box"
// mechanism behind multi-vantage collection
// (docs/user-directed-data-collection-runtimes.md, Multi-Vantage / Egress).
//
// LEGAL / SAFETY POSTURE — read before changing. These are not optional:
//
//   - OPT-IN, DEFAULT OFF. A box never lends its egress unless the owner turns
//     it on (egress_proxy_set enabled=true). No build flag and no remote caller
//     can flip it on — only the owner, locally authenticated.
//   - SAME-USER ONLY, NEVER AN OPEN PROXY. The endpoint lives behind the agent's
//     auth() middleware, so only the owner's own token / paired devices reach
//     it, over the existing authenticated peer transport. It opens NO new public
//     listener.
//   - NO PRIVATE PIVOT (anti-SSRF). By default the proxy refuses to connect to
//     private / loopback / link-local / reserved targets (RFC1918, 127/8, ::1,
//     fc00::/7, 169.254, multicast). That stops a lent egress from reaching the
//     lending box's own LAN. The owner can opt into private targets for their
//     own lab via allowPrivateTargets.
//   - PORT ALLOWLIST. Only 80/443 by default (ordinary web collection). DNS
//     rebinding is defeated by dialing the exact IP we validated, not the name.
//   - AUDITED. Each proxied target host:port + verdict is recorded locally for
//     the owner — host/port only, never the path, query, or payload bytes.
//   - NOT BAN-EVASION. This lends the user's OWN egress to the user's OWN other
//     machine. It is not a rotating-IP pool and must not be used to defeat a
//     geo/IP block a source deliberately imposed (see the doc's Non-Goals).

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// EgressProxyPolicy is the owner's local control surface for lending egress.
type EgressProxyPolicy struct {
	// Enabled is the master switch. Default FALSE — a box lends its IP to
	// nobody, not even the owner's other devices, until explicitly turned on.
	Enabled bool `json:"enabled"`
	// AllowPrivateTargets lets the proxy reach private/reserved addresses.
	// Default FALSE so a lent egress can't be used to pivot into this box's LAN.
	AllowPrivateTargets bool `json:"allowPrivateTargets"`
	// AllowedPorts restricts the destination port. Empty => the default 80/443.
	AllowedPorts []int `json:"allowedPorts,omitempty"`
	UpdatedAt    int64 `json:"updatedAt"`
}

func defaultEgressProxyPolicy() EgressProxyPolicy {
	return EgressProxyPolicy{Enabled: false, AllowPrivateTargets: false}
}

// egressPolicyOverride, when non-nil, replaces the on-disk policy. Test-only.
var egressPolicyOverride *EgressProxyPolicy

var (
	egressProxyMu      sync.Mutex
	egressProxyBaseDir string
)

func egressProxyDir() (string, error) {
	egressProxyMu.Lock()
	defer egressProxyMu.Unlock()
	if egressProxyBaseDir != "" {
		return egressProxyBaseDir, nil
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "egress-proxy")
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	egressProxyBaseDir = p
	return p, nil
}

func egressProxyPolicyPath() (string, error) {
	base, err := egressProxyDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "policy.json"), nil
}

func loadEgressProxyPolicy() EgressProxyPolicy {
	p, err := egressProxyPolicyPath()
	if err != nil {
		return defaultEgressProxyPolicy()
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return defaultEgressProxyPolicy()
	}
	pol := defaultEgressProxyPolicy()
	if err := json.Unmarshal(data, &pol); err != nil {
		return defaultEgressProxyPolicy()
	}
	return pol
}

func saveEgressProxyPolicy(pol EgressProxyPolicy) error {
	p, err := egressProxyPolicyPath()
	if err != nil {
		return err
	}
	pol.UpdatedAt = time.Now().UnixMilli()
	data, _ := json.MarshalIndent(pol, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

// effectiveEgressProxyPolicy returns the test override if set, else the on-disk
// policy. The handler reads through this so tests need no filesystem.
func effectiveEgressProxyPolicy() EgressProxyPolicy {
	if egressPolicyOverride != nil {
		return *egressPolicyOverride
	}
	return loadEgressProxyPolicy()
}

func effectiveEgressPorts(pol EgressProxyPolicy) []int {
	if len(pol.AllowedPorts) == 0 {
		return []int{80, 443}
	}
	return pol.AllowedPorts
}

func egressPortAllowed(pol EgressProxyPolicy, p int) bool {
	for _, ap := range effectiveEgressPorts(pol) {
		if ap == p {
			return true
		}
	}
	return false
}

// isPrivateOrReserved reports whether an IP must not be reachable through a lent
// egress by default. Covers loopback, link-local, multicast, unspecified, and
// all RFC1918 / ULA private ranges (net.IP.IsPrivate covers fc00::/7 too).
func isPrivateOrReserved(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip.IsPrivate()
}

// egressTargetAllowed validates a "host:port" target against the policy and
// returns the exact IP to dial. Resolving here and dialing the returned IP (not
// the hostname) defeats DNS rebinding — a name can't resolve "public" for the
// check and "private" for the dial. ALL resolved IPs must pass, so a name that
// maps to both a public and a private address is refused.
func egressTargetAllowed(pol EgressProxyPolicy, target string) (dialIP net.IP, port int, ok bool, reason string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, 0, false, "missing target host:port"
	}
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return nil, 0, false, "target must be host:port"
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p <= 0 || p > 65535 {
		return nil, 0, false, "invalid port"
	}
	if !egressPortAllowed(pol, p) {
		return nil, 0, false, fmt.Sprintf("port %d not allowed (allowed: %v)", p, effectiveEgressPorts(pol))
	}

	if lit := net.ParseIP(host); lit != nil {
		if !pol.AllowPrivateTargets && isPrivateOrReserved(lit) {
			return nil, 0, false, "target is a private/reserved address; refused (no LAN pivot)"
		}
		return lit, p, true, ""
	}

	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return nil, 0, false, "cannot resolve target host"
	}
	if !pol.AllowPrivateTargets {
		for _, cand := range ips {
			if isPrivateOrReserved(cand) {
				return nil, 0, false, "target resolves to a private/reserved address; refused (no LAN pivot)"
			}
		}
	}
	return ips[0], p, true, ""
}

// handleEgressProxy is the auth-gated upgrade endpoint that lends this box's
// egress. Registered behind s.auth(), so the caller is always the owner or a
// paired device of the same user. The peer asks for a target via ?target=, we
// validate it against the policy, dial the approved IP, and pipe raw bytes
// (the end-to-end TLS handshake, with SNI, happens between the far client and
// the target through this pipe — we never see plaintext).
func (s *HTTPServer) handleEgressProxy(w http.ResponseWriter, r *http.Request) {
	pol := effectiveEgressProxyPolicy()
	if !pol.Enabled {
		jsonError(w, http.StatusForbidden, "egress proxy is disabled on this machine (owner must enable it: egress_proxy_set enabled=true)")
		return
	}

	target := r.URL.Query().Get("target")
	dialIP, port, ok, reason := egressTargetAllowed(pol, target)
	if !ok {
		egressAudit("deny", target, reason)
		jsonError(w, http.StatusForbidden, reason)
		return
	}

	dialAddr := net.JoinHostPort(dialIP.String(), strconv.Itoa(port))
	upstream, err := net.DialTimeout("tcp", dialAddr, 8*time.Second)
	if err != nil {
		egressAudit("error", target, err.Error())
		jsonError(w, http.StatusBadGateway, "dial target: "+err.Error())
		return
	}

	hj, hok := w.(http.Hijacker)
	if !hok {
		upstream.Close()
		jsonError(w, http.StatusInternalServerError, "hijack unsupported")
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		upstream.Close()
		return
	}
	_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: yaver-egress\r\nConnection: Upgrade\r\n\r\n")
	_ = buf.Flush()

	egressAudit("egress", target, "")
	pipeBoth(conn, upstream)
}

// --- local audit (owner-only, host/port + verdict; never bytes) -------------

// EgressAuditEntry is one local audit record of a proxied (or refused) target.
type EgressAuditEntry struct {
	At     int64  `json:"at"`
	Action string `json:"action"` // egress | deny | error
	Target string `json:"target"` // host:port only
	Note   string `json:"note,omitempty"`
}

var (
	egressAuditMu  sync.Mutex
	egressAuditLog []EgressAuditEntry
)

func egressAudit(action, target, note string) {
	egressAuditMu.Lock()
	defer egressAuditMu.Unlock()
	egressAuditLog = append(egressAuditLog, EgressAuditEntry{
		At:     time.Now().UnixMilli(),
		Action: action,
		Target: target,
		Note:   note,
	})
	if len(egressAuditLog) > 200 {
		egressAuditLog = egressAuditLog[len(egressAuditLog)-200:]
	}
}

func egressAuditRecent(n int) []EgressAuditEntry {
	egressAuditMu.Lock()
	defer egressAuditMu.Unlock()
	if n <= 0 || n > len(egressAuditLog) {
		n = len(egressAuditLog)
	}
	out := make([]EgressAuditEntry, n)
	copy(out, egressAuditLog[len(egressAuditLog)-n:])
	return out
}

// --- ops verbs: owner controls THIS box's lending policy --------------------

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "egress_proxy_status",
		Description: "Report whether THIS machine lends its egress to the owner's other devices: " +
			"the policy (enabled, allowed ports, private-target setting), this box's egress identity, " +
			"and a recent local audit of proxied/refused targets. Owner-only. Lending is opt-in and " +
			"default OFF; the proxy is reachable only over authenticated peer transport, never as an open proxy.",
		Schema:     ghostJSONSchema(map[string]interface{}{}),
		Handler:    egressProxyStatusHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "egress_proxy_set",
		Description: "Owner toggle for lending THIS machine's egress to their own other devices. " +
			"enabled=true turns it on (default off). allowPrivateTargets=true lets the proxy reach " +
			"private/LAN addresses (default false — keep it off unless you intend LAN access; it removes " +
			"the anti-pivot guard). allowedPorts overrides the default 80/443. Owner-only. This lends your " +
			"OWN IP to your OWN machines; it is not a tool to defeat a source's geo/IP block.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"enabled":             map[string]interface{}{"type": "boolean", "description": "Master switch (default off)."},
			"allowPrivateTargets": map[string]interface{}{"type": "boolean", "description": "Permit private/reserved destination IPs (default false; removes the anti-LAN-pivot guard)."},
			"allowedPorts":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}, "description": "Allowed destination ports (default [80,443])."},
		}),
		Handler:    egressProxySetHandler,
		AllowGuest: false,
	})
}

func egressProxyStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
	pol := effectiveEgressProxyPolicy()
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"policy":         pol,
		"effectivePorts": effectiveEgressPorts(pol),
		"egress":         detectEgressIdentity(c.Ctx, mustLoadConfigBestEffort(), false),
		"recentAudit":    egressAuditRecent(20),
	}}
}

func egressProxySetHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		Enabled             *bool `json:"enabled"`
		AllowPrivateTargets *bool `json:"allowPrivateTargets"`
		AllowedPorts        []int `json:"allowedPorts"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &args); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	pol := loadEgressProxyPolicy()
	if args.Enabled != nil {
		pol.Enabled = *args.Enabled
	}
	if args.AllowPrivateTargets != nil {
		pol.AllowPrivateTargets = *args.AllowPrivateTargets
	}
	if args.AllowedPorts != nil {
		pol.AllowedPorts = args.AllowedPorts
	}
	if err := saveEgressProxyPolicy(pol); err != nil {
		return OpsResult{OK: false, Code: "internal", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"policy":         pol,
		"effectivePorts": effectiveEgressPorts(pol),
	}}
}

// mustLoadConfigBestEffort returns the loaded config or nil — detectEgressIdentity
// treats nil as "opt-out not set".
func mustLoadConfigBestEffort() *Config {
	cfg, _ := LoadConfig()
	return cfg
}
