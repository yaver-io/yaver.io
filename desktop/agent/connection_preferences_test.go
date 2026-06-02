package main

import "testing"

func TestConnectionPreferencesForHeartbeatHeadscaleOverride(t *testing.T) {
	cfg := &Config{
		ConnectionPreferences: []ConnectionPreference{
			{Kind: connPrefHeadscale, Active: true, Preferred: true, Source: "user-config"},
		},
	}
	got := connectionPreferencesForHeartbeat(cfg, []string{"100.100.1.2"}, nil)
	if !hasConnectionPreference(got, connPrefHeadscale, true, true) {
		t.Fatalf("expected active preferred headscale preference, got %+v", got)
	}
	if hasConnectionPreference(got, connPrefTailscale, true, true) {
		t.Fatalf("expected headscale override to avoid adding tailscale duplicate, got %+v", got)
	}
}

func TestConnectionPreferencesForHeartbeatSeedsCommonTransports(t *testing.T) {
	cfg := &Config{
		CachedRelayServers: []RelayServerConfig{{ID: "public-shared-eu"}},
		CloudflareTunnels:  []CloudflareTunnelConfig{{URL: "https://box.example.com"}},
	}
	got := connectionPreferencesForHeartbeat(cfg, []string{"192.168.1.20", "100.101.1.20"}, []string{"https://box.example.com"})
	for _, want := range []string{connPrefDirectLAN, connPrefTailscale, connPrefHTTPSTunnel, connPrefFreeRelay} {
		if !hasConnectionPreference(got, want, true, false) && !hasConnectionPreference(got, want, true, true) {
			t.Fatalf("expected active %s preference, got %+v", want, got)
		}
	}
}

func TestClassifyCachedRelayPreference(t *testing.T) {
	tests := []struct {
		name  string
		relay RelayServerConfig
		want  string
	}{
		{name: "public", relay: RelayServerConfig{ID: "public-shared"}, want: connPrefFreeRelay},
		{name: "managed", relay: RelayServerConfig{Label: "managed relay"}, want: connPrefPrivateRelay},
		{name: "default", relay: RelayServerConfig{ID: "relay-eu"}, want: connPrefFreeRelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyCachedRelayPreference(tt.relay); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func hasConnectionPreference(prefs []ConnectionPreference, kind string, active, preferred bool) bool {
	for _, pref := range prefs {
		if pref.Kind == kind && pref.Active == active && pref.Preferred == preferred {
			return true
		}
	}
	return false
}
