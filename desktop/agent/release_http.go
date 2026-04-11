package main

// release_http.go — HTTP endpoints for the self-hosted OTA lane.
// End-user apps poll these through the existing P2P relay, so
// no inbound ports, no vendor, no central server. Auth reuses
// the SDK token middleware so releases are scoped to the dev's
// own devices and any guest who's been explicitly allowed.
//
// Routes (registered in httpserver.go next to the other /releases*
// aware handlers):
//
//   GET /releases/list?channel=<ch>                      — full manifest
//   GET /releases/latest?channel=<ch>&device=<id>        — what this device should run
//   GET /releases/bundle?channel=<ch>&semver=<v>         — streams bundle.hbc bytes
//
// Every response is JSON-first. The bundle stream sets
// Content-Type: application/octet-stream and includes an
// X-Yaver-Bundle-Metadata header with the BundleMetadata JSON
// so the end-user side can cross-check bytes before loading.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// handleReleaseList returns the full manifest for a channel.
func (s *HTTPServer) handleReleaseList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		channel = "production"
	}
	m, err := loadManifest(channel)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"manifest": m,
	})
}

// releaseLatestResponse is the JSON shape end-user apps consume.
// It's deliberately tiny so a slow-3G cellular poll is cheap.
type releaseLatestResponse struct {
	OK             bool          `json:"ok"`
	Channel        string        `json:"channel"`
	Semver         string        `json:"semver,omitempty"`
	Size           int64         `json:"size,omitempty"`
	MD5            string        `json:"md5,omitempty"`
	HermesBC       int           `json:"hermesBcVersion,omitempty"`
	PublishedAt    string        `json:"publishedAt,omitempty"`
	BundleURL      string        `json:"bundleUrl,omitempty"`      // same-origin path
	RolloutPercent int           `json:"rolloutPercent"`
	InRollout      bool          `json:"inRollout"`
	Reason         string        `json:"reason,omitempty"` // "in-rollout" | "not-in-rollout" | "no-latest" | ...
	Previous       *ReleaseEntry `json:"previous,omitempty"`
}

// handleReleaseLatest is the polling endpoint. Arguments:
//
//	channel (required)
//	device  (optional but recommended) — stable per-device ID
//	        used for rollout bucketing. Without it, we default
//	        to "in" if rolloutPercent > 0.
//
// The response is cacheable for ~30s (mobile polls on cold
// start; cache-busting is controlled by the manifest's
// updatedAt).
func (s *HTTPServer) handleReleaseLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		channel = "production"
	}
	deviceID := r.URL.Query().Get("device")

	m, err := loadManifest(channel)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := releaseLatestResponse{
		OK:             true,
		Channel:        channel,
		RolloutPercent: m.RolloutPercent,
	}
	if m.Latest == "" {
		resp.Reason = "no-latest"
		jsonReply(w, http.StatusOK, resp)
		return
	}
	// Find the latest entry in the ledger.
	var latest *ReleaseEntry
	for i := range m.Releases {
		if m.Releases[i].Semver == m.Latest {
			latest = &m.Releases[i]
			break
		}
	}
	if latest == nil {
		// Manifest drift — latest points at a missing record.
		// Don't crash the cold-start poll; return no-latest so
		// the app keeps its embedded bundle.
		resp.Reason = "no-latest"
		jsonReply(w, http.StatusOK, resp)
		return
	}

	// Rollout gate. If no device ID was provided, we optimistically
	// include the caller in the rollout so ad-hoc curl testing
	// still returns the bundle.
	inRollout := releaseRolloutHit(deviceID, m.RolloutPercent)
	if deviceID == "" && m.RolloutPercent > 0 {
		inRollout = true
	}
	resp.InRollout = inRollout
	resp.Semver = latest.Semver
	resp.Size = latest.Size
	resp.MD5 = latest.MD5
	resp.HermesBC = latest.HermesBCVersion
	resp.PublishedAt = latest.PublishedAt
	resp.BundleURL = fmt.Sprintf("/releases/bundle?channel=%s&semver=%s",
		urlQueryEscape(channel), urlQueryEscape(latest.Semver))
	if inRollout {
		resp.Reason = "in-rollout"
	} else {
		resp.Reason = "not-in-rollout"
		// Offer the previous release (if any) so a device
		// that was previously on a rolled-back version still
		// gets something reasonable.
		if len(m.Releases) > 1 {
			prev := m.Releases[1]
			resp.Previous = &prev
		}
	}

	// 30s cache — rollout state is stable within a minute, and
	// the manifest's updatedAt forces a bust on real changes.
	w.Header().Set("Cache-Control", "private, max-age=30")
	jsonReply(w, http.StatusOK, resp)
}

// handleReleaseBundle streams a specific semver's bundle.hbc.
// The end-user app already validated the latest pointer via
// /releases/latest; this endpoint exists so the app can pull the
// bytes on a separate request, which is friendlier to the P2P
// relay's HTTP timing.
func (s *HTTPServer) handleReleaseBundle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	channel := r.URL.Query().Get("channel")
	if channel == "" {
		channel = "production"
	}
	semver := r.URL.Query().Get("semver")
	if semver == "" {
		http.Error(w, "semver required", http.StatusBadRequest)
		return
	}

	dir, err := channelDir(channel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Path containment: the resolved absolute path must still
	// live under the channel dir — otherwise a `../` in semver
	// could escape. sanitizeChannelName already strips most of
	// that, but defense in depth.
	safeSemver := strings.ReplaceAll(semver, "..", "")
	safeSemver = strings.ReplaceAll(safeSemver, "/", "")
	bundle := filepath.Join(dir, safeSemver, "bundle.hbc")
	info, err := os.Stat(bundle)
	if err != nil {
		http.Error(w, "release not found", http.StatusNotFound)
		return
	}

	// Cross-check the manifest record so the metadata we emit
	// matches what the publish step recorded. If the record is
	// missing we still serve the bytes — the mobile app will
	// validate the BC version anyway.
	var meta *ReleaseEntry
	if m, merr := loadManifest(channel); merr == nil {
		for i := range m.Releases {
			if m.Releases[i].Semver == safeSemver {
				meta = &m.Releases[i]
				break
			}
		}
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	if meta != nil {
		md := BundleMetadata{
			Version:         1,
			Size:            meta.Size,
			MD5:             meta.MD5,
			HermesBCVersion: meta.HermesBCVersion,
			ModuleName:      "", // published bundles don't carry a module name yet
			Format:          "hbc",
		}
		w.Header().Set("X-Yaver-Bundle-Metadata", md.JSON())
	}
	// 1-hour cache — bundles are immutable per semver, so the
	// ETag is effectively the semver itself. A new release gets
	// a new URL.
	w.Header().Set("Cache-Control", "private, max-age=3600, immutable")
	w.Header().Set("ETag", fmt.Sprintf(`"%s-%s"`, safeSemver, time.Now().Format("20060102")))
	http.ServeFile(w, r, bundle)
}

// urlQueryEscape is a tiny local wrapper so the handler doesn't
// pull in net/url just for one call site.
func urlQueryEscape(s string) string {
	b := strings.Builder{}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '.' || r == '_' || r == '~':
			b.WriteRune(r)
		default:
			b.WriteString(fmt.Sprintf("%%%02X", r))
		}
	}
	return b.String()
}

// debug helper — unused directly but kept so a future handler can
// fail open with a clean JSON response on missing manifests.
func (s *HTTPServer) releaseNoManifestResponse(w http.ResponseWriter, channel string) {
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"channel": channel,
		"reason":  "no-latest",
	})
	_ = json.NewEncoder(w)
}
