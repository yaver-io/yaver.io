package main

// ops_camera.go — security-camera connector (docs/yaver-single-kumanda.md §10b).
// Yaver-as-local-hub: register an own IP camera by its RTSP URL (Hikvision and
// most cams are RTSP/ONVIF-open), then pull snapshots via ffmpeg — REUSING the
// capture.go ffmpeg path. A spare phone running the Yaver app is also a camera
// node (the robot_camera / ExternalCamera push path); this verb family covers
// the RTSP/ONVIF side.
//
// Privacy is strict here (own cameras only): URLs (which carry credentials) live
// in the VAULT, never Convex; feeds stay local; one pull at a time, no swarm.
// AI-watch (local detect → optional LLM) layers on top of camera_snapshot.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const cameraVaultProject = "cameras"

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "camera_add",
		Description: "Register an IP camera. Payload {id, name?, url}. url is an RTSP/HTTP stream (e.g. rtsp://user:pass@ip:554/Streaming/Channels/101). Stored in the vault (credentials never leave the box).",
		Schema: map[string]interface{}{"type": "object", "required": []string{"id", "url"}, "properties": map[string]interface{}{
			"id":   map[string]interface{}{"type": "string"},
			"name": map[string]interface{}{"type": "string"},
			"url":  map[string]interface{}{"type": "string"},
		}},
		Handler: cameraAddHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "camera_list",
		Description: "List registered cameras (id + name only — never the credentialed URL).",
		Schema:      map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Handler:     cameraListHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "camera_remove",
		Description: "Remove a registered camera. Payload {id}.",
		Schema:      map[string]interface{}{"type": "object", "required": []string{"id"}, "properties": map[string]interface{}{"id": map[string]interface{}{"type": "string"}}},
		Handler:     cameraRemoveHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "camera_snapshot",
		Description: "Grab one JPEG frame from a camera via ffmpeg. Payload {id} (registered) or {url} (ad-hoc). Returns {image_b64, mime}. Feed the image to the model for AI-watch (motion/object/LLM).",
		Schema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
			"id":  map[string]interface{}{"type": "string"},
			"url": map[string]interface{}{"type": "string"},
		}},
		Handler: cameraSnapshotHandler,
	})
}

type cameraRecord struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

func cameraSave(rec cameraRecord) error {
	vs, err := openVaultOptional()
	if err != nil {
		return fmt.Errorf("vault unavailable: %w", err)
	}
	b, _ := json.Marshal(rec)
	return vs.Set(VaultEntry{
		Project:  cameraVaultProject,
		Name:     rec.ID,
		Category: "custom",
		Value:    string(b),
		Notes:    "Camera — " + rec.Name,
	})
}

func cameraGet(id string) (cameraRecord, bool) {
	vs, err := openVaultOptional()
	if err != nil {
		return cameraRecord{}, false
	}
	e, gerr := vs.Get(cameraVaultProject, id)
	if gerr != nil || e == nil || e.Value == "" {
		return cameraRecord{}, false
	}
	var rec cameraRecord
	if json.Unmarshal([]byte(e.Value), &rec) != nil {
		return cameraRecord{}, false
	}
	return rec, true
}

func cameraAddHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var rec cameraRecord
	if err := json.Unmarshal(payload, &rec); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	rec.ID, rec.URL = strings.TrimSpace(rec.ID), strings.TrimSpace(rec.URL)
	if rec.ID == "" || rec.URL == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "id and url are required"}
	}
	if err := cameraSave(rec); err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"camera": rec.ID}}
}

func cameraListHandler(c OpsContext, payload json.RawMessage) OpsResult {
	vs, err := openVaultOptional()
	if err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	type item struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	out := []item{}
	for _, sum := range vs.List(cameraVaultProject) {
		rec, ok := cameraGet(sum.Name)
		if !ok {
			continue
		}
		out = append(out, item{ID: rec.ID, Name: rec.Name})
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"cameras": out}}
}

func cameraRemoveHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	vs, err := openVaultOptional()
	if err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	if derr := vs.Delete(cameraVaultProject, strings.TrimSpace(p.ID)); derr != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: derr.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"removed": p.ID}}
}

func cameraSnapshotHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	url := strings.TrimSpace(p.URL)
	if url == "" && strings.TrimSpace(p.ID) != "" {
		rec, ok := cameraGet(strings.TrimSpace(p.ID))
		if !ok {
			return OpsResult{OK: false, Code: "not_found", Error: "camera not found: " + p.ID}
		}
		url = rec.URL
	}
	if url == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "id or url is required"}
	}
	jpeg, err := cameraGrabFrame(c.Ctx, url)
	if err != nil {
		return OpsResult{OK: false, Code: "remote_error", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"image_b64": base64.StdEncoding.EncodeToString(jpeg),
		"mime":      "image/jpeg",
		"bytes":     len(jpeg),
	}}
}

// cameraGrabFrame pulls a single JPEG frame from an RTSP/HTTP source via ffmpeg
// (reuses ffmpegPath from capture.go). TCP transport is forced for reliability
// over flaky RTSP-over-UDP.
func cameraGrabFrame(ctx context.Context, url string) ([]byte, error) {
	ff := ffmpegPath()
	if ff == "" {
		return nil, fmt.Errorf("ffmpeg not installed — needed to grab camera frames")
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{"-y", "-loglevel", "error"}
	if strings.HasPrefix(strings.ToLower(url), "rtsp://") {
		args = append(args, "-rtsp_transport", "tcp")
	}
	args = append(args, "-i", url, "-frames:v", "1", "-q:v", "3", "-f", "image2", "-")
	cmd := exec.CommandContext(cctx, ff, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg snapshot failed: %v", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no frame captured (source dark or unreachable)")
	}
	return out, nil
}
