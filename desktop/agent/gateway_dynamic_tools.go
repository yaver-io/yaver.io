package main

// gateway_dynamic_tools.go — M-G6: expose each authored connector capability as
// its OWN MCP tool, dynamically from the connector registry.
//
// The static gateway_query{connector, capability, params} dispatcher (gateway_mcp.go)
// stays as-is; this file ADDS a per-capability tool surface on top of it so a host
// AI sees "your apps as tools": one tool named gw_<connector>_<capability> per read
// capability, with an inputSchema derived from that capability's flow params.
//
// READ-ONLY: like the rest of the gateway slice, only read (GET-equivalent)
// capabilities are surfaced — ACT/write verbs are skipped entirely.
//
// Naming: gw_<connectorID>_<capabilityID>, sanitized to a valid MCP tool name
// (lowercase [a-z0-9_], any other run collapsed to a single "_"). If two distinct
// (connector, capability) pairs sanitize to the same name, the second+ are
// disambiguated deterministically with a short content hash and the collision is
// logged — a pair is NEVER silently shadowed.
//
// Dispatch (httpserver.go) routes any tool whose name starts with "gw_" back
// through the SAME sanitized-name lookup to recover the raw (connector, capability)
// ids, then calls gatewayInvoke — mirroring the gateway_query dispatch case.

import (
	"crypto/sha1"
	"encoding/hex"
	"log"
	"sort"
	"strings"
)

// gatewayDynamicToolPrefix is the reserved name prefix for every dynamically
// registered per-capability gateway tool.
const gatewayDynamicToolPrefix = "gw_"

// dynamicGatewayTools enumerates the registry and emits one MCP tool definition
// per READ capability, named gw_<connector>_<capability> (sanitized). The shape
// matches getMCPToolsList's []map[string]interface{} tool entries. A registry
// that cannot be listed (e.g. not authed / no connectors) yields an empty slice
// — never an error — so the overall tools list is never broken by the gateway.
func dynamicGatewayTools(reg *ConnectorRegistry) []map[string]interface{} {
	pairs := gatewayCapabilityPairs(reg)
	out := make([]map[string]interface{}, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, map[string]interface{}{
			"name":        p.toolName,
			"description": gatewayDynamicToolDescription(p),
			"inputSchema": gatewayDynamicInputSchema(p),
		})
	}
	return out
}

// gatewayCapabilityPair is one resolved (connector, capability) flattened with
// its final, collision-safe tool name and the metadata needed to build a tool
// definition + reverse-resolve a call.
type gatewayCapabilityPair struct {
	toolName    string
	connector   string
	capability  string
	verb        string
	risk        string
	title       string
	description string
	surface     string
	params      []string // {placeholder} names from the capability flow path
}

// gatewayCapabilityPairs is the single source of truth for the dynamic tool set:
// both the tool-list builder and the dispatcher call it so the sanitized name →
// (connector, capability) mapping is computed identically in both directions.
//
// Iteration is deterministic (connectors sorted by id, capabilities in manifest
// order) so collision disambiguation is stable across calls.
func gatewayCapabilityPairs(reg *ConnectorRegistry) []gatewayCapabilityPair {
	if reg == nil {
		return nil
	}
	connectors, err := reg.List()
	if err != nil {
		// Not authed / unreadable dir / etc. — surface nothing, never error the
		// tool list. (List already skips a single corrupt manifest.)
		return nil
	}
	// List() already sorts by id; be defensive in case that ever changes.
	sort.SliceStable(connectors, func(i, j int) bool { return connectors[i].ID < connectors[j].ID })

	taken := map[string]gatewayCapabilityPair{} // sanitized name -> owning pair
	out := make([]gatewayCapabilityPair, 0)
	for _, c := range connectors {
		for _, cap := range c.Capabilities {
			if !isReadVerb(cap.Verb) {
				continue // READ-ONLY slice: skip ACT/write capabilities entirely
			}
			base := sanitizeGatewayToolName(gatewayDynamicToolPrefix + c.ID + "_" + cap.ID)
			name := base
			if prev, clash := taken[name]; clash {
				// Deterministic disambiguation: append a short hash of the raw
				// (connector, capability) ids. NEVER silently shadow.
				name = base + "_" + gatewayNameHash(c.ID, cap.ID)
				log.Printf("[gateway] dynamic tool name collision: %q (%s/%s) clashes with %q (%s/%s); disambiguated to %q",
					base, c.ID, cap.ID, base, prev.connector, prev.capability, name)
				// Extremely unlikely, but guard against a second-order collision.
				for {
					if _, again := taken[name]; !again {
						break
					}
					name = name + "x"
				}
			}
			pair := gatewayCapabilityPair{
				toolName:    name,
				connector:   c.ID,
				capability:  cap.ID,
				verb:        cap.Verb,
				risk:        cap.Risk,
				title:       cap.Title,
				description: cap.Description,
				surface:     c.Surface,
				params:      flowParams(cap.Flow.Path),
			}
			taken[name] = pair
			out = append(out, pair)
		}
	}
	return out
}

// resolveGatewayDynamicTool maps a tool name back to its (connector, capability)
// ids by rebuilding the same sanitized-name set the tool list was built from.
// Returns ok=false for any name that is not a currently-registered gw_* tool.
func resolveGatewayDynamicTool(reg *ConnectorRegistry, toolName string) (connectorID, capabilityID string, ok bool) {
	if !strings.HasPrefix(toolName, gatewayDynamicToolPrefix) {
		return "", "", false
	}
	for _, p := range gatewayCapabilityPairs(reg) {
		if p.toolName == toolName {
			return p.connector, p.capability, true
		}
	}
	return "", "", false
}

// gatewayDynamicToolDescription builds a human/AI-readable description for one
// per-capability tool from the capability title/verb and the connector surface.
func gatewayDynamicToolDescription(p gatewayCapabilityPair) string {
	var b strings.Builder
	b.WriteString("Personal Agent Gateway: ")
	label := strings.TrimSpace(p.title)
	if label == "" {
		label = strings.TrimSpace(p.description)
	}
	if label == "" {
		label = p.capability
	}
	b.WriteString(label)
	b.WriteString(" — reads ")
	b.WriteString(p.connector)
	b.WriteString("/")
	b.WriteString(p.capability)
	verb := strings.TrimSpace(p.verb)
	if verb == "" {
		verb = "get"
	}
	b.WriteString(" (verb ")
	b.WriteString(verb)
	b.WriteString(") as YOU")
	if s := strings.TrimSpace(p.surface); s != "" {
		b.WriteString(" against ")
		b.WriteString(s)
	}
	b.WriteString(". READ-ONLY; credentials live in your vault, nothing is sent to Convex.")
	return b.String()
}

// gatewayDynamicInputSchema builds a JSON-schema object whose properties are the
// capability's flow params (each a string). All params are surfaced as optional
// strings — substituteParams leaves an unfilled {placeholder} intact rather than
// erroring, so requiredness is the capability's concern, not the schema's.
func gatewayDynamicInputSchema(p gatewayCapabilityPair) map[string]interface{} {
	props := map[string]interface{}{}
	for _, name := range p.params {
		props[name] = map[string]interface{}{
			"type":        "string",
			"description": "Value substituted into the {" + name + "} placeholder of the capability flow path.",
		}
	}
	return map[string]interface{}{
		"type":       "object",
		"properties": props,
	}
}

// sanitizeGatewayToolName lowercases and reduces a candidate name to a valid MCP
// tool name: only [a-z0-9_] survive; any maximal run of other characters
// collapses to a single "_". Leading/trailing "_" introduced by collapsing are
// trimmed, except the reserved gw_ prefix is preserved.
func sanitizeGatewayToolName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevUnderscore := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		// '_' and every other rune collapse to a single underscore.
		if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	out := b.String()
	out = strings.Trim(out, "_")
	// Re-assert the reserved prefix (trimming may have eaten a leading "_" but
	// the literal "gw" survives; ensure the canonical "gw_" form).
	if strings.HasPrefix(out, "gw_") {
		return out
	}
	if strings.HasPrefix(out, "gw") {
		return "gw_" + strings.TrimPrefix(out, "gw")
	}
	return gatewayDynamicToolPrefix + out
}

// gatewayNameHash returns a short, stable hex digest of the raw (connector,
// capability) ids, used to disambiguate a sanitization collision deterministically.
func gatewayNameHash(connector, capability string) string {
	sum := sha1.Sum([]byte(connector + "\x00" + capability))
	return hex.EncodeToString(sum[:])[:6]
}
