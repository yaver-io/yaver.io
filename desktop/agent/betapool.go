package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
	defaultBetaPoolSKU    = "cpx41" // 8 vCPU / 16 GB — Hermes floor (configurable)
	defaultBetaPoolRegion = "hel1"
	defaultBetaMaxIdleSec = 1200 // 20 min idle → reap (hysteresis vs cold-start thrash)
	betaPoolTickSec       = 15
)

type betaPoolController struct {
	relayBase   string // e.g. https://relay.example/ (YAVER_BETA_RELAY)
	adminToken  string // RELAY_ADMIN_TOKEN — writes /beta/state
	hcloudToken string // HCLOUD_TOKEN — empty ⇒ dry-run (no spend)
	sku         string
	region      string
	maxIdleSec  int64
	httpc       *http.Client

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
		relayBase:   strings.TrimRight(os.Getenv("YAVER_BETA_RELAY"), "/"),
		adminToken:  strings.TrimSpace(os.Getenv("RELAY_ADMIN_TOKEN")),
		hcloudToken: strings.TrimSpace(os.Getenv("HCLOUD_TOKEN")),
		sku:         firstNonEmpty(strings.TrimSpace(os.Getenv("YAVER_BETA_POOL_SKU")), defaultBetaPoolSKU),
		region:      firstNonEmpty(strings.TrimSpace(os.Getenv("YAVER_BETA_POOL_REGION")), defaultBetaPoolRegion),
		maxIdleSec:  defaultBetaMaxIdleSec,
		httpc:       &http.Client{Timeout: 20 * time.Second},
		nowFn:       func() int64 { return time.Now().Unix() },
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

func (c *betaPoolController) provision() (string, error) {
	if c.provisionFn != nil {
		return c.provisionFn()
	}
	if c.hcloudToken == "" {
		log.Printf("[betapool] DRY-RUN: would provision %s in %s (HCLOUD_TOKEN unset) with YAVER_BETA_HOST=1", c.sku, c.region)
		return "dry-run-box", nil
	}
	// Real provisioning is a deliberate, separately-wired step (cloud_byo_provision
	// from a golden snapshot + cloud-init YAVER_BETA_HOST=1). Fail closed rather
	// than half-provision: a token alone must not silently start spending.
	return "", fmt.Errorf("betapool: real provisioning not wired (set provisionFn); refusing to spend")
}

func (c *betaPoolController) reap(addr string) error {
	if c.reapFn != nil {
		return c.reapFn(addr)
	}
	if c.hcloudToken == "" {
		log.Printf("[betapool] DRY-RUN: would snapshot+delete %q (HCLOUD_TOKEN unset)", addr)
		return nil
	}
	return fmt.Errorf("betapool: real reap not wired (set reapFn)")
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
