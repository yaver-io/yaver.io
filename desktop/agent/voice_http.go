package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// handleVoiceStatus returns voice capability and provider info.
// Mobile/SDK uses this to decide whether to show mic UI and how to handle audio.
func (s *HTTPServer) handleVoiceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	cfg, _ := LoadConfig()
	result := map[string]interface{}{
		"ok":                true,
		"voiceInputEnabled": true, // always — mobile can always record & send audio
	}

	// S2S provider status
	if cfg != nil && cfg.Voice != nil && cfg.Voice.S2SProvider != "" {
		if p, ok := GetVoiceProvider(cfg.Voice.S2SProvider); ok {
			status := p.Status()
			result["s2sProvider"] = status.Provider
			result["s2sReady"] = status.Ready
			result["s2sEndpoint"] = status.Endpoint
			result["gpuAvailable"] = status.GPUAvailable
			result["gpuName"] = status.GPUName
		}
	} else {
		result["s2sProvider"] = nil
		result["s2sReady"] = false
	}

	// STT provider status (for transcription of voice input)
	if cfg != nil && cfg.Speech != nil && cfg.Speech.Provider != "" {
		result["sttProvider"] = cfg.Speech.Provider
		result["sttReady"] = true
	} else {
		result["sttProvider"] = nil
		result["sttReady"] = false
	}

	// List available providers
	providers := []map[string]interface{}{}
	for _, p := range ListVoiceProviders() {
		status := p.Status()
		providers = append(providers, map[string]interface{}{
			"name":  status.Provider,
			"ready": status.Ready,
			"error": status.Error,
		})
	}
	result["providers"] = providers

	jsonReply(w, http.StatusOK, result)
}

// handleVoiceTranscribe accepts audio (WAV/PCM) and returns transcribed text.
// This is the primary endpoint for voice input from the mobile app and Feedback SDK.
// Works with any configured STT or S2S provider.
func (s *HTTPServer) handleVoiceTranscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}

	// Read audio data (limit 50MB)
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	audioData, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "failed to read audio data")
		return
	}

	if len(audioData) == 0 {
		jsonError(w, http.StatusBadRequest, "empty audio data")
		return
	}

	log.Printf("[voice] Transcribe request: %d bytes", len(audioData))

	cfg, _ := LoadConfig()

	// Try S2S provider first (PersonaPlex/OpenAI Realtime)
	if cfg != nil && cfg.Voice != nil && cfg.Voice.S2SProvider != "" {
		if p, ok := GetVoiceProvider(cfg.Voice.S2SProvider); ok && p.IsAvailable() {
			text, err := p.Transcribe(audioData)
			if err == nil {
				jsonReply(w, http.StatusOK, map[string]interface{}{
					"ok":       true,
					"text":     text,
					"provider": cfg.Voice.S2SProvider,
				})
				return
			}
			log.Printf("[voice] S2S transcription failed, falling back to STT: %v", err)
		}
	}

	// Fall back to STT providers (existing speech.go infrastructure)
	if cfg != nil && cfg.Speech != nil && cfg.Speech.Provider != "" {
		// Save audio to temp file for existing STT pipeline
		tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("yaver-voice-%d.wav", time.Now().UnixNano()))
		if err := os.WriteFile(tmpFile, audioData, 0644); err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to save audio")
			return
		}
		defer os.Remove(tmpFile)

		text, err := TranscribeAudio(tmpFile, cfg.Speech)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, fmt.Sprintf("transcription failed: %v", err))
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":       true,
			"text":     text,
			"provider": cfg.Speech.Provider,
		})
		return
	}

	// No provider — store audio and return a reference
	// The AI agent can process the audio file directly
	audioDir, _ := ConfigDir()
	audioDir = filepath.Join(audioDir, "voice-input")
	os.MkdirAll(audioDir, 0755)

	audioFile := filepath.Join(audioDir, fmt.Sprintf("voice-%d.wav", time.Now().UnixNano()))
	if err := os.WriteFile(audioFile, audioData, 0644); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to save audio")
		return
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"text":      "",
		"audioFile": audioFile,
		"provider":  "none",
		"message":   "No STT provider configured. Audio saved for manual review.",
	})
}

// handleVoiceProviders returns the list of supported voice providers.
func (s *HTTPServer) handleVoiceProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}

	type providerInfo struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		Type        string `json:"type"` // "on-prem" or "cloud"
		Free        bool   `json:"free"`
		Ready       bool   `json:"ready"`
		GPU         bool   `json:"gpuRequired"`
		Error       string `json:"error,omitempty"`
	}

	providers := []providerInfo{
		{
			Name:        "personaplex",
			DisplayName: "NVIDIA PersonaPlex 7B",
			Type:        "on-prem",
			Free:        true,
			GPU:         true,
		},
		{
			Name:        "openai",
			DisplayName: "OpenAI Realtime API",
			Type:        "cloud",
			Free:        false,
			GPU:         false,
		},
	}

	// Enrich with live status
	for i, pi := range providers {
		if p, ok := GetVoiceProvider(pi.Name); ok {
			status := p.Status()
			providers[i].Ready = status.Ready
			providers[i].Error = status.Error
		}
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"providers": providers,
	})
}

// handleVoiceConfig allows getting/setting voice configuration from mobile.
func (s *HTTPServer) handleVoiceConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := LoadConfig()
	if err != nil {
		cfg = &Config{}
	}

	switch r.Method {
	case http.MethodGet:
		voiceCfg := cfg.Voice
		if voiceCfg == nil {
			voiceCfg = &VoiceConfig{}
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":     true,
			"config": voiceCfg,
		})

	case http.MethodPost:
		var update VoiceConfig
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if cfg.Voice == nil {
			cfg.Voice = &VoiceConfig{}
		}
		if update.S2SProvider != "" {
			cfg.Voice.S2SProvider = update.S2SProvider
		}
		if update.S2SPort != 0 {
			cfg.Voice.S2SPort = update.S2SPort
		}
		cfg.Voice.AutoStart = update.AutoStart
		if err := SaveConfig(cfg); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":     true,
			"config": cfg.Voice,
		})

	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}
