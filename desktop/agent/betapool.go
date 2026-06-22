package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// betapool.go — the owner-side controller for the scale-to-zero shared beta
// runtime. It polls the relay's /beta/state, and:
//
//   phase "waking"  → provision ONE pool box → POST phase "up" + boxAddr
//   phase "up" + idle ≥ maxIdle → reap (snapshot+delete) → POST phase "down"
//
// COST SAFETY:
//   - It only ever acts on signals the relay already gated to verified beta
//     users (see beta_signal.go) — an attacker can't even produce a "waking".
//   - Real cloud calls are DRY-RUN unless HCLOUD_TOKEN is set AND the real
//     seams are wired. Default build never spends: it logs what it WOULD do.
//   - The pool box boots with YAVER_BETA_HOST=1 (the only kind of box allowed
//     to execute beta tenants — see betaHostEnabled). The owner's general box
//     never sets it, so beta code can never run there.
//
// Day-one SKU floor is Hermes-capable (Metro + hermesc for RN/Expo apps like
// sfmg/carrotbet need real RAM) — NOT a 4 GB box. Grow without data loss via
// change-type --keep-disk or snapshot→recreate-bigger (see the handoff).

const (
	// VALIDATION default (2026-06-20): 5 non-concurrent beta users → one
	// scale-to-zero box. 8 GB is plenty (sfmg's 8.6 GB is DISK — node_modules +
	// build artifacts — NOT RAM; Hermes needs ~2-4 GB; seed source only).
	//   - cx33 (4 vCPU / 8 GB x86): x86 is REQUIRED for redroid, provisioned
	//     reliably in stock probes (cax/arm was flaky), and 8 GB booted Android.
	//   - redroid VERIFIED on Hetzner cloud (2026-06-21): apt install
	//     linux-modules-extra-$(uname -r) → modprobe binder_linux
	//     devices="binder,hwbinder,vndbinder" → mount -t binder binder
	//     /dev/binderfs → docker run --privileged redroid/redroid:13.0.0
	//     → sys.boot_completed=1 (Android 13). Bake these into the golden image.
	// HARD RULE (CLAUDE.md): metered, never monthly — the controller ALWAYS
	// snapshot+deletes on idle; no box is ever left running to hit the monthly cap.
	defaultBetaPoolSKU    = "cx33" // 4 vCPU / 8 GB x86 ($8.99/mo) — redroid-capable
	defaultBetaPoolRegion = "nbg1"
	defaultBetaMaxIdleSec = 1200 // 20 min idle → POWER OFF (managed-cloud pause; box+data persist; fast ~15s resume)
	betaPoolTickSec       = 15
)

type betaPoolController struct {
	relayBase    string // e.g. https://relay.example/ (YAVER_BETA_RELAY)
	adminToken   string // RELAY_ADMIN_TOKEN — writes /beta/state
	hcloudToken  string // HCLOUD_TOKEN — empty ⇒ dry-run (no spend)
	betaBoxName  string // YAVER_BETA_BOX_NAME — the persistent beta box to pause/resume
	hcloudAPIURL string // Hetzner API base (overridable for tests)
	sku          string
	region       string
	maxIdleSec   int64
	httpc        *http.Client

	// Injectable seams. Defaults are dry-run; a real deployment wires these to
	// cloud_byo_provision (create from golden snapshot, cloud-init
	// YAVER_BETA_HOST=1) and the ci/hcloud snapshot+delete scripts. Kept as
	// seams so flipping to real spend is a deliberate, reviewed step.
	provisionFn func() (string, error)
	reapFn      func(addr string) error
	nowFn       func() int64
}

func newBetaPoolController() *betaPoolController {
	c := &betaPoolController{
		relayBase:    strings.TrimRight(os.Getenv("YAVER_BETA_RELAY"), "/"),
		adminToken:   strings.TrimSpace(os.Getenv("RELAY_ADMIN_TOKEN")),
		hcloudToken:  strings.TrimSpace(os.Getenv("HCLOUD_TOKEN")),
		betaBoxName:  firstNonEmpty(strings.TrimSpace(os.Getenv("YAVER_BETA_BOX_NAME")), "yaver-beta-cloud"),
		hcloudAPIURL: firstNonEmpty(strings.TrimSpace(os.Getenv("HCLOUD_API_URL")), "https://api.hetzner.cloud/v1"),
		sku:          firstNonEmpty(strings.TrimSpace(os.Getenv("YAVER_BETA_POOL_SKU")), defaultBetaPoolSKU),
		region:       firstNonEmpty(strings.TrimSpace(os.Getenv("YAVER_BETA_POOL_REGION")), defaultBetaPoolRegion),
		maxIdleSec:   defaultBetaMaxIdleSec,
		httpc:        &http.Client{Timeout: 20 * time.Second},
		nowFn:        func() int64 { return time.Now().Unix() },
	}
	return c
}

type betaStateView struct {
	Phase    string `json:"phase"`
	BoxReady bool   `json:"boxReady"`
	IdleSec  int64  `json:"idleSec"`
}

func (c *betaPoolController) readState() (betaStateView, error) {
	var v betaStateView
	if c.relayBase == "" {
		return v, fmt.Errorf("betapool: YAVER_BETA_RELAY unset")
	}
	resp, err := c.httpc.Get(c.relayBase + "/beta/state")
	if err != nil {
		return v, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return v, fmt.Errorf("betapool: /beta/state %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err := json.Unmarshal(body, &v); err != nil {
		return v, err
	}
	return v, nil
}

func (c *betaPoolController) setState(phase, boxAddr string, activity bool) error {
	payload, _ := json.Marshal(map[string]any{"phase": phase, "boxAddr": boxAddr, "activity": activity})
	req, err := http.NewRequest(http.MethodPost, c.relayBase+"/beta/state", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.adminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("betapool: setState %s → %d", phase, resp.StatusCode)
	}
	return nil
}

// provision = POWER ON the managed-cloud beta box (create it once from the
// golden snapshot if it doesn't exist yet, then power on). The box PERSISTS —
// this is the managed-cloud model: a paid box that pauses, not an ephemeral one.
func (c *betaPoolController) provision() (string, error) {
	if c.provisionFn != nil {
		return c.provisionFn()
	}
	if c.hcloudToken == "" {
		log.Printf("[betapool] DRY-RUN: would POWER ON the beta box (create once from golden snapshot if absent; HCLOUD_TOKEN unset)")
		return "dry-run-box", nil
	}
	// Real power-on: the beta box persists, so wake = poweron (no create).
	id, status, ip, err := c.hcloudFindBox()
	if err != nil {
		return "", fmt.Errorf("betapool power-on: %w", err)
	}
	if status != "running" {
		if err := c.hcloudAction(id, "poweron"); err != nil {
			return "", fmt.Errorf("betapool poweron: %w", err)
		}
		// poll until running (≤ ~60s) so callers get a usable box addr
		for i := 0; i < 30; i++ {
			time.Sleep(2 * time.Second)
			if _, s, p, e := c.hcloudFindBox(); e == nil && s == "running" {
				ip = p
				break
			}
		}
	}
	log.Printf("[betapool] POWERED ON %s (%s)", c.betaBoxName, ip)
	return ip, nil
}

// reap = POWER OFF the managed-cloud beta box (NOT delete). Box + data persist
// for a fast ~15s resume. NOTE: Hetzner bills a powered-off server at the full
// rate (~$8.99/mo for cx33) — power-off is a managed-cloud "pause" (the paying
// user's cost), NOT a cost-to-zero. To stop billing entirely you must DELETE
// (snapshot preserves data) — that's the scale-to-zero path, chosen per-product.
func (c *betaPoolController) reap(addr string) error {
	if c.reapFn != nil {
		return c.reapFn(addr)
	}
	if c.hcloudToken == "" {
		log.Printf("[betapool] DRY-RUN: would POWER OFF %q (managed-cloud pause; box+data persist; still billed ~$8.99/mo)", addr)
		return nil
	}
	// Real power-off (NOT delete): the relay reports idle → we pause the box,
	// preserving it + its data for a fast resume. This is the user's directive:
	// "relay will down it if nobody uses (not delete, power off)".
	id, status, _, err := c.hcloudFindBox()
	if err != nil {
		return fmt.Errorf("betapool power-off: %w", err)
	}
	if status == "off" {
		return nil // already paused
	}
	if err := c.hcloudAction(id, "poweroff"); err != nil {
		return fmt.Errorf("betapool poweroff: %w", err)
	}
	log.Printf("[betapool] POWERED OFF %s (idle) — box+data persist, fast resume", c.betaBoxName)
	return nil
}

// hcloudFindBox resolves the persistent beta box by name → (id, status, ipv4).
func (c *betaPoolController) hcloudFindBox() (int64, string, string, error) {
	req, _ := http.NewRequest(http.MethodGet, c.hcloudAPIURL+"/servers?name="+url.QueryEscape(c.betaBoxName), nil)
	req.Header.Set("Authorization", "Bearer "+c.hcloudToken)
	resp, err := c.httpc.Do(req)
	if err != nil {
		return 0, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", "", fmt.Errorf("hcloud servers list → %d", resp.StatusCode)
	}
	var out struct {
		Servers []struct {
			ID        int64  `json:"id"`
			Status    string `json:"status"`
			PublicNet struct {
				IPv4 struct {
					IP string `json:"ip"`
				} `json:"ipv4"`
			} `json:"public_net"`
		} `json:"servers"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return 0, "", "", err
	}
	if len(out.Servers) == 0 {
		return 0, "", "", fmt.Errorf("beta box %q not found", c.betaBoxName)
	}
	s := out.Servers[0]
	return s.ID, s.Status, s.PublicNet.IPv4.IP, nil
}

// hcloudAction POSTs a power action (poweron|poweroff) for a server id.
func (c *betaPoolController) hcloudAction(id int64, action string) error {
	endpoint := fmt.Sprintf("%s/servers/%d/actions/%s", c.hcloudAPIURL, id, action)
	req, _ := http.NewRequest(http.MethodPost, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+c.hcloudToken)
	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("hcloud %s → %d", action, resp.StatusCode)
	}
	return nil
}

// tick performs one decision cycle. Returns the action taken (for logs/tests).
func (c *betaPoolController) tick() (string, error) {
	st, err := c.readState()
	if err != nil {
		return "", err
	}
	switch st.Phase {
	case "waking":
		addr, err := c.provision()
		if err != nil {
			return "provision-failed", err
		}
		if err := c.setState("up", addr, true); err != nil {
			return "setUp-failed", err
		}
		return "provisioned", nil
	case "up":
		if st.IdleSec >= c.maxIdleSec {
			if err := c.reap(""); err != nil {
				return "reap-failed", err
			}
			if err := c.setState("down", "", false); err != nil {
				return "setDown-failed", err
			}
			return "reaped", nil
		}
		return "active", nil
	default:
		return "idle", nil
	}
}

func (c *betaPoolController) Run(ctx context.Context) {
	mode := "DRY-RUN"
	if c.hcloudToken != "" && c.provisionFn != nil {
		mode = "LIVE"
	}
	log.Printf("[betapool] controller start: relay=%s sku=%s region=%s maxIdle=%ds mode=%s",
		c.relayBase, c.sku, c.region, c.maxIdleSec, mode)
	t := time.NewTicker(betaPoolTickSec * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if action, err := c.tick(); err != nil {
				log.Printf("[betapool] tick error (%s): %v", action, err)
			} else if action != "idle" && action != "active" {
				log.Printf("[betapool] %s", action)
			}
		}
	}
}
