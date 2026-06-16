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
	"strings"
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
		Name:        "screen_watch",
		Description: "Open a URL (YouTube / a video / a web app) in THIS box's desktop browser and return the live screen-stream URL, so you can watch a remote box (e.g. magara) on your phone. Drive playback further with the browser_* / open_url tools or just watch. Payload {url}. Agnostic: Yaver streams whatever the screen shows as-is (DRM video like Netflix/Gain/Exxen may render black — streamed as-is). You are responsible for the content and the right to stream it.",
		Schema: atvSchema(map[string]interface{}{
			"url": map[string]interface{}{"type": "string", "description": "http(s) URL to open on the box"},
		}),
		Handler: screenWatchHandler,
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
	// PC/box screen — shareable via the existing Remote Desktop / ghost stream.
	// `screen_watch <url>` opens a video here; the phone watches /rd/stream.
	if ghostStream.running() {
		sources = append(sources, map[string]interface{}{
			"source": "screen", "label": "This box's screen", "kind": "video", "live": true,
			"streamUrl": "/ghost/stream", "frameUrl": "/ghost/frame.jpg",
		})
	} else {
		sources = append(sources, map[string]interface{}{
			"source": "screen", "label": "This box's screen", "kind": "video", "live": false,
			"note": "view via Remote Desktop (/rd/stream); open a video here with screen_watch",
		})
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"sources": sources}}
}

// screenWatchHandler opens a URL in the box's desktop browser so a remote box
// (e.g. magara) becomes a watch source the phone can view over the screen
// stream. DRM video blanks under capture — that's surfaced, never worked around.
func screenWatchHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	u := strings.TrimSpace(p.URL)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return OpsResult{OK: false, Code: "bad_payload", Error: "url must be http(s)"}
	}
	openBrowser(u) // desktop browser on THIS box (xdg-open/open/start)
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"opened":    u,
		"viewVia":   "Remote Desktop screen stream (/rd/stream) or /ghost/stream",
		"frameUrl":  "/ghost/frame.jpg",
		"controlBy": "browser_navigate / browser_click / open_url, or just watch the stream",
	}}
}

func streamSnapshotHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Source string `json:"source"`
		Device string `json:"device"`
	}
	_ = json.Unmarshal(payload, &p)
	switch p.Source {
	case "capture", "":
		// Agnostic: return whatever the card provides, including a black frame.
		f := captureStream.frame()
		if len(f) == 0 {
			return OpsResult{OK: false, Code: "no_frame", Error: "no capture frame (start capture first)"}
		}
		out := map[string]interface{}{
			"source": "capture",
			"image":  "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(f),
		}
		if captureStream.hdcpStatus() {
			out["blackHint"] = "frames are persistently black — likely an HDCP-protected source; streamed as-is"
		}
		return OpsResult{OK: true, Initial: out}
	case "appletv":
		_, dataURL := appleTVEng.nowPlayingArtworkDataURL(c.Ctx, p.Device)
		if dataURL == "" {
			return OpsResult{OK: false, Code: "no_artwork", Error: "no now-playing artwork available"}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"source": "appletv", "image": dataURL}}
	case "screen":
		// The box's desktop screen, via the shared ghost/Remote-Desktop frame
		// buffer (populated while /rd/stream or /ghost/stream is active).
		f := ghostStream.frame()
		if len(f) == 0 {
			return OpsResult{OK: false, Code: "no_frame", Error: "screen stream not active — open Remote Desktop on this box first"}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"source": "screen",
			"image":  "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(f),
		}}
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
