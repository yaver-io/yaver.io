package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func cleanSessionSetting(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 80 {
		return value[:80]
	}
	return value
}

func cleanSessionCapabilities(values []string) []string {
	cleaned := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = cleanSessionSetting(strings.ToLower(value))
		if value != "" && !seen[value] {
			seen[value] = true
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func normalizeClientSessionSettings(in *ClientSessionSettings, revision int64, now time.Time) *ClientSessionSettings {
	if in == nil {
		return nil
	}
	out := *in
	out.AppName = cleanSessionSetting(out.AppName)
	out.AppVersion = cleanSessionSetting(out.AppVersion)
	out.BuildNumber = cleanSessionSetting(out.BuildNumber)
	out.Surface = cleanSessionSetting(out.Surface)
	out.ClientSurface = cleanSessionSetting(out.ClientSurface)
	out.Platform = cleanSessionSetting(strings.ToLower(out.Platform))
	out.DeviceClass = cleanSessionSetting(strings.ToLower(out.DeviceClass))
	out.RenderModes = cleanSessionCapabilities(out.RenderModes)
	out.Codecs = cleanSessionCapabilities(out.Codecs)
	out.InputModes = cleanSessionCapabilities(out.InputModes)
	out.ClientSurface = firstNonEmpty(out.ClientSurface, out.Surface, "unknown")
	out.Surface = out.ClientSurface
	if out.Platform == "" {
		out.Platform = "unknown"
	}
	if out.DeviceClass == "" {
		out.DeviceClass = "unknown"
	}
	switch out.RuntimeMode {
	case "native", "dogfood", "yaver-hosted-dogfood":
	default:
		out.RuntimeMode = "native"
	}
	out.Dogfood = out.Dogfood || out.RuntimeMode != "native"
	switch out.Lane {
	case "yaver-native", "browser", "hermes", "webrtc":
	default:
		if out.RuntimeMode == "dogfood" || out.RuntimeMode == "yaver-hosted-dogfood" {
			out.Lane = "browser"
		} else {
			out.Lane = "yaver-native"
		}
	}
	switch out.UsageMode {
	case "chat-only":
		out.ChatEnabled, out.RenderEnabled = true, false
	case "reload-only":
		out.ChatEnabled, out.RenderEnabled = false, true
	case "reload-and-chat":
		out.ChatEnabled, out.RenderEnabled = true, true
	default:
		out.UsageMode = "chat-only"
		out.ChatEnabled, out.RenderEnabled = true, false
	}
	out.Revision = revision
	out.UpdatedAt = now.UTC()
	return &out
}

func cloneClientSessionSettings(in *ClientSessionSettings) *ClientSessionSettings {
	if in == nil {
		return nil
	}
	out := *in
	out.RenderModes = append([]string(nil), in.RenderModes...)
	out.Codecs = append([]string(nil), in.Codecs...)
	out.InputModes = append([]string(nil), in.InputModes...)
	return &out
}

func inferredClientSessionSettings(surface, source string) *ClientSessionSettings {
	surface = strings.ToLower(firstNonEmpty(cleanSessionSetting(surface), cleanSessionSetting(source), "unknown"))
	settings := &ClientSessionSettings{
		AppName: "Yaver client", Surface: surface, ClientSurface: surface,
		Lane: "yaver-native", RuntimeMode: "native", UsageMode: "chat-only",
	}
	switch surface {
	case "web", "yaver-web-dashboard", "browser":
		settings.Platform, settings.DeviceClass, settings.Lane = "web", "browser", "browser"
		settings.RenderModes, settings.Codecs, settings.InputModes = []string{"iframe", "webrtc", "frames"}, []string{"h264", "jpeg"}, []string{"pointer", "keyboard", "touch", "text"}
	case "tv", "tvos", "apple-tv":
		settings.Platform, settings.DeviceClass = "tvos", "tv"
		settings.RenderModes, settings.Codecs, settings.InputModes = []string{"webrtc", "frames"}, []string{"h264", "jpeg"}, []string{"controller", "text", "voice"}
	case "androidtv", "android-tv":
		settings.Platform, settings.DeviceClass = "android", "tv"
	case "visionos", "vision-pro", "spatial", "xr":
		settings.Platform, settings.DeviceClass = "visionos", "xr"
		settings.RenderModes, settings.Codecs, settings.InputModes = []string{"webrtc", "frames"}, []string{"h264", "jpeg"}, []string{"gaze", "pointer", "voice", "text"}
	case "android-xr", "quest":
		settings.Platform, settings.DeviceClass = "android-xr", "xr"
	case "watchos", "apple-watch":
		settings.Platform, settings.DeviceClass = "watchos", "watch"
		settings.InputModes = []string{"voice", "text"}
	case "wearos", "wear-os":
		settings.Platform, settings.DeviceClass = "wearos", "watch"
		settings.InputModes = []string{"voice", "text"}
	case "carplay":
		settings.Platform, settings.DeviceClass = "ios", "car"
		settings.InputModes = []string{"voice", "text"}
	case "android-auto", "androidauto":
		settings.Platform, settings.DeviceClass = "android", "car"
		settings.InputModes = []string{"voice", "text"}
	case "desktop", "desktop-app", "yaver-native-desktop", "yaver-desktop-app", "yaver-desktop-installer":
		settings.Platform, settings.DeviceClass = "desktop", "desktop"
	default:
		settings.Platform, settings.DeviceClass = "unknown", "unknown"
	}
	return settings
}

func mergeInferredClientSessionSettings(in *ClientSessionSettings, surface, source string) *ClientSessionSettings {
	defaults := inferredClientSessionSettings(surface, source)
	if in == nil {
		return defaults
	}
	out := *in
	out.AppName = firstNonEmpty(out.AppName, defaults.AppName)
	out.Surface = firstNonEmpty(out.Surface, out.ClientSurface, defaults.Surface)
	out.ClientSurface = firstNonEmpty(out.ClientSurface, out.Surface, defaults.ClientSurface)
	out.Platform = firstNonEmpty(out.Platform, defaults.Platform)
	out.DeviceClass = firstNonEmpty(out.DeviceClass, defaults.DeviceClass)
	out.Lane = firstNonEmpty(out.Lane, defaults.Lane)
	out.RuntimeMode = firstNonEmpty(out.RuntimeMode, defaults.RuntimeMode)
	out.UsageMode = firstNonEmpty(out.UsageMode, defaults.UsageMode)
	if len(out.RenderModes) == 0 {
		out.RenderModes = defaults.RenderModes
	}
	if len(out.Codecs) == 0 {
		out.Codecs = defaults.Codecs
	}
	if len(out.InputModes) == 0 {
		out.InputModes = defaults.InputModes
	}
	return &out
}

func clientSessionSettingsBriefing(settings *ClientSessionSettings) string {
	if settings == nil {
		return ""
	}
	briefing := fmt.Sprintf(
		"Client session: app=%s %s (build %s), surface=%s, platform=%s, device=%s, lane=%s, runtime=%s, dogfood=%t, usage=%s, chat=%t, render=%t",
		firstNonEmpty(settings.AppName, "client"), firstNonEmpty(settings.AppVersion, "unknown"),
		firstNonEmpty(settings.BuildNumber, "unknown"), settings.ClientSurface, settings.Platform, settings.DeviceClass,
		settings.Lane, settings.RuntimeMode, settings.Dogfood, settings.UsageMode,
		settings.ChatEnabled, settings.RenderEnabled,
	)
	if len(settings.RenderModes)+len(settings.Codecs)+len(settings.InputModes) > 0 {
		briefing += fmt.Sprintf(", render-modes=%s, codecs=%s, inputs=%s", strings.Join(settings.RenderModes, ","), strings.Join(settings.Codecs, ","), strings.Join(settings.InputModes, ","))
	}
	return briefing + ".\n"
}

func (tm *TaskManager) UpdateTaskSessionSettings(id string, settings *ClientSessionSettings) (*ClientSessionSettings, error) {
	if tm == nil || settings == nil {
		return nil, fmt.Errorf("sessionSettings is required")
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	task, ok := tm.tasks[id]
	if !ok || task == nil || task.DeletedAt != nil {
		return nil, fmt.Errorf("task %s not found", id)
	}
	revision := int64(1)
	if task.SessionSettings != nil {
		revision = task.SessionSettings.Revision + 1
		settings = mergeInferredClientSessionSettings(settings, task.SessionSettings.ClientSurface, task.Source)
		settings.AppName = firstNonEmpty(settings.AppName, task.SessionSettings.AppName)
		settings.AppVersion = firstNonEmpty(settings.AppVersion, task.SessionSettings.AppVersion)
		settings.BuildNumber = firstNonEmpty(settings.BuildNumber, task.SessionSettings.BuildNumber)
		settings.Platform = firstNonEmpty(settings.Platform, task.SessionSettings.Platform)
		settings.DeviceClass = firstNonEmpty(settings.DeviceClass, task.SessionSettings.DeviceClass)
	}
	task.SessionSettings = normalizeClientSessionSettings(settings, revision, time.Now())
	task.LastActiveAt = task.SessionSettings.UpdatedAt
	if task.SessionSettings.Surface != "" {
		task.LastSurface = task.SessionSettings.Surface
	}
	tm.persist()
	return cloneClientSessionSettings(task.SessionSettings), nil
}

func (s *HTTPServer) updateTaskSessionSettings(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use PATCH")
		return
	}
	var body struct {
		SessionSettings *ClientSessionSettings `json:"sessionSettings"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	settings, err := s.taskMgr.UpdateTaskSessionSettings(id, body.SessionSettings)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "taskId": id, "sessionSettings": settings})
}
