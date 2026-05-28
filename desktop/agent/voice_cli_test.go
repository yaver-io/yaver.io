package main

import "testing"

func stubVoiceCLICredentials(t *testing.T) map[string]string {
	t.Helper()
	origSet := setVoiceCredentialForCLI
	origHas := hasVoiceCredentialForCLI
	store := map[string]string{}
	setVoiceCredentialForCLI = func(provider, kind, value string) error {
		store[provider+"-"+kind] = value
		return nil
	}
	hasVoiceCredentialForCLI = func(provider, kind, legacyFallback string) bool {
		return store[provider+"-"+kind] != "" || legacyFallback != ""
	}
	t.Cleanup(func() {
		setVoiceCredentialForCLI = origSet
		hasVoiceCredentialForCLI = origHas
	})
	return store
}

func TestApplyVoiceSetupDeepgramCartesia(t *testing.T) {
	store := stubVoiceCLICredentials(t)
	cfg := &Config{}
	opt := &voiceSetupOptions{
		Stack:          "deepgram-cartesia",
		DeepgramAPIKey: "dg-test",
		CartesiaAPIKey: "ck-test",
		DefaultProject: "yaver",
	}
	if err := applyVoiceSetup(cfg, opt, 0); err != nil {
		t.Fatalf("applyVoiceSetup: %v", err)
	}
	if cfg.Voice == nil {
		t.Fatal("voice config missing")
	}
	if !cfg.Voice.Enabled {
		t.Fatal("voice should be enabled")
	}
	if got := cfg.Voice.EffectiveSTTProvider(); got != "deepgram" {
		t.Fatalf("stt provider = %q, want deepgram", got)
	}
	if got := cfg.Voice.EffectiveTTSProvider(); got != "cartesia" {
		t.Fatalf("tts provider = %q, want cartesia", got)
	}
	if !voiceSTTReady(cfg.Voice) || !voiceTTSReady(cfg.Voice) {
		t.Fatalf("expected both providers ready")
	}
	if cfg.Voice.DefaultProject != "yaver" {
		t.Fatalf("default project = %q, want yaver", cfg.Voice.DefaultProject)
	}
	if store["deepgram-api-key"] != "dg-test" || store["cartesia-api-key"] != "ck-test" {
		t.Fatalf("voice keys not written to vault stub: %#v", store)
	}
	if cfg.Voice.DeepgramAPIKey != "" || cfg.Voice.CartesiaAPIKey != "" {
		t.Fatalf("voice keys should not be persisted to config fields")
	}
}

func TestApplyVoiceSetupCartesiaKeepsExistingSTT(t *testing.T) {
	stubVoiceCLICredentials(t)
	cfg := &Config{Voice: &VoiceConfig{STTProvider: "openai", OpenAIAPIKey: "sk-test"}}
	opt := &voiceSetupOptions{Stack: "cartesia", CartesiaAPIKey: "ck-test"}
	if err := applyVoiceSetup(cfg, opt, 0); err != nil {
		t.Fatalf("applyVoiceSetup: %v", err)
	}
	if got := cfg.Voice.EffectiveSTTProvider(); got != "openai" {
		t.Fatalf("stt provider = %q, want openai", got)
	}
	if got := cfg.Voice.EffectiveTTSProvider(); got != "cartesia" {
		t.Fatalf("tts provider = %q, want cartesia", got)
	}
	if !voiceSTTReady(cfg.Voice) || !voiceTTSReady(cfg.Voice) {
		t.Fatalf("expected openai STT and cartesia TTS ready")
	}
}

func TestApplyVoiceSetupOpenAI(t *testing.T) {
	stubVoiceCLICredentials(t)
	cfg := &Config{}
	opt := &voiceSetupOptions{Stack: "openai", OpenAIAPIKey: "sk-test"}
	if err := applyVoiceSetup(cfg, opt, 0); err != nil {
		t.Fatalf("applyVoiceSetup: %v", err)
	}
	if got := cfg.Voice.EffectiveSTTProvider(); got != "openai" {
		t.Fatalf("stt provider = %q, want openai", got)
	}
	if got := cfg.Voice.EffectiveTTSProvider(); got != "openai" {
		t.Fatalf("tts provider = %q, want openai", got)
	}
	if !voiceSTTReady(cfg.Voice) || !voiceTTSReady(cfg.Voice) {
		t.Fatalf("expected openai key to ready both providers")
	}
}

func TestApplyVoiceSetupDeepgramSingleVendor(t *testing.T) {
	stubVoiceCLICredentials(t)
	cfg := &Config{}
	opt := &voiceSetupOptions{
		Stack:          "deepgram",
		DeepgramAPIKey: "dg-test",
	}
	if err := applyVoiceSetup(cfg, opt, 0); err != nil {
		t.Fatalf("applyVoiceSetup: %v", err)
	}
	if got := cfg.Voice.EffectiveSTTProvider(); got != "deepgram" {
		t.Fatalf("stt provider = %q, want deepgram", got)
	}
	if got := cfg.Voice.EffectiveTTSProvider(); got != "deepgram" {
		t.Fatalf("tts provider = %q, want deepgram", got)
	}
	// One key powers both legs.
	if !voiceSTTReady(cfg.Voice) || !voiceTTSReady(cfg.Voice) {
		t.Fatalf("expected single Deepgram key to ready both STT and TTS")
	}
}

func TestApplyVoiceSetupCartesiaRequiresSTTKey(t *testing.T) {
	stubVoiceCLICredentials(t)
	cfg := &Config{}
	opt := &voiceSetupOptions{Stack: "cartesia", CartesiaAPIKey: "ck-test"}
	if err := applyVoiceSetup(cfg, opt, 0); err == nil {
		t.Fatal("expected cartesia setup without an STT key to fail")
	}
}
