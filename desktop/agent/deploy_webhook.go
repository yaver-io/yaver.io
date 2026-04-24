package main

// deploy_webhook.go — fire-and-forget completion webhook for
// /deploy/ship. When Config.DeployWebhookURL is set, every finished
// run POSTs a JSON summary there so the host owner can wire a
// notification into Slack / Discord / Zapier / a home-grown
// dashboard — especially useful for overnight guest-triggered
// deploys where nobody is watching the terminal.
//
// Fire-and-forget: the webhook runs in its own goroutine so a slow
// or failing endpoint never blocks the deploy response. One retry
// after 2 seconds on non-2xx. Then log and give up — a webhook
// doesn't need to be reliable, the run already persisted to
// /deploy/runs history.

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

// DeployWebhookPayload is the JSON body delivered to
// Config.DeployWebhookURL. Fields mirror DeployRun but are
// deliberately compact — a notification surface doesn't need the
// full output tail, just enough to decide "page a human or not".
type DeployWebhookPayload struct {
	ID          string           `json:"id"`
	App         string           `json:"app"`
	Target      string           `json:"target"`
	Stack       string           `json:"stack,omitempty"`
	RequestedBy string           `json:"requested_by,omitempty"`
	IsGuest     bool             `json:"is_guest,omitempty"`
	StartedAt   int64            `json:"started_at"`
	DurationMs  int64            `json:"duration_ms"`
	ExitCode    int              `json:"exit_code"`
	OK          bool             `json:"ok"`
	ErrorClass  DeployErrorClass `json:"error_class,omitempty"`
	TimedOut    bool             `json:"timed_out,omitempty"`
	Host        string           `json:"host,omitempty"` // hostname for multi-machine setups
}

// deployWebhookClient is a dedicated http.Client so the webhook
// POSTs don't share connection state with the rest of the agent.
// Timeout is tight because failure is tolerated — no reason for a
// slow notification endpoint to leak file descriptors for minutes.
var deployWebhookClient = &http.Client{Timeout: 10 * time.Second}

// shouldFireDeployWebhook returns true when the (success/failure)
// outcome matches the Config.DeployWebhookOn filter.
func shouldFireDeployWebhook(ok bool, filter string) bool {
	switch strings.ToLower(strings.TrimSpace(filter)) {
	case "", "all":
		return true
	case "success":
		return ok
	case "failure", "fail", "failures":
		return !ok
	default:
		return true
	}
}

// FireDeployWebhook POSTs a summary of the just-finished run to
// Config.DeployWebhookURL, if set. Runs in its own goroutine so the
// caller (/deploy/ship handler) doesn't block on a slow endpoint.
func FireDeployWebhook(run DeployRun) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil || strings.TrimSpace(cfg.DeployWebhookURL) == "" {
		return
	}
	if !shouldFireDeployWebhook(run.OK, cfg.DeployWebhookOn) {
		return
	}
	payload := DeployWebhookPayload{
		ID:          run.ID,
		App:         run.App,
		Target:      run.Target,
		Stack:       run.Stack,
		RequestedBy: run.RequestedBy,
		IsGuest:     run.IsGuest,
		StartedAt:   run.StartedAt,
		DurationMs:  run.DurationMs,
		ExitCode:    run.ExitCode,
		OK:          run.OK,
		ErrorClass:  run.ErrorClass,
		TimedOut:    run.TimedOut,
		Host:        hostnameForWebhook(),
	}
	go postDeployWebhookWithRetry(cfg.DeployWebhookURL, payload)
}

// hostnameForWebhook is split out so a test can stub it. Returns
// "" on any error — the payload carries an omitempty field, so a
// missing hostname just doesn't appear in the JSON body.
var hostnameForWebhook = func() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// postDeployWebhookWithRetry sends the payload once, and on a
// transport-level error or non-2xx response waits 2 s and retries
// exactly once. Failures are logged, not returned.
func postDeployWebhookWithRetry(url string, payload DeployWebhookPayload) {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[deploy-webhook] marshal failed: %v", err)
		return
	}
	attempt := func() (int, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "yaver-agent/deploy-webhook")
		resp, err := deployWebhookClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer func() {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()
		return resp.StatusCode, nil
	}
	code, err := attempt()
	if err == nil && code >= 200 && code < 300 {
		return
	}
	if err != nil {
		log.Printf("[deploy-webhook] POST %s failed (attempt 1): %v — retrying in 2s", url, err)
	} else {
		log.Printf("[deploy-webhook] POST %s returned HTTP %d (attempt 1) — retrying in 2s", url, code)
	}
	time.Sleep(2 * time.Second)
	code2, err2 := attempt()
	if err2 == nil && code2 >= 200 && code2 < 300 {
		return
	}
	if err2 != nil {
		log.Printf("[deploy-webhook] POST %s failed (attempt 2): %v — giving up", url, err2)
	} else {
		log.Printf("[deploy-webhook] POST %s returned HTTP %d (attempt 2) — giving up", url, code2)
	}
}
