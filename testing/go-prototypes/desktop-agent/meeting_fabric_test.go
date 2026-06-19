package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func resetMeetingFabricForTest(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	meetingFabricMu.Lock()
	meetingFabricCache = nil
	meetingFabricMu.Unlock()
}

func TestCreateMeetingRoomDefaultsToYaverNative(t *testing.T) {
	resetMeetingFabricForTest(t)

	room, err := createMeetingRoom(MeetingRoomCreateRequest{Title: "Founder Standup"})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if room.Provider != MeetingProviderYaver {
		t.Fatalf("provider = %q, want %q", room.Provider, MeetingProviderYaver)
	}
	if room.AdapterMode != MeetingAdapterNativeSFU {
		t.Fatalf("adapter = %q, want %q", room.AdapterMode, MeetingAdapterNativeSFU)
	}
	if room.JoinURL != "/call/founder-standup" {
		t.Fatalf("join URL = %q", room.JoinURL)
	}
	if room.Media.Transport != "webrtc-sfu" {
		t.Fatalf("transport = %q", room.Media.Transport)
	}
}

func TestCreateMeetingRoomRejectsDuplicateSlug(t *testing.T) {
	resetMeetingFabricForTest(t)

	if _, err := createMeetingRoom(MeetingRoomCreateRequest{Slug: "daily", Title: "Daily"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := createMeetingRoom(MeetingRoomCreateRequest{Slug: "daily", Title: "Daily 2"}); err == nil {
		t.Fatal("expected duplicate slug error")
	}
}

func TestMeetingCapabilitiesIncludeProviderFallbacks(t *testing.T) {
	caps := meetingAdapterCapabilities()
	byProvider := map[string]MeetingAdapterCapability{}
	for _, cap := range caps {
		byProvider[cap.Provider] = cap
	}
	for _, provider := range []string{MeetingProviderYaver, MeetingProviderZoom, MeetingProviderGoogleMeet, MeetingProviderMicrosoftTeams} {
		if byProvider[provider].Provider == "" {
			t.Fatalf("missing provider capability %q", provider)
		}
	}
	if got := byProvider[MeetingProviderZoom].Fallback; got != MeetingAdapterRemoteBrowser {
		t.Fatalf("zoom fallback = %q", got)
	}
}

func TestHandleCallJoinMintsScopedParticipantToken(t *testing.T) {
	resetMeetingFabricForTest(t)
	room, err := createMeetingRoom(MeetingRoomCreateRequest{Title: "Customer Call"})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}

	srv := &HTTPServer{}
	body := bytes.NewBufferString(`{"displayName":"Ada","surface":"browser"}`)
	req := httptest.NewRequest(http.MethodPost, "/call/"+room.Slug+"/join", body)
	w := httptest.NewRecorder()
	srv.handleCallPage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		OK          bool                    `json:"ok"`
		Room        MeetingRoom             `json:"room"`
		Participant MeetingParticipantToken `json:"participant"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.OK || out.Room.ID != room.ID {
		t.Fatalf("bad response: %+v", out)
	}
	if out.Participant.DisplayName != "Ada" || out.Participant.Token == "" {
		t.Fatalf("bad participant token: %+v", out.Participant)
	}
	if !strings.HasPrefix(out.Room.JoinURL, "/call/") {
		t.Fatalf("join URL not public call path: %q", out.Room.JoinURL)
	}
}

func TestHandleCallJoinReturnsLiveKitTokenWhenConfigured(t *testing.T) {
	resetMeetingFabricForTest(t)
	t.Setenv("YAVER_LIVEKIT_URL", "wss://livekit.example.test")
	t.Setenv("YAVER_LIVEKIT_API_KEY", "key")
	t.Setenv("YAVER_LIVEKIT_API_SECRET", "secret")

	room, err := createMeetingRoom(MeetingRoomCreateRequest{Title: "Live Call"})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	if room.Media.Status != "ready" {
		t.Fatalf("media status = %q, want ready", room.Media.Status)
	}

	srv := &HTTPServer{}
	req := httptest.NewRequest(http.MethodPost, "/call/"+room.Slug+"/join", bytes.NewBufferString(`{"displayName":"Lin"}`))
	w := httptest.NewRecorder()
	srv.handleCallPage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Media MeetingMediaJoin `json:"media"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Media.Status != "ready" || out.Media.URL != "wss://livekit.example.test" || out.Media.Token == "" {
		t.Fatalf("bad media join: %+v", out.Media)
	}
	if len(strings.Split(out.Media.Token, ".")) != 3 {
		t.Fatalf("media token does not look like JWT: %q", out.Media.Token)
	}
}
