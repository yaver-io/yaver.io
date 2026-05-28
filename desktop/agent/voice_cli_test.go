package main

import "testing"

func TestApplyVoiceSetupDeepgramCartesia(t *testing.T) {
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
}

func TestApplyVoiceSetupCartesiaKeepsExistingSTT(t *testing.T) {
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

func TestApplyVoiceSetupCartesiaRequiresSTTKey(t *testing.T) {
	cfg := &Config{}
	opt := &voiceSetupOptions{Stack: "cartesia", CartesiaAPIKey: "ck-test"}
	if err := applyVoiceSetup(cfg, opt, 0); err == nil {
		t.Fatal("expected cartesia setup without an STT key to fail")
	}
}
