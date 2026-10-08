package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "physical_kvm_status", Description: "Probe the real Pi capture + M5Stack USB HID operations for an enrolled physical PC. Local-first; no Convex data plane.",
		Schema: atvSchema(map[string]interface{}{}), Handler: physicalKVMStatusHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "physical_kvm_doctor", Description: "Decode a fresh frame from the HDMI capture card and probe the paired M5Stack bridge. Returns named causes and remedies.",
		Schema: atvSchema(map[string]interface{}{}), Handler: physicalKVMDoctorHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "physical_kvm_capture_select", Description: "Select one advertised stable UVC capture path when automatic discovery is ambiguous.",
		Schema: atvSchema(map[string]interface{}{
			"device": map[string]interface{}{"type": "string", "description": "Exact path returned by physical_kvm_status capture.devices"},
		}), Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			var req struct {
				Device string `json:"device"`
			}
			if err := json.Unmarshal(payload, &req); err != nil {
				return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			}
			if err := physicalKVM.selectCaptureDevice(req.Device); err != nil {
				return OpsResult{OK: false, Code: "kvm_capture_select_failed", Error: err.Error(), Initial: map[string]any{"devices": captureDevices()}}
			}
			return OpsResult{OK: true, Initial: map[string]any{"selectedDevice": req.Device}}
		},
	})
	registerOpsVerb(opsVerbSpec{
		Name: "physical_kvm_session_open", Description: "Acquire a short input lease after the user physically arms the M5Stack. Returns a sessionId for typed actions.",
		Schema: atvSchema(map[string]interface{}{}), Handler: func(c OpsContext, _ json.RawMessage) OpsResult {
			id, err := physicalKVM.open(c.Ctx)
			if err != nil {
				return OpsResult{OK: false, Code: "kvm_session_refused", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: map[string]any{"sessionId": id, "expiresInMs": 10000}}
		},
	})
	registerOpsVerb(opsVerbSpec{
		Name: "physical_kvm_action", Description: "Send one bounded USB HID action to the enrolled physical PC. Payload {sessionId,kind,text?|key?|x?|y?|x1?|y1?|x2?|y2?|width?|height?|durationMs?|dx?|dy?|button?|delta?}; key accepts chords such as ctrl+l.",
		Schema: atvSchema(map[string]interface{}{
			"sessionId": map[string]interface{}{"type": "string"},
			"kind":      map[string]interface{}{"type": "string", "description": "text|key|tap|drag|move|click|wheel"},
		}), Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			var action map[string]any
			if err := json.Unmarshal(payload, &action); err != nil {
				return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			}
			sessionID, _ := action["sessionId"].(string)
			delete(action, "sessionId")
			seq, err := physicalKVM.action(c.Ctx, sessionID, action)
			if err != nil {
				return OpsResult{OK: false, Code: "kvm_action_refused", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: map[string]any{"sequence": seq}}
		},
	})
	registerOpsVerb(opsVerbSpec{
		Name: "physical_kvm_release_all", Description: "Immediately release every held keyboard/mouse control and close the M5Stack lease.",
		Schema: atvSchema(map[string]interface{}{}), Handler: func(c OpsContext, _ json.RawMessage) OpsResult {
			if err := physicalKVM.releaseAll(c.Ctx); err != nil {
				return OpsResult{OK: false, Code: "kvm_unreachable", Error: err.Error()}
			}
			return OpsResult{OK: true, Initial: map[string]any{"released": true}}
		},
	})
}

func physicalKVMStatusHandler(c OpsContext, _ json.RawMessage) OpsResult {
	status, err := physicalKVMFullStatus(c.Ctx)
	if err != nil {
		return OpsResult{OK: false, Code: "kvm_not_ready", Error: err.Error(), Initial: status}
	}
	return OpsResult{OK: true, Initial: status}
}

func physicalKVMFullStatus(ctx context.Context) (map[string]any, error) {
	input, inputErr := physicalKVM.status(ctx)
	devices := captureDevices()
	selected, selectionErr := physicalKVM.selectedCaptureDevice()
	capture := captureStream.status()
	running, _ := capture["running"].(bool)
	frameAge, hasFrameAge := capture["frameAgeMs"].(int64)
	videoReady := running && hasFrameAge && frameAge >= 0 && frameAge <= 3000
	captureState := "stopped"
	switch {
	case ffmpegPath() == "":
		captureState = "decoder_missing"
	case selectionErr != nil:
		if strings.Contains(selectionErr.Error(), "AMBIGUOUS") {
			captureState = "ambiguous"
		} else {
			captureState = "not_found"
		}
	case running && !videoReady:
		captureState = "stale"
	case videoReady:
		captureState = "ready"
	}
	inputReady := inputErr == nil && input.PairingState == "paired" && input.USBReady
	result := map[string]any{
		"target":       physicalKVMTargetID,
		"protocol":     physicalKVMProtocol,
		"ready":        videoReady && inputReady,
		"controlReady": videoReady && inputReady && input.Armed,
		"input":        input,
		"capture": map[string]any{
			"state": captureState, "selectedDevice": selected, "devices": devices,
			"running": running, "freshFrame": videoReady, "status": capture,
		},
	}
	if inputErr != nil {
		result["inputError"] = inputErr.Error()
	}
	if selectionErr != nil {
		result["captureError"] = selectionErr.Error()
	}
	if inputErr != nil {
		return result, inputErr
	}
	if ffmpegPath() == "" {
		return result, fmt.Errorf("KVM_CAPTURE_DECODER_MISSING: install ffmpeg on the Pi")
	}
	if selectionErr != nil {
		return result, selectionErr
	}
	return result, nil
}

func physicalKVMDoctorHandler(c OpsContext, _ json.RawMessage) OpsResult {
	result, err := runPhysicalKVMDoctor(c.Ctx)
	if err != nil {
		return OpsResult{OK: false, Code: "kvm_doctor_failed", Error: err.Error(), Initial: result}
	}
	return OpsResult{OK: true, Initial: result}
}

func runPhysicalKVMDoctor(ctx context.Context) (map[string]any, error) {
	result := map[string]any{"target": physicalKVMTargetID, "protocol": physicalKVMProtocol}
	status, bridgeErr := physicalKVM.status(ctx)
	result["input"] = status
	if bridgeErr != nil {
		result["inputError"] = bridgeErr.Error()
	}
	devices := captureDevices()
	result["captureDevices"] = devices
	result["ffmpeg"] = ffmpegPath() != ""
	if ffmpegPath() == "" {
		return result, fmt.Errorf("KVM_CAPTURE_DECODER_MISSING: install ffmpeg on the Pi")
	}
	if len(devices) == 0 {
		return result, fmt.Errorf("KVM_CAPTURE_NOT_FOUND: connect a UVC HDMI capture card to the Pi")
	}
	startedHere := !captureStream.running()
	if startedHere {
		device, selectErr := physicalKVM.selectedCaptureDevice()
		if selectErr != nil {
			return result, selectErr
		}
		if err := captureStream.start(device, 2, 640, 360, 10); err != nil {
			return result, fmt.Errorf("KVM_CAPTURE_OPEN_FAILED: %w", err)
		}
		defer captureStream.stop()
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if dims := captureFrameDims(); dims.Width > 0 {
			result["capture"] = map[string]any{"ok": true, "freshFrame": true, "width": dims.Width, "height": dims.Height, "status": captureStream.status()}
			if bridgeErr != nil {
				return result, bridgeErr
			}
			if !status.USBReady {
				return result, fmt.Errorf("KVM_USB_NOT_READY: connect M5Stack USB HID directly to the target PC")
			}
			return result, nil
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(75 * time.Millisecond):
		}
	}
	result["capture"] = map[string]any{"ok": false, "freshFrame": false, "status": captureStream.status()}
	return result, fmt.Errorf("KVM_CAPTURE_NO_FRAME: the capture device opened but did not decode a fresh HDMI frame")
}
