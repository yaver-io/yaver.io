package testkit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// HAR export.
//
// When the dev writes `save_har: checkout` in a spec, we dump the
// network capture we've already accumulated via CDP into a HAR 1.2
// file under <spec>/.yaver-test-results/<name>.har. The HAR format
// is documented at:
//
//   https://w3c.github.io/web-performance/specs/HAR/Overview.html
//
// We implement the minimum set of fields Chrome's DevTools Network
// panel + the common HAR viewers actually read: log.version,
// log.creator, log.entries[].{startedDateTime, time, request, response, timings}.
// Request/response bodies are not captured (they'd balloon disk use
// and aren't in our in-memory network state). The dev can always
// turn on chromedp's body capture separately if they need them.

// HARLog is the top-level wrapper.
type HARLog struct {
	Version string     `json:"version"`
	Creator HARCreator `json:"creator"`
	Entries []HAREntry `json:"entries"`
}

type HARCreator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Comment string `json:"comment"`
}

type HAREntry struct {
	StartedDateTime string       `json:"startedDateTime"`
	Time            int64        `json:"time"`
	Request         HARRequest   `json:"request"`
	Response        HARResponse  `json:"response"`
	Cache           struct{}     `json:"cache"`
	Timings         HARTimings   `json:"timings"`
}

type HARRequest struct {
	Method      string            `json:"method"`
	URL         string            `json:"url"`
	HTTPVersion string            `json:"httpVersion"`
	Cookies     []struct{}        `json:"cookies"`
	Headers     []HARHeader       `json:"headers"`
	QueryString []HARHeader       `json:"queryString"`
	HeadersSize int               `json:"headersSize"`
	BodySize    int               `json:"bodySize"`
}

type HARResponse struct {
	Status      int           `json:"status"`
	StatusText  string        `json:"statusText"`
	HTTPVersion string        `json:"httpVersion"`
	Cookies     []struct{}    `json:"cookies"`
	Headers     []HARHeader   `json:"headers"`
	Content     HARContent    `json:"content"`
	RedirectURL string        `json:"redirectURL"`
	HeadersSize int           `json:"headersSize"`
	BodySize    int64         `json:"bodySize"`
}

type HARHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HARContent struct {
	Size     int64  `json:"size"`
	MimeType string `json:"mimeType"`
}

type HARTimings struct {
	Send    float64 `json:"send"`
	Wait    float64 `json:"wait"`
	Receive float64 `json:"receive"`
}

// SaveHAR writes the current network capture as a HAR file. Returns
// the on-disk path or an error. If no network events were recorded,
// the HAR is still written (empty entries) so the dev can distinguish
// "step fired but no requests happened" from "step didn't fire at
// all."
func SaveHAR(state *InstrumentationState, artifactDir, label string) (string, error) {
	if state == nil {
		return "", fmt.Errorf("save_har: no instrumentation state — set capture.network: true in the spec")
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(artifactDir, sanitizeName(label)+".har")

	state.mu.Lock()
	entries := make([]HAREntry, 0, len(state.Requests))
	for _, n := range state.Requests {
		entry := HAREntry{
			StartedDateTime: n.StartedAt.UTC().Format(time.RFC3339Nano),
			Time:            n.DurationMS,
			Request: HARRequest{
				Method:      n.Method,
				URL:         n.URL,
				HTTPVersion: "HTTP/1.1",
				HeadersSize: -1,
				BodySize:    -1,
			},
			Response: HARResponse{
				Status:      n.Status,
				StatusText:  n.StatusText,
				HTTPVersion: "HTTP/1.1",
				Content: HARContent{
					Size:     n.SizeBytes,
					MimeType: mimeFromType(n.Type),
				},
				HeadersSize: -1,
				BodySize:    n.SizeBytes,
			},
			Timings: HARTimings{
				Send:    0,
				Wait:    float64(n.DurationMS),
				Receive: 0,
			},
		}
		entries = append(entries, entry)
	}
	state.mu.Unlock()

	h := HARLog{
		Version: "1.2",
		Creator: HARCreator{
			Name:    "yaver-test-sdk",
			Version: runtime.Version(),
			Comment: "Captured by yaver test run — all data local to this machine.",
		},
		Entries: entries,
	}
	wrapper := map[string]interface{}{"log": h}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(wrapper); err != nil {
		return path, err
	}
	return path, nil
}

// mimeFromType maps CDP resource-type strings to approximate MIME
// types for the HAR viewer. The capture stream doesn't carry the
// response Content-Type header today (we could add it) so this is a
// best-effort convenience.
func mimeFromType(t string) string {
	switch t {
	case "Document":
		return "text/html"
	case "Stylesheet":
		return "text/css"
	case "Script":
		return "application/javascript"
	case "Image":
		return "image/png"
	case "Fetch", "XHR":
		return "application/json"
	case "WebSocket":
		return "application/octet-stream"
	}
	return "application/octet-stream"
}
