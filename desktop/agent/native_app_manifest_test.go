package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadYaverNativeAppManifestPrefersYAML(t *testing.T) {
	dir := t.TempDir()
	jsonManifest := `{"schemaVersion":1,"id":"game_old","slug":"old","title":"Old"}`
	yamlManifest := `
schemaVersion: 1
kind: game
id: game_sfmg
slug: sfmg
title: SFMG
runtime:
  kind: yaver-strategy-game
auth:
  provider: yaver-oauth
  requiredInYaverBuild: true
  requiredScopes:
    - openid
    - profile
    - yaver.apps.run
    - yaver.apps.events.write
    - yaver.ai.invoke
    - yaver.games.play
    - yaver.games.save
surfaces: [ios, android, tvos]
native:
  host:
    requiresYaverOAuth: true
  apple:
    infoPlist:
      requiredKeys: [NSLocalNetworkUsageDescription]
`
	if err := os.WriteFile(filepath.Join(dir, "yaver.game.json"), []byte(jsonManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yaver.game.yaml"), []byte(yamlManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadYaverNativeAppManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		t.Fatal("expected manifest")
	}
	if m.ID != "game_sfmg" {
		t.Fatalf("ID = %q, want YAML manifest id", m.ID)
	}
	if audit := AuditYaverNativeAppManifest(m); !audit.OK {
		t.Fatalf("expected manifest audit to pass, findings=%#v", audit.Findings)
	}
}

func TestAuditYaverNativeAppManifestRequiresYaverOAuth(t *testing.T) {
	audit := AuditYaverNativeAppManifest(&YaverNativeAppManifest{
		SchemaVersion: 1,
		Kind:          "game",
		ID:            "game_bad",
		Slug:          "bad",
		Title:         "Bad",
		Auth: YaverNativeManifestAuth{
			Provider:             "developer-oauth",
			RequiredInYaverBuild: false,
			RequiredScopes:       []string{"openid"},
		},
	})
	if audit.OK {
		t.Fatal("expected audit failure")
	}
	var sawOAuth, sawRequired bool
	for _, finding := range audit.Findings {
		if finding.Code == "yaver_oauth_required" {
			sawOAuth = true
		}
		if finding.Code == "yaver_auth_must_be_required" {
			sawRequired = true
		}
	}
	if !sawOAuth || !sawRequired {
		t.Fatalf("expected oauth findings, got %#v", audit.Findings)
	}
}
