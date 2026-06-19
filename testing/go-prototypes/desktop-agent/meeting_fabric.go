package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	MeetingProviderYaver          = "yaver-native"
	MeetingProviderZoom           = "zoom"
	MeetingProviderGoogleMeet     = "google-meet"
	MeetingProviderMicrosoftTeams = "microsoft-teams"

	MeetingAdapterNativeSFU       = "native-sfu"
	MeetingAdapterOfficialMedia   = "official-media-api"
	MeetingAdapterRemoteBrowser   = "remote-browser"
	MeetingAdapterLinkOnly        = "link-only"
	MeetingAdapterPSTNAudioBridge = "pstn-audio-bridge"
)

type LobbyParticipant struct {
	Identity    string    `json:"identity"`
	DisplayName string    `json:"displayName"`
	Email       string    `json:"email,omitempty"`
	Surface     string    `json:"surface"`
	RequestedAt time.Time `json:"requestedAt"`
	Status      string    `json:"status"` // pending, approved, denied
}

type InviteToken struct {
	ID        string    `json:"id"`
	RoomID    string    `json:"roomId"`
	Token     string    `json:"token"`
	Email     string    `json:"email,omitempty"`
	Role      string    `json:"role"` // guest, co-host
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	MaxUses   int       `json:"maxUses"`
	UsesCount int       `json:"usesCount"`
}

type MeetingRoom struct {
	ID           string `json:"id"`
	Slug         string `json:"slug"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	Provider     string `json:"provider"`
	AdapterMode  string `json:"adapterMode"`
	ExternalURL  string `json:"externalUrl,omitempty"`
	JoinURL      string `json:"joinUrl"`
	Status       string `json:"status"`
	AllowGuests  bool   `json:"allowGuests"`
	RequireLobby bool   `json:"requireLobby"`
	HostSurface  string `json:"hostSurface,omitempty"`
	OwnerID      string `json:"ownerId,omitempty"`
	// Lobby & Guest Scoping
	LobbyEnabled  bool               `json:"lobbyEnabled"`
	LobbyQueue    []LobbyParticipant `json:"lobbyQueue,omitempty"`
	AllowedGuests []string           `json:"allowedGuests,omitempty"` // Email list
	InviteTokens  []InviteToken      `json:"inviteTokens,omitempty"`
	// Room Expiry
	TTLMinutes int        `json:"ttlMinutes"` // 0 = no expiry
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	// Participant Management
	KickedUsers map[string]string    `json:"kickedUsers,omitempty"` // email -> reason
	BannedUsers map[string]time.Time `json:"bannedUsers,omitempty"` // email -> banned until
	// External Provider
	ExternalRoomID string `json:"externalRoomId,omitempty"`
	ExternalStatus string `json:"externalStatus,omitempty"`
	// Media & PSTN (PSTN is stubbed)
	Media     MeetingMediaConfig `json:"media"`
	PSTN      MeetingPSTNConfig  `json:"pstn"`
	CreatedAt time.Time          `json:"createdAt"`
	UpdatedAt time.Time          `json:"updatedAt"`
}

type MeetingMediaConfig struct {
	Provider      string   `json:"provider"`
	RoomName      string   `json:"roomName"`
	Transport     string   `json:"transport"`
	Status        string   `json:"status"`
	Capabilities  []string `json:"capabilities"`
	SetupRequired []string `json:"setupRequired,omitempty"`
}

type MeetingPSTNConfig struct {
	Enabled        bool     `json:"enabled"`
	Status         string   `json:"status"`
	DialInNumber   string   `json:"dialInNumber,omitempty"`
	ParticipantPIN string   `json:"participantPin,omitempty"`
	SetupRequired  []string `json:"setupRequired,omitempty"`
}

type MeetingParticipantToken struct {
	ID          string    `json:"id"`
	RoomID      string    `json:"roomId"`
	DisplayName string    `json:"displayName"`
	Surface     string    `json:"surface"`
	Role        string    `json:"role"`
	Token       string    `json:"token"`
	ExpiresAt   time.Time `json:"expiresAt"`
	CreatedAt   time.Time `json:"createdAt"`
}

type MeetingMediaJoin struct {
	Provider string `json:"provider"`
	URL      string `json:"url,omitempty"`
	Room     string `json:"room"`
	Token    string `json:"token,omitempty"`
	Status   string `json:"status"`
}

type MeetingAdapterCapability struct {
	Provider             string   `json:"provider"`
	Label                string   `json:"label"`
	PreferredAdapterMode string   `json:"preferredAdapterMode"`
	AdapterModes         []string `json:"adapterModes"`
	CanCreateMeeting     bool     `json:"canCreateMeeting"`
	CanHumanJoinBrowser  bool     `json:"canHumanJoinBrowser"`
	CanReadRealtimeAudio string   `json:"canReadRealtimeAudio"`
	CanSendRealtimeAudio string   `json:"canSendRealtimeAudio"`
	CanReadVideo         string   `json:"canReadVideo"`
	SupportsPSTNBridge   string   `json:"supportsPstnBridge"`
	Fallback             string   `json:"fallback"`
	Notes                []string `json:"notes,omitempty"`
}

type MeetingRoomCreateRequest struct {
	Slug         string `json:"slug"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	Provider     string `json:"provider,omitempty"`
	AdapterMode  string `json:"adapterMode,omitempty"`
	ExternalURL  string `json:"externalUrl,omitempty"`
	AllowGuests  *bool  `json:"allowGuests,omitempty"`
	RequireLobby *bool  `json:"requireLobby,omitempty"`
	HostSurface  string `json:"hostSurface,omitempty"`
	EnablePSTN   bool   `json:"enablePstn,omitempty"`
}

type meetingFabricStore struct {
	Rooms             []MeetingRoom             `json:"rooms"`
	ParticipantTokens []MeetingParticipantToken `json:"participantTokens,omitempty"`
}

var (
	meetingFabricMu    sync.Mutex
	meetingFabricCache *meetingFabricStore
	meetingSlugRe      = regexp.MustCompile(`[^a-z0-9-]+`)
)

func meetingFabricFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "meeting_rooms.json"), nil
}

func loadMeetingFabricLocked() *meetingFabricStore {
	if meetingFabricCache != nil {
		return meetingFabricCache
	}
	p, _ := meetingFabricFile()
	data, err := os.ReadFile(p)
	if err != nil {
		meetingFabricCache = &meetingFabricStore{}
		return meetingFabricCache
	}
	var store meetingFabricStore
	_ = json.Unmarshal(data, &store)
	meetingFabricCache = &store
	return meetingFabricCache
}

func saveMeetingFabricLocked() error {
	p, _ := meetingFabricFile()
	data, _ := json.MarshalIndent(loadMeetingFabricLocked(), "", "  ")
	return os.WriteFile(p, data, 0o600)
}

func listMeetingRooms() []MeetingRoom {
	meetingFabricMu.Lock()
	defer meetingFabricMu.Unlock()
	store := loadMeetingFabricLocked()
	out := append([]MeetingRoom(nil), store.Rooms...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func findMeetingRoomBySlug(slug string) (*MeetingRoom, int) {
	store := loadMeetingFabricLocked()
	for i := range store.Rooms {
		if store.Rooms[i].Slug == slug {
			return &store.Rooms[i], i
		}
	}
	return nil, -1
}

func normalizeMeetingSlug(slug, title string) string {
	s := strings.ToLower(strings.TrimSpace(slug))
	if s == "" {
		s = strings.ToLower(strings.TrimSpace(title))
	}
	s = strings.ReplaceAll(s, "_", "-")
	s = meetingSlugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "call"
	}
	if len(s) > 72 {
		s = strings.Trim(s[:72], "-")
	}
	return s
}

func defaultMeetingAdapter(provider string) string {
	switch provider {
	case MeetingProviderYaver, "":
		return MeetingAdapterNativeSFU
	case MeetingProviderZoom, MeetingProviderGoogleMeet, MeetingProviderMicrosoftTeams:
		return MeetingAdapterRemoteBrowser
	default:
		return MeetingAdapterLinkOnly
	}
}

func meetingAdapterCapabilities() []MeetingAdapterCapability {
	return []MeetingAdapterCapability{
		{
			Provider:             MeetingProviderYaver,
			Label:                "Yaver native call",
			PreferredAdapterMode: MeetingAdapterNativeSFU,
			AdapterModes:         []string{MeetingAdapterNativeSFU, MeetingAdapterPSTNAudioBridge},
			CanCreateMeeting:     true,
			CanHumanJoinBrowser:  true,
			CanReadRealtimeAudio: "yes",
			CanSendRealtimeAudio: "yes",
			CanReadVideo:         "yes",
			SupportsPSTNBridge:   "audio-only",
			Fallback:             MeetingAdapterNativeSFU,
		},
		{
			Provider:             MeetingProviderZoom,
			Label:                "Zoom",
			PreferredAdapterMode: MeetingAdapterOfficialMedia,
			AdapterModes:         []string{MeetingAdapterOfficialMedia, MeetingAdapterRemoteBrowser, MeetingAdapterLinkOnly},
			CanCreateMeeting:     false,
			CanHumanJoinBrowser:  true,
			CanReadRealtimeAudio: "rtms-app-required",
			CanSendRealtimeAudio: "not-via-rtms",
			CanReadVideo:         "rtms-app-required",
			SupportsPSTNBridge:   "via-yaver-bridge",
			Fallback:             MeetingAdapterRemoteBrowser,
			Notes:                []string{"Meeting SDK is for human embedded join; RTMS is the media access path."},
		},
		{
			Provider:             MeetingProviderGoogleMeet,
			Label:                "Google Meet",
			PreferredAdapterMode: MeetingAdapterOfficialMedia,
			AdapterModes:         []string{MeetingAdapterOfficialMedia, MeetingAdapterRemoteBrowser, MeetingAdapterLinkOnly},
			CanCreateMeeting:     true,
			CanHumanJoinBrowser:  true,
			CanReadRealtimeAudio: "workspace-media-api-admin-required",
			CanSendRealtimeAudio: "limited",
			CanReadVideo:         "workspace-media-api-admin-required",
			SupportsPSTNBridge:   "via-yaver-bridge",
			Fallback:             MeetingAdapterRemoteBrowser,
		},
		{
			Provider:             MeetingProviderMicrosoftTeams,
			Label:                "Microsoft Teams",
			PreferredAdapterMode: MeetingAdapterOfficialMedia,
			AdapterModes:         []string{MeetingAdapterOfficialMedia, MeetingAdapterRemoteBrowser, MeetingAdapterLinkOnly},
			CanCreateMeeting:     true,
			CanHumanJoinBrowser:  true,
			CanReadRealtimeAudio: "graph-cloud-communications-tenant-required",
			CanSendRealtimeAudio: "graph-cloud-communications-tenant-required",
			CanReadVideo:         "application-hosted-media-required",
			SupportsPSTNBridge:   "via-yaver-bridge",
			Fallback:             MeetingAdapterRemoteBrowser,
		},
	}
}

func createMeetingRoom(req MeetingRoomCreateRequest) (MeetingRoom, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return MeetingRoom{}, fmt.Errorf("title required")
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = MeetingProviderYaver
	}
	adapter := strings.TrimSpace(req.AdapterMode)
	if adapter == "" {
		adapter = defaultMeetingAdapter(provider)
	}
	allowGuests := true
	if req.AllowGuests != nil {
		allowGuests = *req.AllowGuests
	}
	requireLobby := false
	if req.RequireLobby != nil {
		requireLobby = *req.RequireLobby
	}

	now := time.Now().UTC()
	slug := normalizeMeetingSlug(req.Slug, title)
	room := MeetingRoom{
		ID:           randomFormID(),
		Slug:         slug,
		Title:        title,
		Description:  strings.TrimSpace(req.Description),
		Provider:     provider,
		AdapterMode:  adapter,
		ExternalURL:  strings.TrimSpace(req.ExternalURL),
		AllowGuests:  allowGuests,
		RequireLobby: requireLobby,
		HostSurface:  strings.TrimSpace(req.HostSurface),
		Status:       "ready",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	room.JoinURL = "/call/" + room.Slug
	room.Media = meetingMediaDefaults(room)
	room.PSTN = meetingPSTNDefaults(req.EnablePSTN)

	meetingFabricMu.Lock()
	defer meetingFabricMu.Unlock()
	store := loadMeetingFabricLocked()
	if _, idx := findMeetingRoomBySlug(room.Slug); idx >= 0 {
		return MeetingRoom{}, fmt.Errorf("meeting room slug already exists")
	}
	store.Rooms = append(store.Rooms, room)
	if err := saveMeetingFabricLocked(); err != nil {
		return MeetingRoom{}, err
	}
	return room, nil
}

func meetingMediaDefaults(room MeetingRoom) MeetingMediaConfig {
	switch room.AdapterMode {
	case MeetingAdapterNativeSFU:
		status := "needs-media-server"
		setup := []string{
			"configure LiveKit or a compatible SFU",
			"configure TURN credentials for off-network guests",
		}
		if liveKitConfigured() {
			status = "ready"
			setup = nil
		}
		return MeetingMediaConfig{
			Provider:      "livekit-compatible",
			RoomName:      room.ID,
			Transport:     "webrtc-sfu",
			Status:        status,
			Capabilities:  []string{"audio", "video", "screenshare", "data"},
			SetupRequired: setup,
		}
	case MeetingAdapterOfficialMedia:
		return MeetingMediaConfig{
			Provider:     room.Provider,
			RoomName:     room.ID,
			Transport:    "provider-media-api",
			Status:       "adapter-required",
			Capabilities: []string{"audio", "video"},
			SetupRequired: []string{
				"connect provider OAuth/admin approval",
				"run provider-specific media adapter",
			},
		}
	case MeetingAdapterRemoteBrowser:
		return MeetingMediaConfig{
			Provider:     "yaver-remote-browser",
			RoomName:     room.ID,
			Transport:    "remote-runtime-webrtc",
			Status:       "bridge-required",
			Capabilities: []string{"screen", "audio", "remote-input"},
			SetupRequired: []string{
				"launch remote browser bridge",
				"configure virtual microphone/speaker routing for two-way audio",
			},
		}
	default:
		return MeetingMediaConfig{
			Provider:     room.Provider,
			RoomName:     room.ID,
			Transport:    "link",
			Status:       "link-only",
			Capabilities: []string{"open-link"},
		}
	}
}

func meetingPSTNDefaults(enabled bool) MeetingPSTNConfig {
	if !enabled {
		return MeetingPSTNConfig{Enabled: false, Status: "disabled"}
	}
	return MeetingPSTNConfig{
		Enabled:        true,
		Status:         "needs-sip-provider",
		ParticipantPIN: randomFormID(),
		SetupRequired: []string{
			"configure SIP/PSTN provider",
			"bridge PSTN audio to the selected room adapter",
		},
	}
}

func mintMeetingParticipantToken(slug, displayName, surface string) (MeetingParticipantToken, MeetingRoom, error) {
	meetingFabricMu.Lock()
	defer meetingFabricMu.Unlock()
	room, _ := findMeetingRoomBySlug(slug)
	if room == nil {
		return MeetingParticipantToken{}, MeetingRoom{}, fmt.Errorf("room not found")
	}
	if !room.AllowGuests {
		return MeetingParticipantToken{}, MeetingRoom{}, fmt.Errorf("guests disabled")
	}
	now := time.Now().UTC()
	if displayName == "" {
		displayName = "Guest"
	}
	if surface == "" {
		surface = "browser"
	}
	token := MeetingParticipantToken{
		ID:          randomFormID(),
		RoomID:      room.ID,
		DisplayName: displayName,
		Surface:     surface,
		Role:        "guest",
		Token:       randomFormID() + randomFormID(),
		CreatedAt:   now,
		ExpiresAt:   now.Add(8 * time.Hour),
	}
	store := loadMeetingFabricLocked()
	store.ParticipantTokens = append(store.ParticipantTokens, token)
	if err := saveMeetingFabricLocked(); err != nil {
		return MeetingParticipantToken{}, MeetingRoom{}, err
	}
	return token, *room, nil
}

func liveKitConfigured() bool {
	return os.Getenv("YAVER_LIVEKIT_URL") != "" &&
		os.Getenv("YAVER_LIVEKIT_API_KEY") != "" &&
		os.Getenv("YAVER_LIVEKIT_API_SECRET") != ""
}

func meetingMediaJoin(room MeetingRoom, participant MeetingParticipantToken) MeetingMediaJoin {
	join := MeetingMediaJoin{
		Provider: room.Media.Provider,
		Room:     room.Media.RoomName,
		Status:   room.Media.Status,
	}
	if room.AdapterMode != MeetingAdapterNativeSFU || !liveKitConfigured() {
		return join
	}
	token, err := liveKitAccessToken(os.Getenv("YAVER_LIVEKIT_API_KEY"), os.Getenv("YAVER_LIVEKIT_API_SECRET"), room.Media.RoomName, participant.DisplayName, participant.ExpiresAt)
	if err != nil {
		join.Status = "token-error"
		return join
	}
	join.URL = os.Getenv("YAVER_LIVEKIT_URL")
	join.Token = token
	join.Status = "ready"
	return join
}

func liveKitAccessToken(apiKey, apiSecret, roomName, identity string, expiresAt time.Time) (string, error) {
	now := time.Now().Unix()
	claims := map[string]interface{}{
		"iss":  apiKey,
		"sub":  identity,
		"name": identity,
		"nbf":  now,
		"exp":  expiresAt.Unix(),
		"video": map[string]interface{}{
			"roomJoin":     true,
			"room":         roomName,
			"canPublish":   true,
			"canSubscribe": true,
		},
	}
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	h, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	p, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	mac := hmac.New(sha256.New, []byte(apiSecret))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *HTTPServer) handleMeetingRooms(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":           true,
			"rooms":        listMeetingRooms(),
			"capabilities": meetingAdapterCapabilities(),
		})
	case http.MethodPost:
		var req MeetingRoomCreateRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		room, err := createMeetingRoom(req)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true, "room": room})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET/POST")
	}
}

func (s *HTTPServer) handleMeetingCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "capabilities": meetingAdapterCapabilities()})
}

func (s *HTTPServer) handleCallPage(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/call/")
	path = strings.Trim(path, "/")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(path, "/")
	slug := parts[0]
	if len(parts) > 1 && parts[1] == "join" {
		s.handleCallJoin(w, r, slug)
		return
	}
	meetingFabricMu.Lock()
	room, _ := findMeetingRoomBySlug(slug)
	var out *MeetingRoom
	if room != nil {
		copy := *room
		out = &copy
	}
	meetingFabricMu.Unlock()
	if out == nil {
		http.NotFound(w, r)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "room": out})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, renderCallPage(out))
}

func (s *HTTPServer) handleCallJoin(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req struct {
		DisplayName string `json:"displayName"`
		Surface     string `json:"surface"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
	token, room, err := mintMeetingParticipantToken(slug, strings.TrimSpace(req.DisplayName), strings.TrimSpace(req.Surface))
	if err != nil {
		jsonError(w, http.StatusForbidden, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":          true,
		"room":        room,
		"participant": token,
		"media":       meetingMediaJoin(room, token),
	})
}

func renderCallPage(room *MeetingRoom) string {
	title := html.EscapeString(room.Title)
	desc := html.EscapeString(room.Description)
	status := html.EscapeString(room.Media.Status)
	adapterMode := html.EscapeString(room.AdapterMode)

	return fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title>
<style>body{font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;margin:0;background:#0f172a;color:#f8fafc}.setup{max-width:640px;margin:0 auto;padding:48px 20px}.setup h1{font-size:32px;margin:0 0 8px}.setup .meta{color:#cbd5e1;margin:0 0 24px}.setup .panel{border:1px solid #334155;border-radius:8px;padding:18px;background:#111827}.setup input,.setup button{box-sizing:border-box;width:100%%;padding:12px;margin-top:10px;border-radius:6px;border:1px solid #475569;font-size:16px}.setup button{background:#22c55e;color:#04130a;border:0;font-weight:700}.setup .small{color:#94a3b8;font-size:13px}.room{display:none;padding:16px;min-height:100vh}.room.active{display:block}.room-header{padding:16px;background:#0f172a;border-bottom:1px solid #334155;position:sticky;top:0;z-index:10}.room-title{font-size:18px;font-weight:700;color:#f8fafc}.room-status{font-size:14px;color:#cbd5e1;margin-top:4px}.room-controls{margin-top:16px;display:flex;gap:8px}.room-controls button{width:44px;height:44px;border-radius:50%%;border:1px solid #475569;background:#1e293b;color:#f8fafc;font-size:20px;cursor:pointer;transition:background 0.2s}.room-controls button:hover{background:#334155}.room-controls button.active{background:#22c55e}.room-controls button.inactive{background:#ef4444}.participants{display:grid;grid-template-columns:repeat(auto-fit,minmax(300px,1fr));gap:16px;padding:16px}.participant{background:#111827;border-radius:8px;overflow:hidden;aspect-ratio:16/9;position:relative}.participant video{width:100%%;height:100%%;object-fit:cover}.participant-name{position:absolute;bottom:0;left:0;right:0;padding:8px;background:linear-gradient(transparent,rgba(0,0,0,0.8));color:#f8fafc;font-size:14px;font-weight:600}.participant.speaking .participant-name{background:linear-gradient(transparent,rgba(34,197,94,0.8))}.hidden{display:none}.error{background:#7f1d1d;border:1px solid #991b1b;color:#fecaca;padding:16px;border-radius:8px;margin-bottom:16px}</style></head><body>
<div class="setup" id="setup">
 <h1>%s</h1>
 <p class="meta">%s</p>
 <div class="panel">
  <form id="join">
   <input name="displayName" placeholder="Your name" autocomplete="name" required>
   <button type="submit">Join call</button>
  </form>
  <p class="small">Media adapter: %s · %s</p>
 </div>
</div>
<div class="room" id="room">
 <div class="room-header">
  <div class="room-title">%s</div>
  <div class="room-status" id="roomStatus"></div>
  <div class="room-controls">
   <button id="btnMic" title="Toggle microphone" class="active">🎤</button>
   <button id="btnCamera" title="Toggle camera" class="active">📹</button>
   <button id="btnLeave" title="Leave call">🚪</button>
  </div>
 </div>
 <div class="participants" id="participants"></div>
</div>
<script src="https://unpkg.com/livekit-client/dist/livekit-client.js"></script>
<script>
window.addEventListener('DOMContentLoaded', () => {
 const setup = document.getElementById('setup');
 const room = document.getElementById('room');
 const joinForm = document.getElementById('join');
 const participantsContainer = document.getElementById('participants');
 const roomStatus = document.getElementById('roomStatus');
 const btnMic = document.getElementById('btnMic');
 const btnCamera = document.getElementById('btnCamera');
 const btnLeave = document.getElementById('btnLeave');
 
 let livekitRoom = null;
 let localParticipant = null;
 let isMicEnabled = true;
 let isCameraEnabled = true;

  joinForm.addEventListener('submit', async (e) => {
   e.preventDefault();
   const displayName = new FormData(e.target).get('displayName') || 'Guest';
   const r = await fetch(location.pathname.replace(/\/$/, '') + '/join', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ displayName: displayName, surface: 'browser' })
   });
   const j = await r.json();
   if (!j.ok) {
    alert(j.error || 'Join failed');
    return;
   }
   setup.classList.add('hidden');
   room.classList.add('active');
   await connectToRoom(j.token, j.room);
  });
 
  async function connectToRoom(token, roomInfo) {
   try {
    const LiveKit = window.LiveKitClient;
    const livekitUrl = roomInfo.media.liveKitUrl;
    
    livekitRoom = new LiveKit.Room({
     adaptiveStream: true,
     dynacast: true,
     videoCaptureDefaults: {
      resolution: LiveKit.VideoPresets.h720,
     },
    });

    livekitRoom.on(LiveKit.RoomEvent.ParticipantConnected, (participant) => {
     roomStatus.textContent = 'Participant joined: ' + participant.identity;
     addParticipantToGrid(participant);
    });

    livekitRoom.on(LiveKit.RoomEvent.ParticipantDisconnected, (participant) => {
     roomStatus.textContent = 'Participant left: ' + participant.identity;
     removeParticipantFromGrid(participant.identity);
    });

   livekitRoom.on(LiveKit.RoomEvent.TrackPublished, (pub, participant) => {
    if (participant !== livekitRoom.localParticipant) {
     const track = pub.track;
     if (track) {
      attachTrackToGrid(participant.identity, track, participant.name);
     }
    }
   });

   livekitRoom.on(LiveKit.RoomEvent.TrackUnpublished, (pub, participant) => {
    if (participant !== livekitRoom.localParticipant) {
     removeParticipantFromGrid(participant.identity);
    }
   });

   livekitRoom.on(LiveKit.RoomEvent.TrackSubscribed, (track, publication, participant) => {
    if (participant !== livekitRoom.localParticipant) {
     attachTrackToGrid(participant.identity, track, participant.name);
    }
   });

    livekitRoom.on(LiveKit.RoomEvent.ActiveSpeakersChanged, (speakers) => {
     document.querySelectorAll('.participant').forEach(el => {
      el.classList.remove('speaking');
     });
     speakers.forEach(speaker => {
      const el = document.getElementById('participant-' + speaker.identity);
      if (el) el.classList.add('speaking');
     });
    });
 
    await livekitRoom.connect(livekitUrl, token);
    localParticipant = livekitRoom.localParticipant;
    roomStatus.textContent = 'Connected as ' + localParticipant.identity;
 
    // Publish local tracks
    await publishLocalTracks();
 
   } catch (err) {
    console.error('Failed to connect:', err);
    roomStatus.textContent = 'Connection failed: ' + err.message;
   }
 }

 async function publishLocalTracks() {
  if (!livekitRoom) return;
  
  // Publish microphone
  try {
   await livekitRoom.localParticipant.setMicrophoneEnabled(true);
   const micTrack = livekitRoom.localParticipant.getTrack(LiveKit.TrackSource.Microphone);
   if (micTrack && micTrack.track) {
    attachTrackToGrid('local', micTrack.track, localParticipant.name);
   }
  } catch (err) {
   console.error('Failed to publish microphone:', err);
  }

  // Publish camera
  try {
   await livekitRoom.localParticipant.setCameraEnabled(true);
   const camTrack = livekitRoom.localParticipant.getTrack(LiveKit.TrackSource.Camera);
   if (camTrack && camTrack.track) {
    const localEl = document.getElementById('participant-local');
    if (localEl) localEl.remove(); // Replace with camera
    attachTrackToGrid('local', camTrack.track, localParticipant.name);
   }
  } catch (err) {
   console.error('Failed to publish camera:', err);
  }
 }

 function addParticipantToGrid(participant) {
   const el = document.createElement('div');
   el.className = 'participant';
   el.id = 'participant-' + participant.identity;
   el.innerHTML = '<div class="participant-name">' + participant.name + '</div>';
   participantsContainer.appendChild(el);
 }
 
 function removeParticipantFromGrid(identity) {
   const el = document.getElementById('participant-' + identity);
   if (el) el.remove();
 }
 
 function attachTrackToGrid(identity, track, name) {
   let el = document.getElementById('participant-' + identity);
   if (!el) {
    el = document.createElement('div');
    el.className = 'participant';
    el.id = 'participant-' + identity;
    el.innerHTML = '<div class="participant-name">' + name + '</div>';
    participantsContainer.appendChild(el);
   }

  const videoEl = document.createElement('video');
  videoEl.autoplay = true;
  videoEl.muted = identity === 'local';
  videoEl.playsInline = true;
  
  if (track.kind === 'video') {
   track.attach(videoEl);
   const oldVideo = el.querySelector('video');
   if (oldVideo) oldVideo.remove();
   el.insertBefore(videoEl, el.firstChild);
  } else if (track.kind === 'audio' && identity !== 'local') {
   track.attach(videoEl);
   videoEl.style.display = 'none';
   el.appendChild(videoEl);
  }
 }

 // Control buttons
 btnMic.addEventListener('click', async () => {
  if (!livekitRoom) return;
  isMicEnabled = !isMicEnabled;
  await livekitRoom.localParticipant.setMicrophoneEnabled(isMicEnabled);
  btnMic.classList.toggle('active', isMicEnabled);
  btnMic.classList.toggle('inactive', !isMicEnabled);
 });

 btnCamera.addEventListener('click', async () => {
  if (!livekitRoom) return;
  isCameraEnabled = !isCameraEnabled;
  await livekitRoom.localParticipant.setCameraEnabled(isCameraEnabled);
  btnCamera.classList.toggle('active', isCameraEnabled);
  btnCamera.classList.toggle('inactive', !isCameraEnabled);
  
  const localEl = document.getElementById('participant-local');
  if (isCameraEnabled) {
   const camTrack = livekitRoom.localParticipant.getTrack(LiveKit.TrackSource.Camera);
   if (camTrack && camTrack.track) {
    if (localEl) localEl.remove();
    attachTrackToGrid('local', camTrack.track, localParticipant.name);
   }
  } else {
   if (localEl) {
    const video = localEl.querySelector('video');
    if (video) video.remove();
   }
  }
 });

 btnLeave.addEventListener('click', async () => {
  if (livekitRoom) {
   await livekitRoom.disconnect();
  }
  setup.classList.remove('hidden');
  room.classList.remove('active');
  participantsContainer.innerHTML = '';
 });
   });
 </script></body></html>`, title, title, desc, adapterMode, status, title)
}

// ============================================================================
// PSTN Audio Bridge (STUB)
// ============================================================================

func meetingPSTNStubSetup() MeetingPSTNConfig {
	// STUB: PSTN is not yet implemented
	return MeetingPSTNConfig{
		Enabled: false,
		Status:  "stub-not-implemented",
		SetupRequired: []string{
			"PSTN audio bridge is not yet implemented",
			"To enable PSTN, configure Twilio or SIP provider credentials",
			"Set TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN in environment",
		},
	}
}

func meetingPSTNDialIn(roomSlug string) (string, error) {
	// STUB: Generate dial-in number and PIN
	return "+15551234567 (STUB)", fmt.Errorf("PSTN not implemented")
}

func meetingPSTNGeneratePIN() string {
	// STUB: Generate random PIN
	return "123456"
}

// ============================================================================
// Lobby Management
// ============================================================================

func handleMeetingLobbyApprove(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	meetingFabricMu.Lock()
	defer meetingFabricMu.Unlock()

	var req struct {
		Identity string `json:"identity"`
		Approved bool   `json:"approved"`
		Reason   string `json:"reason,omitempty"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)

	room, idx := findMeetingRoomBySlug(slug)
	if room == nil {
		jsonError(w, http.StatusNotFound, "room not found")
		return
	}

	// Find participant in lobby
	found := -1
	for i, p := range room.LobbyQueue {
		if p.Identity == req.Identity {
			found = i
			break
		}
	}

	if found == -1 {
		jsonError(w, http.StatusNotFound, "participant not in lobby")
		return
	}

	// Update participant status
	store := loadMeetingFabricLocked()
	if req.Approved {
		store.Rooms[idx].LobbyQueue[found].Status = "approved"
	} else {
		store.Rooms[idx].LobbyQueue[found].Status = "denied"
		store.Rooms[idx].LobbyQueue[found] = LobbyParticipant{
			Identity:    store.Rooms[idx].LobbyQueue[found].Identity,
			DisplayName: store.Rooms[idx].LobbyQueue[found].DisplayName,
			Email:       store.Rooms[idx].LobbyQueue[found].Email,
			Surface:     store.Rooms[idx].LobbyQueue[found].Surface,
			RequestedAt: store.Rooms[idx].LobbyQueue[found].RequestedAt,
			Status:      "denied",
		}
	}
	store.Rooms[idx].UpdatedAt = time.Now()

	saveMeetingFabricLocked()
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"room":    store.Rooms[idx],
		"message": fmt.Sprintf("participant %s", map[bool]string{true: "approved", false: "denied"}[req.Approved]),
	})
}

func handleMeetingLobbyQueue(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	meetingFabricMu.Lock()
	defer meetingFabricMu.Unlock()

	room, _ := findMeetingRoomBySlug(slug)
	if room == nil {
		jsonError(w, http.StatusNotFound, "room not found")
		return
	}

	if !room.RequireLobby {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":     true,
			"queue":  []LobbyParticipant{},
			"reason": "lobby not enabled for this room",
		})
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"queue": room.LobbyQueue,
	})
}

func handleMeetingLobbyJoin(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		DisplayName string `json:"displayName"`
		Email       string `json:"email,omitempty"`
		Surface     string `json:"surface"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)

	meetingFabricMu.Lock()
	defer meetingFabricMu.Unlock()

	room, idx := findMeetingRoomBySlug(slug)
	if room == nil {
		jsonError(w, http.StatusNotFound, "room not found")
		return
	}

	// Check if room is expired
	if room.ExpiresAt != nil && room.ExpiresAt.Before(time.Now()) {
		jsonError(w, http.StatusForbidden, "room has expired")
		return
	}

	// Check if user is banned
	if room.BannedUsers != nil {
		if bannedUntil, exists := room.BannedUsers[req.Email]; exists && bannedUntil.After(time.Now()) {
			jsonError(w, http.StatusForbidden, fmt.Sprintf("you are banned until %s", bannedUntil.Format(time.RFC3339)))
			return
		}
	}

	store := loadMeetingFabricLocked()

	// Add to lobby queue
	participant := LobbyParticipant{
		Identity:    randomFormID(),
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Surface:     req.Surface,
		RequestedAt: time.Now(),
		Status:      "pending",
	}

	store.Rooms[idx].LobbyQueue = append(store.Rooms[idx].LobbyQueue, participant)
	store.Rooms[idx].UpdatedAt = time.Now()

	saveMeetingFabricLocked()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":          true,
		"room":        store.Rooms[idx],
		"participant": participant,
		"message":     "added to lobby queue - waiting for host approval",
	})
}

// ============================================================================
// Invite Tokens
// ============================================================================

func handleMeetingInviteCreate(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	meetingFabricMu.Lock()
	defer meetingFabricMu.Unlock()

	var req struct {
		Email    string `json:"email"`
		Role     string `json:"role"` // guest, co-host
		MaxUses  int    `json:"maxUses,omitempty"`
		ExpHours int    `json:"expHours,omitempty"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)

	room, idx := findMeetingRoomBySlug(slug)
	if room == nil {
		jsonError(w, http.StatusNotFound, "room not found")
		return
	}

	// Validate role
	if req.Role != "guest" && req.Role != "co-host" {
		jsonError(w, http.StatusBadRequest, "role must be 'guest' or 'co-host'")
		return
	}

	// Check if room allows guests
	if !room.AllowGuests && req.Role == "guest" {
		jsonError(w, http.StatusForbidden, "guests not allowed in this room")
		return
	}

	// Generate invite token
	token := randomFormID()
	expiresAt := time.Now().Add(24 * time.Hour) // default 24h
	if req.ExpHours > 0 {
		expiresAt = time.Now().Add(time.Duration(req.ExpHours) * time.Hour)
	}

	maxUses := 1 // default
	if req.MaxUses > 0 {
		maxUses = req.MaxUses
	}

	invite := InviteToken{
		ID:        randomFormID(),
		RoomID:    room.ID,
		Token:     token,
		Email:     req.Email,
		Role:      req.Role,
		CreatedBy: "system", // In production, use authenticated user email
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
		MaxUses:   maxUses,
		UsesCount: 0,
	}

	store := loadMeetingFabricLocked()
	store.Rooms[idx].InviteTokens = append(store.Rooms[idx].InviteTokens, invite)
	store.Rooms[idx].UpdatedAt = time.Now()

	saveMeetingLocked()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"room":      store.Rooms[idx],
		"invite":    invite,
		"inviteUrl": fmt.Sprintf("%s?token=%s", room.JoinURL, token),
	})
}

func handleMeetingInviteList(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	meetingFabricMu.Lock()
	defer meetingFabricMu.Unlock()

	room, _ := findMeetingRoomBySlug(slug)
	if room == nil {
		jsonError(w, http.StatusNotFound, "room not found")
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"room":    room,
		"invites": room.InviteTokens,
	})
}

func handleMeetingInviteDelete(w http.ResponseWriter, r *http.Request, slug, inviteId string) {
	if r.Method != http.MethodDelete {
		jsonError(w, http.StatusMethodNotAllowed, "use DELETE")
		return
	}

	meetingFabricMu.Lock()
	defer meetingFabricMu.Unlock()

	room, idx := findMeetingRoomBySlug(slug)
	if room == nil {
		jsonError(w, http.StatusNotFound, "room not found")
		return
	}

	store := loadMeetingFabricLocked()
	found := -1
	for i, invite := range store.Rooms[idx].InviteTokens {
		if invite.ID == inviteId {
			found = i
			break
		}
	}

	if found == -1 {
		jsonError(w, http.StatusNotFound, "invite token not found")
		return
	}

	// Remove invite
	store.Rooms[idx].InviteTokens = append(
		store.Rooms[idx].InviteTokens[:found],
		store.Rooms[idx].InviteTokens[found+1:]...,
	)
	store.Rooms[idx].UpdatedAt = time.Now()

	saveMeetingLocked()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"room":    store.Rooms[idx],
		"message": "invite token revoked",
	})
}

func validateInviteToken(token string) (*InviteToken, *MeetingRoom, error) {
	meetingFabricMu.Lock()
	defer meetingFabricMu.Unlock()

	store := loadMeetingFabricLocked()

	for roomIdx, room := range store.Rooms {
		for i, invite := range room.InviteTokens {
			if invite.Token == token {
				// Check if expired
				if invite.ExpiresAt.Before(time.Now()) {
					return nil, nil, fmt.Errorf("invite token expired")
				}
				// Check if max uses exceeded
				if invite.UsesCount >= invite.MaxUses {
					return nil, nil, fmt.Errorf("invite token usage limit exceeded")
				}
				return &invite, &store.Rooms[roomIdx], nil
			}
		}
	}
	return nil, nil, fmt.Errorf("invalid invite token")
}

func consumeInviteToken(token string) error {
	meetingFabricMu.Lock()
	defer meetingFabricMu.Unlock()

	store := loadMeetingFabricLocked()

	for roomIdx, room := range store.Rooms {
		for i, invite := range room.InviteTokens {
			if invite.Token == token {
				// Increment uses count
				store.Rooms[roomIdx].InviteTokens[i].UsesCount++
				store.Rooms[roomIdx].UpdatedAt = time.Now()

				// Remove if max uses reached
				if store.Rooms[roomIdx].InviteTokens[i].UsesCount >= invite.MaxUses {
					store.Rooms[roomIdx].InviteTokens = append(
						store.Rooms[roomIdx].InviteTokens[:i],
						store.Rooms[roomIdx].InviteTokens[i+1:]...,
					)
				}

				saveMeetingLocked()
				return nil
			}
		}
	}
	return fmt.Errorf("invite token not found")
}

// ============================================================================
// TTL & Room Expiry
// ============================================================================

func meetingStartCleanupTicker() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		meetingFabricMu.Lock()
		store := loadMeetingFabricLocked()
		now := time.Now()

		changed := false
		for i := range store.Rooms {
			// Check if room expired
			if store.Rooms[i].ExpiresAt != nil && store.Rooms[i].ExpiresAt.Before(now) {
				store.Rooms[i].Status = "expired"
				store.Rooms[i].UpdatedAt = now
				changed = true
				continue
			}

			// Clean expired tokens
			for j := len(store.Rooms[i].InviteTokens) - 1; j >= 0; j-- {
				if store.Rooms[i].InviteTokens[j].ExpiresAt.Before(now) {
					store.Rooms[i].InviteTokens = append(
						store.Rooms[i].InviteTokens[:j],
						store.Rooms[i].InviteTokens[j+1:]...,
					)
					changed = true
				}
			}
		}

		if changed {
			saveMeetingLocked()
		}
		meetingFabricMu.Unlock()
	}
}

// Start the cleanup ticker once
func init() {
	go meetingStartCleanupTicker()
}

// ============================================================================
// Participant Management (Kick/Ban)
// ============================================================================

func handleMeetingParticipantKick(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		Email  string `json:"email"`
		Reason string `json:"reason,omitempty"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)

	meetingFabricMu.Lock()
	defer meetingFabricMu.Unlock()

	room, _ := findMeetingRoomBySlug(slug)
	if room == nil {
		jsonError(w, http.StatusNotFound, "room not found")
		return
	}

	// Remove participant from active room (via LiveKit in production)
	// For now, we just mark them as kicked in metadata
	if Room.KickedUsers == nil {
		Room.KickedUsers = make(map[string]string)
	}
	Room.KickedUsers[req.Email] = req.Reason
	Room.UpdatedAt = time.Now()

	saveMeetingLocked()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"room":    Room,
		"message": fmt.Sprintf("participant %s kicked", req.Email),
	})
}

func handleMeetingParticipantBan(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		Email         string `json:"email"`
		DurationHours int    `json:"durationHours,omitempty"` // 0 = permanent
		Reason        string `json:"reason,omitempty"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)

	meetingFabricMu.Lock()
	defer meetingFabricMu.Unlock()

	Room, idx := findMeetingRoomBySlug(slug)
	if Room == nil {
		jsonError(w, http.StatusNotFound, "room not found")
		return
	}

	if Room.BannedUsers == nil {
		Room.BannedUsers = make(map[string]time.Time)
	}

	var bannedUntil time.Time
	if req.DurationHours > 0 {
		bannedUntil = time.Now().Add(time.Duration(req.DurationHours) * time.Hour)
	} // else permanent

	Room.BannedUsers[req.Email] = bannedUntil
	Room.UpdatedAt = time.Now()

	saveMeetingLocked()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"room":    Room,
		"message": fmt.Sprintf("participant %s banned until %s", req.Email, bannedUntil.Format(time.RFC3339)),
	})
}

func handleMeetingParticipantUnban(w http.ResponseWriter, r *http.Request, slug, email string) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	meetingFabricMu.Lock()
	defer meetingFabricMu.Unlock()

	Room, idx := findMeetingRoomBySlug(slug)
	if Room == nil {
		jsonError(w, http.StatusNotFound, "room not found")
		return
	}

	if Room.BannedUsers != nil {
		delete(Room.BannedUsers, email)
	}
	Room.UpdatedAt = time.Now()

	saveMeetingLocked()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"room":    Room,
		"message": fmt.Sprintf("participant %s unbanned", email),
	})
}

// ============================================================================
// External Provider Adapters
// ============================================================================

// Zoom RTMS adapter
func handleMeetingProviderZoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		Title       string `json:"title"`
		StartTime   string `json:"startTime"` // ISO8601
		DurationMin int    `json:"durationMin"`
		Description string `json:"description,omitempty"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)

	// STUB: In production, this would:
	// 1. Call Zoom API to create meeting
	// 2. Get Zoom meeting ID, join URL, host URL
	// 3. Store in meeting room with adapter details

	externalRoomID := "zoom-" + randomFormID()
	joinURL := "https://zoom.us/j/" + externalRoomID
	hostURL := "https://zoom.us/j/" + externalRoomID + "?type=host"

	room := MeetingRoom{
		ID:             randomFormID(),
		Slug:           randomFormID(),
		Title:          req.Title,
		Description:    req.Description,
		Provider:       MeetingProviderZoom,
		AdapterMode:    MeetingAdapterOfficialMedia,
		ExternalURL:    hostURL,
		JoinURL:        joinURL,
		Status:         "active",
		AllowGuests:    false,
		RequireLobby:   false,
		ExternalRoomID: externalRoomID,
		ExternalStatus: "created",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// Save room
	meetingFabricMu.Lock()
	store := loadMeetingFabricLocked()
	store.Rooms = append(store.Rooms, room)
	saveMeetingFabricLocked()
	meetingFabricMu.Unlock()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"room":    room,
		"message": "Zoom meeting created (STUB - not calling real API)",
	})
}

// Google Meet Media API adapter
func handleMeetingProviderGoogleMeet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)

	// STUB: In production, this would:
	// 1. Use Google Calendar API to create Meet event
	// 2. Get Meet conference ID, join URL
	// 3. Store in meeting room with adapter details

	externalRoomID := "meet-" + randomFormID()
	joinURL := "https://meet.google.com/" + externalRoomID

	room := MeetingRoom{
		ID:             randomFormID(),
		Slug:           randomFormID(),
		Title:          req.Title,
		Description:    req.Description,
		Provider:       MeetingProviderGoogleMeet,
		AdapterMode:    MeetingAdapterOfficialMedia,
		ExternalURL:    joinURL,
		JoinURL:        joinURL,
		Status:         "active",
		AllowGuests:    false,
		RequireLobby:   false,
		ExternalRoomID: externalRoomID,
		ExternalStatus: "created",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// Save room
	meetingFabricMu.Lock()
	store := loadMeetingFabricLocked()
	store.Rooms = append(store.Rooms, room)
	saveMeetingLocked()
	meetingFabricMu.Unlock()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"room":    room,
		"message": "Google Meet created (STUB - not calling real API)",
	})
}

// Microsoft Teams Graph adapter
func handleMeetingProviderTeams(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		Title       string `json:"title"`
		Description string `json:"description,omitempty"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)

	// STUB: In production, this would:
	// 1. Use Microsoft Graph API to create online meeting
	//	2. Get Teams meeting ID, join URL
	// 3. Store in meeting room with adapter details

	externalRoomID := "teams-" + randomFormID()
	joinURL := "https://teams.microsoft.com/l/meetup-join/" + externalRoomID

	room := MeetingRoom{
		ID:             randomFormID(),
		Slug:           randomFormID(),
		Title:          req.Title,
		Description:    req.Description,
		Provider:       MeetingProviderMicrosoftTeams,
		AdapterMode:    MeetingAdapterOfficialMedia,
		ExternalURL:    joinURL,
		JoinURL:        joinURL,
		Status:         "active",
		AllowGuests:    false,
		RequireLobby:   false,
		ExternalRoomID: externalRoomID,
		ExternalStatus: "created",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// Save room
	meetingFabricMu.Lock()
	store := loadMeetingFabricLocked()
	store.Rooms = append(store.Rooms, room)
	saveMeetingLocked()
	meetingFabricMu.Unlock()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"room":    room,
		"message": "Teams meeting created (STUB - not calling real API)",
	})
}

// Remote browser bridge (for providers that don't have official APIs)
func handleMeetingProviderRemoteBrowser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	var req struct {
		Title       string `json:"title"`
		ExternalURL string `json:"externalUrl"`
		Description string `json:"description,omitempty"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)

	// Validate external URL
	if req.ExternalURL == "" {
		jsonError(w, http.StatusBadRequest, "externalUrl is required for remote-browser mode")
		return
	}

	room := MeetingRoom{
		ID:             randomFormID(),
		Slug:           randomFormID(),
		Title:          req.Title,
		Description:    req.Description,
		Provider:       "remote-browser", // Generic for remote-browser
		AdapterMode:    MeetingAdapterRemoteBrowser,
		ExternalURL:    req.ExternalURL,
		JoinURL:        req.ExternalURL,
		Status:         "active",
		AllowGuests:    false,
		RequireLobby:   false,
		ExternalRoomID: "", // Not applicable for remote-browser
		ExternalStatus: "linked",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	// Save room
	meetingFabricMu.Lock()
	store := loadMeetingFabricLocked()
	store.Rooms = append(store.Rooms, room)
	saveMeetingLocked()
	meetingFabricMu.Unlock()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"room":    room,
		"message": "Remote browser bridge created",
	})
}

// ============================================================================
// HTTP Route Registrations (add to httpserver.go)
// ============================================================================
//
// Add these to the httpserver.Route() switch statement:
//
// case "/meeting-rooms":
//     switch r.URL.Path {
//     case "/meeting-rooms":
//         s.handleMeetingRooms(w, r)
//     case "/meeting-rooms/capabilities":
//         s.handleMeetingCapabilities(w, r)
//     case "/call/", "/join":
//         s.handleCallPage(w, r)
//     case "/call/lobby/approve":
//         s.handleMeetingLobbyApprove(w, r, slug)
//     case "/call/lobby/queue":
//         s.handleMeetingLobbyQueue(w, r, slug)
//     case "/call/lobby/join":
//         s.handleMeetingLobbyJoin(w, r, slug)
//     case "/call/invites":
//         s.handleMeetingInviteCreate(w, r, slug)
//     case "/call/kick":
//         s.handleMeetingParticipantKick(w, r, slug)
//     case "/call/ban":
//         s.handleMeetingParticipantBan(w, r, slug)
// case "/call/unban":
//         s.handleMeetingParticipantUnban(w, r, slug, email)
// case "/provider/zoom":
//         s.handleMeetingProviderZoom(w, r)
// case "/provider/google-meet":
//         s.handleMeetingProviderGoogleMeet(w, r)
// case "/provider/microsoft-teams":
//         s.handleMeetingProviderTeams(w, r)
// case "/provider/remote-browser":
//         s.handleMeetingProviderRemoteBrowser(w, r)
//     default:
//         http.NotFound(w, r)
//     }
// default:
//     http.NotFound(w, r)
// }
// }
