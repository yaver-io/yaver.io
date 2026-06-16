package main

// collection_plan.go — deterministic planner for user-directed collection.
//
// This composes the pieces that already exist:
//   - access_policy_check / EvaluateAccessPolicy
//   - machine inventory + coarse egress region
//   - local collection source/vantage store
//
// It does not automate a source. It chooses a compliant runtime/egress route and
// records the source/vantage only when collection_plan_apply is called.

import (
	"context"
	"encoding/json"
	"strings"
)

type CollectionPlanRequest struct {
	Source              string `json:"source"`
	Action              string `json:"action"`
	Jurisdiction        string `json:"jurisdiction"`
	PreferredRegion     string `json:"preferredRegion"`
	Runtime             string `json:"runtime"` // auto | yaver_managed_cloud | self_hosted | mobile_user_present | external_mcp_only
	NeedsDurable        bool   `json:"needsDurable"`
	NeedsBrowser        bool   `json:"needsBrowser"`
	NeedsAndroid        bool   `json:"needsAndroid"`
	UserPresentRequired bool   `json:"userPresentRequired"`
	Adapter             string `json:"adapter"` // optional external MCP/domain adapter hint, e.g. yaver-bet
	Dataset             string `json:"dataset"`
}

type CollectionPlan struct {
	OK              bool           `json:"ok"`
	Status          string         `json:"status"` // ready | warn | blocked | manual_required | no_runtime
	Source          string         `json:"source"`
	Action          string         `json:"action"`
	Jurisdiction    string         `json:"jurisdiction,omitempty"`
	Policy          PolicyDecision `json:"policy"`
	Runtime         string         `json:"runtime"`
	CollectorType   string         `json:"collectorType"`
	EgressPolicy    string         `json:"egressPolicy"`
	PreferredRegion string         `json:"preferredRegion,omitempty"`
	Machine         *MachineInfo   `json:"machine,omitempty"`
	ViaPeer         string         `json:"viaPeer,omitempty"`
	Adapter         string         `json:"adapter,omitempty"`
	AccessState     string         `json:"accessState"`
	NextActions     []string       `json:"nextActions"`
	Reason          string         `json:"reason,omitempty"`
}

var listCollectionPlannerMachines = func(ctx context.Context) []MachineInfo {
	return listAllMachines(ctx)
}

// sourceBlockedBeforeFn lets the planner ask whether a source has already been
// blocked from some vantage. Indirected for tests. Do no harm: once a source
// flags a datacenter IP class, prefer the user's own-device/residential runtime
// instead of re-hitting the same kind of box (never rotate to evade a block).
var sourceBlockedBeforeFn = func(name string) bool {
	return collStore.sourceBlockedBefore(name)
}

// isDatacenterMachine is true for a remote cloud/VPS box (the IP class that gets
// abuse-reported when it hammers a third party). The user's own local/home
// machine is residential and not flagged here.
func isDatacenterMachine(m MachineInfo) bool {
	if m.IsLocal {
		return false
	}
	p := strings.ToLower(m.Provider)
	for _, dc := range []string{"hetzner", "aws", "gcp", "google", "azure", "cloud", "vps", "digitalocean", "linode", "vultr", "ovh"} {
		if strings.Contains(p, dc) {
			return true
		}
	}
	return false
}

// pickResidentialMachine returns an online own-device/residential machine that
// meets the request's hard capabilities, or nil if none is available.
func pickResidentialMachine(req CollectionPlanRequest, machines []MachineInfo) *MachineInfo {
	for i := range machines {
		m := machines[i]
		if !m.IsOnline || isDatacenterMachine(m) {
			continue
		}
		if req.NeedsBrowser && (m.Capabilities == nil || !m.Capabilities.SupportsGhostWeb) {
			continue
		}
		if req.NeedsAndroid && (m.Capabilities == nil || !m.Capabilities.SupportsAndroid) {
			continue
		}
		return &machines[i]
	}
	return nil
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "collection_plan",
		Description: "Plan a compliant data-collection route for a source/action. Composes access policy, " +
			"runtime capability, and coarse egress region. It never starts automation and never routes around a block.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"source":              map[string]interface{}{"type": "string", "description": "Domain/service/source name, e.g. superbet.rs or status.example.com."},
			"action":              map[string]interface{}{"type": "string", "description": "data|read|observe|scrape|login|bet|deposit|... Default data."},
			"jurisdiction":        map[string]interface{}{"type": "string", "description": "Where the user is physically located, e.g. TR, US, RS."},
			"preferredRegion":     map[string]interface{}{"type": "string", "description": "Preferred coarse egress region: eu|us|na|ap|sa|af|oc."},
			"runtime":             map[string]interface{}{"type": "string", "description": "auto | yaver_managed_cloud | self_hosted | mobile_user_present | external_mcp_only."},
			"needsDurable":        map[string]interface{}{"type": "boolean", "description": "Prefer always-on managed/self-hosted server/VPS."},
			"needsBrowser":        map[string]interface{}{"type": "boolean"},
			"needsAndroid":        map[string]interface{}{"type": "boolean"},
			"userPresentRequired": map[string]interface{}{"type": "boolean"},
			"adapter":             map[string]interface{}{"type": "string", "description": "Optional domain adapter/MCP server hint."},
			"dataset":             map[string]interface{}{"type": "string"},
		}, "source"),
		Handler:    collectionPlanHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "collection_plan_apply",
		Description: "Plan a collection route and register the source + selected vantage in the local collection store. " +
			"Returns sourceId/vantageId. Does not start collection.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"source":              map[string]interface{}{"type": "string"},
			"action":              map[string]interface{}{"type": "string"},
			"jurisdiction":        map[string]interface{}{"type": "string"},
			"preferredRegion":     map[string]interface{}{"type": "string"},
			"runtime":             map[string]interface{}{"type": "string"},
			"needsDurable":        map[string]interface{}{"type": "boolean"},
			"needsBrowser":        map[string]interface{}{"type": "boolean"},
			"needsAndroid":        map[string]interface{}{"type": "boolean"},
			"userPresentRequired": map[string]interface{}{"type": "boolean"},
			"adapter":             map[string]interface{}{"type": "string"},
			"dataset":             map[string]interface{}{"type": "string"},
		}, "source"),
		Handler:    collectionPlanApplyHandler,
		AllowGuest: false,
	})
}

func collectionPlanHandler(c OpsContext, payload json.RawMessage) OpsResult {
	req, err := decodeCollectionPlanRequest(payload)
	if err != "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: err}
	}
	plan := buildCollectionPlan(c.Ctx, req)
	return OpsResult{OK: plan.Status != "blocked" && plan.Status != "no_runtime", Initial: map[string]interface{}{"plan": plan}}
}

func collectionPlanApplyHandler(c OpsContext, payload json.RawMessage) OpsResult {
	req, err := decodeCollectionPlanRequest(payload)
	if err != "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: err}
	}
	plan := buildCollectionPlan(c.Ctx, req)
	if plan.Status == "blocked" {
		return OpsResult{OK: false, Code: "policy_blocked", Error: plan.Policy.Reason, Initial: map[string]interface{}{"plan": plan}}
	}
	if plan.Status == "no_runtime" {
		return OpsResult{OK: false, Code: "no_runtime", Error: plan.Reason, Initial: map[string]interface{}{"plan": plan}}
	}

	source := collStore.upsertSource(CollectionSource{
		Name:        plan.Source,
		Kind:        collectionKindForPlan(plan),
		BaseURL:     plan.Source,
		AccessState: plan.AccessState,
	})
	vantage := collStore.upsertVantage(CollectionVantage{
		RuntimeID:     planMachineID(plan),
		EgressPolicy:  plan.EgressPolicy,
		EgressGeo:     plan.PreferredRegion,
		EgressCountry: strings.ToUpper(strings.TrimSpace(req.PreferredRegion)),
		ViaPeer:       plan.ViaPeer,
	})
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"plan":      plan,
		"source":    source,
		"vantage":   vantage,
		"sourceId":  source.SourceID,
		"vantageId": vantage.VantageID,
	}}
}

func decodeCollectionPlanRequest(payload json.RawMessage) (CollectionPlanRequest, string) {
	var req CollectionPlanRequest
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return req, err.Error()
		}
	}
	req.Source = strings.TrimSpace(req.Source)
	req.Action = strings.TrimSpace(req.Action)
	if req.Action == "" {
		req.Action = "data"
	}
	req.Jurisdiction = strings.ToUpper(strings.TrimSpace(req.Jurisdiction))
	req.PreferredRegion = strings.ToLower(strings.TrimSpace(req.PreferredRegion))
	req.Runtime = strings.TrimSpace(req.Runtime)
	req.Adapter = strings.TrimSpace(req.Adapter)
	req.Dataset = strings.TrimSpace(req.Dataset)
	if req.Source == "" {
		return req, "source required"
	}
	return req, ""
}

func buildCollectionPlan(ctx context.Context, req CollectionPlanRequest) CollectionPlan {
	if ctx == nil {
		ctx = context.Background()
	}
	policy := EvaluateAccessPolicy(req.Source, req.Action, req.Jurisdiction)
	plan := CollectionPlan{
		OK:              true,
		Status:          "ready",
		Source:          req.Source,
		Action:          req.Action,
		Jurisdiction:    req.Jurisdiction,
		Policy:          policy,
		Runtime:         "self_hosted_desktop",
		CollectorType:   collectionCollectorType(req),
		EgressPolicy:    "machine_native",
		PreferredRegion: req.PreferredRegion,
		Adapter:         req.Adapter,
		AccessState:     accessStateFromPolicy(policy),
		NextActions:     []string{"register_source", "register_vantage"},
	}
	if policy.Decision == "block" {
		plan.OK = false
		plan.Status = "blocked"
		plan.Runtime = ""
		plan.EgressPolicy = ""
		plan.NextActions = []string{"surface_policy_block", "do_not_automate"}
		plan.Reason = policy.Reason
		return plan
	}
	if policy.Decision == "warn" {
		plan.Status = "warn"
		plan.NextActions = append(plan.NextActions, "surface_warning")
	}
	if req.UserPresentRequired {
		plan.Runtime = "mobile_user_present"
		plan.Status = maxPlanStatus(plan.Status, "manual_required")
		plan.NextActions = append(plan.NextActions, "ask_user_present")
	}
	if req.Runtime == "external_mcp_only" {
		plan.Runtime = "external_mcp_only"
		plan.EgressPolicy = ""
		plan.NextActions = append(plan.NextActions, "call_external_mcp_adapter")
		return plan
	}

	machines := listCollectionPlannerMachines(ctx)
	selected := selectCollectionMachine(req, machines)
	if selected == nil && plan.Runtime != "mobile_user_present" {
		plan.OK = false
		plan.Status = "no_runtime"
		plan.Reason = "no online machine matched the requested collection capabilities"
		plan.NextActions = append(plan.NextActions, "start_or_auth_runtime")
		return plan
	}
	if selected != nil {
		plan.Machine = selected
		plan.Runtime = runtimeForMachine(req, *selected)
		if req.PreferredRegion != "" && selected.GeoRegion != "" && selected.GeoRegion != req.PreferredRegion {
			plan.EgressPolicy = "peer_egress"
			plan.ViaPeer = selected.DeviceID
			plan.NextActions = append(plan.NextActions, "start_peer_egress_bridge")
		}
		if selected.GeoRegion != "" && plan.PreferredRegion == "" {
			plan.PreferredRegion = selected.GeoRegion
		}
		if req.NeedsBrowser {
			plan.NextActions = append(plan.NextActions, "browser_open")
		}
		if req.NeedsAndroid {
			plan.NextActions = append(plan.NextActions, "android_user_present_or_redroid")
		}
	}

	// Do no harm: if this source already flagged a datacenter IP class, don't
	// re-route it through the same kind of box. Prefer the user's own-device /
	// residential runtime; if none is online, advise user-present collection.
	// Never rotate IPs to knock again — a block is a "no". (See CLAUDE.md.)
	if plan.Machine != nil && isDatacenterMachine(*plan.Machine) && sourceBlockedBeforeFn(req.Source) {
		if alt := pickResidentialMachine(req, machines); alt != nil && alt.DeviceID != plan.Machine.DeviceID {
			plan.Machine = alt
			plan.Runtime = runtimeForMachine(req, *alt)
			plan.EgressPolicy = "machine_native"
			plan.ViaPeer = ""
			if alt.GeoRegion != "" {
				plan.PreferredRegion = alt.GeoRegion
			}
		} else {
			plan.NextActions = append(plan.NextActions, "ask_user_present")
		}
		plan.Status = maxPlanStatus(plan.Status, "warn")
		plan.NextActions = append(plan.NextActions, "prefer_residential_vantage")
		plan.Reason = req.Source + " was previously blocked from a datacenter vantage; prefer an own-device/residential runtime over re-hitting the same cloud IP class — never rotate to evade a block."
	}
	return plan
}

func selectCollectionMachine(req CollectionPlanRequest, machines []MachineInfo) *MachineInfo {
	var best *MachineInfo
	bestScore := -1
	for i := range machines {
		m := machines[i]
		if !m.IsOnline {
			continue
		}
		if req.NeedsBrowser && (m.Capabilities == nil || !m.Capabilities.SupportsGhostWeb) {
			continue
		}
		if req.NeedsAndroid && (m.Capabilities == nil || !m.Capabilities.SupportsAndroid) {
			continue
		}
		score := 0
		if m.IsLocal {
			score += 10
		}
		if req.NeedsDurable && !m.IsLocal {
			score += 35
		}
		if req.Runtime == "yaver_managed_cloud" && strings.Contains(m.Provider, "yaver") {
			score += 60
		}
		if req.Runtime == "self_hosted" && !strings.Contains(m.Provider, "yaver") {
			score += 30
		}
		if req.PreferredRegion != "" && strings.EqualFold(m.GeoRegion, req.PreferredRegion) {
			score += 45
		}
		if strings.Contains(m.Provider, "hetzner") || strings.Contains(m.Provider, "cloud") {
			score += 8
		}
		if score > bestScore {
			bestScore = score
			best = &machines[i]
		}
	}
	return best
}

func runtimeForMachine(req CollectionPlanRequest, m MachineInfo) string {
	if req.Runtime != "" && req.Runtime != "auto" {
		return req.Runtime
	}
	switch {
	case strings.Contains(m.Provider, "yaver"):
		return "yaver_managed_cloud"
	case !m.IsLocal && (strings.Contains(m.Provider, "hetzner") || strings.Contains(m.Provider, "aws") || strings.Contains(m.Provider, "gcp")):
		return "self_hosted_vps"
	case m.IsLocal:
		return "self_hosted_desktop"
	default:
		return "self_hosted_server"
	}
}

func collectionCollectorType(req CollectionPlanRequest) string {
	switch {
	case req.UserPresentRequired:
		return "manual_user_present"
	case req.NeedsAndroid:
		return "redroid_or_physical_android"
	case req.NeedsBrowser:
		return "browser_visible_dom"
	case req.Adapter != "":
		return "external_mcp"
	default:
		return "http_or_browser"
	}
}

func collectionKindForPlan(plan CollectionPlan) string {
	switch plan.CollectorType {
	case "manual_user_present":
		return "manual"
	case "redroid_or_physical_android":
		return "app"
	case "external_mcp":
		return "official_api"
	default:
		return "public_web"
	}
}

func accessStateFromPolicy(p PolicyDecision) string {
	switch p.Decision {
	case "block":
		return "blocked_policy"
	case "warn":
		return "warning"
	default:
		return "public_allowed"
	}
}

func planMachineID(plan CollectionPlan) string {
	if plan.Machine == nil {
		return plan.Runtime
	}
	if strings.TrimSpace(plan.Machine.DeviceID) != "" {
		return plan.Machine.DeviceID
	}
	return plan.Machine.Name
}

func maxPlanStatus(current, next string) string {
	rank := map[string]int{"ready": 1, "warn": 2, "manual_required": 3, "blocked": 4, "no_runtime": 4}
	if rank[next] > rank[current] {
		return next
	}
	return current
}
