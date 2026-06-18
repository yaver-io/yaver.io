package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNormalizeDpadTarget(t *testing.T) {
	cases := map[string]string{
		"appletv":     "appletv",
		"apple-tv":    "appletv",
		"tv-apple":    "appletv",
		"android-tv":  "androidtv",
		"google-tv":   "androidtv",
		"tv-android":  "androidtv",
		"home-device": "home",
	}
	for in, want := range cases {
		if got := normalizeDpadTarget(in); got != want {
			t.Fatalf("normalizeDpadTarget(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeDpadKey(t *testing.T) {
	cases := map[string]string{
		"DPAD_UP":     "up",
		"enter":       "select",
		"ok":          "select",
		"back":        "menu",
		"playpause":   "play_pause",
		"prev":        "previous",
		"vol_up":      "volume_up",
		"volume_down": "volume_down",
	}
	for in, want := range cases {
		got, ok := normalizeDpadKey(in)
		if !ok {
			t.Fatalf("normalizeDpadKey(%q) returned ok=false", in)
		}
		if got != want {
			t.Fatalf("normalizeDpadKey(%q) = %q, want %q", in, got, want)
		}
	}
	if got, ok := normalizeDpadKey("teleport"); ok || got != "" {
		t.Fatalf("unsupported key = (%q, %v), want empty false", got, ok)
	}
}

func TestDpadKeyForAndroidTV(t *testing.T) {
	if got := dpadKeyForAndroidTV("volume_up"); got != "vol_up" {
		t.Fatalf("volume_up -> %q", got)
	}
	if got := dpadKeyForAndroidTV("volume_down"); got != "vol_down" {
		t.Fatalf("volume_down -> %q", got)
	}
	if got := dpadKeyForAndroidTV("select"); got != "select" {
		t.Fatalf("select -> %q", got)
	}
}

func TestDpadInputRejectsBadPayload(t *testing.T) {
	res := dpadInputHandler(OpsContext{Ctx: context.Background(), Caller: "owner"}, nil)
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("expected bad_payload, got %#v", res)
	}

	body, _ := json.Marshal(map[string]interface{}{"target": "tv", "key": "teleport"})
	res = dpadInputHandler(OpsContext{Ctx: context.Background(), Caller: "owner"}, body)
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("expected bad_payload for unsupported target/key, got %#v", res)
	}

	body, _ = json.Marshal(map[string]interface{}{"target": "androidtv", "key": "up", "repeat": 11, "host": "127.0.0.1"})
	res = dpadInputHandler(OpsContext{Ctx: context.Background(), Caller: "owner"}, body)
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("expected repeat bad_payload, got %#v", res)
	}
}

func TestDpadInputAndroidTVRequiresHost(t *testing.T) {
	body, _ := json.Marshal(map[string]interface{}{"target": "androidtv", "key": "up"})
	res := dpadInputHandler(OpsContext{Ctx: context.Background(), Caller: "owner"}, body)
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("expected host bad_payload, got %#v", res)
	}
}

func TestDpadInputHomeRequiresDevice(t *testing.T) {
	body, _ := json.Marshal(map[string]interface{}{"target": "home", "key": "up"})
	res := dpadInputHandler(OpsContext{Ctx: context.Background(), Caller: "owner"}, body)
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("expected device bad_payload, got %#v", res)
	}
}
