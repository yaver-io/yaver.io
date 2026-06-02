package main

// shots_http.go — POST /shots/upload : the on-device capture endpoint
// (Engine 2). The feedback SDK walks the app's routes, screenshots each
// with react-native-view-shot, and uploads the base64 frames here. The
// agent converts + resizes them to the App Store size and runs the same
// App Store Connect backend as `yaver shots` (upload → metadata → submit).
//
// SDK-authed (same-user envelope, like /feedback). Privacy: frames are
// written to the local shots disk root and never forwarded to Convex.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type shotsUploadFrame struct {
	Route    string `json:"route"`
	Base64   string `json:"base64"`
	MimeType string `json:"mimeType"`
}

type shotsUploadRequest struct {
	App      string             `json:"app"`
	BundleID string             `json:"bundleId"`
	Locale   string             `json:"locale"`
	Submit   bool               `json:"submit"`
	Frames   []shotsUploadFrame `json:"frames"`
}

// handleShotsUpload receives on-device screenshot frames and runs the ASC
// backend. Synchronous: the SDK awaits the result.
func (s *HTTPServer) handleShotsUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20) // 64MB of frames max
	var req shotsUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	if len(req.Frames) == 0 {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "no frames"})
		return
	}
	if req.Locale == "" {
		req.Locale = "en-US"
	}
	if req.App == "" {
		req.App = "app"
	}

	// Resolve bundle id: explicit wins; else resolve the project path from
	// the app name and read app.json.
	bundleID := strings.TrimSpace(req.BundleID)
	if bundleID == "" {
		if _, path, err := resolveDeployStackPath(req.App, "", ""); err == nil {
			bundleID = readBundleIDFromAppJSON(path)
		}
	}
	if bundleID == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "could not determine bundle id — pass bundleId"})
		return
	}

	raw, upload, root, err := shotsRunDir(req.App)
	if err != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(raw, 0o755); err != nil || os.MkdirAll(upload, 0o755) != nil {
		jsonReply(w, http.StatusInternalServerError, map[string]string{"error": "mkdir run dir"})
		return
	}

	// Decode each frame → raw file → sips convert+resample → upload/<n>.png.
	written := 0
	for i, fr := range req.Frames {
		data, derr := base64.StdEncoding.DecodeString(fr.Base64)
		if derr != nil || len(data) == 0 {
			continue
		}
		ext := "jpg"
		if strings.Contains(fr.MimeType, "png") {
			ext = "png"
		}
		name := sanitizeShotName(fr.Route, i)
		rawPath := filepath.Join(raw, fmt.Sprintf("%s.%s", name, ext))
		if err := os.WriteFile(rawPath, data, 0o644); err != nil {
			continue
		}
		outPath := filepath.Join(upload, name+".png")
		// sips: convert to png + resample to App Store size (height width).
		if out, err := runCmd("sips", "-s", "format", "png",
			"--resampleHeightWidth", fmt.Sprint(shotsTargetH), fmt.Sprint(shotsTargetW),
			rawPath, "--out", outPath); err != nil {
			fmt.Fprintf(os.Stderr, "[shots-upload] sips %s: %s — %v\n", name, out, err)
			continue
		}
		written++
	}
	if written == 0 {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "no frames decoded"})
		return
	}

	// ASC backend: upload screenshots, then optional metadata + submit.
	if err := ascUploadScreenshots(bundleID, upload, req.Locale); err != nil {
		jsonReply(w, http.StatusBadGateway, map[string]interface{}{
			"ok": false, "uploaded": 0, "error": "screenshot upload: " + err.Error(), "runDir": root,
		})
		return
	}

	resp := map[string]interface{}{"ok": true, "uploaded": written, "runDir": root}
	if req.Submit {
		if err := ascSetMetadata(bundleID, ""); err != nil {
			resp["metadataError"] = err.Error()
		}
		submitted, serr := ascSubmitForReview(bundleID, "")
		if serr != nil {
			resp["submitError"] = serr.Error()
		} else {
			resp["submitted"] = submitted
			resp["staged"] = !submitted
			if submitted {
				resp["message"] = "submitted for App Store review"
			} else {
				resp["message"] = "staged — one manual tap remains in App Store Connect"
			}
		}
	} else {
		resp["message"] = fmt.Sprintf("%d screenshots uploaded to App Store Connect", written)
	}
	jsonReply(w, http.StatusOK, resp)
}

// sanitizeShotName normalizes a route into an ordered screenshot filename.
func sanitizeShotName(route string, index int) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			return r
		default:
			return '_'
		}
	}, route)
	// Collapse runs of '_' (e.g. "(tabs)/" → "_tabs_" not "_tabs__").
	for strings.Contains(clean, "__") {
		clean = strings.ReplaceAll(clean, "__", "_")
	}
	clean = strings.Trim(clean, "_")
	if clean == "" {
		clean = "screen"
	}
	// If the SDK already prefixed NN_, keep it; else add our own.
	if len(clean) >= 3 && clean[2] == '_' && clean[0] >= '0' && clean[0] <= '9' {
		return clean
	}
	return fmt.Sprintf("%02d_%s", index+1, clean)
}
