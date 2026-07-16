package main

// voice_mcp_test.go — P3 tests. Drives the inner runVoiceListenStart /
// runVoiceSpeak functions with a live BlackBoxManager so we exercise
// the actual command delivery path a client would receive, without
// requiring an HTTP server.

import (
	"testing"
	"time"
)

func TestVoiceListenStart_RequiresDevice(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	got := runVoiceListenStart(mgr, voiceListenStartArgs{})
	if got["ok"] != false {
		t.Fatalf("empty device must fail, got %+v", got)
	}
}

func TestVoiceListenStart_ScopedDelivery(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	devA := mgr.GetOrCreateSession("dev-A", "ios", "voice-listener")
	ch := devA.SubscribeCommands()
	defer devA.UnsubscribeCommands(ch)

	got := runVoiceListenStart(mgr, voiceListenStartArgs{
		Device: "dev-A", Provider: "whisper", SessionID: "rr_voice_1",
	})
	if got["ok"] != true || got["mode"] != "scoped" || got["reachedSession"] != true {
		t.Fatalf("expected scoped delivery, got %+v", got)
	}
	select {
	case cmd := <-ch:
		if cmd.Command != "voice_listen_start" {
			t.Fatalf("client received %q, want voice_listen_start", cmd.Command)
		}
		if cmd.Data["provider"] != "whisper" {
			t.Fatalf("provider field lost, got %+v", cmd.Data)
		}
		if cmd.Data["sessionId"] != "rr_voice_1" {
			t.Fatalf("sessionId field lost, got %+v", cmd.Data)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("target did not receive voice_listen_start")
	}
}

func TestVoiceSpeak_RequiresText(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	got := runVoiceSpeak(mgr, voiceSpeakArgs{Device: "dev-A"})
	if got["ok"] != false {
		t.Fatalf("empty text must fail, got %+v", got)
	}
}

func TestVoiceSpeak_ScopedDeliveryCarriesRenderOn(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	tv := mgr.GetOrCreateSession("tv-1", "tvos", "yaver-tv")
	ch := tv.SubscribeCommands()
	defer tv.UnsubscribeCommands(ch)

	got := runVoiceSpeak(mgr, voiceSpeakArgs{
		Device: "tv-1", Text: "hello Yaver", Voice: "en-US-Neural", Rate: 1.1, RenderOn: "phone-42",
	})
	if got["ok"] != true || got["mode"] != "scoped" || got["reachedSession"] != true {
		t.Fatalf("expected scoped delivery, got %+v", got)
	}
	select {
	case cmd := <-ch:
		if cmd.Command != "voice_speak" {
			t.Fatalf("client received %q, want voice_speak", cmd.Command)
		}
		if cmd.Data["text"] != "hello Yaver" {
			t.Fatalf("text lost, got %+v", cmd.Data)
		}
		if cmd.Data["renderOn"] != "phone-42" {
			t.Fatalf("renderOn lost (Axis-3 broken), got %+v", cmd.Data)
		}
		if cmd.Data["voice"] != "en-US-Neural" {
			t.Fatalf("voice hint lost, got %+v", cmd.Data)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("target did not receive voice_speak")
	}
}

func TestVoiceSpeak_BroadcastWhenDeviceEmpty(t *testing.T) {
	mgr, err := NewBlackBoxManager()
	if err != nil {
		t.Fatalf("NewBlackBoxManager: %v", err)
	}
	a := mgr.GetOrCreateSession("dev-A", "ios", "")
	b := mgr.GetOrCreateSession("dev-B", "android", "")
	chA := a.SubscribeCommands()
	defer a.UnsubscribeCommands(chA)
	chB := b.SubscribeCommands()
	defer b.UnsubscribeCommands(chB)

	got := runVoiceSpeak(mgr, voiceSpeakArgs{Text: "broadcast to all"})
	if got["ok"] != true || got["mode"] != "broadcast" {
		t.Fatalf("expected broadcast, got %+v", got)
	}
	seen := 0
	for i := 0; i < 2; i++ {
		select {
		case cmd := <-chA:
			if cmd.Command == "voice_speak" {
				seen++
			}
		case cmd := <-chB:
			if cmd.Command == "voice_speak" {
				seen++
			}
		case <-time.After(200 * time.Millisecond):
		}
	}
	if seen != 2 {
		t.Fatalf("broadcast reached %d/2 subscribers", seen)
	}
}

func TestVoice_NoBlackboxManagerFailsCleanly(t *testing.T) {
	if r := runVoiceListenStart(nil, voiceListenStartArgs{Device: "x"}); r["ok"] != false {
		t.Fatalf("nil mgr must fail, got %+v", r)
	}
	if r := runVoiceSpeak(nil, voiceSpeakArgs{Text: "hi"}); r["ok"] != false {
		t.Fatalf("nil mgr must fail, got %+v", r)
	}
}
