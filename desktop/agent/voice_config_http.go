package main

// voice_config_http.go — POST /voice/config so the mobile Settings →
// Voice picker can write the user's provider choice + API keys to
// ~/.yaver/config.json without a CLI roundtrip.
//
// Owner auth only (s.auth, not authSDK) — API keys cross the wire so
// SDK tokens cannot push them. Keys NEVER reach Convex per the privacy
// contract (convex_privacy_test enforces the forbidden-keys fence).
//
// Body accepts partial config — only fields the client sets are
// updated; unset fields keep their current values. Empty string for
// an API-key field is interpreted as "leave it alone, don't clear" —
// to clear, set null on a future protocol extension or rely on the
// CLI's `yaver vault remove` equivalent for the corresponding key.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type voiceConfigUpdate struct {
	Enabled              *bool                `json:"enabled,omitempty"`
	STTProvider          *string              `json:"sttProvider,omitempty"`
	TTSProvider          *string              `json:"ttsProvider,omitempty"`
	OpenAIAPIKey         *string              `json:"openaiApiKey,omitempty"`
	OpenAISTTModel       *string              `json:"openaiSttModel,omitempty"`
	OpenAITTSModel       *string              `json:"openaiTtsModel,omitempty"`
	OpenAITTSVoice       *string              `json:"openaiTtsVoice,omitempty"`
	DeepgramAPIKey       *string              `json:"deepgramApiKey,omitempty"`
	DeepgramTTSModel     *string              `json:"deepgramTtsModel,omitempty"`
	CartesiaAPIKey       *string              `json:"cartesiaApiKey,omitempty"`
	CartesiaVoiceID      *string              `json:"cartesiaVoiceId,omitempty"`
	AssemblyAIAPIKey     *string              `json:"assemblyaiApiKey,omitempty"`
	AssemblyAILanguage   *string              `json:"assemblyaiLanguage,omitempty"`
	ElevenLabsAPIKey     *string              `json:"elevenlabsApiKey,omitempty"`
	ElevenLabsTTSVoiceID *string              `json:"elevenlabsTtsVoiceId,omitempty"`
	ElevenLabsTTSModel   *string              `json:"elevenlabsTtsModel,omitempty"`
	DefaultProject       *string              `json:"defaultProject,omitempty"`
	ProjectKeyterms      *map[string][]string `json:"projectKeyterms,omitempty"`
	LaunchProjects       *map[string]string   `json:"launchProjects,omitempty"`
}

// voiceSTTProviders / voiceTTSProviders are the validated enums for the
// POST /voice/config endpoint. Adding a new provider: append the slug
// here AND add the corresponding code path in voice_dispatch.go. The
// "on-device" / "device" entries are surfaced to the mobile UI; the
// agent itself never dispatches them (mobile owns local capture).
var voiceSTTProviders = map[string]bool{
	"":           true, // "" = use default (openai)
	"openai":     true,
	"deepgram":   true,
	"assemblyai": true,
	"on-device":  true, // mobile whisper.rn / Apple SpeechAnalyzer
	"local":      true, // agent-side whisper.cpp — free/offline, no key
}

var voiceTTSProviders = map[string]bool{
	"":           true,
	"openai":     true,
	"cartesia":   true,
	"elevenlabs": true,
	"deepgram":   true, // Aura-2; shares the Deepgram STT key (one signup)
	"device":     true, // mobile AVSpeechSynthesizer / Android TextToSpeech
	"local":      true, // agent-side say/espeak — free/offline, no key
}

func (s *HTTPServer) handleVoiceConfigSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var upd voiceConfigUpdate
	if err := json.NewDecoder(r.Body).Decode(&upd); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	cfg, err := LoadConfig()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "load config: "+err.Error())
		return
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Voice == nil {
		cfg.Voice = &VoiceConfig{}
	}

	if upd.Enabled != nil {
		cfg.Voice.Enabled = *upd.Enabled
	}
	if upd.STTProvider != nil {
		p := strings.ToLower(strings.TrimSpace(*upd.STTProvider))
		if !voiceSTTProviders[p] {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("sttProvider %q not supported", p))
			return
		}
		cfg.Voice.STTProvider = p
	}
	if upd.TTSProvider != nil {
		p := strings.ToLower(strings.TrimSpace(*upd.TTSProvider))
		if !voiceTTSProviders[p] {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("ttsProvider %q not supported", p))
			return
		}
		cfg.Voice.TTSProvider = p
	}
	// API keys: write to vault (encrypted + P2P-synced). Legacy plaintext
	// config fields remain readable as fallback for old installs, but new
	// writes clear them so provider credentials do not live in config.json.
	if upd.OpenAIAPIKey != nil {
		v := strings.TrimSpace(*upd.OpenAIAPIKey)
		if err := SetVoiceCredential("openai", "api-key", v); err != nil {
			jsonError(w, http.StatusInternalServerError, "save openai credential: "+err.Error())
			return
		}
		cfg.Voice.OpenAIAPIKey = ""
	}
	if upd.OpenAISTTModel != nil {
		cfg.Voice.OpenAISTTModel = strings.TrimSpace(*upd.OpenAISTTModel)
	}
	if upd.OpenAITTSModel != nil {
		cfg.Voice.OpenAITTSModel = strings.TrimSpace(*upd.OpenAITTSModel)
	}
	if upd.OpenAITTSVoice != nil {
		cfg.Voice.OpenAITTSVoice = strings.TrimSpace(*upd.OpenAITTSVoice)
	}
	if upd.DeepgramAPIKey != nil {
		v := strings.TrimSpace(*upd.DeepgramAPIKey)
		if err := SetVoiceCredential("deepgram", "api-key", v); err != nil {
			jsonError(w, http.StatusInternalServerError, "save deepgram credential: "+err.Error())
			return
		}
		cfg.Voice.DeepgramAPIKey = ""
	}
	if upd.DeepgramTTSModel != nil {
		cfg.Voice.DeepgramTTSModel = strings.TrimSpace(*upd.DeepgramTTSModel)
	}
	if upd.CartesiaAPIKey != nil {
		v := strings.TrimSpace(*upd.CartesiaAPIKey)
		if err := SetVoiceCredential("cartesia", "api-key", v); err != nil {
			jsonError(w, http.StatusInternalServerError, "save cartesia credential: "+err.Error())
			return
		}
		cfg.Voice.CartesiaAPIKey = ""
	}
	if upd.CartesiaVoiceID != nil {
		cfg.Voice.CartesiaVoiceID = strings.TrimSpace(*upd.CartesiaVoiceID)
	}
	if upd.AssemblyAIAPIKey != nil {
		v := strings.TrimSpace(*upd.AssemblyAIAPIKey)
		if err := SetVoiceCredential("assemblyai", "api-key", v); err != nil {
			jsonError(w, http.StatusInternalServerError, "save assemblyai credential: "+err.Error())
			return
		}
		cfg.Voice.AssemblyAIAPIKey = ""
	}
	if upd.AssemblyAILanguage != nil {
		cfg.Voice.AssemblyAILanguage = strings.TrimSpace(*upd.AssemblyAILanguage)
	}
	if upd.ElevenLabsAPIKey != nil {
		v := strings.TrimSpace(*upd.ElevenLabsAPIKey)
		if err := SetVoiceCredential("elevenlabs", "api-key", v); err != nil {
			jsonError(w, http.StatusInternalServerError, "save elevenlabs credential: "+err.Error())
			return
		}
		cfg.Voice.ElevenLabsAPIKey = ""
	}
	if upd.ElevenLabsTTSVoiceID != nil {
		cfg.Voice.ElevenLabsTTSVoiceID = strings.TrimSpace(*upd.ElevenLabsTTSVoiceID)
	}
	if upd.ElevenLabsTTSModel != nil {
		cfg.Voice.ElevenLabsTTSModel = strings.TrimSpace(*upd.ElevenLabsTTSModel)
	}
	if upd.DefaultProject != nil {
		cfg.Voice.DefaultProject = strings.TrimSpace(*upd.DefaultProject)
	}
	if upd.ProjectKeyterms != nil {
		cfg.Voice.ProjectKeyterms = *upd.ProjectKeyterms
	}
	if upd.LaunchProjects != nil {
		cfg.Voice.LaunchProjects = *upd.LaunchProjects
	}

	if err := SaveConfig(cfg); err != nil {
		jsonError(w, http.StatusInternalServerError, "save config: "+err.Error())
		return
	}

	// Return sanitized state — keys masked. The mobile UI re-renders
	// from this response so the user sees confirmation without us
	// echoing back the secrets.
	// Sanitized state — keys masked, "set" booleans computed through the
	// resolver so vault-only entries (no legacy field) still show as set.
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":             true,
		"enabled":        cfg.Voice.Enabled,
		"sttProvider":    cfg.Voice.EffectiveSTTProvider(),
		"ttsProvider":    cfg.Voice.EffectiveTTSProvider(),
		"openaiSet":      HasVoiceCredential("openai", "api-key", cfg.Voice.OpenAIAPIKey),
		"deepgramSet":    HasVoiceCredential("deepgram", "api-key", cfg.Voice.DeepgramAPIKey),
		"cartesiaSet":    HasVoiceCredential("cartesia", "api-key", cfg.Voice.CartesiaAPIKey),
		"assemblyaiSet":  HasVoiceCredential("assemblyai", "api-key", cfg.Voice.AssemblyAIAPIKey),
		"elevenlabsSet":  HasVoiceCredential("elevenlabs", "api-key", cfg.Voice.ElevenLabsAPIKey),
		"defaultProject": cfg.Voice.DefaultProject,
		"availableSTT":   sortedProviderKeys(voiceSTTProviders),
		"availableTTS":   sortedProviderKeys(voiceTTSProviders),
	})
}

// sortedProviderKeys returns the validated provider slugs as a stable
// slice for the mobile UI to render the picker from. Filters out the
// empty-string sentinel (which is only a server-side "use default"
// shortcut).
func sortedProviderKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if k == "" {
			continue
		}
		out = append(out, k)
	}
	// Insertion-stable sort — simple bubble is fine for ~6 items.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
