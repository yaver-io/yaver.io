package main

// collection_store.go — local-first store for user-directed data collection.
// Sources, vantages, runs, observations, and per-(source,vantage) health. This
// is the persistence layer for multi-vantage collection
// (docs/user-directed-data-collection-runtimes.md, Generic Data Model).
//
// PRIVACY CONTRACT — this store is LOCAL ONLY. Collected observations, raw
// values, and egress IPs NEVER go to Convex (forbidden by the privacy contract;
// see convex_privacy_test.go). It persists to ~/.yaver/collection/store.json on
// the user's own machine, exactly like the scheduler and vault.
//
// Egress identity is recorded as VANTAGE metadata (which IP/geo an observation
// came from) — it is provenance, never a normalized data field. collection_observe
// refuses rows that try to smuggle an egress/client IP into the data itself.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// CollectionSource is a thing we collect from (a page, API, app, manual feed).
type CollectionSource struct {
	SourceID    string `json:"sourceId"`
	Name        string `json:"name"`
	Kind        string `json:"kind"` // public_web | official_api | app | manual
	BaseURL     string `json:"baseUrl,omitempty"`
	AccessState string `json:"accessState"` // public_allowed | official_api | manual_required | ...
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// CollectionVantage is the (runtime + egress identity) an observation came from.
type CollectionVantage struct {
	VantageID     string `json:"vantageId"`
	RuntimeID     string `json:"runtimeId"`
	EgressPolicy  string `json:"egressPolicy"` // machine_native | peer_egress | user_proxy
	EgressIP      string `json:"egressIp,omitempty"`
	EgressGeo     string `json:"egressGeo,omitempty"`     // coarse region: eu|us|...
	EgressCountry string `json:"egressCountry,omitempty"` // ISO-2
	EgressASN     string `json:"egressAsn,omitempty"`
	ViaPeer       string `json:"viaPeer,omitempty"` // device id when egress is routed via a peer
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

// CollectionRun is one execution of a collector against a source via a vantage.
type CollectionRun struct {
	RunID         string `json:"runId"`
	SourceID      string `json:"sourceId"`
	VantageID     string `json:"vantageId"`
	CollectorType string `json:"collectorType"`
	Status        string `json:"status"` // ok | no_data | blocked_geo | blocked_ip | rate_limited | ...
	StartedAt     int64  `json:"startedAt"`
	FinishedAt    int64  `json:"finishedAt,omitempty"`
	RowsExtracted int    `json:"rowsExtracted"`
	EgressIPUsed  string `json:"egressIpUsed,omitempty"`
	EgressGeoUsed string `json:"egressGeoUsed,omitempty"`
	BlockKind     string `json:"blockKind,omitempty"` // "" | geo | ip | rate_limit
	ErrorCode     string `json:"errorCode,omitempty"`
	ErrorMessage  string `json:"errorMessage,omitempty"`
}

// CollectionObservation is one normalized row, tagged with its vantage so geo/IP
// provenance survives normalization. Fields holds the domain data ONLY — never
// the egress IP (enforced on write).
type CollectionObservation struct {
	ObservationID string                 `json:"observationId"`
	SourceID      string                 `json:"sourceId"`
	VantageID     string                 `json:"vantageId"`
	RunID         string                 `json:"runId,omitempty"`
	Dataset       string                 `json:"dataset"`
	At            int64                  `json:"at"`
	Fields        map[string]interface{} `json:"fields"`
}

type blockEvent struct {
	At   int64  `json:"at"`
	Kind string `json:"kind"` // geo | ip | rate_limit
}

// CollectionSourceHealth is keyed by (source, vantage): a source can be healthy
// from one vantage and blocked_geo from another.
type CollectionSourceHealth struct {
	SourceID    string       `json:"sourceId"`
	VantageID   string       `json:"vantageId"`
	State       string       `json:"state"` // healthy | blocked_geo | blocked_ip | rate_limited | stale | ...
	LastOkAt    int64        `json:"lastOkAt,omitempty"`
	LastErrorAt int64        `json:"lastErrorAt,omitempty"`
	LastRows    int          `json:"lastRows"`
	BlockEvents []blockEvent `json:"blockEvents,omitempty"`
	UpdatedAt   int64        `json:"updatedAt"`
}

// blockCounts24h returns geo/ip/rate counts within the trailing 24h.
func (h *CollectionSourceHealth) blockCounts24h(now int64) (geo, ip, rate int) {
	cutoff := now - 24*60*60*1000
	for _, e := range h.BlockEvents {
		if e.At < cutoff {
			continue
		}
		switch e.Kind {
		case "geo":
			geo++
		case "ip":
			ip++
		case "rate_limit":
			rate++
		}
	}
	return
}

type collectionStoreT struct {
	mu     sync.Mutex
	path   string // "" => in-memory only (tests)
	loaded bool
	seq    int64

	Sources      map[string]*CollectionSource       `json:"sources"`
	Vantages     map[string]*CollectionVantage      `json:"vantages"`
	Runs         map[string]*CollectionRun          `json:"runs"`
	Observations []*CollectionObservation           `json:"observations"`
	Health       map[string]*CollectionSourceHealth `json:"health"`
}

var collStore = &collectionStoreT{}

func (s *collectionStoreT) ensureLoaded() {
	if s.loaded {
		return
	}
	if s.Sources == nil {
		s.Sources = map[string]*CollectionSource{}
	}
	if s.Vantages == nil {
		s.Vantages = map[string]*CollectionVantage{}
	}
	if s.Runs == nil {
		s.Runs = map[string]*CollectionRun{}
	}
	if s.Health == nil {
		s.Health = map[string]*CollectionSourceHealth{}
	}
	if s.path == "" {
		// Resolve the default path lazily; tests set s.path directly.
		if dir, err := ConfigDir(); err == nil {
			cdir := filepath.Join(dir, "collection")
			if mkErr := os.MkdirAll(cdir, 0o700); mkErr == nil {
				s.path = filepath.Join(cdir, "store.json")
			}
		}
	}
	if s.path != "" {
		if data, err := os.ReadFile(s.path); err == nil {
			_ = json.Unmarshal(data, s) // best-effort; partial store is fine
		}
	}
	s.loaded = true
}

func (s *collectionStoreT) save() {
	if s.path == "" {
		return // in-memory (tests)
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(s.path, data, 0o600)
}

func (s *collectionStoreT) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), s.seq)
}

func healthKey(sourceID, vantageID string) string { return sourceID + "|" + vantageID }

// reservedObservationFieldKeys are forbidden in a normalized row: egress/client
// IP is vantage provenance, not data. Keeps IPs out of the dataset (privacy).
var reservedObservationFieldKeys = map[string]bool{
	"egressip": true, "egress_ip": true, "egress": true,
	"clientip": true, "client_ip": true, "sourceip": true, "source_ip": true,
}

func observationFieldsAllowed(fields map[string]interface{}) (string, bool) {
	for k := range fields {
		if reservedObservationFieldKeys[strings.ToLower(strings.TrimSpace(k))] {
			return k, false
		}
	}
	return "", true
}

// --- mutations --------------------------------------------------------------

func (s *collectionStoreT) upsertSource(src CollectionSource) *CollectionSource {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	now := time.Now().UnixMilli()
	if src.SourceID == "" {
		src.SourceID = s.nextID("src")
	}
	existing, ok := s.Sources[src.SourceID]
	if ok {
		if src.Name != "" {
			existing.Name = src.Name
		}
		if src.Kind != "" {
			existing.Kind = src.Kind
		}
		if src.BaseURL != "" {
			existing.BaseURL = src.BaseURL
		}
		if src.AccessState != "" {
			existing.AccessState = src.AccessState
		}
		existing.UpdatedAt = now
		s.save()
		return existing
	}
	src.CreatedAt = now
	src.UpdatedAt = now
	if src.AccessState == "" {
		src.AccessState = "public_allowed"
	}
	s.Sources[src.SourceID] = &src
	s.save()
	return &src
}

func (s *collectionStoreT) upsertVantage(v CollectionVantage) *CollectionVantage {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	now := time.Now().UnixMilli()
	if v.VantageID == "" {
		v.VantageID = s.nextID("van")
	}
	if v.EgressPolicy == "" {
		v.EgressPolicy = "machine_native"
	}
	if existing, ok := s.Vantages[v.VantageID]; ok {
		*existing = v
		existing.UpdatedAt = now
		if existing.CreatedAt == 0 {
			existing.CreatedAt = now
		}
		s.save()
		return existing
	}
	v.CreatedAt = now
	v.UpdatedAt = now
	s.Vantages[v.VantageID] = &v
	s.save()
	return &v
}

func (s *collectionStoreT) recordRun(run CollectionRun) *CollectionRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	now := time.Now().UnixMilli()
	if run.RunID == "" {
		run.RunID = s.nextID("run")
	}
	if run.StartedAt == 0 {
		run.StartedAt = now
	}
	if run.FinishedAt == 0 {
		run.FinishedAt = now
	}
	s.Runs[run.RunID] = &run

	// Update per-(source,vantage) health.
	if run.SourceID != "" && run.VantageID != "" {
		k := healthKey(run.SourceID, run.VantageID)
		h, ok := s.Health[k]
		if !ok {
			h = &CollectionSourceHealth{SourceID: run.SourceID, VantageID: run.VantageID}
			s.Health[k] = h
		}
		h.LastRows = run.RowsExtracted
		h.UpdatedAt = now
		switch {
		case run.BlockKind == "geo" || run.Status == "blocked_geo":
			h.State = "blocked_geo"
			h.LastErrorAt = now
			h.BlockEvents = appendBlockEvent(h.BlockEvents, blockEvent{At: now, Kind: "geo"})
		case run.BlockKind == "ip" || run.Status == "blocked_ip":
			h.State = "blocked_ip"
			h.LastErrorAt = now
			h.BlockEvents = appendBlockEvent(h.BlockEvents, blockEvent{At: now, Kind: "ip"})
		case run.BlockKind == "rate_limit" || run.Status == "rate_limited":
			h.State = "rate_limited"
			h.LastErrorAt = now
			h.BlockEvents = appendBlockEvent(h.BlockEvents, blockEvent{At: now, Kind: "rate_limit"})
		case run.Status == "ok":
			h.State = "healthy"
			h.LastOkAt = now
		case run.ErrorCode != "" || run.Status == "parse_error":
			h.State = "stale"
			h.LastErrorAt = now
		default:
			if h.State == "" {
				h.State = "healthy"
			}
		}
	}
	s.save()
	return &run
}

func appendBlockEvent(events []blockEvent, e blockEvent) []blockEvent {
	events = append(events, e)
	if len(events) > 100 {
		events = events[len(events)-100:]
	}
	return events
}

func (s *collectionStoreT) addObservation(obs CollectionObservation) (*CollectionObservation, error) {
	if bad, ok := observationFieldsAllowed(obs.Fields); !ok {
		return nil, fmt.Errorf("field %q is not allowed in a normalized row: egress/client IP is vantage provenance, not data", bad)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	if obs.ObservationID == "" {
		obs.ObservationID = s.nextID("obs")
	}
	if obs.At == 0 {
		obs.At = time.Now().UnixMilli()
	}
	if obs.Dataset == "" {
		obs.Dataset = "default"
	}
	s.Observations = append(s.Observations, &obs)
	s.save()
	return &obs, nil
}

// --- queries ----------------------------------------------------------------

func (s *collectionStoreT) queryObservations(dataset, sourceID, vantageID string, limit int) []*CollectionObservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	var out []*CollectionObservation
	for i := len(s.Observations) - 1; i >= 0; i-- {
		o := s.Observations[i]
		if dataset != "" && o.Dataset != dataset {
			continue
		}
		if sourceID != "" && o.SourceID != sourceID {
			continue
		}
		if vantageID != "" && o.VantageID != vantageID {
			continue
		}
		out = append(out, o)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func (s *collectionStoreT) healthRows(sourceID string) []map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	now := time.Now().UnixMilli()
	var out []map[string]interface{}
	for _, h := range s.Health {
		if sourceID != "" && h.SourceID != sourceID {
			continue
		}
		geo, ip, rate := h.blockCounts24h(now)
		out = append(out, map[string]interface{}{
			"sourceId":          h.SourceID,
			"vantageId":         h.VantageID,
			"state":             h.State,
			"lastOkAt":          h.LastOkAt,
			"lastErrorAt":       h.LastErrorAt,
			"lastRows":          h.LastRows,
			"geoBlockCount24h":  geo,
			"ipBlockCount24h":   ip,
			"rateLimitCount24h": rate,
			"updatedAt":         h.UpdatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return fmt.Sprint(out[i]["vantageId"]) < fmt.Sprint(out[j]["vantageId"])
	})
	return out
}

// vantageCompare builds the cross-vantage diff for a source/dataset: for each
// vantage, the latest observation's fields, plus the vantage's egress/geo and
// current health state (so a blocked vantage shows as blocked, not missing).
// This is the multi-vantage payoff and lives in core (domain-agnostic).
func (s *collectionStoreT) vantageCompare(sourceID, dataset string) map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()

	latest := map[string]*CollectionObservation{} // vantageID -> latest obs
	for _, o := range s.Observations {
		if o.SourceID != sourceID {
			continue
		}
		if dataset != "" && o.Dataset != dataset {
			continue
		}
		if cur, ok := latest[o.VantageID]; !ok || o.At > cur.At {
			latest[o.VantageID] = o
		}
	}

	// Vantage set: any vantage with an observation OR a health row for this source.
	vantageSet := map[string]bool{}
	for vid := range latest {
		vantageSet[vid] = true
	}
	for _, h := range s.Health {
		if h.SourceID == sourceID {
			vantageSet[h.VantageID] = true
		}
	}

	vantages := make([]string, 0, len(vantageSet))
	for vid := range vantageSet {
		vantages = append(vantages, vid)
	}
	sort.Strings(vantages)

	// Collect the union of fields across vantages.
	fieldSet := map[string]bool{}
	for _, o := range latest {
		for k := range o.Fields {
			fieldSet[k] = true
		}
	}
	fields := make([]string, 0, len(fieldSet))
	for k := range fieldSet {
		fields = append(fields, k)
	}
	sort.Strings(fields)

	rows := make([]map[string]interface{}, 0, len(vantages))
	for _, vid := range vantages {
		entry := map[string]interface{}{"vantageId": vid}
		if v, ok := s.Vantages[vid]; ok {
			entry["egressIp"] = v.EgressIP
			entry["egressGeo"] = v.EgressGeo
			entry["egressCountry"] = v.EgressCountry
			entry["egressPolicy"] = v.EgressPolicy
		}
		if h, ok := s.Health[healthKey(sourceID, vid)]; ok {
			entry["state"] = h.State
		}
		vals := map[string]interface{}{}
		if o, ok := latest[vid]; ok {
			for _, f := range fields {
				if val, has := o.Fields[f]; has {
					vals[f] = val
				}
			}
			entry["at"] = o.At
		}
		entry["values"] = vals
		rows = append(rows, entry)
	}

	return map[string]interface{}{
		"sourceId": sourceID,
		"dataset":  dataset,
		"fields":   fields,
		"vantages": rows,
	}
}

// resetCollectionStoreForTest replaces the global store with a fresh one. When
// dir != "" it persists there; when "" it stays in-memory. Test-only.
func resetCollectionStoreForTest(dir string) {
	path := ""
	if dir != "" {
		path = filepath.Join(dir, "store.json")
	}
	collStore = &collectionStoreT{
		path:     path,
		loaded:   true,
		Sources:  map[string]*CollectionSource{},
		Vantages: map[string]*CollectionVantage{},
		Runs:     map[string]*CollectionRun{},
		Health:   map[string]*CollectionSourceHealth{},
	}
}
