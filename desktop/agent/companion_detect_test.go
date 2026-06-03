package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDetectCompanionSynthetic builds a minimal Supabase-shaped repo and asserts
// the detector finds the token-authed cron endpoint and proposes a
// subscription-reconcile when billing is webhook-only. Self-contained (no
// dependency on a sibling repo).
func TestDetectCompanionSynthetic(t *testing.T) {
	repo := t.TempDir()
	fnRest := filepath.Join(repo, "supabase", "functions", "rest")
	if err := os.MkdirAll(fnRest, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(repo, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join("supabase", "config.toml"), "project_id = \"demo\"\n")
	write(filepath.Join("supabase", "functions", "rest", "autoMailSender.ts"), `
export async function handleAutoMailSenderDirect(req: Request) {
  const providedToken = new URL(req.url).searchParams.get("token");
  if (providedToken !== config.CRON_AUTH_UUID) {
    return new Response("unauthorized", { status: 401 });
  }
  return new Response("ok");
}
`)
	write(filepath.Join("supabase", "functions", "rest", "index.ts"), `
import { handleAutoMailSenderDirect } from "./autoMailSender.ts";
serve(async (req) => {
  const path = new URL(req.url).pathname;
  if (path === "/rest/autoMailSenderDirect") {
    return await handleAutoMailSenderDirect(req);
  }
});
`)
	// Webhook-only billing: next_payment_date present, no reconcile.
	write(filepath.Join("supabase", "functions", "rest", "payment.ts"), `
// updates Subscription.next_payment_date on webhook only
export function handleWebhook() { /* set next_payment_date */ }
`)

	m, items, err := DetectCompanion(repo)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}

	var cron, reconcile bool
	for _, it := range items {
		if it.Kind == "cron" && it.Name == "automailsenderdirect" && it.Status == "detected" {
			cron = true
		}
		if it.Name == "subscription-reconcile" && it.Status == "proposed-missing-endpoint" {
			reconcile = true
		}
	}
	if !cron {
		t.Fatalf("expected detected auto-mail cron; items=%+v", items)
	}
	if !reconcile {
		t.Fatalf("expected proposed subscription-reconcile; items=%+v", items)
	}

	// The proposed manifest should carry the armable cron + the proposed one.
	var armable, proposed int
	for _, c := range m.Crons {
		if c.Status == "proposed" {
			proposed++
		} else {
			armable++
		}
	}
	if armable < 1 || proposed < 1 {
		t.Fatalf("manifest crons unexpected: armable=%d proposed=%d (%+v)", armable, proposed, m.Crons)
	}
	if m.Runtime.BaseURLFrom != "env:SUPABASE_FUNCTIONS_URL" {
		t.Fatalf("expected base_url_from env:SUPABASE_FUNCTIONS_URL, got %q", m.Runtime.BaseURLFrom)
	}
}

// TestDetectCompanionEbackIfPresent runs against the real sibling e-back repo
// when it exists, asserting the two known cron endpoints + the reconcile
// proposal. Skips in CI / on machines without the sibling checkout.
func TestDetectCompanionEbackIfPresent(t *testing.T) {
	repo := filepath.Join("..", "..", "..", "e-back")
	if _, err := os.Stat(filepath.Join(repo, "supabase", "config.toml")); err != nil {
		t.Skip("sibling e-back repo not present; skipping live detection")
	}
	_, items, err := DetectCompanion(repo)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	names := map[string]string{}
	for _, it := range items {
		names[it.Name] = it.Status
	}
	if names["automailsenderdirect"] != "detected" {
		t.Errorf("expected auto-mail-sender detected, got %v", names)
	}
	if names["dailysummarymaildirect"] != "detected" {
		t.Errorf("expected daily-summary detected, got %v", names)
	}
	if names["subscription-reconcile"] != "proposed-missing-endpoint" {
		t.Errorf("expected subscription-reconcile proposed, got %v", names)
	}
}
