package testkit

import (
	"bytes"
	"net/http"
	"time"
)

// postWithTimeout is a tiny helper used by notify.go's webhook
// dispatch. Hard 5s timeout so a slow webhook never blocks the
// runner.
func postWithTimeout(url string, body []byte) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}
