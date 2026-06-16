package main

// ev_charging.go — generic, public-safe EV-charging *discovery* for Yaver.
// Exposed as three MCP tools so any AI agent connected to Yaver can find
// charging stations, list networks, and reference connector types without
// each agent shipping its own integration:
//
//   - ev_charging        — nearby stations from OpenChargeMap (live)
//   - ev_networks        — curated network directory by country (static)
//   - ev_connector_types — connector taxonomy + the vehicle-preset table (static)
//
// DISCOVERY ONLY. This file knows how to *find* charging stations and
// reason about connectors; it does NOT start, stop, or otherwise control a
// charge session. Charge *control* is a proprietary, protocol-specific
// concern (OCPP and friends) that lives behind the generic ChargeController
// seam in charge_controller.go and is registered from a PRIVATE overlay —
// never in this open-source repo.
//
// Policy Guard (CLAUDE.md): the live path identifies honestly with a normal
// client + contact User-Agent, never spoofs a browser to defeat bot
// detection, and on a 403/429/451 it backs off and returns a structured
// "blocked" result instead of retry-spamming or rotating identity. A block
// is a "no", not a puzzle.

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// ── OpenChargeMap live discovery ─────────────────────────────────────────────

// openChargeMapEndpoint is the public POI search endpoint. It is keyless-
// friendly: an API key raises rate limits but is not required for modest
// interactive use. We read the key from the environment / vault if present
// and otherwise hit the public endpoint.
const openChargeMapEndpoint = "https://api.openchargemap.io/v3/poi"

// evContactUA is the honest client identity we send. Yaver identifies itself
// and gives a contact URL so the operator can reach us — the opposite of
// browser-spoofing to defeat bot detection.
const evContactUA = "Yaver/1.0 (+https://yaver.io; EV charging discovery)"

// EVConnector is one physical connector on a station, normalized across
// providers to the lowest common denominator an AI consumer needs.
type EVConnector struct {
	Type    string  `json:"type"`               // human label, e.g. "CCS2 (DC)"
	TypeID  string  `json:"type_id,omitempty"`  // taxonomy id usable as connector_type filter
	PowerKW float64 `json:"power_kw,omitempty"` // max power in kW (0 = unknown)
	Current string  `json:"current,omitempty"`  // AC / DC hint
	Count   int     `json:"count,omitempty"`    // how many of this connector
}

// EVStation is a single charging location.
type EVStation struct {
	Name       string        `json:"name"`
	Operator   string        `json:"operator,omitempty"`
	Network    string        `json:"network,omitempty"`
	Address    string        `json:"address,omitempty"`
	Town       string        `json:"town,omitempty"`
	Country    string        `json:"country,omitempty"`
	Lat        float64       `json:"lat"`
	Lon        float64       `json:"lon"`
	DistanceKM float64       `json:"distance_km,omitempty"`
	Connectors []EVConnector `json:"connectors,omitempty"`
	MaxPowerKW float64       `json:"max_power_kw,omitempty"`
	StatusHint string        `json:"status_hint,omitempty"`
	DeepLink   string        `json:"deep_link,omitempty"` // Google Maps directions URL
	Source     string        `json:"source,omitempty"`    // "openchargemap"
}

// mcpEVCharging finds EV charging stations near (lat, lon) via OpenChargeMap.
// radius is in km (default 10); connectorType / network / country / minPowerKW
// are optional filters applied client-side after the fetch (OpenChargeMap's
// server-side filters are coarse, so we post-filter for predictable results).
func mcpEVCharging(lat, lon float64, radius int, connectorType, network, country string, minPowerKW int) interface{} {
	if lat == 0 && lon == 0 {
		return map[string]interface{}{"error": "lat and lon are required"}
	}
	if radius <= 0 {
		radius = 10
	}
	if radius > 200 {
		radius = 200 // OpenChargeMap practical cap; keep requests modest
	}

	q := url.Values{}
	q.Set("output", "json")
	q.Set("latitude", fmt.Sprintf("%.6f", lat))
	q.Set("longitude", fmt.Sprintf("%.6f", lon))
	q.Set("distance", fmt.Sprintf("%d", radius))
	q.Set("distanceunit", "KM")
	q.Set("maxresults", "60")
	q.Set("compact", "true")
	q.Set("verbose", "false")
	if cc := openChargeMapCountryCode(country); cc != "" {
		q.Set("countrycode", cc)
	}
	if key := openChargeMapKey(); key != "" {
		q.Set("key", key)
	}
	endpoint := openChargeMapEndpoint + "?" + q.Encode()

	req, _ := http.NewRequest("GET", endpoint, nil)
	req.Header.Set("User-Agent", evContactUA)
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("openchargemap: %v", err)}
	}
	defer resp.Body.Close()

	// Policy Guard: a block is a "no". Back off and surface it; do not retry.
	if resp.StatusCode == 403 || resp.StatusCode == 429 || resp.StatusCode == 451 {
		return map[string]interface{}{
			"blocked":     true,
			"status_code": resp.StatusCode,
			"detail": "OpenChargeMap returned a block (403/429/451). Backing off — " +
				"not retrying or rotating identity. Set OPENCHARGEMAP_API_KEY (free at " +
				"openchargemap.io) to raise rate limits, then try again later.",
			"source": "openchargemap",
		}
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return map[string]interface{}{
			"error":  fmt.Sprintf("openchargemap: status %d", resp.StatusCode),
			"detail": strings.TrimSpace(string(body)),
		}
	}

	var raw []ocmPOI
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("openchargemap decode: %v", err)}
	}

	wantConn := strings.ToLower(strings.TrimSpace(connectorType))
	wantNet := strings.ToLower(strings.TrimSpace(network))
	stations := make([]EVStation, 0, len(raw))
	for _, p := range raw {
		st := p.toStation(lat, lon)
		if minPowerKW > 0 && st.MaxPowerKW > 0 && st.MaxPowerKW < float64(minPowerKW) {
			continue
		}
		if wantNet != "" && !stationMatchesNetwork(st, wantNet) {
			continue
		}
		if wantConn != "" && !stationHasConnector(st, wantConn) {
			continue
		}
		stations = append(stations, st)
	}
	sort.SliceStable(stations, func(i, j int) bool {
		return stations[i].DistanceKM < stations[j].DistanceKM
	})

	return map[string]interface{}{
		"source":    "openchargemap",
		"keyless":   openChargeMapKey() == "",
		"count":     len(stations),
		"radius_km": radius,
		"stations":  stations,
		"note":      "Discovery only. Charge start/stop is not available from this tool (see ChargeController seam).",
	}
}

// ── OpenChargeMap raw POI shape ──────────────────────────────────────────────

type ocmPOI struct {
	AddressInfo struct {
		Title        string  `json:"Title"`
		AddressLine1 string  `json:"AddressLine1"`
		Town         string  `json:"Town"`
		Latitude     float64 `json:"Latitude"`
		Longitude    float64 `json:"Longitude"`
		Distance     float64 `json:"Distance"`
		Country      struct {
			Title   string `json:"Title"`
			ISOCode string `json:"ISOCode"`
		} `json:"Country"`
	} `json:"AddressInfo"`
	OperatorInfo *struct {
		Title string `json:"Title"`
	} `json:"OperatorInfo"`
	StatusType *struct {
		Title         string `json:"Title"`
		IsOperational *bool  `json:"IsOperational"`
	} `json:"StatusType"`
	Connections []struct {
		ConnectionType *struct {
			Title string `json:"Title"`
		} `json:"ConnectionType"`
		PowerKW     float64 `json:"PowerKW"`
		Quantity    int     `json:"Quantity"`
		CurrentType *struct {
			Title string `json:"Title"`
		} `json:"CurrentType"`
	} `json:"Connections"`
}

func (p ocmPOI) toStation(originLat, originLon float64) EVStation {
	a := p.AddressInfo
	st := EVStation{
		Name:    strings.TrimSpace(a.Title),
		Address: strings.TrimSpace(a.AddressLine1),
		Town:    strings.TrimSpace(a.Town),
		Country: strings.TrimSpace(a.Country.Title),
		Lat:     a.Latitude,
		Lon:     a.Longitude,
		Source:  "openchargemap",
	}
	if p.OperatorInfo != nil {
		st.Operator = strings.TrimSpace(p.OperatorInfo.Title)
		st.Network = st.Operator
	}
	if p.StatusType != nil {
		st.StatusHint = strings.TrimSpace(p.StatusType.Title)
		if p.StatusType.IsOperational != nil && *p.StatusType.IsOperational {
			st.StatusHint = "operational"
		}
	}
	if a.Distance > 0 {
		st.DistanceKM = round1(a.Distance)
	} else {
		st.DistanceKM = round1(haversineKM(originLat, originLon, a.Latitude, a.Longitude))
	}
	for _, c := range p.Connections {
		conn := EVConnector{PowerKW: c.PowerKW, Count: c.Quantity}
		if c.ConnectionType != nil {
			conn.Type = strings.TrimSpace(c.ConnectionType.Title)
			conn.TypeID = normalizeConnectorID(conn.Type)
		}
		if c.CurrentType != nil {
			conn.Current = strings.TrimSpace(c.CurrentType.Title)
		}
		if conn.PowerKW > st.MaxPowerKW {
			st.MaxPowerKW = conn.PowerKW
		}
		st.Connectors = append(st.Connectors, conn)
	}
	st.DeepLink = fmt.Sprintf("https://www.google.com/maps/dir/?api=1&destination=%.6f,%.6f", st.Lat, st.Lon)
	return st
}

// ── filters / matching ───────────────────────────────────────────────────────

func stationMatchesNetwork(st EVStation, want string) bool {
	hay := strings.ToLower(st.Network + " " + st.Operator)
	// Map a few known network ids to substrings actually present in operator names.
	if alts, ok := networkAliases[want]; ok {
		for _, a := range alts {
			if strings.Contains(hay, a) {
				return true
			}
		}
	}
	return strings.Contains(hay, want)
}

func stationHasConnector(st EVStation, want string) bool {
	for _, c := range st.Connectors {
		if c.TypeID == want || strings.Contains(strings.ToLower(c.Type), want) {
			return true
		}
	}
	return false
}

// networkAliases maps a filter token to substrings that appear in real
// operator names across regions. Keeps `network: tesla` matching
// "Tesla Supercharger" etc.
var networkAliases = map[string][]string{
	"tesla":       {"tesla", "supercharger"},
	"ionity":      {"ionity"},
	"chargepoint": {"chargepoint"},
	"evgo":        {"evgo"},
	"shell":       {"shell", "recharge", "newmotion"},
	"bp":          {"bp pulse", "bp ", "aral"},
	"fastned":     {"fastned"},
	"trugo":       {"trugo", "togg"},
	"zes":         {"zes"},
	"esarj":       {"eşarj", "esarj", "enerjisa"},
	"sharz":       {"sharz"},
	"voltrun":     {"voltrun"},
}

// normalizeConnectorID maps an OpenChargeMap connection-type title to one of
// our taxonomy ids (see mcpEVConnectorTypes). Unknown titles fall through to
// a slugged form so callers still get a stable token.
func normalizeConnectorID(title string) string {
	t := strings.ToLower(title)
	switch {
	case strings.Contains(t, "ccs") && strings.Contains(t, "type 1"):
		return "ccs1"
	case strings.Contains(t, "ccs") && strings.Contains(t, "combo 1"):
		return "ccs1"
	case strings.Contains(t, "ccs") || strings.Contains(t, "combo 2"):
		return "ccs2"
	case strings.Contains(t, "chademo"):
		return "chademo"
	case strings.Contains(t, "nacs"), strings.Contains(t, "tesla"):
		return "nacs"
	case strings.Contains(t, "type 2"), strings.Contains(t, "mennekes"):
		return "type2"
	case strings.Contains(t, "type 1"), strings.Contains(t, "j1772"):
		return "type1"
	default:
		return strings.ReplaceAll(strings.TrimSpace(t), " ", "_")
	}
}

// ── credentials / country mapping ────────────────────────────────────────────

// openChargeMapKey reads an optional OpenChargeMap API key. Env wins; the
// vault is consulted as a fallback so a daemon-stored key works without
// exporting it. Empty string => keyless public endpoint.
func openChargeMapKey() string {
	if k := strings.TrimSpace(os.Getenv("OPENCHARGEMAP_API_KEY")); k != "" {
		return k
	}
	if vs, err := openVaultOptional(); err == nil && vs != nil {
		if e, gerr := vs.Get("ev", "OPENCHARGEMAP_API_KEY"); gerr == nil && e != nil {
			return strings.TrimSpace(e.Value)
		}
	}
	return ""
}

// openChargeMapCountryCode normalizes a country name/code to an ISO 3166-1
// alpha-2 code OpenChargeMap accepts. Returns "" for unknown/empty input
// (the endpoint then searches purely by radius).
func openChargeMapCountryCode(country string) string {
	c := strings.ToLower(strings.TrimSpace(country))
	switch c {
	case "":
		return ""
	case "tr", "turkey", "türkiye", "turkiye":
		return "TR"
	case "us", "usa", "united states":
		return "US"
	case "de", "germany", "deutschland":
		return "DE"
	case "gb", "uk", "united kingdom":
		return "GB"
	case "nl", "netherlands":
		return "NL"
	case "fr", "france":
		return "FR"
	default:
		if len(c) == 2 {
			return strings.ToUpper(c)
		}
		return ""
	}
}

// ── networks directory (static) ──────────────────────────────────────────────

// EVNetwork is a curated charging-network entry.
type EVNetwork struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Country string `json:"country"`
	Note    string `json:"note,omitempty"`
}

var evNetworksByCountry = map[string][]EVNetwork{
	"TR": {
		{ID: "trugo", Name: "Trugo", Country: "TR", Note: "Togg's network; CCS2 DC fast"},
		{ID: "zes", Name: "ZES", Country: "TR", Note: "Zorlu Enerji; wide AC+DC coverage"},
		{ID: "esarj", Name: "Eşarj", Country: "TR", Note: "Enerjisa"},
		{ID: "sharz", Name: "Sharz.net", Country: "TR"},
		{ID: "voltrun", Name: "Voltrun", Country: "TR"},
		{ID: "beefull", Name: "Beefull", Country: "TR"},
		{ID: "astor", Name: "Astor Şarj", Country: "TR"},
		{ID: "onsarj", Name: "On Şarj", Country: "TR"},
		{ID: "otowatt", Name: "Otowatt", Country: "TR"},
		{ID: "powercity", Name: "PowerCity", Country: "TR"},
	},
	"US": {
		{ID: "tesla", Name: "Tesla Supercharger", Country: "US", Note: "NACS; some sites open to non-Tesla via Magic Dock"},
		{ID: "electrify_america", Name: "Electrify America", Country: "US", Note: "CCS1 + CHAdeMO DC fast"},
		{ID: "chargepoint", Name: "ChargePoint", Country: "US"},
		{ID: "evgo", Name: "EVgo", Country: "US"},
	},
	"EU": {
		{ID: "ionity", Name: "IONITY", Country: "EU", Note: "CCS2 high-power (up to 350 kW)"},
		{ID: "fastned", Name: "Fastned", Country: "EU", Note: "CCS2 DC fast"},
		{ID: "shell", Name: "Shell Recharge", Country: "EU"},
		{ID: "bp", Name: "BP Pulse", Country: "EU"},
	},
}

// mcpEVNetworks returns the curated network directory, optionally filtered by
// country. country "" returns all regions.
func mcpEVNetworks(country string) interface{} {
	c := strings.ToLower(strings.TrimSpace(country))
	pick := func(key string) []EVNetwork { return evNetworksByCountry[key] }

	var out []EVNetwork
	switch c {
	case "":
		for _, key := range []string{"TR", "US", "EU"} {
			out = append(out, pick(key)...)
		}
	case "tr", "turkey", "türkiye", "turkiye":
		out = pick("TR")
	case "us", "usa", "united states":
		out = pick("US")
	case "eu", "europe", "de", "germany", "nl", "fr", "gb", "uk":
		out = pick("EU")
	default:
		// Unknown: return all so the caller still gets something useful.
		for _, key := range []string{"TR", "US", "EU"} {
			out = append(out, pick(key)...)
		}
	}
	return map[string]interface{}{
		"count":    len(out),
		"networks": out,
		"note":     "Curated directory. Live station data: ev_charging (OpenChargeMap).",
	}
}

// ── connector taxonomy + vehicle presets (static) ────────────────────────────

// EVConnectorType is one entry in the connector taxonomy.
type EVConnectorType struct {
	ID         string  `json:"id"` // usable as the connector_type filter
	Name       string  `json:"name"`
	Current    string  `json:"current"` // AC / DC
	Region     string  `json:"region"`
	MaxPowerKW float64 `json:"max_power_kw"`
	Note       string  `json:"note,omitempty"`
}

var evConnectorTypes = []EVConnectorType{
	{ID: "type2", Name: "Type 2 (Mennekes)", Current: "AC", Region: "EU/TR", MaxPowerKW: 43, Note: "Default AC connector in Turkey/EU"},
	{ID: "ccs2", Name: "CCS2 (Combo 2)", Current: "DC", Region: "EU/TR", MaxPowerKW: 350, Note: "Turkey/EU DC fast-charge standard"},
	{ID: "ccs1", Name: "CCS1 (Combo 1)", Current: "DC", Region: "US", MaxPowerKW: 350},
	{ID: "chademo", Name: "CHAdeMO", Current: "DC", Region: "JP/legacy", MaxPowerKW: 100},
	{ID: "nacs", Name: "Tesla NACS", Current: "AC/DC", Region: "US", MaxPowerKW: 250, Note: "Tesla connector; becoming a US standard"},
	{ID: "type1", Name: "Type 1 (J1772)", Current: "AC", Region: "US/JP", MaxPowerKW: 7.4},
}

// EVVehiclePreset bundles a vehicle's default connector filters so a caller
// can default ev_charging filters by car instead of hand-picking connectors.
type EVVehiclePreset struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Connectors []string `json:"connectors"`    // preferred connector ids (DC first)
	PreferKW   int      `json:"prefer_min_kw"` // sensible min-power filter for this car
	Note       string   `json:"note,omitempty"`
}

// evVehiclePresets is an extensible table — add a row to support a new car.
// Not exhaustive by design; callers pass a preset id to default their filters.
var evVehiclePresets = map[string]EVVehiclePreset{
	"togg_t10x": {
		ID: "togg_t10x", Name: "Togg T10X",
		Connectors: []string{"ccs2", "type2"}, PreferKW: 120,
		Note: "CCS2 DC fast (≥120 kW preferred), Type 2 for AC",
	},
	"mg_zs_ev": {
		ID: "mg_zs_ev", Name: "MG ZS EV",
		Connectors: []string{"ccs2", "type2"}, PreferKW: 50,
		Note: "CCS2 DC (≥50 kW), Type 2 for AC",
	},
}

// mcpEVConnectorTypes returns the connector taxonomy plus the vehicle-preset
// table so an AI consumer can both reference connectors and default filters
// by vehicle.
func mcpEVConnectorTypes() interface{} {
	presets := make([]EVVehiclePreset, 0, len(evVehiclePresets))
	for _, k := range sortedKeys(evVehiclePresets) { // generic helper in diagnose.go
		presets = append(presets, evVehiclePresets[k])
	}
	return map[string]interface{}{
		"connector_types": evConnectorTypes,
		"vehicle_presets": presets,
		"note":            "Use an id from connector_types as the ev_charging connector_type filter. vehicle_presets default filters by car.",
	}
}

// ── small geo helpers ────────────────────────────────────────────────────────
// round1 lives in diskhealth.go and is reused here.

func haversineKM(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0 // km
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	return r * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
