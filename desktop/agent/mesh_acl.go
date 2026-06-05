package main

// mesh_acl.go — resolves the control-plane mesh ACL rules (Convex meshAcls,
// authored by tag/device/user) into overlay-IP rules the data-plane Matcher can
// enforce. Resolution is agent-side, matching sdk/js/src/acl.ts's
// "Convex composes, agent enforces" split.
//
// A rule referencing an endpoint the agent cannot resolve (e.g. a device not in
// the visible peer set) is SKIPPED rather than widened — fail-safe, never
// fail-open.

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/yaver-io/agent/mesh"
)

// meshACLRule mirrors backend/convex/mesh.ts listMeshAcls.
type meshACLRule struct {
	SrcType string   `json:"srcType"`
	Src     string   `json:"src"`
	DstType string   `json:"dstType"`
	Dst     string   `json:"dst"`
	Ports   []string `json:"ports"`
	Action  string   `json:"action"`
}

type meshTagRow struct {
	DeviceID string `json:"deviceId"`
	Tag      string `json:"tag"`
}

func fetchMeshAcls(cfg *Config) ([]meshACLRule, error) {
	raw, err := meshConvexCall(cfg, "query", "mesh:listMeshAcls", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var rules []meshACLRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func fetchMeshTags(cfg *Config) ([]meshTagRow, error) {
	raw, err := meshConvexCall(cfg, "query", "mesh:listMeshTags", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var tags []meshTagRow
	if err := json.Unmarshal(raw, &tags); err != nil {
		return nil, err
	}
	return tags, nil
}

// buildMeshACLSource returns a mesh.ACLSource that recompiles the matcher from
// the current control-plane rules + peer/tag resolution each time it's called.
func buildMeshACLSource() mesh.ACLSource {
	return func() (*mesh.Matcher, error) {
		cfg, err := LoadConfig()
		if err != nil {
			return nil, err
		}
		rules, err := fetchMeshAcls(cfg)
		if err != nil {
			return nil, err
		}
		if len(rules) == 0 {
			return mesh.NewMatcher(nil), nil // default-allow
		}
		peers, err := fetchMeshPeers(cfg)
		if err != nil {
			return nil, err
		}
		dev2ip := map[string]string{}
		user2ips := map[string][]string{}
		for _, p := range peers {
			if p.MeshIPv4 != "" {
				dev2ip[p.DeviceID] = p.MeshIPv4
				if p.OwnerUserID != "" {
					user2ips[p.OwnerUserID] = append(user2ips[p.OwnerUserID], p.MeshIPv4)
				}
			}
		}
		tag2ips := map[string][]string{}
		if tags, terr := fetchMeshTags(cfg); terr == nil {
			for _, t := range tags {
				if ip, ok := dev2ip[t.DeviceID]; ok {
					tag2ips[t.Tag] = append(tag2ips[t.Tag], ip)
				}
			}
		}

		var ipRules []mesh.IPRule
		for _, r := range rules {
			src, ok1 := resolveACLEndpoint(r.SrcType, r.Src, dev2ip, tag2ips, user2ips)
			dst, ok2 := resolveACLEndpoint(r.DstType, r.Dst, dev2ip, tag2ips, user2ips)
			if !ok1 || !ok2 {
				continue // unresolvable → skip (fail-safe)
			}
			ports, perr := parseACLPorts(r.Ports)
			if perr != nil {
				continue
			}
			action := mesh.ACLAccept
			if r.Action == "drop" {
				action = mesh.ACLDrop
			}
			ipRules = append(ipRules, mesh.IPRule{Src: src, Dst: dst, Ports: ports, Action: action})
		}
		return mesh.NewMatcher(ipRules), nil
	}
}

// resolveACLEndpoint maps an ACL endpoint (any/device/tag/user) to overlay IP
// prefixes. "any" -> nil (unconstrained). An endpoint that resolves to no known
// IP returns ok=false → the rule is skipped rather than widened (fail-safe).
func resolveACLEndpoint(kind, val string, dev2ip map[string]string, tag2ips, user2ips map[string][]string) ([]netip.Prefix, bool) {
	switch kind {
	case "any":
		return nil, true
	case "device":
		ip, ok := dev2ip[val]
		if !ok {
			return nil, false
		}
		p, err := hostPrefix(ip)
		if err != nil {
			return nil, false
		}
		return []netip.Prefix{p}, true
	case "tag":
		return prefixesFromIPs(tag2ips[strings.TrimPrefix(val, "tag:")])
	case "user":
		return prefixesFromIPs(user2ips[val])
	default: // unknown — don't widen.
		return nil, false
	}
}

// prefixesFromIPs turns a list of overlay IPs into /32 prefixes; returns
// ok=false if none resolved.
func prefixesFromIPs(ips []string) ([]netip.Prefix, bool) {
	if len(ips) == 0 {
		return nil, false
	}
	var out []netip.Prefix
	for _, ip := range ips {
		if p, err := hostPrefix(ip); err == nil {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func hostPrefix(ip string) (netip.Prefix, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(addr, 32), nil
}

// parseACLPorts turns ["22","80-90","*"] into PortRanges. "*" (or empty) means
// all ports (returned as an empty slice the matcher reads as "any port").
func parseACLPorts(specs []string) ([]mesh.PortRange, error) {
	var out []mesh.PortRange
	for _, s := range specs {
		s = strings.TrimSpace(s)
		if s == "" || s == "*" {
			return nil, nil // all ports
		}
		if lo, hi, ok := strings.Cut(s, "-"); ok {
			l, err1 := strconv.ParseUint(lo, 10, 16)
			h, err2 := strconv.ParseUint(hi, 10, 16)
			if err1 != nil || err2 != nil || l > h {
				return nil, fmt.Errorf("bad port range %q", s)
			}
			out = append(out, mesh.PortRange{Lo: uint16(l), Hi: uint16(h)})
			continue
		}
		p, err := strconv.ParseUint(s, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("bad port %q", s)
		}
		out = append(out, mesh.PortRange{Lo: uint16(p), Hi: uint16(p)})
	}
	return out, nil
}
