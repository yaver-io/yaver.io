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
	Enabled         *bool              `json:"enabled,omitempty"`
	STTProvider     *string            `json:"sttProvider,omitempty"`
	TTSProvider     *string            `json:"ttsProvider,omitempty"`
	OpenAIAPIKey    *string            `json:"openaiApiKey,omitempty"`
	OpenAISTTModel  *string            `json:"openaiSttModel,omitempty"`
	OpenAITTSModel  *string            `json:"openaiTtsModel,omitempty"`
	OpenAITTSVoice  *string            `json:"openaiTtsVoice,omitempty"`
	DeepgramAPIKey  *string            `json:"deepgramApiKey,omitempty"`
	CartesiaAPIKey  *string            `json:"cartesiaApiKey,omitempty"`
	CartesiaVoiceID *string            `json:"cartesiaVoiceId,omitempty"`
	DefaultProject  *string            `json:"defaultProject,omitempty"`
	ProjectKeyterms *map[string][]string `json:"projectKeyterms,omitempty"`
	LaunchProjects  *map[string]string   `json:"launchProjects,omitempty"`
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
		if p != "" && p != "openai" && p != "deepgram" {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("sttProvider must be 'openai' or 'deepgram', got %q", p))
			return
		}
		cfg.Voice.STTProvider = p
	}
	if upd.TTSProvider != nil {
		p := strings.ToLower(strings.TrimSpace(*upd.TTSProvider))
		if p != "" && p != "openai" && p != "cartesia" {
			jsonError(w, http.StatusBadRequest, fmt.Sprintf("ttsProvider must be 'openai' or 'cartesia', got %q", p))
			return
		}
		cfg.Voice.TTSProvider = p
	}
	if upd.OpenAIAPIKey != nil {
		cfg.Voice.OpenAIAPIKey = strings.TrimSpace(*upd.OpenAIAPIKey)
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
		cfg.Voice.DeepgramAPIKey = strings.TrimSpace(*upd.DeepgramAPIKey)
	}
	if upd.CartesiaAPIKey != nil {
		cfg.Voice.CartesiaAPIKey = strings.TrimSpace(*upd.CartesiaAPIKey)
	}
	if upd.CartesiaVoiceID != nil {
		cfg.Voice.CartesiaVoiceID = strings.TrimSpace(*upd.CartesiaVoiceID)
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
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":             true,
		"enabled":        cfg.Voice.Enabled,
		"sttProvider":    cfg.Voice.EffectiveSTTProvider(),
		"ttsProvider":    cfg.Voice.EffectiveTTSProvider(),
		"openaiSet":      cfg.Voice.OpenAIAPIKey != "",
		"deepgramSet":    cfg.Voice.DeepgramAPIKey != "",
		"cartesiaSet":    cfg.Voice.CartesiaAPIKey != "",
		"defaultProject": cfg.Voice.DefaultProject,
	})
}
