package main

// ops_voice.go — verb "voice": read/write the hands-free agent loop's
// STT + TTS provider config through the unified ops surface. Mirrors the
// HTTP edges (GET /voice/status, POST /voice/config) so external AI
// agents (Cursor, Claude Desktop, Codex, Goose) can drive Settings
// without learning a second schema.
//
// Owner-only. Voice keys NEVER reach Convex — they live in the local
// vault + ~/.yaver/config.json (privacy contract enforced by
// convex_privacy_test.go's forbidden-keys fence). Guests refused even
// for op="status" so that a guest token can't enumerate which providers
// the host has configured.

import (
	"encoding/json"
	"fmt"
	"strings"
)

type opsVoicePayload struct {
	// Op: "status" (default) | "providers" | "set" | "enable" | "disable".
	Op string `json:"op,omitempty"`

	// Provider selection. Empty string leaves the existing value alone.
	STTProvider string `json:"stt_provider,omitempty"`
	TTSProvider string `json:"tts_provider,omitempty"`

	// API keys — written to vault (encrypted, P2P-synced, never Convex).
	// Empty string leaves the existing key alone; to clear, use the vault
	// surface directly. Models are plain strings, no secret handling.
	OpenAIAPIKey         string              `json:"openai_api_key,omitempty"`
	OpenAISTTModel       string              `json:"openai_stt_model,omitempty"`
	OpenAITTSModel       string              `json:"openai_tts_model,omitempty"`
	OpenAITTSVoice       string              `json:"openai_tts_voice,omitempty"`
	DeepgramAPIKey       string              `json:"deepgram_api_key,omitempty"`
	DeepgramTTSModel     string              `json:"deepgram_tts_model,omitempty"`
	CartesiaAPIKey       string              `json:"cartesia_api_key,omitempty"`
	CartesiaVoiceID      string              `json:"cartesia_voice_id,omitempty"`
	AssemblyAIAPIKey     string              `json:"assemblyai_api_key,omitempty"`
	AssemblyAILanguage   string              `json:"assemblyai_language,omitempty"`
	ElevenLabsAPIKey     string              `json:"elevenlabs_api_key,omitempty"`
	ElevenLabsTTSVoiceID string              `json:"elevenlabs_tts_voice_id,omitempty"`
	ElevenLabsTTSModel   string              `json:"elevenlabs_tts_model,omitempty"`
	DefaultProject       string              `json:"default_project,omitempty"`
	ProjectKeyterms      map[string][]string `json:"project_keyterms,omitempty"`
	LaunchProjects       map[string]string   `json:"launch_projects,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "voice",
		Description: "Read or update hands-free voice loop config (STT + TTS providers, API keys, per-project keyterm bias). " +
			"ops: status (provider + readiness, no secrets returned), providers (enum of supported STT/TTS slugs), " +
			"set (partial update — only fields you supply are changed), enable, disable. " +
			"Keys are written to the encrypted on-device vault and NEVER sync to Convex (privacy contract). Owner-only.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"op": map[string]interface{}{
					"type":    "string",
					"enum":    []string{"status", "providers", "set", "enable", "disable"},
					"default": "status",
				},
				"stt_provider":            map[string]interface{}{"type": "string", "enum": []string{"openai", "deepgram", "assemblyai", "local", "on-device"}},
				"tts_provider":            map[string]interface{}{"type": "string", "enum": []string{"openai", "deepgram", "cartesia", "elevenlabs", "local", "device"}},
				"openai_api_key":          map[string]interface{}{"type": "string", "description": "OpenAI API key; written to vault, never returned."},
				"openai_stt_model":        map[string]interface{}{"type": "string"},
				"openai_tts_model":        map[string]interface{}{"type": "string"},
				"openai_tts_voice":        map[string]interface{}{"type": "string"},
				"deepgram_api_key":        map[string]interface{}{"type": "string", "description": "Deepgram API key (powers both Flux STT and Aura-2 TTS); written to vault, never returned."},
				"deepgram_tts_model":      map[string]interface{}{"type": "string", "description": "Deepgram Aura-2 voice model, e.g. aura-2-thalia-en. Empty = thalia default."},
				"cartesia_api_key":        map[string]interface{}{"type": "string"},
				"cartesia_voice_id":       map[string]interface{}{"type": "string"},
				"assemblyai_api_key":      map[string]interface{}{"type": "string"},
				"assemblyai_language":     map[string]interface{}{"type": "string"},
				"elevenlabs_api_key":      map[string]interface{}{"type": "string"},
				"elevenlabs_tts_voice_id": map[string]interface{}{"type": "string"},
				"elevenlabs_tts_model":    map[string]interface{}{"type": "string"},
				"default_project":         map[string]interface{}{"type": "string"},
				"project_keyterms":        map[string]interface{}{"type": "object", "description": "Per-project STT keyterm bias (Deepgram only). Map of project slug -> list of nouns."},
				"launch_projects":         map[string]interface{}{"type": "object", "description": "Spoken slug -> workDir map for `launch X` intent matching."},
			},
			"additionalProperties": false,
		},
		Handler:    opsVoiceHandler,
		Streaming:  false,
		AllowGuest: false,
	})
}

func opsVoiceHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var p opsVoicePayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	op := strings.ToLower(strings.TrimSpace(p.Op))
	if op == "" {
		op = "status"
	}

	switch op {
	case "status":
		return opsVoiceStatus()
	case "providers":
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"stt": sortedProviderKeys(voiceSTTProviders),
			"tts": sortedProviderKeys(voiceTTSProviders),
		}}
	case "enable":
		return opsVoiceToggle(true)
	case "disable":
		return opsVoiceToggle(false)
	case "set":
		return opsVoiceSet(p)
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "unknown op: " + op}
	}
}

func opsVoiceStatus() OpsResult {
	cfg, err := LoadConfig()
	if err != nil {
		return OpsResult{OK: false, Code: "io_error", Error: "load config: " + err.Error()}
	}
	v := voiceCfgOrNil(cfg)
	if v == nil {
		// Fresh install — return the same shape as a configured-but-disabled
		// agent so callers don't have to branch on null.
		return OpsResult{OK: true, Initial: opsVoiceStatusShape(&VoiceConfig{})}
	}
	return OpsResult{OK: true, Initial: opsVoiceStatusShape(v)}
}

func opsVoiceStatusShape(v *VoiceConfig) map[string]interface{} {
	stt := v.EffectiveSTTProvider()
	tts := v.EffectiveTTSProvider()
	return map[string]interface{}{
		"enabled":         v.Enabled,
		"stt_provider":    stt,
		"tts_provider":    tts,
		"stt_ready":       voiceSTTReady(v),
		"tts_ready":       voiceTTSReady(v),
		"default_project": v.DefaultProject,
		// Per-provider "key is set" booleans go through the resolver so
		// vault-only entries (no legacy field) report as set. Plaintext
		// keys are NEVER included — agents must POST to rotate.
		"keys_set": map[string]bool{
			"openai":     HasVoiceCredential("openai", "api-key", v.OpenAIAPIKey),
			"deepgram":   HasVoiceCredential("deepgram", "api-key", v.DeepgramAPIKey),
			"cartesia":   HasVoiceCredential("cartesia", "api-key", v.CartesiaAPIKey),
			"assemblyai": HasVoiceCredential("assemblyai", "api-key", v.AssemblyAIAPIKey),
			"elevenlabs": HasVoiceCredential("elevenlabs", "api-key", v.ElevenLabsAPIKey),
		},
		"available": map[string][]string{
			"stt": sortedProviderKeys(voiceSTTProviders),
			"tts": sortedProviderKeys(voiceTTSProviders),
		},
	}
}

func opsVoiceToggle(enabled bool) OpsResult {
	cfg, err := LoadConfig()
	if err != nil {
		return OpsResult{OK: false, Code: "io_error", Error: "load config: " + err.Error()}
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Voice == nil {
		cfg.Voice = &VoiceConfig{}
	}
	cfg.Voice.Enabled = enabled
	if err := SaveConfig(cfg); err != nil {
		return OpsResult{OK: false, Code: "io_error", Error: "save config: " + err.Error()}
	}
	return OpsResult{OK: true, Initial: opsVoiceStatusShape(cfg.Voice)}
}

func opsVoiceSet(p opsVoicePayload) OpsResult {
	cfg, err := LoadConfig()
	if err != nil {
		return OpsResult{OK: false, Code: "io_error", Error: "load config: " + err.Error()}
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Voice == nil {
		cfg.Voice = &VoiceConfig{}
	}
	v := cfg.Voice

	if p.STTProvider != "" {
		slug := strings.ToLower(strings.TrimSpace(p.STTProvider))
		if !voiceSTTProviders[slug] {
			return OpsResult{OK: false, Code: "bad_payload", Error: fmt.Sprintf("stt_provider %q not supported", slug)}
		}
		v.STTProvider = slug
	}
	if p.TTSProvider != "" {
		slug := strings.ToLower(strings.TrimSpace(p.TTSProvider))
		if !voiceTTSProviders[slug] {
			return OpsResult{OK: false, Code: "bad_payload", Error: fmt.Sprintf("tts_provider %q not supported", slug)}
		}
		v.TTSProvider = slug
	}

	// API keys: vault-only (matches POST /voice/config). New writes land in
	// the encrypted voice vault and the legacy plaintext field is cleared
	// so config.json never holds the secret. Empty payload string is "leave
	// alone" so callers can target a single provider without clearing
	// the others. Errors propagate — a half-saved vault is worse than none.
	if k := strings.TrimSpace(p.OpenAIAPIKey); k != "" {
		if err := SetVoiceCredential("openai", "api-key", k); err != nil {
			return OpsResult{OK: false, Code: "io_error", Error: "save openai credential: " + err.Error()}
		}
		v.OpenAIAPIKey = ""
	}
	if k := strings.TrimSpace(p.DeepgramAPIKey); k != "" {
		if err := SetVoiceCredential("deepgram", "api-key", k); err != nil {
			return OpsResult{OK: false, Code: "io_error", Error: "save deepgram credential: " + err.Error()}
		}
		v.DeepgramAPIKey = ""
	}
	if k := strings.TrimSpace(p.CartesiaAPIKey); k != "" {
		if err := SetVoiceCredential("cartesia", "api-key", k); err != nil {
			return OpsResult{OK: false, Code: "io_error", Error: "save cartesia credential: " + err.Error()}
		}
		v.CartesiaAPIKey = ""
	}
	if k := strings.TrimSpace(p.AssemblyAIAPIKey); k != "" {
		if err := SetVoiceCredential("assemblyai", "api-key", k); err != nil {
			return OpsResult{OK: false, Code: "io_error", Error: "save assemblyai credential: " + err.Error()}
		}
		v.AssemblyAIAPIKey = ""
	}
	if k := strings.TrimSpace(p.ElevenLabsAPIKey); k != "" {
		if err := SetVoiceCredential("elevenlabs", "api-key", k); err != nil {
			return OpsResult{OK: false, Code: "io_error", Error: "save elevenlabs credential: " + err.Error()}
		}
		v.ElevenLabsAPIKey = ""
	}

	if p.OpenAISTTModel != "" {
		v.OpenAISTTModel = strings.TrimSpace(p.OpenAISTTModel)
	}
	if p.OpenAITTSModel != "" {
		v.OpenAITTSModel = strings.TrimSpace(p.OpenAITTSModel)
	}
	if p.OpenAITTSVoice != "" {
		v.OpenAITTSVoice = strings.TrimSpace(p.OpenAITTSVoice)
	}
	if p.DeepgramTTSModel != "" {
		v.DeepgramTTSModel = strings.TrimSpace(p.DeepgramTTSModel)
	}
	if p.CartesiaVoiceID != "" {
		v.CartesiaVoiceID = strings.TrimSpace(p.CartesiaVoiceID)
	}
	if p.AssemblyAILanguage != "" {
		v.AssemblyAILanguage = strings.TrimSpace(p.AssemblyAILanguage)
	}
	if p.ElevenLabsTTSVoiceID != "" {
		v.ElevenLabsTTSVoiceID = strings.TrimSpace(p.ElevenLabsTTSVoiceID)
	}
	if p.ElevenLabsTTSModel != "" {
		v.ElevenLabsTTSModel = strings.TrimSpace(p.ElevenLabsTTSModel)
	}
	if p.DefaultProject != "" {
		v.DefaultProject = strings.TrimSpace(p.DefaultProject)
	}
	if p.ProjectKeyterms != nil {
		v.ProjectKeyterms = p.ProjectKeyterms
	}
	if p.LaunchProjects != nil {
		v.LaunchProjects = p.LaunchProjects
	}

	if err := SaveConfig(cfg); err != nil {
		return OpsResult{OK: false, Code: "io_error", Error: "save config: " + err.Error()}
	}
	return OpsResult{OK: true, Initial: opsVoiceStatusShape(v)}
}
