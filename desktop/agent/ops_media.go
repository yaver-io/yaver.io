package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ops_media.go — provider-neutral media open/search for constrained surfaces.
//
// This intentionally starts with URL construction + runtime opening only. It
// does not scrape YouTube/Twitch/Kick, does not require provider OAuth, and
// does not pretend to control playback inside third-party apps. Car/watch/TV
// can ask Yaver to open the official web/app URL on the chosen runtime.

type mediaOpenPayload struct {
	Provider string `json:"provider,omitempty"` // auto | youtube | twitch | kick | vimeo | spotify | apple_music | web
	Query    string `json:"query,omitempty"`
	URL      string `json:"url,omitempty"`
	Live     bool   `json:"live,omitempty"`
	Open     bool   `json:"open,omitempty"`
	OpenMode string `json:"openMode,omitempty"` // system | browser | automation | selenium
	Surface  string `json:"surface,omitempty"`  // car | watch | tv | mobile | mcp | cli
}

type MediaOpenPlan struct {
	Provider         string `json:"provider"`
	Query            string `json:"query,omitempty"`
	URL              string `json:"url"`
	OpenURL          string `json:"openUrl"`
	OpenMode         string `json:"openMode"`
	Surface          string `json:"surface,omitempty"`
	Live             bool   `json:"live,omitempty"`
	Opened           bool   `json:"opened,omitempty"`
	BrowserSessionID string `json:"browserSessionId,omitempty"`
	Spoken           string `json:"spoken"`
	Note             string `json:"note,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "media_open",
		Description: "Open or search a media provider on this runtime host. Supports YouTube, Twitch, Kick, Vimeo, Spotify, Apple Music, and generic URLs. Payload {provider?, query?, url?, live?, open?, openMode?: system|browser|automation|selenium, surface?}. No scraping or provider credentials required.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider": map[string]interface{}{"type": "string"},
				"query":    map[string]interface{}{"type": "string"},
				"url":      map[string]interface{}{"type": "string"},
				"live":     map[string]interface{}{"type": "boolean"},
				"open":     map[string]interface{}{"type": "boolean"},
				"openMode": map[string]interface{}{"type": "string"},
				"surface":  map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler: mediaOpenOpsHandler,
	})
}

func mediaOpenOpsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p mediaOpenPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	plan, err := buildMediaOpenPlan(p)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Open {
		sessionID, err := openMediaURLWithMode(c, plan.OpenURL, plan.OpenMode)
		if err != nil {
			return OpsResult{OK: false, Code: "open_failed", Error: err.Error(), Initial: plan}
		}
		plan.Opened = true
		plan.BrowserSessionID = sessionID
		plan.Spoken = mediaOpenedSpeech(plan)
	}
	return OpsResult{OK: true, Initial: plan}
}

func buildMediaOpenPlan(p mediaOpenPayload) (MediaOpenPlan, error) {
	provider := normalizeMediaProvider(p.Provider)
	query := strings.TrimSpace(p.Query)
	openMode := normalizeMeetingOpenMode(p.OpenMode)
	surface := normalizeMeetingSurface(p.Surface)
	var openURL string
	var err error
	if strings.TrimSpace(p.URL) != "" {
		openURL, err = sanitizePublicMediaURL(p.URL)
		if err != nil {
			return MediaOpenPlan{}, err
		}
		if provider == "" || provider == "auto" {
			provider = providerFromMediaURL(openURL)
		}
	} else {
		if query == "" {
			return MediaOpenPlan{}, fmt.Errorf("query or url is required")
		}
		if provider == "" || provider == "auto" {
			provider = "youtube"
		}
		openURL, err = mediaSearchURL(provider, query, p.Live)
		if err != nil {
			return MediaOpenPlan{}, err
		}
	}
	if provider == "" || provider == "auto" {
		provider = "web"
	}
	plan := MediaOpenPlan{
		Provider: provider,
		Query:    query,
		URL:      openURL,
		OpenURL:  openURL,
		OpenMode: openMode,
		Surface:  surface,
		Live:     p.Live,
		Spoken:   mediaFoundSpeech(provider, query, p.Live),
		Note:     "Opens the official provider URL on the selected Yaver runtime. Playback, login, chat, and subscriptions stay inside the provider app/browser.",
	}
	return plan, nil
}

func normalizeMediaProvider(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return "auto"
	case "yt", "youtube", "youtube_music", "youtube-music":
		return "youtube"
	case "twitch":
		return "twitch"
	case "kick":
		return "kick"
	case "vimeo":
		return "vimeo"
	case "spotify":
		return "spotify"
	case "apple", "applemusic", "apple-music", "apple_music":
		return "apple_music"
	case "web", "browser", "url":
		return "web"
	default:
		return strings.ToLower(strings.TrimSpace(s))
	}
}

func mediaSearchURL(provider, query string, live bool) (string, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return "", fmt.Errorf("query is required")
	}
	search := q
	if live && !strings.Contains(strings.ToLower(search), "live") {
		search += " live"
	}
	switch normalizeMediaProvider(provider) {
	case "youtube":
		v := url.Values{}
		v.Set("search_query", search)
		return "https://www.youtube.com/results?" + v.Encode(), nil
	case "twitch":
		v := url.Values{}
		v.Set("term", search)
		return "https://www.twitch.tv/search?" + v.Encode(), nil
	case "kick":
		v := url.Values{}
		v.Set("query", search)
		return "https://kick.com/search?" + v.Encode(), nil
	case "vimeo":
		v := url.Values{}
		v.Set("q", search)
		return "https://vimeo.com/search?" + v.Encode(), nil
	case "spotify":
		return "https://open.spotify.com/search/" + url.PathEscape(search), nil
	case "apple_music":
		v := url.Values{}
		v.Set("term", search)
		return "https://music.apple.com/search?" + v.Encode(), nil
	case "web":
		v := url.Values{}
		v.Set("q", search)
		return "https://www.google.com/search?" + v.Encode(), nil
	default:
		return "", fmt.Errorf("unsupported media provider %q", provider)
	}
}

func sanitizePublicMediaURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid URL")
	}
	if strings.ToLower(u.Scheme) != "https" {
		return "", fmt.Errorf("media URLs must use https")
	}
	return u.String(), nil
}

func providerFromMediaURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "web"
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "youtu.be" || strings.HasSuffix(host, "youtube.com"):
		return "youtube"
	case strings.HasSuffix(host, "twitch.tv"):
		return "twitch"
	case strings.HasSuffix(host, "kick.com"):
		return "kick"
	case strings.HasSuffix(host, "vimeo.com"):
		return "vimeo"
	case strings.HasSuffix(host, "spotify.com"):
		return "spotify"
	case strings.HasSuffix(host, "music.apple.com"):
		return "apple_music"
	default:
		return "web"
	}
}

func openMediaURLWithMode(c OpsContext, raw, mode string) (string, error) {
	switch normalizeMeetingOpenMode(mode) {
	case "system":
		return "", openMediaURL(raw)
	case "browser":
		return openMediaURLInRuntimeBrowser(c, raw)
	default:
		return "", fmt.Errorf("unsupported media openMode %q", mode)
	}
}

func openMediaURLInRuntimeBrowser(c OpsContext, raw string) (string, error) {
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
	sessionID := fmt.Sprintf("media-%d", time.Now().UnixNano())
	if err := c.Server.browserMgr.OpenSessionWithProfile(sessionID, true, "", profileDirFor("media")); err != nil {
		return "", err
	}
	if _, err := c.Server.browserMgr.Navigate(sessionID, u); err != nil {
		return sessionID, err
	}
	return sessionID, nil
}

func openMediaURL(raw string) error {
	u, err := sanitizePublicMediaURL(raw)
	if err != nil {
		return err
	}
	return openMeetingURL(u)
}

func mediaFoundSpeech(provider, query string, live bool) string {
	label := mediaProviderLabel(provider)
	if strings.TrimSpace(query) == "" {
		return "I found the media link."
	}
	if live {
		return fmt.Sprintf("I found %s live results for %s.", label, query)
	}
	return fmt.Sprintf("I found %s results for %s.", label, query)
}

func mediaOpenedSpeech(plan MediaOpenPlan) string {
	label := mediaProviderLabel(plan.Provider)
	if strings.TrimSpace(plan.Query) == "" {
		return "Opening " + label + "."
	}
	if plan.Live {
		return fmt.Sprintf("Opening %s live results for %s.", label, plan.Query)
	}
	return fmt.Sprintf("Opening %s for %s.", label, plan.Query)
}

func mediaProviderLabel(provider string) string {
	switch normalizeMediaProvider(provider) {
	case "youtube":
		return "YouTube"
	case "twitch":
		return "Twitch"
	case "kick":
		return "Kick"
	case "vimeo":
		return "Vimeo"
	case "spotify":
		return "Spotify"
	case "apple_music":
		return "Apple Music"
	default:
		return "media"
	}
}
