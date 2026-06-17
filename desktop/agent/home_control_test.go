package main

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestMiboxKeycode(t *testing.T) {
	cases := map[string]int{
		"up": 19, "down": 20, "left": 21, "right": 22, "ok": 23,
		"back": 4, "home": 3, "play_pause": 85, "power": 26,
		"vol_up": 24, "vol_down": 25, "channel_up": 166, "5": 12,
	}
	for k, want := range cases {
		got, ok := miboxKeycode(k)
		if !ok || got != want {
			t.Errorf("miboxKeycode(%q) = %d,%v; want %d,true", k, got, ok, want)
		}
	}
	if _, ok := miboxKeycode("teleport"); ok {
		t.Error("miboxKeycode should reject unknown key")
	}
}

func TestAtvLogicalKey(t *testing.T) {
	cases := map[string]string{
		"ok": "select", "back": "menu", "vol_up": "volume_up",
		"up": "up", "play_pause": "play_pause",
	}
	for k, want := range cases {
		got, ok := atvLogicalKey(k)
		if !ok || got != want {
			t.Errorf("atvLogicalKey(%q) = %q,%v; want %q,true", k, got, ok, want)
		}
	}
	// digits/channel/mute aren't meaningful on Apple TV
	for _, k := range []string{"5", "channel_up", "mute"} {
		if _, ok := atvLogicalKey(k); ok {
			t.Errorf("atvLogicalKey(%q) should be unsupported", k)
		}
	}
}

func TestRunActivitySteps_AbortVsContinue(t *testing.T) {
	steps := []homeStep{
		{Device: "tv", Key: "power_on"},
		{Device: "tv", Key: "input_bad"}, // this one fails
		{Device: "sat", Key: "power"},
	}

	// Default on_error (abort): the failing 2nd step stops the run.
	calls := 0
	res, completed := runActivitySteps(steps, func(st homeStep) error {
		calls++
		if st.Key == "input_bad" {
			return errors.New("boom")
		}
		return nil
	}, nil)
	if completed {
		t.Error("expected activity to abort on the failing step")
	}
	if calls != 2 || len(res) != 2 {
		t.Errorf("expected to stop after 2 steps, ran %d (results=%d)", calls, len(res))
	}
	if res[1].OK || res[1].Error == "" {
		t.Error("failing step should be recorded with OK=false and an error")
	}

	// With onError=continue on the failing step, the run reaches the end.
	steps[1].OnError = "continue"
	calls = 0
	res, completed = runActivitySteps(steps, func(st homeStep) error {
		calls++
		if st.Key == "input_bad" {
			return errors.New("boom")
		}
		return nil
	}, nil)
	if !completed {
		t.Error("expected activity to complete when failing step is onError=continue")
	}
	if calls != 3 || len(res) != 3 {
		t.Errorf("expected all 3 steps to run, ran %d", calls)
	}
}

func TestActivityCreateValidation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	mk := func(steps []homeStep) OpsResult {
		body, _ := json.Marshal(map[string]interface{}{"name": "X", "steps": steps})
		return homeActivityCreateHandler(OpsContext{}, body)
	}

	// generic-verb step is valid (no device/key needed)
	if r := mk([]homeStep{{Verb: "ac_set", Payload: map[string]interface{}{"id": "bedroom", "power": true}}}); !r.OK {
		t.Errorf("verb-only step should be valid, got %+v", r)
	}
	// key step is valid
	if r := mk([]homeStep{{Device: "tv", Key: "power_on"}}); !r.OK {
		t.Errorf("key step should be valid, got %+v", r)
	}
	// a step with neither is rejected
	if r := mk([]homeStep{{App: "nothing"}}); r.OK || r.Code != "bad_payload" {
		t.Errorf("empty step should be rejected as bad_payload, got %+v", r)
	}
}

func TestSwitchCommand(t *testing.T) {
	cases := map[string]string{"open": "on", "on": "on", "close": "off", "off": "off", "toggle": "toggle", "stop": "stop"}
	for k, want := range cases {
		got, ok := switchCommand(k)
		if !ok || got != want {
			t.Errorf("switchCommand(%q) = %q,%v; want %q,true", k, got, ok, want)
		}
	}
	if _, ok := switchCommand("explode"); ok {
		t.Error("switchCommand should reject unknown key")
	}
}

func TestAtv2KeyName(t *testing.T) {
	cases := map[string]string{
		"up": "DPAD_UP", "ok": "DPAD_CENTER", "back": "BACK", "home": "HOME",
		"play_pause": "MEDIA_PLAY_PAUSE", "power": "POWER", "vol_up": "VOLUME_UP", "5": "5",
	}
	for k, want := range cases {
		got, ok := atv2KeyName(k)
		if !ok || got != want {
			t.Errorf("atv2KeyName(%q) = %q,%v; want %q,true", k, got, ok, want)
		}
	}
	if _, ok := atv2KeyName("nope"); ok {
		t.Error("atv2KeyName should reject unknown key")
	}
}

func TestIrCodeName(t *testing.T) {
	if got := irCodeName("livingtv", "power"); got != "livingtv/power" {
		t.Errorf("irCodeName = %q; want livingtv/power", got)
	}
}

func TestHomeStoreRoundTrip(t *testing.T) {
	// Redirect the store to a temp HOME so we don't touch the real ~/.yaver.
	t.Setenv("HOME", t.TempDir())

	s, err := loadHomeStore()
	if err != nil {
		t.Fatalf("loadHomeStore on empty: %v", err)
	}
	if len(s.Devices) != 0 || len(s.Activities) != 0 {
		t.Fatalf("fresh store should be empty, got %+v", s)
	}

	s.Devices = append(s.Devices, homeDevice{ID: "livingtv", Name: "Living Room", Kind: "apple_tv", Address: "AABBCC"})
	s.Activities = append(s.Activities, homeActivity{Name: "Watch", Steps: []homeStep{{Device: "livingtv", Key: "power_on"}}})
	if err := saveHomeStore(s); err != nil {
		t.Fatalf("saveHomeStore: %v", err)
	}

	got, err := loadHomeStore()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	d, ok := got.device("livingtv")
	if !ok || d.Kind != "apple_tv" || d.Address != "AABBCC" {
		t.Errorf("device not persisted correctly: %+v ok=%v", d, ok)
	}
	if _, ok := got.activity("watch"); !ok { // case-insensitive lookup
		t.Error("activity lookup should be case-insensitive")
	}
}
