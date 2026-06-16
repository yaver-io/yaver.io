package main

// stream_profile.go — the adaptive "watch layer" foundation (Part H of
// docs/yaver-appletv-remote-control.md). Deliver min(source, link, sink, lock):
// a watcher on a phone/glass/cellular shouldn't make the box encode 1080p30.
// A StreamProfile is the resolved encode target; profileForConstraints computes
// it from the viewer's declared capabilities (device class, render size, net),
// honoring a per-source user lock. The encode points (stream_webrtc, capture,
// scene, broadcast) read the resolved profile.
//
// Tiers are deliberately few (cheap fan-out, §H.6). Auto = compute the floor +
// (later) adapt live; lock = pin a tier.

import (
	"encoding/json"
	"strings"
	"sync"
)

type StreamProfile struct {
	Name        string `json:"name"`
	MaxWidth    int    `json:"maxWidth"`    // 0 = source (no downscale)
	MaxHeight   int    `json:"maxHeight"`   // 0 = source
	FPS         int    `json:"fps"`         // 0 = source/native
	JPEGQuality int    `json:"jpegQuality"` // ffmpeg -q:v 2(best)–31(worst); MJPEG/snapshot
	BitrateKbps int    `json:"bitrateKbps"` // 0 = CRF/auto; WebRTC/RTMP encode
}

var streamProfileTiers = map[string]StreamProfile{
	"source":   {Name: "source", MaxWidth: 0, MaxHeight: 0, FPS: 0, JPEGQuality: 4, BitrateKbps: 0},
	"high":     {Name: "high", MaxWidth: 1920, MaxHeight: 1080, FPS: 30, JPEGQuality: 5, BitrateKbps: 4000},
	"balanced": {Name: "balanced", MaxWidth: 1280, MaxHeight: 720, FPS: 25, JPEGQuality: 7, BitrateKbps: 1800},
	"saver":    {Name: "saver", MaxWidth: 854, MaxHeight: 480, FPS: 15, JPEGQuality: 12, BitrateKbps: 600},
}

var streamTierOrder = []string{"saver", "balanced", "high", "source"}

func stepDownTier(name string) string {
	for i, t := range streamTierOrder {
		if t == name && i > 0 {
			return streamTierOrder[i-1]
		}
	}
	return "saver"
}

// profileForConstraints computes the encode target. `locked` (a tier name, or
// "" / "auto") overrides the computation. Then we pick a base tier by device
// class, step down on cellular, and clamp dimensions to the reported render
// size so we never send more pixels than the sink shows.
func profileForConstraints(deviceClass string, wPx, hPx int, netType, locked string) StreamProfile {
	locked = strings.ToLower(strings.TrimSpace(locked))
	if p, ok := streamProfileTiers[locked]; ok { // lock wins
		return p
	}
	base := "balanced"
	switch strings.ToLower(strings.TrimSpace(deviceClass)) {
	case "tv", "projector":
		base = "high"
	case "glass":
		base = "saver"
	}
	if n := strings.ToLower(netType); strings.Contains(n, "cell") || strings.Contains(n, "4g") || strings.Contains(n, "3g") {
		base = stepDownTier(base)
	}
	p := streamProfileTiers[base]
	// Never exceed the reported render size (don't pay for unseen pixels).
	if wPx > 0 && (p.MaxWidth == 0 || wPx < p.MaxWidth) {
		p.MaxWidth = wPx
	}
	if hPx > 0 && (p.MaxHeight == 0 || hPx < p.MaxHeight) {
		p.MaxHeight = hPx
	}
	p.Name = base
	return p
}

// ---- per-source lock store --------------------------------------------------

type sourceQuality struct {
	Profile string `json:"profile"` // tier name or "auto"
	Locked  bool   `json:"locked"`
}

var (
	sourceQualityMu    sync.Mutex
	sourceQualityStore = map[string]sourceQuality{}
)

func setSourceQuality(source, profile string, lock bool) {
	sourceQualityMu.Lock()
	sourceQualityStore[source] = sourceQuality{Profile: profile, Locked: lock}
	sourceQualityMu.Unlock()
}

func getSourceQuality(source string) sourceQuality {
	sourceQualityMu.Lock()
	defer sourceQualityMu.Unlock()
	if q, ok := sourceQualityStore[source]; ok {
		return q
	}
	return sourceQuality{Profile: "auto", Locked: false}
}

// lockedProfileFor returns the tier name to force for a source, or "" if the
// source isn't locked (so the viewer's constraints drive it).
func lockedProfileFor(source string) string {
	q := getSourceQuality(source)
	if q.Locked && q.Profile != "" && q.Profile != "auto" {
		return q.Profile
	}
	return ""
}

// activeEncodeProfile holds the resolved profile per source, set by the offer
// handler before the encoder pump starts; SpawnCapture reads it.
var activeEncodeProfile sync.Map // source -> StreamProfile

func setActiveEncodeProfile(source string, p StreamProfile) { activeEncodeProfile.Store(source, p) }
func getActiveEncodeProfile(source string) StreamProfile {
	if v, ok := activeEncodeProfile.Load(source); ok {
		return v.(StreamProfile)
	}
	return streamProfileTiers["balanced"]
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "stream_quality",
		Description: "Set/lock the quality profile for a source. Payload {source, profile, lock?}. profile = source|high|balanced|saver|auto. lock=true pins it (disables auto-adaptation for that source); lock=false lets viewer capability decide.",
		Schema: atvSchema(map[string]interface{}{
			"source":  map[string]interface{}{"type": "string"},
			"profile": map[string]interface{}{"type": "string"},
			"lock":    map[string]interface{}{"type": "boolean"},
		}),
		Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			var p struct {
				Source  string `json:"source"`
				Profile string `json:"profile"`
				Lock    bool   `json:"lock"`
			}
			if err := json.Unmarshal(payload, &p); err != nil || p.Source == "" {
				return OpsResult{OK: false, Code: "bad_payload", Error: "source required"}
			}
			if p.Profile == "" {
				p.Profile = "auto"
			}
			if p.Profile != "auto" {
				if _, ok := streamProfileTiers[p.Profile]; !ok {
					return OpsResult{OK: false, Code: "bad_payload", Error: "profile must be source|high|balanced|saver|auto"}
				}
			}
			setSourceQuality(p.Source, p.Profile, p.Lock)
			return OpsResult{OK: true, Initial: map[string]interface{}{"source": p.Source, "profile": p.Profile, "locked": p.Lock, "tiers": streamProfileTiers}}
		},
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "stream_quality_get",
		Description: "Get the quality lock for a source + the available tiers. Payload {source}.",
		Schema:      atvSchema(map[string]interface{}{"source": map[string]interface{}{"type": "string"}}),
		Handler: func(c OpsContext, payload json.RawMessage) OpsResult {
			var p struct {
				Source string `json:"source"`
			}
			_ = json.Unmarshal(payload, &p)
			q := getSourceQuality(p.Source)
			return OpsResult{OK: true, Initial: map[string]interface{}{"source": p.Source, "quality": q, "tiers": streamProfileTiers, "active": getActiveEncodeProfile(p.Source)}}
		},
		AllowGuest: true,
	})
}
