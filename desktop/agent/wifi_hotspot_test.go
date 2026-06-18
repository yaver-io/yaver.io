package main

import "testing"

func TestParseIWPhyCapabilitiesAPSTA(t *testing.T) {
	out := `
Wiphy phy0
	Band 1:
		* 2412 MHz [1] (20.0 dBm)
	Band 2:
		* 5180 MHz [36] (23.0 dBm)
	Supported interface modes:
		 * managed
		 * AP
		Device supports HT-IBSS.
		Capabilities: 0x19ef
			HT40
	VHT Capabilities (0x338001b0):
		Supported Channel Width: neither 160 nor 80+80
		HE Iftypes: AP
	valid interface combinations:
		 * #{ managed } <= 1, #{ AP, mesh point } <= 1,
		   total <= 2, #channels <= 1
`
	caps := parseIWPhyCapabilities(out)
	if !caps.SupportsAP {
		t.Fatal("expected AP support")
	}
	if !caps.SupportsSTA {
		t.Fatal("expected STA/managed support")
	}
	if !caps.SupportsAPSTA {
		t.Fatal("expected AP+STA support from valid interface combination")
	}
	if !caps.Supports24GHz || !caps.Supports5GHz {
		t.Fatalf("bands = 2.4:%v 5:%v, want both", caps.Supports24GHz, caps.Supports5GHz)
	}
	if caps.ChannelCount != 2 {
		t.Fatalf("channel count = %d, want 2", caps.ChannelCount)
	}
	if !caps.SupportsHT40 || !caps.SupportsVHT {
		t.Fatalf("expected HT40 and VHT support: %+v", caps)
	}
}

func TestParseIWPhyCapabilitiesAPOnly(t *testing.T) {
	out := `
Wiphy phy0
	Band 1:
		* 2412 MHz [1] (20.0 dBm)
	Supported interface modes:
		 * managed
		 * AP
	valid interface combinations:
		 * #{ AP } <= 1,
		   total <= 1, #channels <= 1
`
	caps := parseIWPhyCapabilities(out)
	if !caps.SupportsAP {
		t.Fatal("expected AP support")
	}
	if caps.SupportsAPSTA {
		t.Fatal("did not expect AP+STA support from AP-only combination")
	}
}

func TestComboTotalAtLeastTwo(t *testing.T) {
	if comboTotalAtLeastTwo("total <= 1, #channels <= 1") {
		t.Fatal("total <= 1 should not support AP+STA")
	}
	if !comboTotalAtLeastTwo("total <= 2, #channels <= 1") {
		t.Fatal("total <= 2 should support AP+STA")
	}
	if !comboTotalAtLeastTwo("#{ managed } <= 1, #{ AP } <= 1") {
		t.Fatal("missing total should be treated as non-blocking")
	}
}
