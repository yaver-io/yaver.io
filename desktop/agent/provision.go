package main

// provision.go — agent side of zero-touch (DPP-style) device provisioning.
//
// The big picture (see backend/convex/provisioning.ts for the server half):
// a Yaver-powered box is flashed at the factory with a per-device "seed"
// written to its SD boot partition. The seed holds an Ed25519 PRIVATE key
// and a one-time claimSecret. The matching PUBLIC key + claimSecret are
// printed as a QR on the box's label. The buyer scans the QR — while the
// box is still in the shrink-wrap — and becomes the owner. On first boot
// the agent reads the seed and cryptographically attests to Convex, which
// hands back a session token bound to whoever claimed the QR. No human at
// the device, no LAN, no relay password; works through NAT.
//
// This file provides:
//   - the seed format + where to find/migrate it (boot partition → config)
//   - mint helpers (generate a keypair + claimSecret, build the QR URI)
//   - the attestation request + the bootstrap-time poll loop that drives
//     the box from "fresh" to "credentialed" by reusing the existing
//     pairing-session handoff (so the save-token + re-exec path is shared).

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const provisionSeedFileName = "provision.json"

// provisionSeedVersion is bumped if the on-disk seed format changes in a
// non-backward-compatible way.
const provisionSeedVersion = 1

// ProvisionSeed is the per-device identity written to the SD boot
// partition at flash time. It is the ONLY copy of the device's Ed25519
// private key — Convex never receives it. After first boot the agent
// migrates this file off the (removable, FAT) boot partition into the
// config dir with 0600 perms and deletes the boot-partition copy.
type ProvisionSeed struct {
	Version int    `json:"v"`
	// DeviceID is the stable id this box will register under. Minted at
	// flash time so the QR, the provisionedDevices row, and the eventual
	// devices row all agree.
	DeviceID string `json:"deviceId"`
	// Ed25519Seed is the 32-byte ed25519 seed (base64 std) from which the
	// signing key is derived (ed25519.NewKeyFromSeed).
	Ed25519Seed string `json:"ed25519Seed"`
	// ClaimSecret is the high-entropy one-time secret. Its SHA-256 is what
	// Convex stored at mint; the raw value proves possession of the seed
	// (here) and of the label/QR (at claim). Same value lives on the QR.
	ClaimSecret string `json:"claimSecret"`
	// ProductID / Model identify the SKU for the claim UI. Optional.
	ProductID     string `json:"productId,omitempty"`
	Model         string `json:"model,omitempty"`
	Platform      string `json:"platform,omitempty"`
	ConvexSiteURL string `json:"convexSiteUrl,omitempty"`
}

// bootPartitionSeedPaths are the well-known locations a flashed image
// drops the seed for first-boot pickup. Mirrors how cloud-init user-data
// rides the FAT boot partition on a Raspberry Pi.
var bootPartitionSeedPaths = []string{
	"/boot/firmware/yaver-provision.json",
	"/boot/yaver-provision.json",
}

// signingKey derives the Ed25519 private key from the stored seed.
func (s *ProvisionSeed) signingKey() (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s.Ed25519Seed))
	if err != nil {
		return nil, fmt.Errorf("decode ed25519 seed: %w", err)
	}
	if len(raw) != ed25519.SeedSize {
		return nil, fmt.Errorf("ed25519 seed is %d bytes, want %d", len(raw), ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(raw), nil
}

// PublicKeyBase64 returns the std-base64 Ed25519 public key (what Convex
// stored at mint and verifies attestation signatures against).
func (s *ProvisionSeed) PublicKeyBase64() (string, error) {
	key, err := s.signingKey()
	if err != nil {
		return "", err
	}
	pub := key.Public().(ed25519.PublicKey)
	return base64.StdEncoding.EncodeToString(pub), nil
}

// claimSecretHashHex returns the lowercase hex SHA-256 of a claimSecret —
// must match Convex's sha256Hex(claimSecret) exactly (UTF-8 bytes).
func claimSecretHashHex(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// provisionSeedPath is the config-dir home for the migrated seed.
func provisionSeedPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, provisionSeedFileName), nil
}

// LoadProvisionSeed finds a provisioning seed, migrating a boot-partition
// copy into the config dir on first boot. Resolution order:
//
//	1. $YAVER_PROVISION_SEED (explicit path; used by tests + power users)
//	2. <config>/provision.json (already migrated)
//	3. boot-partition well-known paths → migrate to (2), then delete source
//
// Returns (nil, nil) when no seed exists — i.e. this is an ordinary,
// non-provisioned install and the normal bootstrap path should run.
func LoadProvisionSeed() (*ProvisionSeed, error) {
	if explicit := strings.TrimSpace(os.Getenv("YAVER_PROVISION_SEED")); explicit != "" {
		return readProvisionSeed(explicit)
	}

	cfgPath, err := provisionSeedPath()
	if err == nil {
		if seed, rErr := readProvisionSeed(cfgPath); rErr == nil && seed != nil {
			return seed, nil
		}
	}

	for _, candidate := range bootPartitionSeedPaths {
		seed, rErr := readProvisionSeed(candidate)
		if rErr != nil || seed == nil {
			continue
		}
		// Migrate off the removable boot partition into the protected
		// config dir, then best-effort wipe the boot-partition copy so
		// the private key doesn't linger on an easily-imaged FAT volume.
		if cfgPath != "" {
			if wErr := writeProvisionSeed(cfgPath, seed); wErr != nil {
				log.Printf("[provision] could not migrate seed to config dir: %v (using boot-partition copy)", wErr)
			} else {
				if rmErr := os.Remove(candidate); rmErr != nil {
					log.Printf("[provision] migrated seed but could not remove boot-partition copy %s: %v", candidate, rmErr)
				} else {
					log.Printf("[provision] migrated provisioning seed off boot partition (device %s)", shortID(seed.DeviceID))
				}
			}
		}
		return seed, nil
	}

	return nil, nil
}

func readProvisionSeed(path string) (*ProvisionSeed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var seed ProvisionSeed
	if err := json.Unmarshal(data, &seed); err != nil {
		return nil, fmt.Errorf("parse provision seed %s: %w", path, err)
	}
	if strings.TrimSpace(seed.DeviceID) == "" || strings.TrimSpace(seed.Ed25519Seed) == "" || strings.TrimSpace(seed.ClaimSecret) == "" {
		return nil, fmt.Errorf("provision seed %s missing required fields", path)
	}
	return &seed, nil
}

func writeProvisionSeed(path string, seed *ProvisionSeed) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(seed, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// shortID is a log-safe prefix of a (possibly empty) id.
func shortID(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

// ── Mint (flash-time generation) ────────────────────────────────────────

// GenerateProvisionSeed mints a fresh per-device identity: a random
// deviceId, an Ed25519 keypair, and a 256-bit claimSecret. The returned
// seed is what gets flashed to the SD; the returned public key + the
// claimSecret get registered with Convex and printed as the QR.
func GenerateProvisionSeed(productID, model, platform, convexSiteURL string) (*ProvisionSeed, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}
	_ = pub // public key is recoverable from the seed; kept for clarity

	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, fmt.Errorf("generate claim secret: %w", err)
	}

	return &ProvisionSeed{
		Version:       provisionSeedVersion,
		DeviceID:      uuid.New().String(),
		Ed25519Seed:   base64.StdEncoding.EncodeToString(priv.Seed()),
		ClaimSecret:   base64.RawURLEncoding.EncodeToString(secretBytes),
		ProductID:     productID,
		Model:         model,
		Platform:      platform,
		ConvexSiteURL: convexSiteURL,
	}, nil
}

// ProvisionQRURI builds the scannable label payload. Carries only public
// material + the claimSecret (which is what authorizes the buyer to claim
// — possession of the physical label is the proof). The private key is
// NEVER in the QR. Mobile/web claim UIs parse this with ParseProvisionQR.
//
//	yaver://provision/v1?d=<deviceId>&k=<pubkeyB64url>&s=<claimSecret>&p=<productId>&m=<model>&u=<convexSiteUrl>
func (s *ProvisionSeed) ProvisionQRURI() (string, error) {
	pubStd, err := s.PublicKeyBase64()
	if err != nil {
		return "", err
	}
	// Re-encode the public key url-safe for compact QR.
	pubRaw, _ := base64.StdEncoding.DecodeString(pubStd)
	q := url.Values{}
	q.Set("d", s.DeviceID)
	q.Set("k", base64.RawURLEncoding.EncodeToString(pubRaw))
	q.Set("s", s.ClaimSecret)
	if s.ProductID != "" {
		q.Set("p", s.ProductID)
	}
	if s.Model != "" {
		q.Set("m", s.Model)
	}
	if s.ConvexSiteURL != "" {
		q.Set("u", s.ConvexSiteURL)
	}
	return "yaver://provision/v1?" + q.Encode(), nil
}

// ProvisionQRClaim is the subset a claim UI needs after scanning.
type ProvisionQRClaim struct {
	DeviceID      string
	ClaimSecret   string
	ProductID     string
	Model         string
	ConvexSiteURL string
	PublicKeyB64  string // std base64
}

// ParseProvisionQR decodes a scanned yaver://provision/v1 URI.
func ParseProvisionQR(raw string) (*ProvisionQRClaim, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("not a yaver provision QR: %w", err)
	}
	if u.Scheme != "yaver" || u.Host != "provision" {
		return nil, fmt.Errorf("not a yaver provision QR (scheme=%q host=%q)", u.Scheme, u.Host)
	}
	q := u.Query()
	deviceID := strings.TrimSpace(q.Get("d"))
	secret := strings.TrimSpace(q.Get("s"))
	if deviceID == "" || secret == "" {
		return nil, fmt.Errorf("provision QR missing device id or claim secret")
	}
	claim := &ProvisionQRClaim{
		DeviceID:      deviceID,
		ClaimSecret:   secret,
		ProductID:     q.Get("p"),
		Model:         q.Get("m"),
		ConvexSiteURL: q.Get("u"),
	}
	if k := strings.TrimSpace(q.Get("k")); k != "" {
		if raw, dErr := base64.RawURLEncoding.DecodeString(k); dErr == nil {
			claim.PublicKeyB64 = base64.StdEncoding.EncodeToString(raw)
		}
	}
	return claim, nil
}

// ── Attestation (first-boot) ────────────────────────────────────────────

// provisionAttestResult mirrors the Convex /devices/provision-attest body.
type provisionAttestResult struct {
	Status string `json:"status"`
	Token  string `json:"token,omitempty"`
}

// attestProvision performs one signed attestation round-trip. It signs a
// fresh timestamp with the seed's private key and posts the proof. The
// HTTP status maps onto the result.Status the caller switches on.
func attestProvision(ctx context.Context, seed *ProvisionSeed) (*provisionAttestResult, error) {
	convexURL := strings.TrimRight(seed.ConvexSiteURL, "/")
	if convexURL == "" {
		convexURL = strings.TrimRight(defaultConvexSiteURL, "/")
	}
	key, err := seed.signingKey()
	if err != nil {
		return nil, err
	}

	ts := time.Now().UnixMilli()
	msg := fmt.Sprintf("provision-attest|%s|%d", seed.DeviceID, ts)
	sig := ed25519.Sign(key, []byte(msg))

	body, _ := json.Marshal(map[string]interface{}{
		"deviceId":    seed.DeviceID,
		"claimSecret": seed.ClaimSecret,
		"timestampMs": ts,
		"signature":   base64.StdEncoding.EncodeToString(sig),
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, convexURL+"/devices/provision-attest", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	var out provisionAttestResult
	if err := json.Unmarshal(raw, &out); err != nil || out.Status == "" {
		return nil, fmt.Errorf("attest: unexpected response (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return &out, nil
}

// runProvisionAttestLoop drives a freshly-booted provisioned box to a
// credentialed state. It runs as a goroutine alongside the normal
// bootstrap pairing server, so zero-touch is the happy path and manual
// pairing remains a live fallback. On success it completes the active
// pairing session (token + URL), which makes runBootstrapServe's main
// loop save the token and re-exec into authenticated serve — the same
// handoff a manual pair uses.
func runProvisionAttestLoop(ctx context.Context, seed *ProvisionSeed) {
	log.Printf("[provision] zero-touch enabled for device %s — attesting to claim owner", shortID(seed.DeviceID))
	announcedWaiting := false
	// Poll briskly while waiting for the owner to scan/claim, backing off
	// on transient network errors. The claim can happen long after boot,
	// so this loop is patient; the box stays manually-pairable meanwhile.
	delay := 3 * time.Second
	const maxDelay = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		res, err := attestProvision(ctx, seed)
		if err != nil {
			log.Printf("[provision] attest attempt failed: %v (retrying in %s)", err, delay)
			if !sleepCtx(ctx, delay) {
				return
			}
			if delay < maxDelay {
				delay *= 2
				if delay > maxDelay {
					delay = maxDelay
				}
			}
			continue
		}
		delay = 3 * time.Second // reset backoff after a clean round-trip

		switch res.Status {
		case "active":
			if strings.TrimSpace(res.Token) == "" {
				log.Printf("[provision] server reported active but returned no token; falling back to manual pairing")
				return
			}
			log.Printf("[provision] ✓ owner claimed device %s — received session token, finishing setup", shortID(seed.DeviceID))
			if !completeActivePairingWithToken(res.Token, seed.ConvexSiteURL) {
				log.Printf("[provision] no active pairing session to hand the token to; will retry")
				if !sleepCtx(ctx, 2*time.Second) {
					return
				}
				continue
			}
			return

		case "awaiting-claim":
			if !announcedWaiting {
				log.Printf("[provision] device %s is waiting to be claimed — scan its QR in the Yaver app to take ownership", shortID(seed.DeviceID))
				announcedWaiting = true
			}
			if !sleepCtx(ctx, 5*time.Second) {
				return
			}

		case "revoked":
			log.Printf("[provision] device %s has been revoked server-side; zero-touch disabled (manual pairing still available)", shortID(seed.DeviceID))
			return

		case "not-found":
			log.Printf("[provision] device %s is not registered for provisioning; falling back to manual pairing", shortID(seed.DeviceID))
			return

		default: // bad-secret / bad-signature / stale
			log.Printf("[provision] attestation rejected (%s) — seed may be corrupt; falling back to manual pairing", res.Status)
			return
		}
	}
}

// sleepCtx sleeps for d unless ctx is cancelled first; returns false if
// cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// applyProvisionSeedToConfig pins the provisioned deviceId + convex URL
// into the config BEFORE the bootstrap relay/notify code runs, so the
// relay tunnel registers under the provisioned id (not a fresh random
// one) and saveBootstrapToken keeps the right id. Idempotent; never
// overwrites an existing non-empty deviceId.
func applyProvisionSeedToConfig(seed *ProvisionSeed) {
	if seed == nil {
		return
	}
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		cfg = &Config{}
	}
	changed := false
	if strings.TrimSpace(cfg.DeviceID) == "" && seed.DeviceID != "" {
		cfg.DeviceID = seed.DeviceID
		changed = true
	}
	if strings.TrimSpace(cfg.ConvexSiteURL) == "" && seed.ConvexSiteURL != "" {
		cfg.ConvexSiteURL = seed.ConvexSiteURL
		changed = true
	}
	if changed {
		if err := SaveConfig(cfg); err != nil {
			log.Printf("[provision] could not persist provisioned identity to config: %v", err)
		}
	}
}
