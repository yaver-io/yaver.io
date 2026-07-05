package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
)

// ops_meetings.go — provider-neutral meeting control for constrained surfaces.
//
// Car/watch/TV/mobile/MCP should all call these verbs instead of learning
// Google Calendar, Microsoft Graph, Zoom, or browser/runtime details. The verb
// returns a join plan, and optionally opens the official provider URL on the
// selected Yaver runtime host. Audio/video stays in the official client unless
// a future Yaver-native room is explicitly selected.

type meetingNextPayload struct {
	Provider     string `json:"provider,omitempty"`       // auto | local | google | o365 | microsoft | teams | zoom
	WithinHours  int    `json:"withinHours,omitempty"`    // default 24
	IncludePastM int    `json:"includePastMin,omitempty"` // tolerate already-started meetings; default 5
}

type meetingJoinPayload struct {
	Provider     string `json:"provider,omitempty"`
	WithinHours  int    `json:"withinHours,omitempty"`
	IncludePastM int    `json:"includePastMin,omitempty"`
	Open         bool   `json:"open,omitempty"`
	OpenMode     string `json:"openMode,omitempty"` // system | browser | automation | selenium
	Surface      string `json:"surface,omitempty"`  // car | watch | tv | mobile | mcp | cli
}

type meetingOpenPayload struct {
	URL      string `json:"url"`
	Open     bool   `json:"open,omitempty"`
	OpenMode string `json:"openMode,omitempty"` // system | browser | automation | selenium
	Surface  string `json:"surface,omitempty"`
}

type MeetingJoinPlan struct {
	Provider         string `json:"provider"`
	MeetingID        string `json:"meetingId,omitempty"`
	Title            string `json:"title"`
	StartsAt         string `json:"startsAt,omitempty"`
	EndsAt           string `json:"endsAt,omitempty"`
	Organizer        string `json:"organizer,omitempty"`
	Location         string `json:"location,omitempty"`
	JoinURL          string `json:"joinUrl,omitempty"`
	OpenURL          string `json:"openUrl,omitempty"`
	OpenStrategy     string `json:"openStrategy"`
	OpenMode         string `json:"openMode,omitempty"`
	BrowserSessionID string `json:"browserSessionId,omitempty"`
	Surface          string `json:"surface,omitempty"`
	Source           string `json:"source,omitempty"`
	Note             string `json:"note,omitempty"`
	Opened           bool   `json:"opened,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "meeting_next",
		Description: "Find the next meeting from local Yaver bookings or configured Google/O365 calendar. Payload {provider?: auto|local|google|o365|teams|zoom, withinHours?, includePastMin?}. Returns a provider-neutral join plan.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider":       map[string]interface{}{"type": "string"},
				"withinHours":    map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 168},
				"includePastMin": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 120},
			},
			"additionalProperties": false,
		},
		Handler: meetingNextOpsHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "meeting_join_next",
		Description: "Find the next meeting and optionally open the official Teams/Meet/Zoom/Yaver link on this runtime host. Payload {provider?, open?, openMode?: system|browser|automation|selenium, surface?, withinHours?, includePastMin?}.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"provider":       map[string]interface{}{"type": "string"},
				"open":           map[string]interface{}{"type": "boolean"},
				"openMode":       map[string]interface{}{"type": "string"},
				"surface":        map[string]interface{}{"type": "string"},
				"withinHours":    map[string]interface{}{"type": "integer", "minimum": 1, "maximum": 168},
				"includePastMin": map[string]interface{}{"type": "integer", "minimum": 0, "maximum": 120},
			},
			"additionalProperties": false,
		},
		Handler: meetingJoinNextOpsHandler,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "meeting_open_url",
		Description: "Classify and optionally open a meeting URL on this runtime host. Payload {url, open?, openMode?: system|browser|automation|selenium, surface?}. Used by car/watch/TV handoff after an API found a join link.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"url"},
			"properties": map[string]interface{}{
				"url":      map[string]interface{}{"type": "string"},
				"open":     map[string]interface{}{"type": "boolean"},
				"openMode": map[string]interface{}{"type": "string"},
				"surface":  map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler: meetingOpenURLOpsHandler,
	})
}

func meetingNextOpsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p meetingNextPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	plan, err := meetingNextPlan(c.Ctx, p)
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: plan}
}

func meetingJoinNextOpsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p meetingJoinPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	plan, err := meetingNextPlan(c.Ctx, meetingNextPayload{Provider: p.Provider, WithinHours: p.WithinHours, IncludePastM: p.IncludePastM})
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	plan.Surface = normalizeMeetingSurface(p.Surface)
	plan.OpenMode = normalizeMeetingOpenMode(p.OpenMode)
	if p.Open {
		if plan.OpenURL == "" {
			return OpsResult{OK: false, Code: "not_found", Error: "next meeting has no join URL to open"}
		}
		sessionID, err := openMeetingURLWithMode(c, plan.OpenURL, plan.OpenMode)
		if err != nil {
			return OpsResult{OK: false, Code: "open_failed", Error: err.Error(), Initial: plan}
		}
		plan.Opened = true
		plan.BrowserSessionID = sessionID
		if sessionID != "" {
			plan.OpenStrategy = "remote-runtime-browser-automation"
			plan.Note = "Opened in a Yaver-controlled runtime browser session using the shared meetings profile. Media still runs in the official provider web client."
		}
	}
	return OpsResult{OK: true, Initial: plan}
}

func meetingOpenURLOpsHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p meetingOpenPayload
	if len(payload) == 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "payload is required"}
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	provider, ok := classifyMeetingProviderFromURL(p.URL)
	if !ok {
		return OpsResult{OK: false, Code: "bad_payload", Error: "url is not a supported meeting URL"}
	}
	openURL, err := sanitizeMeetingURL(p.URL)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	plan := MeetingJoinPlan{
		Provider:     provider,
		Title:        "Meeting",
		JoinURL:      openURL,
		OpenURL:      openURL,
		OpenStrategy: "official-client-or-browser",
		OpenMode:     normalizeMeetingOpenMode(p.OpenMode),
		Surface:      normalizeMeetingSurface(p.Surface),
		Source:       "url",
		Note:         "Open the official provider app or browser. MCP/Yaver orchestrates; media stays in the official client.",
	}
	if p.Open {
		sessionID, err := openMeetingURLWithMode(c, openURL, plan.OpenMode)
		if err != nil {
			return OpsResult{OK: false, Code: "open_failed", Error: err.Error(), Initial: plan}
		}
		plan.Opened = true
		plan.BrowserSessionID = sessionID
		if sessionID != "" {
			plan.OpenStrategy = "remote-runtime-browser-automation"
			plan.Note = "Opened in a Yaver-controlled runtime browser session using the shared meetings profile. Media still runs in the official provider web client."
		}
	}
	return OpsResult{OK: true, Initial: plan}
}

func meetingNextPlan(ctx context.Context, p meetingNextPayload) (MeetingJoinPlan, error) {
	provider := normalizeMeetingProvider(p.Provider)
	within := p.WithinHours
	if within <= 0 {
		within = 24
	}
	if within > 168 {
		within = 168
	}
	past := p.IncludePastM
	if past <= 0 {
		past = 5
	}
	now := time.Now().UTC()
	startMin := now.Add(-time.Duration(past) * time.Minute)
	endMax := now.Add(time.Duration(within) * time.Hour)

	if provider == "" || provider == "auto" || provider == "local" || provider == "zoom" || provider == "yaver" {
		if plan, ok := nextLocalBooking(provider, startMin, endMax); ok {
			return plan, nil
		}
		if provider == "local" || provider == "zoom" || provider == "yaver" {
			return MeetingJoinPlan{}, fmt.Errorf("no local %s meeting found in the next %d hours", provider, within)
		}
	}

	cfg, _ := LoadConfig()
	if cfg == nil || cfg.Email == nil {
		return MeetingJoinPlan{}, fmt.Errorf("no meeting found locally and email/calendar OAuth is not configured")
	}
	if provider == "" || provider == "auto" {
		provider = providerFromEmailConfig(cfg.Email)
	}
	switch provider {
	case "google":
		return nextGoogleCalendarMeeting(ctx, cfg.Email, startMin, endMax)
	case "o365", "microsoft", "teams":
		return nextGraphCalendarMeeting(ctx, cfg.Email, startMin, endMax)
	default:
		return MeetingJoinPlan{}, fmt.Errorf("unsupported provider %q", p.Provider)
	}
}

func nextLocalBooking(provider string, startMin, endMax time.Time) (MeetingJoinPlan, bool) {
	var candidates []Booking
	for _, b := range loadBookings() {
		if b.StartsAt.Before(startMin) || b.StartsAt.After(endMax) {
			continue
		}
		if provider != "" && provider != "auto" && provider != "local" && provider != normalizeMeetingProvider(b.Provider) && provider != classifyProviderLoose(b.JoinURL) {
			continue
		}
		candidates = append(candidates, b)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].StartsAt.Before(candidates[j].StartsAt) })
	if len(candidates) == 0 {
		return MeetingJoinPlan{}, false
	}
	b := candidates[0]
	p := normalizeMeetingProvider(b.Provider)
	if fromURL := classifyProviderLoose(b.JoinURL); fromURL != "" {
		p = fromURL
	}
	if p == "" {
		p = "local"
	}
	return MeetingJoinPlan{
		Provider:     p,
		MeetingID:    b.ID,
		Title:        b.EventSlug,
		StartsAt:     b.StartsAt.UTC().Format(time.RFC3339),
		EndsAt:       b.EndsAt.UTC().Format(time.RFC3339),
		Organizer:    b.Name,
		JoinURL:      b.JoinURL,
		OpenURL:      b.JoinURL,
		OpenStrategy: meetingOpenStrategy(p),
		Source:       "local-bookings",
		Note:         "Local Yaver booking. Open the official provider link when present.",
	}, true
}

func nextGoogleCalendarMeeting(ctx context.Context, cfg *EmailConfig, startMin, endMax time.Time) (MeetingJoinPlan, error) {
	token, err := gmailAccess(cfg)
	if err != nil {
		return MeetingJoinPlan{}, err
	}
	q := url.Values{}
	q.Set("singleEvents", "true")
	q.Set("orderBy", "startTime")
	q.Set("timeMin", startMin.Format(time.RFC3339))
	q.Set("timeMax", endMax.Format(time.RFC3339))
	q.Set("maxResults", "10")
	endpoint := "https://www.googleapis.com/calendar/v3/calendars/primary/events?" + q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", gatewayContactUA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return MeetingJoinPlan{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return MeetingJoinPlan{}, fmt.Errorf("google calendar %d: %s", resp.StatusCode, gatewayTruncate(string(raw), 512))
	}
	var out struct {
		Items []struct {
			ID             string                          `json:"id"`
			Summary        string                          `json:"summary"`
			Location       string                          `json:"location"`
			HangoutLink    string                          `json:"hangoutLink"`
			HTMLLink       string                          `json:"htmlLink"`
			Start          struct{ DateTime, Date string } `json:"start"`
			End            struct{ DateTime, Date string } `json:"end"`
			ConferenceData struct {
				EntryPoints []struct {
					URI            string `json:"uri"`
					EntryPointType string `json:"entryPointType"`
				} `json:"entryPoints"`
			} `json:"conferenceData"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return MeetingJoinPlan{}, err
	}
	for _, item := range out.Items {
		join := item.HangoutLink
		for _, ep := range item.ConferenceData.EntryPoints {
			if ep.EntryPointType == "video" && ep.URI != "" {
				join = ep.URI
				break
			}
		}
		if join == "" {
			join = firstMeetingURL(item.Location)
		}
		start := parseProviderTime(firstMeetingNonEmpty(item.Start.DateTime, item.Start.Date))
		end := parseProviderTime(firstMeetingNonEmpty(item.End.DateTime, item.End.Date))
		return MeetingJoinPlan{
			Provider:     firstMeetingNonEmpty(classifyProviderLoose(join), "google"),
			MeetingID:    item.ID,
			Title:        firstMeetingNonEmpty(item.Summary, "Google Calendar meeting"),
			StartsAt:     formatOptionalTime(start),
			EndsAt:       formatOptionalTime(end),
			Location:     item.Location,
			JoinURL:      join,
			OpenURL:      firstMeetingNonEmpty(join, item.HTMLLink),
			OpenStrategy: meetingOpenStrategy(firstMeetingNonEmpty(classifyProviderLoose(join), "google")),
			Source:       "google-calendar",
			Note:         "Calendar metadata via Google API; media opens in official Meet/browser client.",
		}, nil
	}
	return MeetingJoinPlan{}, fmt.Errorf("no Google Calendar meeting found in the requested window")
}

func nextGraphCalendarMeeting(ctx context.Context, cfg *EmailConfig, startMin, endMax time.Time) (MeetingJoinPlan, error) {
	token, err := graphAccess(cfg)
	if err != nil {
		return MeetingJoinPlan{}, err
	}
	user := strings.TrimSpace(cfg.SenderEmail)
	if user == "" {
		return MeetingJoinPlan{}, fmt.Errorf("sender_email is required for Microsoft calendar lookup")
	}
	q := url.Values{}
	q.Set("startDateTime", startMin.Format(time.RFC3339))
	q.Set("endDateTime", endMax.Format(time.RFC3339))
	q.Set("$top", "10")
	q.Set("$orderby", "start/dateTime")
	q.Set("$select", "id,subject,start,end,location,onlineMeeting,webLink,bodyPreview,organizer")
	endpoint := fmt.Sprintf("%s/users/%s/calendarView?%s", graphBase, url.PathEscape(user), q.Encode())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", gatewayContactUA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return MeetingJoinPlan{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		return MeetingJoinPlan{}, fmt.Errorf("graph calendar %d: %s", resp.StatusCode, gatewayTruncate(string(raw), 512))
	}
	var out struct {
		Value []struct {
			ID        string                              `json:"id"`
			Subject   string                              `json:"subject"`
			WebLink   string                              `json:"webLink"`
			BodyPrev  string                              `json:"bodyPreview"`
			Start     struct{ DateTime, TimeZone string } `json:"start"`
			End       struct{ DateTime, TimeZone string } `json:"end"`
			Location  struct{ DisplayName string }        `json:"location"`
			Organizer struct {
				EmailAddress struct{ Name, Address string } `json:"emailAddress"`
			} `json:"organizer"`
			OnlineMeeting struct {
				JoinURL string `json:"joinUrl"`
			} `json:"onlineMeeting"`
		} `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return MeetingJoinPlan{}, err
	}
	for _, item := range out.Value {
		join := firstMeetingNonEmpty(item.OnlineMeeting.JoinURL, firstMeetingURL(item.Location.DisplayName), firstMeetingURL(item.BodyPrev))
		start := parseProviderTime(item.Start.DateTime)
		end := parseProviderTime(item.End.DateTime)
		provider := firstMeetingNonEmpty(classifyProviderLoose(join), "o365")
		return MeetingJoinPlan{
			Provider:     provider,
			MeetingID:    item.ID,
			Title:        firstMeetingNonEmpty(item.Subject, "Microsoft 365 meeting"),
			StartsAt:     formatOptionalTime(start),
			EndsAt:       formatOptionalTime(end),
			Organizer:    firstMeetingNonEmpty(item.Organizer.EmailAddress.Name, item.Organizer.EmailAddress.Address),
			Location:     item.Location.DisplayName,
			JoinURL:      join,
			OpenURL:      firstMeetingNonEmpty(join, item.WebLink),
			OpenStrategy: meetingOpenStrategy(provider),
			Source:       "microsoft-graph-calendar",
			Note:         "Calendar metadata via Microsoft Graph; media opens in official Teams/browser client.",
		}, nil
	}
	return MeetingJoinPlan{}, fmt.Errorf("no Microsoft 365 meeting found in the requested window")
}

func normalizeMeetingProvider(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "", "auto":
		return "auto"
	case "office365", "m365", "microsoft365", "microsoft", "msgraph", "graph":
		return "o365"
	case "teams", "microsoft-teams":
		return "teams"
	case "google-meet", "meet", "gmeet":
		return "google"
	case "yaver-native":
		return "yaver"
	default:
		return strings.ToLower(strings.TrimSpace(p))
	}
}

func providerFromEmailConfig(cfg *EmailConfig) string {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "gmail", "google":
		return "google"
	case "office365", "o365", "microsoft", "microsoft365":
		return "o365"
	default:
		if cfg.GoogleRefreshToken != "" {
			return "google"
		}
		if cfg.AzureTenantID != "" {
			return "o365"
		}
		return ""
	}
}

func classifyMeetingProviderFromURL(raw string) (string, bool) {
	u, err := sanitizeMeetingURL(raw)
	if err != nil {
		return "", false
	}
	p := classifyProviderLoose(u)
	return p, p != ""
}

func classifyProviderLoose(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Host)
	switch {
	case strings.Contains(host, "teams.microsoft.com") || strings.Contains(host, "teams.live.com"):
		return "teams"
	case strings.Contains(host, "meet.google.com"):
		return "google"
	case strings.Contains(host, "zoom.us"):
		return "zoom"
	case strings.Contains(host, "yaver.io"):
		return "yaver"
	default:
		return ""
	}
}

func firstMeetingURL(text string) string {
	for _, field := range strings.Fields(text) {
		candidate := strings.Trim(field, "<>()[]{}.,;\"'")
		if provider, ok := classifyMeetingProviderFromURL(candidate); ok && provider != "" {
			return candidate
		}
	}
	return ""
}

func sanitizeMeetingURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid URL")
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http":
	default:
		return "", fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	if strings.ToLower(u.Scheme) != "https" && !strings.HasSuffix(strings.ToLower(u.Host), ".local") && u.Host != "localhost" {
		return "", fmt.Errorf("meeting URLs must use https unless localhost/local")
	}
	return u.String(), nil
}

func meetingOpenStrategy(provider string) string {
	switch provider {
	case "teams":
		return "official-teams-client-or-browser"
	case "google":
		return "official-google-meet-client-or-browser"
	case "zoom":
		return "official-zoom-client-or-browser"
	case "yaver":
		return "yaver-native-room"
	default:
		return "official-client-or-browser"
	}
}

func normalizeMeetingSurface(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "car", "android-auto", "carplay":
		return "car"
	case "watch", "wear", "wearos", "watchos":
		return "watch"
	case "tv", "appletv", "androidtv":
		return "tv"
	case "mobile", "phone":
		return "mobile"
	case "mcp", "cli":
		return strings.ToLower(strings.TrimSpace(s))
	default:
		return strings.TrimSpace(s)
	}
}

func normalizeMeetingOpenMode(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "default", "system", "app", "official":
		return "system"
	case "browser", "automation", "remote-browser", "runtime-browser", "chrome", "chromedp", "selenium":
		return "browser"
	default:
		return strings.ToLower(strings.TrimSpace(s))
	}
}

func openMeetingURLWithMode(c OpsContext, raw, mode string) (string, error) {
	switch normalizeMeetingOpenMode(mode) {
	case "system":
		return "", openMeetingURL(raw)
	case "browser":
		return openMeetingURLInRuntimeBrowser(c, raw)
	default:
		return "", fmt.Errorf("unsupported meeting openMode %q", mode)
	}
}

func openMeetingURLInRuntimeBrowser(c OpsContext, raw string) (string, error) {
	u, err := sanitizeMeetingURL(raw)
	if err != nil {
		return "", err
	}
	if c.Server == nil {
		return "", fmt.Errorf("browser automation openMode requires a runtime HTTP server context")
	}
	if c.Server.browserMgr == nil {
		c.Server.browserMgr = NewBrowserManager()
	}
	c.Server.browserMgr.ensureVPM(c.Server.vibePreviewMgr, ActiveVibePreviewManager())
	sessionID := fmt.Sprintf("meeting-%d", time.Now().UnixNano())
	if err := c.Server.browserMgr.OpenSessionWithProfile(sessionID, true, "", profileDirFor("meetings")); err != nil {
		return "", err
	}
	if _, err := c.Server.browserMgr.Navigate(sessionID, u); err != nil {
		return sessionID, err
	}
	return sessionID, nil
}

func openMeetingURL(raw string) error {
	u, err := sanitizeMeetingURL(raw)
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	default:
		if _, err := exec.LookPath("xdg-open"); err == nil {
			return exec.Command("xdg-open", u).Start()
		}
		if _, err := exec.LookPath("wslview"); err == nil {
			return exec.Command("wslview", u).Start()
		}
		return fmt.Errorf("no URL opener found on %s", runtime.GOOS)
	}
}

func parseProviderTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	layouts := []string{
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05.0000000",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t
		}
	}
	return time.Time{}
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func firstMeetingNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
