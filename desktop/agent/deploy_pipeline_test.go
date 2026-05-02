package main

// deploy_pipeline_test.go — regression tests for the inbound /deploy/webhook
// handler. Specifically protects against the C-3 vulnerability: empty
// webhookSecret + GitHub-style POST = drive-by RCE on every default-config
// project.

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeDeployConfig(t *testing.T, dir string, cfg DeployConfig) {
	t.Helper()
	yamlDir := filepath.Join(dir, ".yaver")
	if err := os.MkdirAll(yamlDir, 0o755); err != nil {
		t.Fatalf("mkdir .yaver: %v", err)
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal deploy config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(yamlDir, "deploy.yaml"), body, 0o600); err != nil {
		t.Fatalf("write deploy.yaml: %v", err)
	}
}

func newWebhookRequest(t *testing.T, projectDir, secret string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/deploy/webhook?project="+projectDir, bytes.NewReader(body))
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	return req
}

func TestDeployWebhookRefusesEmptySecret(t *testing.T) {
	// C-3: a project with no webhookSecret configured must NOT accept
	// any webhook POST, signed or not. Previously this short-circuited
	// the HMAC check and ran RunDeploy unconditionally.
	dir := t.TempDir()
	writeDeployConfig(t, dir, DeployConfig{Branch: "main", AutoDeploy: true})

	srv := &HTTPServer{}
	rec := httptest.NewRecorder()
	srv.handleDeployWebhook(rec, newWebhookRequest(t, dir, "", []byte(`{"ref":"refs/heads/main"}`)))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on empty secret, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "webhook secret not configured") {
		t.Errorf("expected explanatory error, got %s", rec.Body.String())
	}
}

func TestDeployWebhookRequiresValidSignature(t *testing.T) {
	dir := t.TempDir()
	writeDeployConfig(t, dir, DeployConfig{Branch: "main", AutoDeploy: true, WebhookSecret: "topsecret"})

	srv := &HTTPServer{}
	rec := httptest.NewRecorder()
	body := []byte(`{"ref":"refs/heads/main"}`)
	// Wrong secret → wrong HMAC → reject.
	req := newWebhookRequest(t, dir, "wrong", body)
	srv.handleDeployWebhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on bad signature, got %d", rec.Code)
	}
}

func TestDeployWebhookRefusesMissingSignature(t *testing.T) {
	dir := t.TempDir()
	writeDeployConfig(t, dir, DeployConfig{Branch: "main", AutoDeploy: true, WebhookSecret: "topsecret"})

	srv := &HTTPServer{}
	rec := httptest.NewRecorder()
	body := []byte(`{"ref":"refs/heads/main"}`)
	req := newWebhookRequest(t, dir, "", body) // no signature header at all
	srv.handleDeployWebhook(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on missing signature, got %d", rec.Code)
	}
}

func TestDeployWebhookAcceptsValidSignatureButAutoDeployOff(t *testing.T) {
	dir := t.TempDir()
	writeDeployConfig(t, dir, DeployConfig{Branch: "main", AutoDeploy: false, WebhookSecret: "topsecret"})

	srv := &HTTPServer{}
	rec := httptest.NewRecorder()
	body := []byte(`{"ref":"refs/heads/main"}`)
	srv.handleDeployWebhook(rec, newWebhookRequest(t, dir, "topsecret", body))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with autoDeploy off, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "autoDeploy disabled") {
		t.Errorf("expected autoDeploy-disabled note, got %s", rec.Body.String())
	}
}
