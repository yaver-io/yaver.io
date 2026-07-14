package main

// ev_rank.go — source redundancy + weighted ranking for EV charging discovery.
//
// Two problems this solves, both of which bite on a real road trip:
//
//  1. ONE SOURCE IS A SINGLE POINT OF FAILURE. ev_charging.go talks to
//     OpenChargeMap. When OCM rate-limits (403/429), the Policy Guard correctly
//     backs off — and the driver gets nothing. Redundancy means asking a second,
//     independent provider. NOTE the distinction that keeps this honest: falling
//     back to a DIFFERENT provider is not evading a block. Retrying the blocked
//     provider, rotating IPs, or spoofing a UA to get back in WOULD be. We never
//     re-hit a provider that said no.
//
//  2. NEAREST IS NOT BEST. Sorting by distance alone puts a 22 kW AC post 3 km
//     away above a 180 kW Trugo DC charger 6 km away. For a Togg T10X on the way
//     to Bodrum that ranking is actively wrong: the "closer" option costs an hour.
//     Ranking is a weighted score over distance, power, network preference, and
//     connector fit — with the weights exposed, because the right trade-off
//     differs between "I'm nearly empty" and "top up while we eat".
//
// Pure functions, no I/O — ev_charging.go owns the network calls.

import (
	"math"
	"sort"
	"strings"
)

// EVRankWeights tunes the ranking. Higher = more influential. Zero disables a
// term entirely, so a caller can rank purely by distance by zeroing the rest.
type EVRankWeights struct {
	// Distance is the penalty per km. The only negative term.
	Distance float64 `json:"distance"`
	// Power rewards kW. Normalized against 250 kW so it can't dwarf everything.
	Power float64 `json:"power"`
	// Network rewards a station on one of PreferNetworks.
	Network float64 `json:"network"`
	// Connector rewards a station that actually has the car's connector.
	Connector float64 `json:"connector"`
	// Corroboration rewards a station confirmed by more than one provider —
	// a station two sources agree on is likelier to exist and be live.
	Corroboration float64 `json:"corroboration"`
}

// defaultEVRankWeights is tuned for a long drive in an EV that DC-fast-charges:
// power matters a lot, distance matters, and a 20 km detour to a 180 kW charger
// beats a 2 km crawl to a 7 kW wall box.
func defaultEVRankWeights() EVRankWeights {
	return EVRankWeights{
		Distance:      1.0,
		Power:         28.0,
		Network:       8.0,
		Connector:     12.0,
		Corroboration: 4.0,
	}
}

// EVRankPrefs is the driver's context.
type EVRankPrefs struct {
	// PreferNetworks are network ids ("trugo", "zes") the driver has an account
	// with. Membership beats a marginally closer station they can't use.
	PreferNetworks []string `json:"prefer_networks,omitempty"`
	// ConnectorID is the car's connector ("ccs2"). Usually from a vehicle preset.
	ConnectorID string `json:"connector_type,omitempty"`
	// Weights overrides the defaults; zero value means "use defaults".
	Weights *EVRankWeights `json:"weights,omitempty"`
}

// rankEVStations scores and sorts stations best-first. It does NOT filter —
// filtering is the caller's job; a driver who is nearly empty would rather be
// told about a slow charger than told about nothing.
func rankEVStations(stations []EVStation, prefs EVRankPrefs) []EVStation {
	w := defaultEVRankWeights()
	if prefs.Weights != nil {
		w = *prefs.Weights
	}
	out := make([]EVStation, len(stations))
	copy(out, stations)

	for i := range out {
		out[i].Score, out[i].Why = scoreEVStation(out[i], w, prefs)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		// Deterministic tiebreak, so the same query never reorders on a driver.
		return out[a].DistanceKM < out[b].DistanceKM
	})
	return out
}

// scoreEVStation returns the weighted score and a SHORT human reason. The reason
// exists because this ends up spoken aloud in a car: "Trugo, 180 kilowatt, 12
// kilometers" is a justification a driver can accept or override. A score with
// no explanation is not actionable at 120 km/h.
func scoreEVStation(s EVStation, w EVRankWeights, prefs EVRankPrefs) (float64, string) {
	var score float64
	var why []string

	score -= w.Distance * s.DistanceKM

	// Power, normalized to 250 kW and capped — 350 kW is not 2x better than
	// 180 kW in practice, because the car's own curve is the bottleneck.
	if s.MaxPowerKW > 0 {
		p := math.Min(s.MaxPowerKW, 250) / 250
		score += w.Power * p
		if s.MaxPowerKW >= 100 {
			why = append(why, "fast DC")
		}
	}

	if matchesPreferredNetwork(s, prefs.PreferNetworks) {
		score += w.Network
		why = append(why, "your network")
	}

	if prefs.ConnectorID != "" && stationHasConnector(s, strings.ToLower(strings.TrimSpace(prefs.ConnectorID))) {
		score += w.Connector
		why = append(why, "connector fits")
	}

	// Corroboration: more than one independent provider listed this station.
	if len(s.Sources) > 1 {
		score += w.Corroboration * float64(len(s.Sources)-1)
		why = append(why, "confirmed by "+strings.Join(s.Sources, "+"))
	}

	// A provider that explicitly says it's out of service should sink, but not
	// vanish — status data is often stale, and a driver may want to try anyway.
	if isOutOfService(s.StatusHint) {
		score -= 25
		why = append(why, "reported out of service")
	}

	return round1(score), strings.Join(why, ", ")
}

func matchesPreferredNetwork(s EVStation, prefer []string) bool {
	if len(prefer) == 0 {
		return false
	}
	hay := strings.ToLower(s.Network + " " + s.Operator + " " + s.Name)
	for _, p := range prefer {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" && strings.Contains(hay, p) {
			return true
		}
	}
	return false
}

// stationHasConnector lives in ev_charging.go — it already matches both the
// taxonomy id and the human label, so don't fork a second implementation here.

func isOutOfService(hint string) bool {
	h := strings.ToLower(hint)
	return strings.Contains(h, "not operational") ||
		strings.Contains(h, "out of service") ||
		strings.Contains(h, "removed") ||
		strings.Contains(h, "faulted")
}

// ── Redundancy: merge stations from independent providers ────────────────────

// evDedupeRadiusKM is how close two rows must be to be considered the same
// physical site. 80 m is wide enough to absorb the geocoding disagreement
// between providers, tight enough not to merge two chargers across a motorway.
const evDedupeRadiusKM = 0.08

// mergeEVStations folds results from several providers into one list, deduping
// the same physical station and recording WHICH providers saw it (Sources) so
// the ranker can reward corroboration. Order of `lists` is priority order: the
// first provider to describe a field wins, later ones only fill gaps.
//
// This is the redundancy seam. Add a provider by appending its result list.
func mergeEVStations(lists ...[]EVStation) []EVStation {
	var out []EVStation

	for _, list := range lists {
		for _, s := range list {
			if s.Source != "" && len(s.Sources) == 0 {
				s.Sources = []string{s.Source}
			}
			if idx := findEVStationMatch(out, s); idx >= 0 {
				out[idx] = mergeEVStationPair(out[idx], s)
				continue
			}
			out = append(out, s)
		}
	}
	return out
}

// findEVStationMatch returns the index of an existing station that is the same
// physical site as `s`, or -1. Proximity is the primary signal; a name check
// guards against merging two distinct operators sharing a car park.
func findEVStationMatch(existing []EVStation, s EVStation) int {
	for i, e := range existing {
		if haversineKM(e.Lat, e.Lon, s.Lat, s.Lon) > evDedupeRadiusKM {
			continue
		}
		// Same spot. Same operator (or one side unknown) → same station.
		if sameishOperator(e, s) {
			return i
		}
	}
	return -1
}

func sameishOperator(a, b EVStation) bool {
	na := normalizeEVName(a.Network + a.Operator)
	nb := normalizeEVName(b.Network + b.Operator)
	if na == "" || nb == "" {
		return true // one side didn't say; trust the proximity match
	}
	return strings.Contains(na, nb) || strings.Contains(nb, na)
}

func normalizeEVName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// mergeEVStationPair keeps `keep` authoritative and fills its gaps from `add`,
// unioning the provider list and taking the higher power / shorter distance
// (providers disagree; take the more useful reading and let corroboration
// speak to confidence).
func mergeEVStationPair(keep, add EVStation) EVStation {
	if keep.Name == "" {
		keep.Name = add.Name
	}
	if keep.Operator == "" {
		keep.Operator = add.Operator
	}
	if keep.Network == "" {
		keep.Network = add.Network
	}
	if keep.Address == "" {
		keep.Address = add.Address
	}
	if keep.Town == "" {
		keep.Town = add.Town
	}
	if keep.DeepLink == "" {
		keep.DeepLink = add.DeepLink
	}
	if keep.StatusHint == "" {
		keep.StatusHint = add.StatusHint
	}
	if add.MaxPowerKW > keep.MaxPowerKW {
		keep.MaxPowerKW = add.MaxPowerKW
	}
	if add.DistanceKM > 0 && (keep.DistanceKM == 0 || add.DistanceKM < keep.DistanceKM) {
		keep.DistanceKM = add.DistanceKM
	}
	if len(keep.Connectors) == 0 {
		keep.Connectors = add.Connectors
	}
	for _, src := range add.Sources {
		if !containsFold(keep.Sources, src) {
			keep.Sources = append(keep.Sources, src)
		}
	}
	return keep
}

func containsFold(hay []string, needle string) bool {
	for _, h := range hay {
		if strings.EqualFold(h, needle) {
			return true
		}
	}
	return false
}
