package main

// Phone remote-control foundation: a single-frame screenshot of a PAIRED
// physical phone, on demand, over Yaver's own blackbox channel.
//
// Why this exists: every other screenshot primitive in the agent (vibe_preview
// snapshot, sim clips, robot_camera) targets a browser/simulator/host-camera on
// the BOX — none can see the physical iPhone. External tools don't help either
// (libimobiledevice can't reach an iOS-26 device over the network). So the
// capability has to live inside Yaver: the agent asks the phone to capture, the
// phone snapshots its own screen (react-native-view-shot captureScreen) and
// POSTs the JPEG back here.
//
// This is the enabler for "develop Yaver with Yaver" — a closed-loop test where
// the orchestrator changes code, reloads on the phone, screenshots the phone,
// and asserts the pixels changed. It is equally a third-party-app-dev primitive:
// drive + screenshot the paired phone over Yaver MCP.
//
// The wire:
//   agent → phone : BlackBoxCommand{Command:"capture_screenshot", Data:{turnId}}
//   phone → agent : POST /blackbox/frame {deviceId, format, dataBase64, turnId}
//   MCP  → agent  : device_screenshot verb → broadcast → wait for a fresh frame

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

type phoneFrame struct {
	data   []byte
	ctype  string
	at     time.Time
	seq    int64
	turnID string
}

type phoneFrameStore struct {
	mu     sync.RWMutex
	frames map[string]phoneFrame // deviceID -> latest frame
	seq    int64
}

var phoneFrames = &phoneFrameStore{frames: make(map[string]phoneFrame)}

func (s *phoneFrameStore) set(deviceID string, data []byte, ctype, turnID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.frames[deviceID] = phoneFrame{data: data, ctype: ctype, at: time.Now().UTC(), seq: s.seq, turnID: turnID}
	return s.seq
}

func (s *phoneFrameStore) get(deviceID string) (phoneFrame, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.frames[deviceID]
	return f, ok
}

// baselineSeq returns the current frame seq for a device (0 if none), so a
// caller can wait for a STRICTLY NEWER frame and never accept a stale one.
func (s *phoneFrameStore) baselineSeq(deviceID string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if f, ok := s.frames[deviceID]; ok {
		return f.seq
	}
	return 0
}

// handleBlackBoxFrame receives a single JPEG/PNG frame from a paired phone.
// Body: {deviceId, format:"jpg"|"png", dataBase64, turnId?}.
func (srv *HTTPServer) handleBlackBoxFrame(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req struct {
		DeviceID   string `json:"deviceId"`
		Format     string `json:"format"`
		DataBase64 string `json:"dataBase64"`
		TurnID     string `json:"turnId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 25<<20)).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	deviceID := firstNonEmptyStr(strings.TrimSpace(req.DeviceID), strings.TrimSpace(r.Header.Get("X-Device-ID")))
	if deviceID == "" {
		deviceID = "unknown"
	}
	raw := strings.TrimSpace(req.DataBase64)
	// Tolerate a data: URL prefix.
	if i := strings.Index(raw, ","); strings.HasPrefix(raw, "data:") && i > 0 {
		raw = raw[i+1:]
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(data) == 0 {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "dataBase64 is empty or not valid base64"})
		return
	}
	ctype := "image/jpeg"
	if strings.EqualFold(strings.TrimSpace(req.Format), "png") {
		ctype = "image/png"
	}
	seq := phoneFrames.set(deviceID, data, ctype, strings.TrimSpace(req.TurnID))
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "seq": seq, "bytes": len(data)})
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "device_screenshot",
		Description: "Screenshot a PAIRED physical phone over the blackbox channel. Broadcasts capture_screenshot, waits for the phone to snapshot its screen and upload a frame, and returns it as a base64 data URL. Foundation for closed-loop phone testing and remote phone management over MCP. Accepts {deviceId?, timeoutMs?}.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"deviceId":  map[string]interface{}{"type": "string"},
				"timeoutMs": map[string]interface{}{"type": "number"},
			},
			"additionalProperties": false,
		},
		Handler:    opsDeviceScreenshotHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsDeviceScreenshotHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var req struct {
		DeviceID  string `json:"deviceId"`
		TimeoutMs int    `json:"timeoutMs"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &req); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: "invalid payload: " + err.Error()}
		}
	}
	if c.Server == nil || c.Server.blackboxMgr == nil {
		return OpsResult{OK: false, Code: "unavailable", Error: "no device command channel on this agent"}
	}
	resp, code, errStr := captureDeviceScreenshot(c.Server.blackboxMgr, req.DeviceID, req.TimeoutMs)
	if errStr != "" {
		return OpsResult{OK: false, Code: code, Error: errStr, Initial: resp}
	}
	return OpsResult{OK: true, Initial: resp}
}

// captureDeviceScreenshot is the shared core (used by the ops verb and the
// first-class MCP image tool): broadcast a capture request, wait for a frame
// strictly newer than the pre-request baseline, return it as a data URL.
func captureDeviceScreenshot(mgr *BlackBoxManager, deviceID string, timeoutMs int) (map[string]interface{}, string, string) {
	deviceID = strings.TrimSpace(deviceID)
	if timeoutMs <= 0 || timeoutMs > 30000 {
		timeoutMs = 8000
	}

	baseline := int64(0)
	if deviceID != "" {
		baseline = phoneFrames.baselineSeq(deviceID)
	}

	cmd := BlackBoxCommand{Command: "capture_screenshot", Data: map[string]interface{}{"reason": "device_screenshot"}}
	var delivered int
	if deviceID != "" {
		if mgr.SendCommandToDevice(deviceID, cmd) {
			delivered = 1
		}
	} else {
		delivered = mgr.BroadcastCommand(cmd)
	}
	if delivered == 0 {
		return map[string]interface{}{"ok": false, "delivered": 0},
			"no_listener",
			"no phone is holding the command stream — open Yaver on the paired phone and retry"
	}

	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for time.Now().Before(deadline) {
		if deviceID != "" {
			if f, ok := phoneFrames.get(deviceID); ok && f.seq > baseline {
				return frameResult(deviceID, f, delivered), "", ""
			}
		} else {
			// No device named: accept the newest frame that lands after we asked.
			if dev, f, ok := newestFrameSince(baseline); ok {
				return frameResult(dev, f, delivered), "", ""
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return map[string]interface{}{"ok": false, "delivered": delivered},
		"timeout",
		"the phone did not return a frame in time (is it foregrounded and on the Tasks/preview screen?)"
}

func newestFrameSince(baseline int64) (string, phoneFrame, bool) {
	phoneFrames.mu.RLock()
	defer phoneFrames.mu.RUnlock()
	var bestDev string
	var best phoneFrame
	for dev, f := range phoneFrames.frames {
		if f.seq > baseline && f.seq > best.seq {
			best = f
			bestDev = dev
		}
	}
	return bestDev, best, bestDev != ""
}

func frameResult(deviceID string, f phoneFrame, delivered int) map[string]interface{} {
	return map[string]interface{}{
		"ok":         true,
		"deviceId":   deviceID,
		"delivered":  delivered,
		"seq":        f.seq,
		"bytes":      len(f.data),
		"capturedAt": f.at.Format(time.RFC3339),
		"image":      "data:" + f.ctype + ";base64," + base64.StdEncoding.EncodeToString(f.data),
	}
}
