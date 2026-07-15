package main

import (
	"testing"
	"time"
)

func TestHeartbeatFromDeviceUsesConvexRegistryFields(t *testing.T) {
	now := time.Now().Add(-30 * time.Second).UnixMilli()
	got := heartbeatFromDevice(&DeviceInfo{
		IsOnline:       true,
		NeedsAuth:      true,
		RelayConnected: true,
		LastHeartbeat:  now,
		AgentVersion:   "1.2.3",
	})
	if got == nil {
		t.Fatal("heartbeatFromDevice() = nil")
	}
	if !got.Online || !got.NeedsAuth || !got.RelayConnected || !got.Fresh {
		t.Fatalf("heartbeatFromDevice() = %+v, want online needsAuth relayConnected fresh", got)
	}
	if got.AgentVersion != "1.2.3" {
		t.Fatalf("AgentVersion = %q, want 1.2.3", got.AgentVersion)
	}
	if got.AgeSeconds < 0 || got.AgeSeconds > 120 {
		t.Fatalf("AgeSeconds = %d, want recent", got.AgeSeconds)
	}
}

func TestDoctorIPLayerClassification(t *testing.T) {
	tests := []struct {
		name string
		host string
		kind string
		want string
	}{
		{name: "relay wins", host: "public.yaver.io", kind: "relay", want: "relay-gateway"},
		{name: "lan", host: "192.168.1.20", want: "same-lan"},
		{name: "tailscale", host: "100.64.0.5", want: "tailscale"},
		{name: "mesh", host: "100.96.0.5", want: "mesh"},
		{name: "public", host: "8.8.8.8", want: "public"},
		{name: "hostname", host: "box.local", want: "hostname"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyDoctorIPLayer(tt.host, tt.kind); got != tt.want {
				t.Fatalf("classifyDoctorIPLayer(%q, %q) = %q, want %q", tt.host, tt.kind, got, tt.want)
			}
		})
	}
}

func TestTargetFromDeviceClassifiesRemoteBoxKinds(t *testing.T) {
	tests := []struct {
		name        string
		device      DeviceInfo
		wantHosting string
		wantPath    string
	}{
		{
			name:        "managed",
			device:      DeviceInfo{Hosting: "yaver-hosted", Managed: true, PublicEndpoints: []string{"https://box.cloud.yaver.io"}},
			wantHosting: "managed",
			wantPath:    "public-endpoint-or-relay",
		},
		{
			name:        "byo",
			device:      DeviceInfo{Hosting: "byo"},
			wantHosting: "byo",
			wantPath:    "relay",
		},
		{
			name:        "self-hosted tailscale",
			device:      DeviceInfo{Hosting: "self-hosted", LocalIps: []string{"100.64.0.5"}},
			wantHosting: "self-hosted",
			wantPath:    "tailscale-or-relay",
		},
		{
			name:        "shared",
			device:      DeviceInfo{IsGuest: true, AccessScope: "shared-scoped"},
			wantHosting: "shared",
			wantPath:    "lan-or-relay",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := targetFromDevice(&tt.device)
			if got == nil {
				t.Fatal("targetFromDevice() = nil")
			}
			if got.Hosting != tt.wantHosting {
				t.Fatalf("Hosting = %q, want %q", got.Hosting, tt.wantHosting)
			}
			if got.ExpectedPath != tt.wantPath {
				t.Fatalf("ExpectedPath = %q, want %q", got.ExpectedPath, tt.wantPath)
			}
		})
	}
}

func TestParsePingLatencyMs(t *testing.T) {
	for _, in := range []string{
		"64 bytes from 1.1.1.1: icmp_seq=0 ttl=58 time=12.345 ms",
		"64 bytes from 1.1.1.1: icmp_seq=1 ttl=58 time=12.345ms",
	} {
		if got := parsePingLatencyMs(in); got != 12 {
			t.Fatalf("parsePingLatencyMs(%q) = %d, want 12", in, got)
		}
	}
	if got := parsePingLatencyMs("no timing"); got != 0 {
		t.Fatalf("parsePingLatencyMs(no timing) = %d, want 0", got)
	}
}
