package main

// dns_provider.go — pluggable DNS provider adapter.
//
// The existing `DomainManager` (domain.go) and `/dns/cloudflare/*` HTTP
// handlers (cloudflare_dns.go) talk to Cloudflare's API directly. That's
// fine as long as the user's DNS is at Cloudflare, but:
//   • A user who just bought myapp.com at Namecheap/Porkbun doesn't have
//     their DNS at Cloudflare.
//   • We want to fall back to "paste these records at your registrar"
//     instructions when we can't automate.
//
// This file adds a narrow interface for the minimum CRUD we actually need
// for the user-domains flow (ownership verification TXT + A/CNAME route),
// plus two implementations:
//
//   cloudflareProvider — delegates to Cloudflare API (auto-create records).
//   manualProvider     — returns instructions; no API calls. The "any
//                        registrar" safety net.
//
// New code (userDomains web UI, future CLI `yaver domain add` flow) can
// route through GetDNSProvider. Existing code paths in domain.go are
// untouched on purpose — Cloudflare-only flow still works as before.

import (
	"fmt"
	"os"
	"strings"
)

// DNSRecord is the common shape of a record across providers.
type DNSRecord struct {
	Type    string `json:"type"`    // "A" | "CNAME" | "TXT" | ...
	Name    string `json:"name"`    // relative ("sub" for sub.example.com) or absolute
	Content string `json:"content"` // IP for A, hostname for CNAME, value for TXT
	TTL     int    `json:"ttl"`     // 0 = auto
	Proxied bool   `json:"proxied,omitempty"`
}

// DNSInstruction is what a manual provider returns so the UI / CLI can
// tell the user exactly what to paste at their registrar.
type DNSInstruction struct {
	Record DNSRecord `json:"record"`
	Note   string    `json:"note"`
}

// DNSProvider is the minimum surface both managed and manual adapters
// satisfy. Create/Delete return `manual=true` to signal the caller should
// render the returned instructions instead of treating the call as done.
type DNSProvider interface {
	Name() string
	Description() string
	// CreateRecord returns (recordID, manual, instruction, error). For the
	// manual adapter, manual=true and the caller is expected to show the
	// instruction to the user. recordID is "" for manual.
	CreateRecord(zone string, rec DNSRecord) (string, bool, *DNSInstruction, error)
	DeleteRecord(zone, recordID string) (bool, *DNSInstruction, error)
}

// GetDNSProvider returns the configured provider for a user. For now the
// selection is static: CF_API_TOKEN present → cloudflare, otherwise manual.
// When we add Namecheap/Porkbun/Route53 adapters they slot in here.
func GetDNSProvider(preferred string) DNSProvider {
	switch strings.ToLower(preferred) {
	case "manual":
		return &manualProvider{}
	case "cloudflare":
		if os.Getenv("CF_API_TOKEN") != "" {
			return &cloudflareProvider{dm: &DomainManager{}}
		}
		// Fall through to manual if no CF creds — never claim we can
		// do auto when we can't.
		return &manualProvider{reason: "CF_API_TOKEN not set; falling back to manual"}
	case "":
		// Auto — prefer cloudflare, fall back to manual.
		if os.Getenv("CF_API_TOKEN") != "" {
			return &cloudflareProvider{dm: &DomainManager{}}
		}
		return &manualProvider{}
	}
	// Unknown provider name — always safe to degrade to manual.
	return &manualProvider{reason: "unknown provider: " + preferred}
}

// ─── Cloudflare adapter ──────────────────────────────────────────────

type cloudflareProvider struct {
	dm *DomainManager
}

func (*cloudflareProvider) Name() string        { return "cloudflare" }
func (*cloudflareProvider) Description() string { return "Cloudflare (auto-manage via API token)" }

func (p *cloudflareProvider) CreateRecord(zone string, rec DNSRecord) (string, bool, *DNSInstruction, error) {
	zoneID, err := p.dm.cloudflareGetZoneID(zone)
	if err != nil {
		// Auto flow can't find the zone → degrade to manual instructions
		// rather than erroring out. User still gets a clear next step.
		return "", true, &DNSInstruction{
			Record: rec,
			Note:   fmt.Sprintf("Zone %q not in your Cloudflare account — add it there first, or paste this record at your registrar.", zone),
		}, nil
	}
	if err := p.dm.cloudflareCreateRecord(zoneID, rec.Type, rec.Name, rec.Content, rec.Proxied); err != nil {
		return "", false, nil, err
	}
	// We don't track per-record IDs through the legacy methods; return empty.
	return "", false, nil, nil
}

func (p *cloudflareProvider) DeleteRecord(_zone, recordID string) (bool, *DNSInstruction, error) {
	// The legacy cloudflareAPI helper + the dm's knowledge of zoneID is not
	// exposed here; the user-domains code path that uses us doesn't need
	// per-record deletes yet. Stub with a clear error rather than pretend.
	return false, nil, fmt.Errorf("cloudflareProvider.DeleteRecord not implemented")
}

// ─── Manual adapter ──────────────────────────────────────────────────

type manualProvider struct {
	reason string // optional reason for the fallback (shown to the user)
}

func (*manualProvider) Name() string { return "manual" }
func (m *manualProvider) Description() string {
	if m.reason != "" {
		return "Manual (" + m.reason + ")"
	}
	return "Manual — copy-paste these records at your registrar"
}

func (m *manualProvider) CreateRecord(zone string, rec DNSRecord) (string, bool, *DNSInstruction, error) {
	note := fmt.Sprintf("Add this %s record at your DNS host for %s.", rec.Type, zone)
	if rec.Type == "TXT" && strings.HasPrefix(rec.Name, "_yaver-verify") {
		note = "Add this TXT record to prove you own the domain."
	}
	if rec.Type == "A" {
		note = fmt.Sprintf("Add this A record to point %s at the server.", zone)
	}
	if rec.Type == "CNAME" {
		note = fmt.Sprintf("Add this CNAME to alias %s to the Yaver-managed hostname.", zone)
	}
	return "", true, &DNSInstruction{Record: rec, Note: note}, nil
}

func (m *manualProvider) DeleteRecord(_zone, _recordID string) (bool, *DNSInstruction, error) {
	return true, &DNSInstruction{
		Record: DNSRecord{},
		Note:   "Remove the Yaver DNS records at your registrar to finish.",
	}, nil
}
