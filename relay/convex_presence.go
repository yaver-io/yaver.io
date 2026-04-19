package main

// convex_presence.go — optional relay→Convex presence push.
//
// When a tunnel registers or disconnects, the relay can fire a small
// HTTP call to a Convex HTTP action that updates devices[].isOnline +
// devices[].lastTunnelEvent. This closes the heartbeat-lag gap for
// clients that watch the reactive Convex list: instead of polling
// /presence every 30s, they see state flip the moment tunnel state
// changes (typically < 2s end-to-end).
//
// The push is opt-in — set both env vars to enable:
//
//   CONVEX_PRESENCE_URL=https://<deployment>.convex.site/devices/presence
//   CONVEX_PRESENCE_SECRET=<shared-secret>
//
// Without them the relay's behaviour is unchanged (clients still hit
// /presence for pull-based state). With them every tunnel event
// also fires a POST to Convex. Failures are best-effort: a flaky
// push never blocks tunnel registration or cleanup.
//
// The secret is verified on the Convex side (see
// backend/convex/http.ts /devices/presence). Separate from the
// end-user's auth token — the relay doesn't hold those. Rotate the
// secret via env redeploy on the relay + Convex dashboard var.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type presencePayload struct {
	DeviceID     string `json:"deviceId"`
	Online       bool   `json:"online"`
	RelayID      string `json:"relayId,omitempty"`
	PeerAddr     string `json:"peerAddr,omitempty"`
	ConnectedAt  int64  `json:"connectedAt,omitempty"`  // epoch ms
	DurationSec  int    `json:"durationSec,omitempty"`  // populated on disconnect
}

// pushPresence fires a fire-and-forget update to Convex. Returns
// quickly (1.5s upper bound) so slow networks never wedge the relay's
// tunnel lifecycle. Errors are logged but never surfaced — an
// authoritative view of tunnel state always lives in the relay's
// in-memory map regardless.
func pushPresence(payload presencePayload) {
	url := os.Getenv("CONVEX_PRESENCE_URL")
	secret := os.Getenv("CONVEX_PRESENCE_SECRET")
	if url == "" || secret == "" {
		return // feature disabled
	}
	url = strings.TrimSpace(url)
	secret = strings.TrimSpace(secret)

	// Best-effort JSON marshal; log and drop if we somehow produce
	// garbage. Panic here would tear down the relay tunnel loop.
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[PRESENCE] marshal error: %v", err)
		return
	}

	go func(body []byte) {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			log.Printf("[PRESENCE] request error: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Relay-Secret", secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("[PRESENCE] push error for %s: %v", payload.DeviceID[:minI(8, len(payload.DeviceID))], err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			out, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
			log.Printf("[PRESENCE] push HTTP %d for %s: %s", resp.StatusCode, payload.DeviceID[:minI(8, len(payload.DeviceID))], strings.TrimSpace(string(out)))
		}
	}(body)
}

func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}
