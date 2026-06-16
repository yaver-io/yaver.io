package main

// stream_audio.go — audio for the streaming plane. Video-only is fine for
// monitoring, but watching uydu yayını / a console / broadcasting needs sound.
// This adds (1) ALSA capture-device enumeration (so the user picks the capture
// card's audio input) and (2) the wiring the RTMP broadcaster uses to mux AAC
// audio alongside the video. WebRTC/MJPEG audio is a separate, larger pion-Opus
// effort (documented gap).
//
// Linux-first (the box is a Pi). On non-Linux the device list is empty and
// audio degrades to video-only.

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
)

// audioCaptureDevices lists ALSA capture cards from /proc/asound/cards (no
// external dep). Each entry maps to an ffmpeg `-f alsa -i hw:<index>,0` input.
func audioCaptureDevices() []map[string]interface{} {
	out := []map[string]interface{}{}
	if runtime.GOOS != "linux" {
		return out
	}
	b, err := os.ReadFile("/proc/asound/cards")
	if err != nil {
		return out
	}
	// Lines look like: " 1 [Capture        ]: USB-Audio - USB Capture Device"
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "[") {
			continue // continuation lines start indented with [
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		idx := fields[0]
		if _, err := os.Stat("/dev/snd/pcmC" + idx + "D0c"); err != nil {
			// no capture PCM on this card — skip (playback-only)
			continue
		}
		name := strings.TrimSpace(line)
		out = append(out, map[string]interface{}{
			"index":      idx,
			"name":       name,
			"alsaDevice": "hw:" + idx + ",0",
		})
	}
	return out
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "audio_devices",
		Description: "List ALSA audio capture devices on this box (the capture card's audio input) for muxing into an RTMP broadcast. Each entry's alsaDevice (hw:N,0) goes in stream_broadcast {audioDevice}.",
		Schema:      atvSchema(map[string]interface{}{}),
		Handler: func(c OpsContext, _ json.RawMessage) OpsResult {
			return OpsResult{OK: true, Initial: map[string]interface{}{"devices": audioCaptureDevices()}}
		},
	})

	// stream_status — one-call overview of the whole streaming plane for a
	// control UI: capture, scene, broadcast, live WebRTC encodes, pushed
	// sources, and quality locks.
	registerOpsVerb(opsVerbSpec{
		Name:        "stream_status",
		Description: "Overview of all streaming on this box: capture, scene compositor, RTMP broadcast, live WebRTC tiered encodes, pushed sources, and audio devices.",
		Schema:      atvSchema(map[string]interface{}{}),
		Handler: func(c OpsContext, _ json.RawMessage) OpsResult {
			return OpsResult{OK: true, Initial: map[string]interface{}{
				"capture":      captureStream.status(),
				"scene":        sceneComp.status(),
				"broadcast":    bcast.status(),
				"webrtcTiers":  sharedEncodeStatus(),
				"pushed":       listFreshPushed(),
				"audioDevices": audioCaptureDevices(),
				"ffmpeg":       ffmpegPath() != "",
			}}
		},
		AllowGuest: true,
	})
}
