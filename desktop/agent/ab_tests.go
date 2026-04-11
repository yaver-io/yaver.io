package main

// ab_tests.go — A/B / multivariate testing on top of the existing
// flag ledger. Replaces Statsig / GrowthBook / LaunchDarkly
// experiments for the solo dev who wants variant splits + sticky
// bucketing + conversion tracking without paying per MAU.
//
// Model:
//
//   Experiment {
//     key:       same key space as flags; reuses the flag ledger
//                for "is this experiment on at all?"
//     variants:  [{ name, weight }]
//     metric:    event name that counts as a conversion
//     startedAt  unix ms
//     stoppedAt  optional unix ms
//   }
//
//   The deterministic bucket uses SHA256(key + userId) % 100 so
//   a given user always lands in the same variant — same recipe
//   as RolloutPercent in flags.go, just split into N buckets.
//
//   Events arrive via /ab/events { experimentKey, variant,
//   userId, kind: "exposure" | "conversion" }. They're appended
//   to ~/.yaver/ab-events.jsonl so we never grow an in-memory
//   table unbounded.
//
// HTTP surface:
//
//   POST /ab/experiments               owner — create/update
//   GET  /ab/experiments               owner — list
//   GET  /ab/assign?key=&userId=       public/SDK — deterministic
//                                       variant assignment
//   POST /ab/events                    public/SDK — log exposure
//                                       or conversion
//   GET  /ab/results?key=              owner — conversion rates
//                                       per variant

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Experiment is one A/B test definition.
type Experiment struct {
	Key       string    `json:"key"`
	Name      string    `json:"name,omitempty"`
	Variants  []Variant `json:"variants"`
	Metric    string    `json:"metric"` // event name that counts as conversion
	StartedAt time.Time `json:"startedAt"`
	StoppedAt time.Time `json:"stoppedAt,omitempty"`
}

type Variant struct {
	Name   string `json:"name"`
	Weight int    `json:"weight"` // relative weight; sum normalised
}

// ABEvent is one row in the append-only event log.
type ABEvent struct {
	Key     string    `json:"key"`
	Variant string    `json:"variant"`
	UserID  string    `json:"userId"`
	Kind    string    `json:"kind"` // "exposure" | "conversion"
	At      time.Time `json:"at"`
}

var (
	abMu          sync.Mutex
	abExperiments []Experiment
)

func abFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ab-experiments.json"), nil
}

func abEventsFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ab-events.jsonl"), nil
}

func loadExperiments() []Experiment {
	abMu.Lock()
	defer abMu.Unlock()
	if abExperiments != nil {
		return abExperiments
	}
	p, _ := abFile()
	data, err := os.ReadFile(p)
	if err != nil {
		abExperiments = []Experiment{}
		return abExperiments
	}
	_ = json.Unmarshal(data, &abExperiments)
	return abExperiments
}

func saveExperiments() error {
	p, _ := abFile()
	data, _ := json.MarshalIndent(abExperiments, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

// AssignVariant returns the deterministic variant name for a
// given (experimentKey, userId). The math:
//
//   bucket = SHA256(key + "|" + userId)[0..8] mod weightSum
//
// and the variant whose cumulative weight covers `bucket` wins.
// Identical inputs always hash to the same bucket → stable
// assignment across sessions with no server state.
func AssignVariant(exp *Experiment, userID string) string {
	if len(exp.Variants) == 0 {
		return ""
	}
	sum := 0
	for _, v := range exp.Variants {
		sum += v.Weight
	}
	if sum <= 0 {
		sum = len(exp.Variants)
	}
	h := sha256.Sum256([]byte(exp.Key + "|" + userID))
	hexStr := hex.EncodeToString(h[:4])
	var n uint64
	fmt.Sscanf(hexStr, "%x", &n)
	bucket := int(n) % sum
	cum := 0
	for _, v := range exp.Variants {
		w := v.Weight
		if w <= 0 {
			w = 1
		}
		cum += w
		if bucket < cum {
			return v.Name
		}
	}
	return exp.Variants[len(exp.Variants)-1].Name
}

// appendABEvent writes an event row to the JSONL log.
func appendABEvent(e ABEvent) error {
	p, _ := abEventsFile()
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(e)
}

// --- HTTP ------------------------------------------------------------------

func (s *HTTPServer) handleABExperiments(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "experiments": loadExperiments()})
	case http.MethodPost:
		var e Experiment
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if e.Key == "" || len(e.Variants) == 0 {
			jsonError(w, http.StatusBadRequest, "key + variants required")
			return
		}
		if e.StartedAt.IsZero() {
			e.StartedAt = time.Now().UTC()
		}
		exps := loadExperiments()
		found := false
		for i := range exps {
			if exps[i].Key == e.Key {
				exps[i] = e
				found = true
				break
			}
		}
		if !found {
			exps = append(exps, e)
		}
		abMu.Lock()
		abExperiments = exps
		_ = saveExperiments()
		abMu.Unlock()
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "experiment": e})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET/POST")
	}
}

func (s *HTTPServer) handleABAssign(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	userID := r.URL.Query().Get("userId")
	if key == "" || userID == "" {
		jsonError(w, http.StatusBadRequest, "key and userId required")
		return
	}
	var exp *Experiment
	for i, e := range loadExperiments() {
		if e.Key == key {
			exp = &abExperiments[i]
			break
		}
	}
	if exp == nil || !exp.StoppedAt.IsZero() {
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "variant": "", "running": false})
		return
	}
	variant := AssignVariant(exp, userID)
	// Fire-and-forget exposure log.
	go func() {
		_ = appendABEvent(ABEvent{Key: key, Variant: variant, UserID: userID, Kind: "exposure", At: time.Now().UTC()})
	}()
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"variant": variant,
		"running": true,
	})
}

func (s *HTTPServer) handleABEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var e ABEvent
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if e.Key == "" || e.Kind == "" {
		jsonError(w, http.StatusBadRequest, "key and kind required")
		return
	}
	e.At = time.Now().UTC()
	if err := appendABEvent(e); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleABResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		jsonError(w, http.StatusBadRequest, "key required")
		return
	}
	// Walk the JSONL log and tally exposure + conversion counts
	// per variant. Good enough for MAUs in the thousands; we can
	// add a materialised view later if a solo dev actually hits
	// scale.
	p, _ := abEventsFile()
	data, err := os.ReadFile(p)
	if err != nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "results": map[string]interface{}{}})
		return
	}
	type bucket struct {
		Exposures   int `json:"exposures"`
		Conversions int `json:"conversions"`
	}
	results := map[string]*bucket{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var e ABEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e.Key != key {
			continue
		}
		b := results[e.Variant]
		if b == nil {
			b = &bucket{}
			results[e.Variant] = b
		}
		switch e.Kind {
		case "exposure":
			b.Exposures++
		case "conversion":
			b.Conversions++
		}
	}
	// Compute conversion rate per variant.
	out := map[string]interface{}{}
	for variant, b := range results {
		rate := 0.0
		if b.Exposures > 0 {
			rate = float64(b.Conversions) / float64(b.Exposures)
		}
		out[variant] = map[string]interface{}{
			"exposures":      b.Exposures,
			"conversions":    b.Conversions,
			"conversionRate": rate,
		}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "results": out})
}
