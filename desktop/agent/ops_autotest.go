package main

import "encoding/json"

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "autotest",
		Description: "Start a local Auto Test run. Drives web specs through Chrome CDP by default; Selenium is opt-in and lazy. Results stay under .yaver/results.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"workDir":         map[string]interface{}{"type": "string"},
				"scope":           map[string]interface{}{"type": "string", "description": "full | changed | screen:<name>"},
				"viewport":        map[string]interface{}{"type": "string"},
				"driver":          map[string]interface{}{"type": "string", "enum": []string{"cdp", "chrome", "chrome-cdp", "selenium"}},
				"stream":          map[string]interface{}{"type": "boolean"},
				"propose":         map[string]interface{}{"type": "boolean"},
				"maxFlows":        map[string]interface{}{"type": "integer"},
				"maxWallClockSec": map[string]interface{}{"type": "integer"},
				"acPowerOnly":     map[string]interface{}{"type": "boolean"},
			},
			"additionalProperties": false,
		},
		Handler:    opsAutotestHandler,
		Streaming:  true,
		AllowGuest: false,
	})
}

func opsAutotestHandler(c OpsContext, payload json.RawMessage) OpsResult {
	req, err := decodeAutotestRequest(payload)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if c.Server == nil {
		return OpsResult{OK: false, Code: "unavailable", Error: "autotest needs an HTTP server context"}
	}
	started, _, err := startAutotestRun(c.Ctx, c.Server, req)
	if err != nil {
		return OpsResult{OK: false, Code: "autotest_failed", Error: err.Error()}
	}
	streamID, _ := started["streamId"].(string)
	return OpsResult{OK: true, StreamID: streamID, Initial: started}
}
