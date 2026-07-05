package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ops_maps.go — provider-neutral maps/traffic open/search.
//
// Like media_open, this is URL-first: it opens the official Google/Yandex/Apple
// Maps/Waze web/app URL on the runtime. No scraping, no hidden API key, no
// unofficial provider control.

type mapsOpenPayload struct {
	Provider    string `json:"provider,omitempty"` // auto | google | yandex | apple | waze
	Query       string `json:"query,omitempty"`
	Origin      string `json:"origin,omitempty"`
	Destination string `json:"destination,omitempty"`
	Traffic     bool   `json:"traffic,omitempty"`
	Open        bool   `json:"open,omitempty"`
	OpenMode    string `json:"openMode,omitempty"`
	Surface     string `json:"surface,omitempty"`
}

type MapsOpenPlan struct {
	Provider         string `json:"provider"`
	Query            string `json:"query,omitempty"`
	Origin           string `json:"origin,omitempty"`
	Destination      string `json:"destination,omitempty"`
	Traffic          bool   `json:"traffic,omitempty"`
	URL              string `json:"url"`
	OpenURL          string `json:"openUrl"`
	OpenMode         string `json:"openMode"`
	Surface          string `json:"surface,omitempty"`
	Opened           bool   `json:"opened,omitempty"`
	BrowserSessionID string `json:"browserSessionId,omitempty"`
	Spoken           string `json:"spoken"`
	Note             string `json:"note,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "maps_open",
		Description: "Open a maps/traffic query on this runtime host. Supports Google Maps, Yandex Maps, Apple Maps, and Waze. Payload {provider?, query?, origin?, destination?, traffic?, open?, openMode?: system|browser|automation|selenium, surface?}.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider":    map[string]interface{}{"type": "string"},
				"query":       map[string]interface{}{"type": "string"},
				"origin":      map[string]interface{}{"type": "string"},
				"destination": map[string]interface{}{"type": "string"},
				"traffic":     map[string]interface{}{"type": "boolean"},
				"open":        map[string]interface{}{"type": "boolean"},
				"openMode":    map[string]interface{}{"type": "string"},
				"surface":     map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler: mapsOpenOpsHandler,
	})
}

func mapsOpenOpsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p mapsOpenPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	plan, err := buildMapsOpenPlan(p)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Open {
		sessionID, err := openMapsURLWithMode(c, plan.OpenURL, plan.OpenMode)
		if err != nil {
			return OpsResult{OK: false, Code: "open_failed", Error: err.Error(), Initial: plan}
		}
		plan.Opened = true
		plan.BrowserSessionID = sessionID
		plan.Spoken = mapsOpenedSpeech(plan)
	}
	return OpsResult{OK: true, Initial: plan}
}

func buildMapsOpenPlan(p mapsOpenPayload) (MapsOpenPlan, error) {
	provider := normalizeMapsProvider(p.Provider)
	if provider == "" || provider == "auto" {
		provider = "google"
	}
	query := strings.TrimSpace(p.Query)
	origin := strings.TrimSpace(p.Origin)
	dest := strings.TrimSpace(p.Destination)
	if query == "" && dest == "" {
		return MapsOpenPlan{}, fmt.Errorf("query or destination is required")
	}
	openURL, err := mapsProviderURL(provider, query, origin, dest, p.Traffic)
	if err != nil {
		return MapsOpenPlan{}, err
	}
	plan := MapsOpenPlan{
		Provider:    provider,
		Query:       query,
		Origin:      origin,
		Destination: dest,
		Traffic:     p.Traffic,
		URL:         openURL,
		OpenURL:     openURL,
		OpenMode:    normalizeMeetingOpenMode(p.OpenMode),
		Surface:     normalizeMeetingSurface(p.Surface),
		Spoken:      mapsFoundSpeech(provider, query, dest, p.Traffic),
		Note:        "Opens the official maps provider URL on the selected Yaver runtime. Navigation, live traffic, account state, and rerouting stay in the provider app/browser.",
	}
	return plan, nil
}

func normalizeMapsProvider(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return "auto"
	case "google", "google_maps", "google-maps", "maps":
		return "google"
	case "yandex", "yandex_maps", "yandex-maps":
		return "yandex"
	case "apple", "apple_maps", "apple-maps":
		return "apple"
	case "waze":
		return "waze"
	default:
		return strings.ToLower(strings.TrimSpace(s))
	}
}

func mapsProviderURL(provider, query, origin, destination string, traffic bool) (string, error) {
	query = strings.TrimSpace(query)
	origin = strings.TrimSpace(origin)
	destination = strings.TrimSpace(destination)
	search := query
	if destination != "" {
		search = destination
	}
	if traffic && search != "" && !strings.Contains(strings.ToLower(search), "traffic") {
		search += " traffic"
	}
	switch normalizeMapsProvider(provider) {
	case "google":
		v := url.Values{}
		if destination != "" {
			v.Set("api", "1")
			if origin != "" {
				v.Set("origin", origin)
			}
			v.Set("destination", destination)
			v.Set("travelmode", "driving")
			return "https://www.google.com/maps/dir/?" + v.Encode(), nil
		}
		v.Set("api", "1")
		v.Set("query", search)
		return "https://www.google.com/maps/search/?" + v.Encode(), nil
	case "yandex":
		v := url.Values{}
		v.Set("text", search)
		if traffic {
			v.Set("l", "map,trf")
		}
		return "https://yandex.com/maps/?" + v.Encode(), nil
	case "apple":
		v := url.Values{}
		if destination != "" {
			if origin != "" {
				v.Set("saddr", origin)
			}
			v.Set("daddr", destination)
			v.Set("dirflg", "d")
		} else {
			v.Set("q", search)
		}
		return "https://maps.apple.com/?" + v.Encode(), nil
	case "waze":
		v := url.Values{}
		v.Set("q", search)
		if traffic {
			v.Set("navigate", "yes")
		}
		return "https://www.waze.com/live-map/?" + v.Encode(), nil
	default:
		return "", fmt.Errorf("unsupported maps provider %q", provider)
	}
}

func openMapsURLWithMode(c OpsContext, raw, mode string) (string, error) {
	switch normalizeMeetingOpenMode(mode) {
	case "system":
		return "", openMediaURL(raw)
	case "browser":
		return openMapsURLInRuntimeBrowser(c, raw)
	default:
		return "", fmt.Errorf("unsupported maps openMode %q", mode)
	}
}

func openMapsURLInRuntimeBrowser(c OpsContext, raw string) (string, error) {
	u, err := sanitizePublicMediaURL(raw)
	if err != nil {
		return "", err
	}
	if c.Server == nil {
		return "", fmt.Errorf("browser openMode requires a runtime HTTP server context")
	}
	if c.Server.browserMgr == nil {
		c.Server.browserMgr = NewBrowserManager()
	}
	c.Server.browserMgr.ensureVPM(c.Server.vibePreviewMgr, ActiveVibePreviewManager())
	sessionID := fmt.Sprintf("maps-%d", time.Now().UnixNano())
	if err := c.Server.browserMgr.OpenSessionWithProfile(sessionID, true, "", profileDirFor("maps")); err != nil {
		return "", err
	}
	if _, err := c.Server.browserMgr.Navigate(sessionID, u); err != nil {
		return sessionID, err
	}
	return sessionID, nil
}

func mapsFoundSpeech(provider, query, destination string, traffic bool) string {
	label := mapsProviderLabel(provider)
	target := strings.TrimSpace(destination)
	if target == "" {
		target = strings.TrimSpace(query)
	}
	if traffic {
		return fmt.Sprintf("I found %s traffic for %s.", label, target)
	}
	return fmt.Sprintf("I found %s for %s.", label, target)
}

func mapsOpenedSpeech(plan MapsOpenPlan) string {
	label := mapsProviderLabel(plan.Provider)
	target := strings.TrimSpace(plan.Destination)
	if target == "" {
		target = strings.TrimSpace(plan.Query)
	}
	if plan.Traffic {
		return fmt.Sprintf("Opening %s traffic for %s.", label, target)
	}
	return fmt.Sprintf("Opening %s for %s.", label, target)
}

func mapsProviderLabel(provider string) string {
	switch normalizeMapsProvider(provider) {
	case "google":
		return "Google Maps"
	case "yandex":
		return "Yandex Maps"
	case "apple":
		return "Apple Maps"
	case "waze":
		return "Waze"
	default:
		return "maps"
	}
}
