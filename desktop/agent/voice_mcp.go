package main

// voice_mcp.go — P3 of the n2n plan: the two missing MCP verbs that
// let a runner start STT on a chosen surface and cast TTS to a chosen
// surface. Agent-side wiring only — the actual audio-capture /
// speech-out bindings are per-client handoffs (RN core is ready;
// tvOS/watch/Wear native bridges are follow-on work).
//
// Both verbs ride the same BlackBoxCommand pipe device_broadcast_command
// uses (mcp_device_broadcast.go), so we don't introduce a new
// transport. Client SDK listeners react to command == "voice_listen_start"
// / "voice_speak" and drive the local mic / TTS.

import (
	"strings"
)

// voiceListenStartArgs is the payload accepted by voice_listen_start.
// device is required — this verb only makes sense as a directed
// command (starting STT on every attached surface would be a mess).
type voiceListenStartArgs struct {
	Device string `json:"device"`
	// Provider hints the client which STT backend to prefer
	// ("whisper", "expo-speech-recognition", "web-speech-api", ...).
	// Empty = client default from its own settings.
	Provider string `json:"provider,omitempty"`
	// SessionID lets a runner tie the microphone session to a
	// runtime_create session so voice intents can drive runtime_control
	// on the same target. Optional — clients that don't care can drop it.
	SessionID string `json:"sessionId,omitempty"`
}

// voiceSpeakArgs is the payload accepted by voice_speak. Text is
// required; device is optional (empty = broadcast to every surface
// that has TTS bound, matching device_broadcast_command semantics).
type voiceSpeakArgs struct {
	Device string `json:"device,omitempty"`
	Text   string `json:"text"`
	// Voice + rate are hints the client can respect if its TTS
	// engine supports them. Empty = engine default.
	Voice string  `json:"voice,omitempty"`
	Rate  float64 `json:"rate,omitempty"`
	// RenderOn is Axis 3: a runner on the car may say `voice_speak`
	// {device: car, renderOn: phone} to play the audio on the phone
	// instead. Client sinks look at this to decide "am I the target
	// of a cast?" — full presence-based routing lands in P5.
	RenderOn string `json:"renderOn,omitempty"`
}

// runVoiceListenStart broadcasts a `voice_listen_start` BlackBox
// command to the target device. Split from the MCP wrapper so tests
// can drive it with a constructed BlackBoxManager (mirrors
// runDeviceBroadcastCommand).
func runVoiceListenStart(mgr *BlackBoxManager, args voiceListenStartArgs) map[string]interface{} {
	device := strings.TrimSpace(args.Device)
	if device == "" {
		return map[string]interface{}{"ok": false, "mode": "no_device", "error": "device is required — voice_listen_start is a directed command"}
	}
	if mgr == nil {
		return map[string]interface{}{"ok": false, "mode": "no_blackbox", "error": "agent has no BlackBox manager — pair the device first"}
	}
	data := map[string]interface{}{}
	if args.Provider != "" {
		data["provider"] = args.Provider
	}
	if args.SessionID != "" {
		data["sessionId"] = args.SessionID
	}
	reached := mgr.SendCommandToDevice(device, BlackBoxCommand{Command: "voice_listen_start", Data: data})
	return map[string]interface{}{
		"ok":             true,
		"mode":           "scoped",
		"targetDeviceId": device,
		"reachedSession": reached,
		"note":           "Client SDK on the target device must bind an AudioCaptureAdapter to react — RN core is ready; native tvOS/watch/Wear bridges land with client handoffs.",
	}
}

// runVoiceSpeak broadcasts a `voice_speak` BlackBox command. Empty
// device = broadcast (matches device_broadcast_command).
func runVoiceSpeak(mgr *BlackBoxManager, args voiceSpeakArgs) map[string]interface{} {
	text := strings.TrimSpace(args.Text)
	if text == "" {
		return map[string]interface{}{"ok": false, "mode": "no_text", "error": "text is required"}
	}
	if mgr == nil {
		return map[string]interface{}{"ok": false, "mode": "no_blackbox", "error": "agent has no BlackBox manager — pair a device first"}
	}
	data := map[string]interface{}{"text": text}
	if args.Voice != "" {
		data["voice"] = args.Voice
	}
	if args.Rate > 0 {
		data["rate"] = args.Rate
	}
	if args.RenderOn != "" {
		data["renderOn"] = args.RenderOn
	}
	cmd := BlackBoxCommand{Command: "voice_speak", Data: data}
	device := strings.TrimSpace(args.Device)
	if device != "" {
		reached := mgr.SendCommandToDevice(device, cmd)
		return map[string]interface{}{
			"ok":             true,
			"mode":           "scoped",
			"targetDeviceId": device,
			"reachedSession": reached,
		}
	}
	mgr.BroadcastCommand(cmd)
	return map[string]interface{}{"ok": true, "mode": "broadcast"}
}

func (s *HTTPServer) mcpVoiceListenStart(args voiceListenStartArgs) interface{} {
	return runVoiceListenStart(s.blackboxMgr, args)
}
func (s *HTTPServer) mcpVoiceSpeak(args voiceSpeakArgs) interface{} {
	return runVoiceSpeak(s.blackboxMgr, args)
}
