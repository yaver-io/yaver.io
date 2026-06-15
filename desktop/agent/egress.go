package main

// egress.go — egress identity: the (IP, geo, ASN) a remote source actually
// sees for this runtime. This is the vantage primitive for multi-vantage
// collection: the planner selects vantages from each runtime's advertised
// egress, and a multi-vantage run records which egress every observation came
// from. See docs/user-directed-data-collection-runtimes.md (Multi-Vantage /
// Egress).
//
// The IP reuses detectAutoPublicIP (cached, best-effort). Geo/ASN is a separate
// best-effort, longer-cached lookup keyed by the detected IP — an egress with a
// known IP but unknown geo is still a usable vantage. The whole feature honors
// the same disable_auto_public_ip opt-out: a user who does not want their box's
// IP probed gets a {source:"disabled"} identity with no network call.
//
// Privacy: the egress IP is the IP of the box the user owns, the same value
// auto_public_ip.go already advertises on the device row. It is vantage
// provenance, never a normalized data field — keep it out of collected rows
// (enforced by the collection layer, not here).

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// egressGeoCacheTTL is long because an IP's geo/ASN rarely changes; the cache is
// additionally invalidated whenever the detected egress IP changes.
const egressGeoCacheTTL = 6 * time.Hour

// EgressIdentity is what a source observes for a runtime's outbound traffic.
type EgressIdentity struct {
	IP         string `json:"ip,omitempty"`
	Country    string `json:"country,omitempty"`    // ISO-3166 alpha-2, e.g. "DE"
	Region     string `json:"region,omitempty"`     // coarse bucket: eu|us|na|ap|sa|af|oc
	RegionName string `json:"regionName,omitempty"` // provider region/state name
	City       string `json:"city,omitempty"`
	ASN        string `json:"asn,omitempty"` // e.g. "AS24940"
	Org        string `json:"org,omitempty"`

	// StableKnown is false when we cannot determine IP stability; callers
	// should then assume the egress could change (residential/dynamic).
	Stable      bool `json:"stable"`
	StableKnown bool `json:"stableKnown"`
	// ViaProxy is true when this runtime's native egress is itself already
	// routed through a proxy/peer. Per-session browser proxies are separate.
	ViaProxy bool `json:"viaProxy"`

	Source     string `json:"source"`              // probe|cache|disabled|unavailable
	GeoSource  string `json:"geoSource,omitempty"` // which geo service answered
	DetectedAt string `json:"detectedAt,omitempty"`
}

type egressCacheState struct {
	mu sync.Mutex
	id EgressIdentity
	ts time.Time
}

var egressCache egressCacheState

// cachedEgressRegion returns this runtime's COARSE egress region (eu|us|ap|...)
// ONLY if it is already cached — it never triggers a network probe, so it is
// safe on the hot heartbeat path. Returns "" when unknown or opted out. Coarse
// region only; the egress IP itself is NEVER published to Convex (privacy).
func cachedEgressRegion() string {
	egressCache.mu.Lock()
	defer egressCache.mu.Unlock()
	if egressCache.id.Source == "disabled" {
		return ""
	}
	return egressCache.id.Region
}

// resetEgressCache clears the in-process egress identity cache. Test-only.
func resetEgressCache() {
	egressCache.mu.Lock()
	egressCache.id = EgressIdentity{}
	egressCache.ts = time.Time{}
	egressCache.mu.Unlock()
}

// resolveEgressGeo is a function variable so tests can stub geo resolution
// without hitting the network. It returns partial fields and a source label;
// failure is non-fatal (the IP-only identity is still useful).
var resolveEgressGeo = httpResolveEgressGeo

// detectEgressIdentity returns this runtime's egress identity, cached. With
// refresh=true the IP and geo caches are busted. Honors the auto-public-IP
// opt-out: when disabled, returns {source:"disabled"} with no network call.
func detectEgressIdentity(ctx context.Context, cfg *Config, refresh bool) EgressIdentity {
	if cfg != nil && cfg.DisableAutoPublicIP {
		return EgressIdentity{Source: "disabled", DetectedAt: time.Now().UTC().Format(time.RFC3339)}
	}

	if refresh {
		resetAutoPublicIPCache()
	}

	egressCache.mu.Lock()
	cached := egressCache.id
	fresh := !refresh && cached.IP != "" && time.Since(egressCache.ts) < egressGeoCacheTTL
	egressCache.mu.Unlock()

	ip := detectAutoPublicIP(ctx)
	if ip == "" {
		// No reachable egress IP this round. Surface the last good identity if
		// we have one, otherwise an explicit "unavailable".
		if cached.IP != "" {
			cached.Source = "cache"
			return cached
		}
		return EgressIdentity{Source: "unavailable", DetectedAt: time.Now().UTC().Format(time.RFC3339)}
	}

	// Reuse the cached identity only if it is for the SAME IP and still fresh —
	// a dynamic-IP box that reconnected is a different vantage.
	if fresh && cached.IP == ip {
		cached.Source = "cache"
		return cached
	}

	id := EgressIdentity{
		IP:         ip,
		Source:     "probe",
		DetectedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if geo, ok := resolveEgressGeo(ctx, ip); ok {
		id.Country = geo.Country
		id.Region = geo.Region
		id.RegionName = geo.RegionName
		id.City = geo.City
		id.ASN = geo.ASN
		id.Org = geo.Org
		id.GeoSource = geo.GeoSource
		if geo.StableKnown {
			id.Stable = geo.Stable
			id.StableKnown = true
		}
	}

	egressCache.mu.Lock()
	egressCache.id = id
	egressCache.ts = time.Now()
	egressCache.mu.Unlock()

	return id
}

// coarseRegion buckets a country/continent into the same coarse labels Yaver
// uses elsewhere (cloudMachines region is "eu"/"us"). US is special-cased; the
// rest fall back to a lowercased continent code.
func coarseRegion(countryCode, continentCode string) string {
	switch strings.ToUpper(strings.TrimSpace(countryCode)) {
	case "US":
		return "us"
	}
	switch strings.ToUpper(strings.TrimSpace(continentCode)) {
	case "EU":
		return "eu"
	case "NA":
		return "na"
	case "AS":
		return "ap"
	case "SA":
		return "sa"
	case "AF":
		return "af"
	case "OC":
		return "oc"
	}
	return ""
}

// parseASN extracts the "ASxxxx" token from an ip-api "as" string like
// "AS24940 Hetzner Online GmbH". Returns "" when no AS token is present.
func parseASN(asField string) string {
	for _, tok := range strings.Fields(asField) {
		up := strings.ToUpper(tok)
		if strings.HasPrefix(up, "AS") && len(up) > 2 {
			if _, err := atoiStrict(up[2:]); err == nil {
				return up
			}
		}
	}
	return ""
}

// atoiStrict reports whether s is all digits (and parses it). Avoids importing
// strconv just for a digit check while keeping parseASN honest about "ASxxxx".
func atoiStrict(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, errNotNumber
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errNotNumber
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

var errNotNumber = &egressErr{"not a number"}

type egressErr struct{ s string }

func (e *egressErr) Error() string { return e.s }

// httpResolveEgressGeo geolocates an IP via the keyless ip-api.com endpoint.
// Best-effort: returns ok=false on any error so the caller keeps the IP-only
// identity. ip-api free tier is HTTP-only; we send only the user's own public
// IP (low sensitivity, and the user explicitly asked for geo).
func httpResolveEgressGeo(ctx context.Context, ip string) (EgressIdentity, bool) {
	const endpoint = "http://ip-api.com/json/"
	const fields = "?fields=status,country,countryCode,region,regionName,city,continentCode,as,org,hosting,query"

	reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint+ip+fields, nil)
	if err != nil {
		return EgressIdentity{}, false
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return EgressIdentity{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return EgressIdentity{}, false
	}

	var body struct {
		Status        string `json:"status"`
		Country       string `json:"country"`
		CountryCode   string `json:"countryCode"`
		Region        string `json:"region"`
		RegionName    string `json:"regionName"`
		City          string `json:"city"`
		ContinentCode string `json:"continentCode"`
		AS            string `json:"as"`
		Org           string `json:"org"`
		Hosting       bool   `json:"hosting"`
		Query         string `json:"query"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return EgressIdentity{}, false
	}
	if body.Status != "success" {
		return EgressIdentity{}, false
	}

	id := EgressIdentity{
		Country:    strings.ToUpper(body.CountryCode),
		Region:     coarseRegion(body.CountryCode, body.ContinentCode),
		RegionName: body.RegionName,
		City:       body.City,
		ASN:        parseASN(body.AS),
		Org:        body.Org,
		GeoSource:  "ip-api.com",
	}
	// A datacenter/hosting egress (managed cloud, VPS) is a stable vantage; a
	// non-hosting (residential/mobile) egress should be treated as dynamic.
	id.Stable = body.Hosting
	id.StableKnown = true
	return id, true
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "runtime_egress",
		Description: "Report this runtime's egress identity — the (IP, geo, ASN) a remote " +
			"source actually sees for its outbound traffic. This is the vantage primitive: " +
			"the collection planner selects vantages from each runtime's egress, and multi-vantage " +
			"runs record which egress each observation came from. Route to a peer via the machine " +
			"param to read THAT runtime's egress. Owner-only; the IP is vantage provenance, never a " +
			"collected data field. Honors the disable_auto_public_ip opt-out.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"refresh": map[string]interface{}{"type": "boolean", "description": "Bypass the cache and re-probe IP + geo (default false)."},
		}),
		Handler:    runtimeEgressHandler,
		AllowGuest: false,
	})
}

func runtimeEgressHandler(c OpsContext, payload json.RawMessage) OpsResult {
	var args struct {
		Refresh bool `json:"refresh"`
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &args); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}

	// Config is loaded fresh (HTTPServer holds no cached *Config); we only need
	// the disable_auto_public_ip opt-out flag. A load error is non-fatal —
	// detectEgressIdentity treats a nil cfg as "opt-out not set".
	cfg, _ := LoadConfig()

	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	id := detectEgressIdentity(ctx, cfg, args.Refresh)
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"egress": id,
	}}
}
