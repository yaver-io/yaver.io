package main

import "testing"

func TestNetMediumFromGateway(t *testing.T) {
	cases := []struct {
		gw, iface, ssid, port, want string
	}{
		{"172.20.10.1", "en0", "Kıvanç’s iPhone", "wifi", "hotspot-ios"}, // hotspot subnet wins over port
		{"192.168.43.1", "wlan0", "", "", "hotspot-android"},
		{"192.168.1.1", "en0", "HomeWiFi", "", "wifi"},
		{"192.168.1.1", "en0", "", "wifi", "wifi"},     // macOS en0 is Wi-Fi via port hint, not ethernet
		{"10.0.0.1", "en3", "", "ethernet", "ethernet"}, // port hint says ethernet
		{"10.0.0.1", "eth0", "", "", "ethernet"},        // name fallback when no hint
		{"", "iPhone USB", "", "", "hotspot-usb"},
		{"", "", "", "", "unknown"},
	}
	for _, c := range cases {
		if got := netMediumFromGateway(c.gw, c.iface, c.ssid, c.port); got != c.want {
			t.Errorf("netMediumFromGateway(%q,%q,%q,%q) = %q, want %q", c.gw, c.iface, c.ssid, c.port, got, c.want)
		}
	}
}

func TestNetPingRegex(t *testing.T) {
	macOS := `3 packets transmitted, 3 packets received, 0.0% packet loss
round-trip min/avg/max/stddev = 14.1/15.2/16.3/0.9 ms`
	if m := netLossRe.FindStringSubmatch(macOS); m == nil || m[1] != "0.0" {
		t.Errorf("loss parse failed: %v", m)
	}
	if m := netRttRe.FindStringSubmatch(macOS); m == nil || m[2] != "15.2" {
		t.Errorf("rtt avg parse failed: %v", m)
	}
	linux := `3 packets transmitted, 3 received, 25% packet loss, time 2003ms
rtt min/avg/max/mdev = 14.1/120.5/300.3/0.9 ms`
	if m := netLossRe.FindStringSubmatch(linux); m == nil || m[1] != "25" {
		t.Errorf("linux loss parse failed: %v", m)
	}
	if m := netRttRe.FindStringSubmatch(linux); m == nil || m[2] != "120.5" {
		t.Errorf("linux rtt avg parse failed: %v", m)
	}
}

// netSynthesize must surface the FIRST failing layer as the root cause and
// ignore the advisory "yaver" layer when computing the overall verdict.
func TestNetSynthesizeRootCause(t *testing.T) {
	rep := NetDoctorReport{Layers: []NetLayer{
		{Name: "link", Status: NetOK, Detail: "up"},
		{Name: "gateway", Status: NetOK, Detail: "ok"},
		{Name: "internet", Status: NetOK, Detail: "ok"},
		{Name: "dns", Status: NetFail, Detail: "names don't resolve", Hint: "use 1.1.1.1"},
		{Name: "https", Status: NetFail, Detail: "downstream symptom"},
		{Name: "yaver", Status: NetWarn, Detail: "relay", Hint: "pair via relay"},
	}}
	netSynthesize(&rep)
	if rep.Status != NetFail {
		t.Errorf("overall status = %q, want fail", rep.Status)
	}
	if rep.RootCause != "dns" {
		t.Errorf("root cause = %q, want dns (first fail, not the downstream https symptom)", rep.RootCause)
	}
	if len(rep.Remediation) == 0 || rep.Remediation[0] != "use 1.1.1.1" {
		t.Errorf("remediation = %v, want dns hint first", rep.Remediation)
	}
	// The yaver hint should still be appended so pairing advice survives.
	found := false
	for _, r := range rep.Remediation {
		if r == "pair via relay" {
			found = true
		}
	}
	if !found {
		t.Errorf("yaver pairing hint dropped from remediation: %v", rep.Remediation)
	}
}

func TestNetSynthesizeHealthy(t *testing.T) {
	rep := NetDoctorReport{Layers: []NetLayer{
		{Name: "link", Status: NetOK},
		{Name: "internet", Status: NetOK},
		{Name: "dns", Status: NetOK},
		{Name: "yaver", Status: NetWarn, Detail: "hotspot", Hint: "use relay"},
	}}
	netSynthesize(&rep)
	if rep.Status != NetOK {
		t.Errorf("status = %q, want ok (yaver warn must not downgrade)", rep.Status)
	}
	if rep.RootCause != "" {
		t.Errorf("root cause = %q, want empty", rep.RootCause)
	}
}
