package main

// mcp_health.go — Personal Health Agent scaffolding.
//
// These tools deliberately do not scrape or diagnose. They give host agents a
// concrete connector contract, scheduling payload, and handoff policy for
// sensitive health portals such as e-Nabız. Browser/redroid execution lands in a
// later slice; OAuth/e-Devlet/2FA/CAPTCHA/block screens must stop for user
// handoff, never bypass.

import (
	"encoding/json"
	"strings"
)

func healthAgentTools() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "health_agent_policy",
			"description": "Personal Health Agent policy: local-first sensitive health automation, user handoff for OAuth/2FA/CAPTCHA/blocks, no diagnosis, no medication changes, no health data in Convex.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		{
			"name":        "health_connector_template",
			"description": "Return a safe connector template for a health portal. Currently supports e-Nabız as read-only first: results, prescriptions, appointments, reports. This is a manifest/plan helper, not a scraper.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"connector": map[string]interface{}{"type": "string", "description": "Connector id. Default: enabiz"},
			}},
		},
		{
			"name":        "health_schedule_plan",
			"description": "Build a local routine payload for scheduled health-result checks. Human-cadence only; returns a schedule_task/routine-ready payload, does not create it.",
			"inputSchema": map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"connector":  map[string]interface{}{"type": "string", "description": "Connector id. Default: enabiz"},
				"cron":       map[string]interface{}{"type": "string", "description": "Cron expression. Default: 0 9 * * *"},
				"timezone":   map[string]interface{}{"type": "string", "description": "Timezone label. Default: local"},
				"capability": map[string]interface{}{"type": "string", "description": "Capability id. Default: results.check_new"},
			}},
		},
	}
}

func mcpHealthToolCall(name string, args json.RawMessage) interface{} {
	switch name {
	case "health_agent_policy":
		return mcpToolJSON(healthAgentPolicy())
	case "health_connector_template":
		var a struct {
			Connector string `json:"connector"`
		}
		_ = json.Unmarshal(args, &a)
		return mcpToolJSON(healthConnectorTemplate(a.Connector))
	case "health_schedule_plan":
		var a struct {
			Connector  string `json:"connector"`
			Cron       string `json:"cron"`
			Timezone   string `json:"timezone"`
			Capability string `json:"capability"`
		}
		_ = json.Unmarshal(args, &a)
		return mcpToolJSON(healthSchedulePlan(a.Connector, a.Capability, a.Cron, a.Timezone))
	default:
		return mcpToolError("unknown health tool: " + name)
	}
}

func healthAgentPolicy() map[string]interface{} {
	return map[string]interface{}{
		"ok":       true,
		"category": "sensitive-read",
		"rules": []string{
			"read-only first: results, prescriptions, appointments, report metadata",
			"OAuth/e-Devlet/2FA/CAPTCHA/block screens require visible user handoff",
			"never bypass CAPTCHA, solve 2FA, spoof headers, rotate IPs, or evade blocks",
			"no diagnosis, no medication start/stop/change advice, no autonomous writes",
			"raw health artifacts stay in the user's local/runtime store; Convex gets coordination metadata only",
			"managed cloud and managed inference are opt-in paid modes, not requirements",
		},
		"inference_modes": []string{"none", "local", "byok", "yaver_managed"},
		"handoff": map[string]interface{}{
			"on": []string{"oauth", "edevlet", "2fa", "captcha", "rate_limit", "blocked", "suspicious_login"},
			"steps": []string{
				"stop automation",
				"keep the browser/redroid session visible",
				"notify the user",
				"resume only after explicit user approval",
				"write local audit event",
			},
		},
	}
}

func healthConnectorTemplate(connector string) map[string]interface{} {
	id := strings.TrimSpace(connector)
	if id == "" {
		id = "enabiz"
	}
	title := "e-Nabız"
	if id != "enabiz" && id != "health_enabiz" {
		title = id
	}
	return map[string]interface{}{
		"id":     id,
		"title":  title,
		"status": "template",
		"engine_order": []string{
			"official_api_if_available",
			"browser_visible_session",
			"redroid_visible_session",
		},
		"auth": map[string]interface{}{
			"method":     "user_session_vault",
			"handoff":    "required_for_oauth_edevlet_2fa_captcha_or_block",
			"credential": "local_vault_only",
		},
		"capabilities": []map[string]interface{}{
			healthCapability("results.list", "List recent lab results", "read", map[string]string{"items": "array"}),
			healthCapability("results.get", "Read one lab result detail", "read", map[string]string{"result": "object", "referenceRanges": "array"}),
			healthCapability("results.check_new", "Check whether new results arrived", "read", map[string]string{"hasNew": "boolean", "items": "array"}),
			healthCapability("prescriptions.list", "List prescriptions", "read", map[string]string{"items": "array"}),
			healthCapability("appointments.list", "List health appointments", "read", map[string]string{"items": "array"}),
			healthCapability("reports.index", "Index report metadata", "read", map[string]string{"reports": "array"}),
		},
		"data_policy": map[string]interface{}{
			"raw_results":      "local_runtime_only",
			"screenshots":      "local_runtime_only",
			"detailed_audit":   "local_only",
			"convex_payloads":  "coordination_metadata_only",
			"notifications":    "minimal_by_default",
			"inference_opt_in": true,
		},
		"notes": []string{
			"This template is intentionally not executable until a user binds a visible browser/redroid session.",
			"Do not put portal credentials or health values in the connector manifest.",
			"Use health_schedule_plan to create a human-cadence routine payload.",
		},
	}
}

func healthCapability(id, title, risk string, answerSchema map[string]string) map[string]interface{} {
	return map[string]interface{}{
		"id":           id,
		"verb":         "get",
		"risk":         risk,
		"title":        title,
		"answerSchema": answerSchema,
	}
}

func healthSchedulePlan(connector, capability, cron, timezone string) map[string]interface{} {
	connector = strings.TrimSpace(connector)
	if connector == "" {
		connector = "enabiz"
	}
	capability = strings.TrimSpace(capability)
	if capability == "" {
		capability = "results.check_new"
	}
	cron = strings.TrimSpace(cron)
	if cron == "" {
		cron = "0 9 * * *"
	}
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = "local"
	}
	return map[string]interface{}{
		"ok": true,
		"schedule_task": map[string]interface{}{
			"title": "Check " + connector + " health results",
			"cron":  cron,
			"runner": "routine",
		},
		"routine": map[string]interface{}{
			"verb": "gateway_query",
			"opsPayload": map[string]interface{}{
				"connector":  connector,
				"capability": capability,
				"params": map[string]string{
					"timezone": timezone,
				},
			},
		},
		"policy": "human-cadence only; pause on auth/2FA/CAPTCHA/block and ask the user; no diagnosis.",
		"next":   "After the executable connector is bound, create a Verb-mode schedule with this payload or ask the user before scheduling it.",
	}
}
