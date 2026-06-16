package main

// ops_stream.go — read-only "share a live view" verbs. The generalized peer-
// streaming primitive: any source on this box (the capture card, the Apple TV's
// now-playing artwork, a robot/host camera) can be enumerated and snapshotted by
// a viewer — including a GUEST account holding a "stream"-scoped token, which is
// isolated to exactly these stream_* verbs (see capabilityScopeVerbPrefix in
// ops.go) and can reach nothing else on the machine.
//
// Snapshots return a base64 data URL in Initial (the cad_get / robot_camera
// pattern) so a phone / TV / web viewer renders them with no extra HTTP route or
// guest-auth plumbing. High-fps live video keeps using the MJPEG endpoints
// (/capture/stream); guest-authed MJPEG is the next increment (see
// docs/yaver-appletv-remote-control.md Part C). Privacy: frames flow P2P over
// the authed mesh; nothing touches Convex.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "stream_list",
		Description: "List the live sources this box can SHARE as a view (capture card, Apple TV now-playing, robot/host camera). Read-only; guest-viewable.",
		Schema:      atvSchema(map[string]interface{}{}),
		Handler:     streamListHandler,
		AllowGuest:  true,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "stream_snapshot",
		Description: "Pull one frame from a shared source as a base64 data URL. Payload {source} — \"capture\" (capture card), \"appletv\" (now-playing artwork), \"camera\" (robot/host camera). Read-only; guest-viewable.",
		Schema: atvSchema(map[string]interface{}{
			"source": map[string]interface{}{"type": "string", "description": "capture|appletv|camera"},
			"device": map[string]interface{}{"type": "string", "description": "for source=appletv: which paired TV"},
		}),
		Handler:    streamSnapshotHandler,
		AllowGuest: true,
	})
}

func streamListHandler(c OpsContext, _ json.RawMessage) OpsResult {
	sources := []map[string]interface{}{}
	// Capture card
	if captureStream.running() {
		st := captureStream.status()
		sources = append(sources, map[string]interface{}{
			"source": "capture", "label": "Capture card", "kind": "video",
			"live": !captureStream.hdcpStatus(), "hdcpBlocked": captureStream.hdcpStatus(),
			"streamUrl": "/capture/stream", "frameUrl": "/capture/frame.jpg",
			"fps": st["fps"],
		})
	} else if ffmpegPath() != "" && len(captureDevices()) > 0 {
		sources = append(sources, map[string]interface{}{
			"source": "capture", "label": "Capture card", "kind": "video", "live": false,
			"note": "available but not started (capture_start)",
		})
	}
	// Apple TV now-playing
	if devs, err := appletvListDevices(); err == nil && len(devs) > 0 {
		sources = append(sources, map[string]interface{}{
			"source": "appletv", "label": "Apple TV now-playing", "kind": "metadata+artwork",
			"devices": len(devs),
		})
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"sources": sources}}
}

func streamSnapshotHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Source string `json:"source"`
		Device string `json:"device"`
	}
	_ = json.Unmarshal(payload, &p)
	switch p.Source {
	case "capture", "":
		if captureStream.hdcpStatus() {
			return OpsResult{OK: false, Code: "hdcp_blocked", Error: "source appears HDCP-protected — capture unavailable"}
		}
		f := captureStream.frame()
		if len(f) == 0 {
			return OpsResult{OK: false, Code: "no_frame", Error: "no capture frame (start capture first)"}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"source": "capture",
			"image":  "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(f),
		}}
	case "appletv":
		_, dataURL := appleTVEng.nowPlayingArtworkDataURL(c.Ctx, p.Device)
		if dataURL == "" {
			return OpsResult{OK: false, Code: "no_artwork", Error: "no now-playing artwork available"}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"source": "appletv", "image": dataURL}}
	case "camera":
		// Reuse the existing robot/host camera path so a shared "camera" works
		// wherever robot_snapshot does.
		out := dispatchOps(OpsContext{Ctx: c.Ctx, Server: c.Server, Caller: "owner"},
			OpsRequest{Machine: "local", Verb: "robot_snapshot"})
		if !out.OK {
			return OpsResult{OK: false, Code: "no_camera", Error: out.Error}
		}
		return OpsResult{OK: true, Initial: out.Initial}
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: fmt.Sprintf("unknown source %q", p.Source)}
	}
}
