package main

import "testing"

// The scenario this whole file exists for: a Togg T10X on the way to Bodrum.
// A nearby slow AC post must NOT outrank a slightly-farther Trugo DC charger.
func TestRankPrefersFastDCOverNearbySlowAC(t *testing.T) {
	stations := []EVStation{
		{
			Name: "Otopark AC", Network: "voltrun",
			DistanceKM: 2.0, MaxPowerKW: 22,
			Connectors: []EVConnector{{TypeID: "type2", PowerKW: 22}},
			Source:     "openchargemap", Sources: []string{"openchargemap"},
		},
		{
			Name: "Trugo — Otoyol", Network: "trugo",
			DistanceKM: 9.0, MaxPowerKW: 180,
			Connectors: []EVConnector{{TypeID: "ccs2", PowerKW: 180}},
			Source:     "openchargemap", Sources: []string{"openchargemap"},
		},
	}
	ranked := rankEVStations(stations, EVRankPrefs{
		PreferNetworks: []string{"trugo"},
		ConnectorID:    "ccs2",
	})
	if ranked[0].Name != "Trugo — Otoyol" {
		t.Fatalf("expected the fast DC Trugo first, got %q (scores: %v / %v)",
			ranked[0].Name, ranked[0].Score, ranked[1].Score)
	}
	if ranked[0].Why == "" {
		t.Error("winner must carry a speakable reason — it gets read aloud")
	}
}

// Weights are the product surface: a driver who is nearly empty cares about
// distance far more than power. Zeroing/raising a weight must flip the order.
func TestWeightsAreHonoured(t *testing.T) {
	stations := []EVStation{
		{Name: "Close slow", DistanceKM: 2, MaxPowerKW: 22},
		{Name: "Far fast", DistanceKM: 9, MaxPowerKW: 180},
	}
	// Distance-dominant: nearly empty, take whatever is closest.
	desperate := rankEVStations(stations, EVRankPrefs{
		Weights: &EVRankWeights{Distance: 10, Power: 1},
	})
	if desperate[0].Name != "Close slow" {
		t.Errorf("distance-weighted: expected 'Close slow', got %q", desperate[0].Name)
	}
	// Power-dominant: plenty of range, optimise the stop.
	relaxed := rankEVStations(stations, EVRankPrefs{
		Weights: &EVRankWeights{Distance: 1, Power: 60},
	})
	if relaxed[0].Name != "Far fast" {
		t.Errorf("power-weighted: expected 'Far fast', got %q", relaxed[0].Name)
	}
}

func TestOutOfServiceSinksButSurvives(t *testing.T) {
	stations := []EVStation{
		{Name: "Broken", DistanceKM: 1, MaxPowerKW: 180, StatusHint: "Not Operational"},
		{Name: "Working", DistanceKM: 6, MaxPowerKW: 120},
	}
	ranked := rankEVStations(stations, EVRankPrefs{})
	if ranked[0].Name != "Working" {
		t.Errorf("expected the working charger first, got %q", ranked[0].Name)
	}
	// It must still be OFFERED — status data goes stale, and a stranded driver
	// would rather try a maybe-broken charger than be told nothing exists.
	if len(ranked) != 2 {
		t.Fatalf("out-of-service station must not be dropped, got %d", len(ranked))
	}
}

// ── redundancy ───────────────────────────────────────────────────────────────

func TestMergeDedupesSameStationAcrossProviders(t *testing.T) {
	ocm := []EVStation{{
		Name: "Trugo Aydın", Network: "trugo",
		Lat: 37.8460, Lon: 27.8390, MaxPowerKW: 180, DistanceKM: 12.4,
		Source: "openchargemap", Sources: []string{"openchargemap"},
	}}
	// Same physical site, ~40 m off, seen by a second provider with less detail.
	other := []EVStation{{
		Name: "Trugo Aydin Otoyol", Network: "trugo",
		Lat: 37.8463, Lon: 27.8393, MaxPowerKW: 0, DistanceKM: 12.6,
		Address: "O-31 Otoyolu",
		Source:  "someprovider", Sources: []string{"someprovider"},
	}}

	merged := mergeEVStations(ocm, other)
	if len(merged) != 1 {
		t.Fatalf("same station from two providers must merge to 1, got %d", len(merged))
	}
	m := merged[0]
	if len(m.Sources) != 2 {
		t.Errorf("expected both providers recorded, got %v", m.Sources)
	}
	if m.MaxPowerKW != 180 {
		t.Errorf("merge must keep the more useful power reading, got %v", m.MaxPowerKW)
	}
	if m.Address == "" {
		t.Error("merge must fill gaps from the second provider (address)")
	}
}

func TestMergeKeepsDistinctStationsApart(t *testing.T) {
	// Two chargers ~1.5 km apart — different sites, must not collapse.
	a := []EVStation{{Name: "ZES A", Network: "zes", Lat: 37.8460, Lon: 27.8390}}
	b := []EVStation{{Name: "Esarj B", Network: "esarj", Lat: 37.8590, Lon: 27.8390}}
	if got := len(mergeEVStations(a, b)); got != 2 {
		t.Fatalf("distinct stations must stay distinct, got %d", got)
	}
}

func TestCorroborationIsRewarded(t *testing.T) {
	solo := EVStation{Name: "Solo", DistanceKM: 5, MaxPowerKW: 120, Sources: []string{"a"}}
	both := EVStation{Name: "Both", DistanceKM: 5, MaxPowerKW: 120, Sources: []string{"a", "b"}}
	ranked := rankEVStations([]EVStation{solo, both}, EVRankPrefs{})
	if ranked[0].Name != "Both" {
		t.Errorf("a station two providers agree on should outrank an identical solo listing, got %q", ranked[0].Name)
	}
}
