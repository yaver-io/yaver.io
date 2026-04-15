package main

// apm.go — lightweight per-route APM rollups. Solo-dev
// alternative to Datadog APM / New Relic for the "which
// endpoint is slow in my shipped app" case.
//
// Data source: the Feedback SDK's BlackBox already streams
// `network` events with a method/url/status/duration payload.
// This file consumes those events in PushEvent and keeps an
// in-memory rolling histogram per (route-prefix, status-class)
// pair. Percentile math is done with a tiny t-digest-free
// fallback: a sorted ring of the last 256 observations per
// key, which is plenty for solo-dev traffic patterns.
//
// Output surface: /apm returns the current rollup for the
// mobile Monitor > APM sub-tab. Nothing persists — a fresh
// agent starts with a clean window on purpose so stale latency
// data never misleads the dev after a deploy.

import (
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// APMBucket holds the rolling observations for one
// (route, statusClass) key.
type APMBucket struct {
	Route       string  `json:"route"`
	StatusClass string  `json:"statusClass"` // "2xx" / "4xx" / "5xx"
	Count       int     `json:"count"`
	P50MS       float64 `json:"p50Ms"`
	P95MS       float64 `json:"p95Ms"`
	P99MS       float64 `json:"p99Ms"`
	MaxMS       float64 `json:"maxMs"`
	ErrorRate   float64 `json:"errorRate"`
	samples     []float64
	errors      int
	totalCount  int
	mu          sync.Mutex
	lastSeen    time.Time
}

// APMStore is the process-wide aggregator.
type APMStore struct {
	mu      sync.Mutex
	buckets map[string]*APMBucket
}

var (
	apmStoreOnce sync.Once
	apmStoreInst *APMStore
)

// GlobalAPMStore returns the singleton aggregator.
func GlobalAPMStore() *APMStore {
	apmStoreOnce.Do(func() {
		apmStoreInst = &APMStore{buckets: map[string]*APMBucket{}}
	})
	return apmStoreInst
}

// Record ingests one network event from the BlackBox stream.
// Non-network events are filtered out at the call site so this
// function stays a hot path.
func (s *APMStore) Record(ev BlackBoxEvent) {
	if ev.Type != "network" || ev.Duration <= 0 {
		return
	}
	route := ev.Route
	if route == "" {
		// Derive from metadata if the SDK put the URL there.
		if u, ok := ev.Metadata["url"].(string); ok {
			route = normalizeRoute(u)
		} else if u, ok := ev.Metadata["URL"].(string); ok {
			route = normalizeRoute(u)
		} else {
			route = "unknown"
		}
	}
	statusClass := "2xx"
	if st, ok := ev.Metadata["status"].(float64); ok {
		statusClass = classifyStatus(int(st))
	}
	key := route + "|" + statusClass

	s.mu.Lock()
	b := s.buckets[key]
	if b == nil {
		b = &APMBucket{Route: route, StatusClass: statusClass}
		s.buckets[key] = b
	}
	s.mu.Unlock()

	b.mu.Lock()
	defer b.mu.Unlock()
	b.samples = append(b.samples, ev.Duration)
	if len(b.samples) > 256 {
		b.samples = b.samples[len(b.samples)-256:]
	}
	b.totalCount++
	if statusClass == "5xx" || statusClass == "4xx" {
		b.errors++
	}
	b.lastSeen = time.Now()
	b.computeLocked()
}

func (b *APMBucket) computeLocked() {
	if len(b.samples) == 0 {
		return
	}
	copied := make([]float64, len(b.samples))
	copy(copied, b.samples)
	sort.Float64s(copied)
	b.Count = len(copied)
	b.P50MS = copied[percentileIdx(len(copied), 0.50)]
	b.P95MS = copied[percentileIdx(len(copied), 0.95)]
	b.P99MS = copied[percentileIdx(len(copied), 0.99)]
	b.MaxMS = copied[len(copied)-1]
	if b.totalCount > 0 {
		b.ErrorRate = float64(b.errors) / float64(b.totalCount)
	}
}

// APMBucketView is the mutex-free projection of APMBucket used for
// snapshots and JSON responses. It exists because APMBucket embeds a
// sync.Mutex and `go vet` flags any value-copy of a struct-with-lock;
// Snapshot() returns these instead of bare APMBuckets so callers
// (dashboard JSON, tests) can safely range and copy the result.
type APMBucketView struct {
	Route       string  `json:"route"`
	StatusClass string  `json:"statusClass"`
	Count       int     `json:"count"`
	P50MS       float64 `json:"p50Ms"`
	P95MS       float64 `json:"p95Ms"`
	P99MS       float64 `json:"p99Ms"`
	MaxMS       float64 `json:"maxMs"`
	ErrorRate   float64 `json:"errorRate"`
}

// Snapshot returns a copy of every bucket, sorted by P95 desc
// so the "slow endpoints first" view is the default.
func (s *APMStore) Snapshot() []APMBucketView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]APMBucketView, 0, len(s.buckets))
	for _, b := range s.buckets {
		b.mu.Lock()
		cp := APMBucketView{
			Route:       b.Route,
			StatusClass: b.StatusClass,
			Count:       b.Count,
			P50MS:       b.P50MS,
			P95MS:       b.P95MS,
			P99MS:       b.P99MS,
			MaxMS:       b.MaxMS,
			ErrorRate:   b.ErrorRate,
		}
		b.mu.Unlock()
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].P95MS > out[j].P95MS })
	return out
}

// percentileIdx maps a percentile (0..1) onto a slice of length n.
func percentileIdx(n int, p float64) int {
	if n <= 1 {
		return 0
	}
	i := int(float64(n-1) * p)
	if i < 0 {
		i = 0
	}
	if i >= n {
		i = n - 1
	}
	return i
}

func classifyStatus(status int) string {
	if status >= 500 {
		return "5xx"
	}
	if status >= 400 {
		return "4xx"
	}
	if status >= 300 {
		return "3xx"
	}
	return "2xx"
}

// normalizeRoute strips query + fragment + numeric path
// segments so "GET /users/123/posts/456" all collapses to
// "GET /users/:id/posts/:id" and every APM bucket has
// meaningful aggregation.
func normalizeRoute(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	segs := strings.Split(u.Path, "/")
	for i, seg := range segs {
		if seg == "" {
			continue
		}
		if allDigits(seg) || looksLikeUUID(seg) {
			segs[i] = ":id"
		}
	}
	return strings.Join(segs, "/")
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func looksLikeUUID(s string) bool {
	return len(s) == 36 && strings.Count(s, "-") == 4
}

// --- HTTP ----------------------------------------------------------------

func (s *HTTPServer) handleAPM(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"buckets": GlobalAPMStore().Snapshot(),
	})
}
