package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

type PublisherProgram struct {
	Platform  string `json:"platform"`
	Status    string `json:"status"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

type PublisherProfile struct {
	EntityType    string             `json:"entityType,omitempty"`
	LegalName     string             `json:"legalName,omitempty"`
	PublisherName string             `json:"publisherName,omitempty"`
	DUNSNumber    string             `json:"dunsNumber,omitempty"`
	CountryCode   string             `json:"countryCode,omitempty"`
	AddressLine1  string             `json:"addressLine1,omitempty"`
	AddressLine2  string             `json:"addressLine2,omitempty"`
	City          string             `json:"city,omitempty"`
	Region        string             `json:"region,omitempty"`
	PostalCode    string             `json:"postalCode,omitempty"`
	Website       string             `json:"website,omitempty"`
	BusinessEmail string             `json:"businessEmail,omitempty"`
	SupportEmail  string             `json:"supportEmail,omitempty"`
	Phone         string             `json:"phone,omitempty"`
	Programs      []PublisherProgram `json:"programs,omitempty"`
	UpdatedAt     int64              `json:"updatedAt,omitempty"`
}

type PublisherSetupRequest struct {
	Op       string            `json:"op"`
	Platform string            `json:"platform,omitempty"`
	Profile  *PublisherProfile `json:"profile,omitempty"`
}

func decodePublisherSetupRequest(data []byte) (PublisherSetupRequest, error) {
	var req PublisherSetupRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return PublisherSetupRequest{}, err
	}
	return req, nil
}

type publisherPlatformGuide struct {
	Label      string
	URL        string
	HumanGates []string
}

var publisherPlatformGuides = map[string]publisherPlatformGuide{
	"apple": {
		Label: "Apple Developer", URL: "https://developer.apple.com/programs/enroll/",
		HumanGates: []string{"Apple Account login and 2FA", "identity or organization verification", "program agreement", "membership payment", "final enrollment submission"},
	},
	"google-play": {
		Label: "Google Play Console", URL: "https://play.google.com/console/signup",
		HumanGates: []string{"Google Account login and 2FA", "identity or organization verification", "developer agreement", "registration payment", "final enrollment submission"},
	},
	"microsoft-store": {
		Label: "Microsoft Store Partner Center", URL: "https://developer.microsoft.com/microsoft-store/register",
		HumanGates: []string{"Microsoft Account login and 2FA", "identity or company verification", "agreements", "payment when requested", "final enrollment submission"},
	},
	"xbox": {
		Label: "ID@Xbox", URL: "https://www.xbox.com/publish",
		HumanGates: []string{"Microsoft Account login and 2FA", "NDA", "concept/program application", "platform approval", "restricted SDK and devkit access"},
	},
	"playstation": {
		Label: "PlayStation Partners", URL: "https://partners.playstation.net/",
		HumanGates: []string{"account login and 2FA", "partner application", "agreements", "platform approval", "restricted SDK and devkit access"},
	},
}

func normalizePublisherPlatform(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "apple", "ios", "ipados", "tvos", "watchos", "visionos", "app-store", "appstore":
		return "apple"
	case "android", "google", "google-play", "play", "play-store":
		return "google-play"
	case "windows", "windows-store", "microsoft", "microsoft-store":
		return "microsoft-store"
	case "xbox", "id@xbox", "idxbox":
		return "xbox"
	case "ps4", "ps5", "playstation":
		return "playstation"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func publisherMissingFields(profile PublisherProfile, platform string) []string {
	missing := []string{}
	fields := []struct{ name, value string }{
		{"legalName", profile.LegalName}, {"publisherName", profile.PublisherName}, {"countryCode", profile.CountryCode},
		{"addressLine1", profile.AddressLine1}, {"city", profile.City}, {"postalCode", profile.PostalCode},
		{"website", profile.Website}, {"businessEmail", profile.BusinessEmail}, {"phone", profile.Phone},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			missing = append(missing, field.name)
		}
	}
	if profile.EntityType == "organization" && strings.TrimSpace(profile.DUNSNumber) == "" && platform != "xbox" && platform != "playstation" {
		missing = append(missing, "dunsNumber")
	}
	return missing
}

func publisherPlan(profile PublisherProfile, platform string) (map[string]any, error) {
	platform = normalizePublisherPlatform(platform)
	guide, ok := publisherPlatformGuides[platform]
	if !ok {
		return nil, fmt.Errorf("unsupported publisher platform %q", platform)
	}
	missing := publisherMissingFields(profile, platform)
	return map[string]any{
		"platform": platform, "label": guide.Label, "officialUrl": guide.URL,
		"profile": profile, "missingFields": missing, "profileReady": len(missing) == 0,
		"humanGates": guide.HumanGates,
		"automationPolicy": map[string]any{
			"mayFill":   []string{"legal/business identity", "address", "website", "business/support contact details"},
			"mustPause": []string{"login", "2FA", "captcha", "identity verification", "legal attestations and agreements", "payment", "tax/bank details", "final submit"},
		},
		"nextTool": map[string]any{
			"name":            "browser_interactive_start",
			"arguments":       map[string]any{"url": guide.URL, "profile": "publisher-" + platform, "headful": true},
			"afterHumanLogin": "Continue on the same session with browser_snapshot/browser_type/browser_select. Inspect named DOM selectors; never coordinate-click. Stop before every mustPause action.",
		},
	}, nil
}

func fetchPublisherProfile(ctx context.Context) (PublisherProfile, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.ConvexSiteURL) == "" || strings.TrimSpace(cfg.AuthToken) == "" {
		return PublisherProfile{}, fmt.Errorf("not signed in — run `yaver auth`")
	}
	req, err := newBearerRequest(http.MethodGet, strings.TrimRight(cfg.ConvexSiteURL, "/")+"/settings", cfg.AuthToken, nil)
	if err != nil {
		return PublisherProfile{}, err
	}
	req = req.WithContext(ctx)
	resp, err := httpClient.Do(req)
	if err != nil {
		return PublisherProfile{}, fmt.Errorf("fetch publisher settings: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return PublisherProfile{}, fmt.Errorf("fetch publisher settings: HTTP %d — %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Settings struct {
			PublisherProfile PublisherProfile `json:"publisherProfile"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return PublisherProfile{}, fmt.Errorf("decode publisher settings: %w", err)
	}
	return envelope.Settings.PublisherProfile, nil
}

func savePublisherProfile(ctx context.Context, profile PublisherProfile) error {
	return writePublisherProfile(ctx, profile)
}

func clearPublisherProfile(ctx context.Context) error {
	return writePublisherProfile(ctx, nil)
}

func writePublisherProfile(ctx context.Context, profile any) error {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.ConvexSiteURL) == "" || strings.TrimSpace(cfg.AuthToken) == "" {
		return fmt.Errorf("not signed in — run `yaver auth`")
	}
	payload, err := json.Marshal(map[string]any{"publisherProfile": profile})
	if err != nil {
		return err
	}
	req, err := newBearerRequest(http.MethodPost, strings.TrimRight(cfg.ConvexSiteURL, "/")+"/settings", cfg.AuthToken, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("save publisher settings: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("save publisher settings: HTTP %d — %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func runPublisherSetup(ctx context.Context, req PublisherSetupRequest) (map[string]any, error) {
	switch strings.ToLower(strings.TrimSpace(req.Op)) {
	case "", "status":
		profile, err := fetchPublisherProfile(ctx)
		if err != nil {
			return nil, err
		}
		plans := map[string]any{}
		keys := make([]string, 0, len(publisherPlatformGuides))
		for platform := range publisherPlatformGuides {
			keys = append(keys, platform)
		}
		sort.Strings(keys)
		for _, platform := range keys {
			plan, _ := publisherPlan(profile, platform)
			plans[platform] = plan
		}
		return map[string]any{"profile": profile, "platforms": plans}, nil
	case "save":
		if req.Profile == nil {
			return nil, fmt.Errorf("profile is required for op=save")
		}
		if err := savePublisherProfile(ctx, *req.Profile); err != nil {
			return nil, err
		}
		return map[string]any{"saved": true, "profile": req.Profile}, nil
	case "clear":
		if err := clearPublisherProfile(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"cleared": true}, nil
	case "plan", "browser-plan":
		profile, err := fetchPublisherProfile(ctx)
		if err != nil {
			return nil, err
		}
		return publisherPlan(profile, req.Platform)
	default:
		return nil, fmt.Errorf("op must be status, save, clear, or plan")
	}
}
