package main

import (
	"os"
	"strings"
	"testing"
)

func TestGetDNSProvider_Auto(t *testing.T) {
	t.Setenv("CF_API_TOKEN", "dummy")
	p := GetDNSProvider("")
	if p.Name() != "cloudflare" {
		t.Fatalf("with CF_API_TOKEN set, auto should prefer cloudflare; got %q", p.Name())
	}
}

func TestGetDNSProvider_AutoFallback(t *testing.T) {
	// Explicit unset via Setenv cycle — Go's test harness restores afterwards.
	t.Setenv("CF_API_TOKEN", "")
	// Some shells leak the var via os.Setenv precedence; force-clear.
	_ = os.Unsetenv("CF_API_TOKEN")
	p := GetDNSProvider("")
	if p.Name() != "manual" {
		t.Fatalf("without CF_API_TOKEN, auto should fall back to manual; got %q", p.Name())
	}
}

func TestGetDNSProvider_ExplicitManual(t *testing.T) {
	t.Setenv("CF_API_TOKEN", "dummy") // even with token, explicit manual wins
	p := GetDNSProvider("manual")
	if p.Name() != "manual" {
		t.Fatalf("explicit manual ignored; got %q", p.Name())
	}
}

func TestManualProvider_CreateRecord(t *testing.T) {
	p := &manualProvider{}
	rec := DNSRecord{Type: "TXT", Name: "_yaver-verify.myapp.com", Content: "yaver-verify-abc"}
	id, manual, instr, err := p.CreateRecord("myapp.com", rec)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if id != "" {
		t.Fatalf("manual provider must return empty recordID; got %q", id)
	}
	if !manual {
		t.Fatal("manual provider must signal manual=true")
	}
	if instr == nil || !strings.Contains(instr.Note, "own the domain") {
		t.Fatalf("expected ownership note; got %+v", instr)
	}
}
