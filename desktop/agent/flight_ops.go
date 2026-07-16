package main

// flight_ops.go — `flight_events` MCP verb: read a machine's black box.
//
// This is the surface that matters most for the recorder's actual use case.
// When a remote box dies you are usually NOT at a terminal — you are on the
// phone, and the box is unreachable by definition. `yaver flight` needs a shell
// on a machine; this verb needs nothing but the phone.
//
// Local-only by design: it reports THIS agent's buffer. To ask about a box that
// is still down, query Convex (GET /devices/flight) — a dead box cannot answer
// an MCP call, which is the whole reason the events are synced.

import (
	"encoding/json"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "flight_events",
		Description: "Read this machine's black box: the lifecycle history (boot / shutdown / unclean_stop / sleep / wake) plus a verdict on whether the last stop was graceful or a hard death (power loss, panic, forced kill). Use this to tell 'the box died' apart from 'Yaver crashed' after a machine goes silent. Newest first.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"limit": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": flightRecorderMaxEvents},
			},
			"additionalProperties": false,
		},
		Handler: opsFlightEventsHandler,
	})
}

func opsFlightEventsHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Limit int `json:"limit"`
	}
	// An absent payload is the common call ("just show me"), not an error.
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	limit := p.Limit
	if limit <= 0 || limit > flightRecorderMaxEvents {
		limit = flightRecorderMaxEvents
	}

	events := FlightEvents()
	// Newest-first: the last record before silence is the point.
	newestFirst := make([]FlightEvent, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		newestFirst = append(newestFirst, events[i])
	}
	if len(newestFirst) > limit {
		newestFirst = newestFirst[:limit]
	}

	return OpsResult{OK: true, Initial: map[string]interface{}{
		"events":  newestFirst,
		"verdict": flightVerdict(newestFirst),
	}}
}
