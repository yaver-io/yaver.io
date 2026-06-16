package main

// stream_plan.go — M15 Q6: path auto-select. The first adaptation (§H.3) is
// choosing the TRANSPORT, before tuning the encode: a phone glancing at a card
// wants a cheap MJPEG poll; a projector wants low-latency WebRTC; a public stream
// wants RTMP. stream_plan takes the sink's shape and returns the recommended
// path + endpoint + the resolved quality profile, so a client picks the right
// pipe instead of hardcoding one.

import (
	"encoding/json"
	"strings"
)

type streamPlanResult struct {
	Path            string        `json:"path"` // webrtc | mjpeg | rtmp
	Endpoint        string        `json:"endpoint"`
	Reason          string        `json:"reason"`
	Profile         StreamProfile `json:"profile"`
	ApproxLatencyMs int           `json:"approxLatencyMs"`
}

// planStreamPath is the pure decision (unit-tested). Order matters: public →
// RTMP; low-latency / big screen → WebRTC; small / low-power → MJPEG; else a
// cheap MJPEG default that a client can upgrade to WebRTC for full-screen.
func planStreamPath(source, deviceClass, net string, w, h int, latency string, public bool) streamPlanResult {
	prof := profileForConstraints(deviceClass, w, h, net, "")
	dc := strings.ToLower(strings.TrimSpace(deviceClass))
	switch {
	case public:
		return streamPlanResult{Path: "rtmp", Endpoint: "stream_broadcast {rtmpUrl}", Reason: "public broadcast → RTMP to a platform or your own server", Profile: prof, ApproxLatencyMs: 3000}
	case latency == "low" || dc == "tv" || dc == "projector":
		return streamPlanResult{Path: "webrtc", Endpoint: "/stream/webrtc/offer", Reason: "low-latency / full-screen sink → WebRTC (sub-second)", Profile: prof, ApproxLatencyMs: 300}
	case dc == "glass" || (w > 0 && w <= 480):
		return streamPlanResult{Path: "mjpeg", Endpoint: "/capture/frame.jpg (poll) or stream_snapshot", Reason: "small / low-power sink → cheap MJPEG snapshot-poll (iOS-safe)", Profile: prof, ApproxLatencyMs: 1200}
	default:
		return streamPlanResult{Path: "mjpeg", Endpoint: "/capture/stream (MJPEG) or stream_snapshot", Reason: "default card/thumbnail → MJPEG; switch to /stream/webrtc/offer for full-screen", Profile: prof, ApproxLatencyMs: 800}
	}
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "stream_plan",
		Description: "Recommend the best transport PATH (webrtc|mjpeg|rtmp) + endpoint + quality profile for a sink. Payload {source?, deviceClass, w?, h?, net?, latency?(low|normal), public?}. WebRTC for low-latency/TV/projector, MJPEG for small/low-power/iOS, RTMP for public broadcast.",
		Schema: atvSchema(map[string]interface{}{
			"source":      map[string]interface{}{"type": "string"},
			"deviceClass": map[string]interface{}{"type": "string", "description": "phone|web|tv|projector|glass"},
			"w":           map[string]interface{}{"type": "integer"},
			"h":           map[string]interface{}{"type": "integer"},
			"net":         map[string]interface{}{"type": "string"},
			"latency":     map[string]interface{}{"type": "string", "description": "low|normal"},
			"public":      map[string]interface{}{"type": "boolean"},
		}),
		Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			var p struct {
				Source      string `json:"source"`
				DeviceClass string `json:"deviceClass"`
				Net         string `json:"net"`
				Latency     string `json:"latency"`
				W           int    `json:"w"`
				H           int    `json:"h"`
				Public      bool   `json:"public"`
			}
			_ = json.Unmarshal(payload, &p)
			return OpsResult{OK: true, Initial: planStreamPath(p.Source, p.DeviceClass, p.Net, p.W, p.H, p.Latency, p.Public)}
		},
		AllowGuest: true,
	})
}
