package main

import (
	"testing"
)

// TestProcessModelEpisodes verifies the deterministic segmentation: contiguous
// same-system frames merge into one episode; a system change starts a new one;
// frames + (best-effort) events attribute to the right episode.
func TestProcessModelEpisodes(t *testing.T) {
	withTempScreenlogDir(t)
	base := int64(1_000_000)
	sess := &ScreenlogSession{
		ID:     "slog-pm1",
		Host:   "test-host",
		Config: defaultScreenlogConfig(),
		Frames: []ScreenlogFrame{
			{Idx: 1, CapturedAt: base, ActiveToMs: base + 2000, ActiveApp: "erp", ActiveWindow: "Orders", File: "f1.jpg"},
			{Idx: 2, CapturedAt: base + 2000, ActiveToMs: base + 4000, ActiveApp: "erp", ActiveWindow: "Orders", File: "f2.jpg"},
			{Idx: 3, CapturedAt: base + 4500, ActiveToMs: base + 6500, ActiveApp: "excel", ActiveWindow: "Sheet1", File: "f3.jpg"},
		},
	}
	if err := saveScreenlogSession(sess); err != nil {
		t.Fatal(err)
	}

	pm, _, err := buildProcessSkeleton("slog-pm1")
	if err != nil {
		t.Fatal(err)
	}
	if len(pm.Episodes) != 2 {
		t.Fatalf("expected 2 episodes (erp, excel), got %d: %+v", len(pm.Episodes), pm.Episodes)
	}
	if pm.Episodes[0].System != "erp" || pm.Episodes[1].System != "excel" {
		t.Errorf("episode systems wrong: %q, %q", pm.Episodes[0].System, pm.Episodes[1].System)
	}
	if pm.Episodes[0].Frames != 2 {
		t.Errorf("erp episode should have 2 frames, got %d", pm.Episodes[0].Frames)
	}
	if pm.Episodes[0].Screen != "Orders" {
		t.Errorf("erp episode screen should be Orders, got %q", pm.Episodes[0].Screen)
	}
	if pm.Host != "test-host" {
		t.Errorf("model host not carried: %q", pm.Host)
	}
	// the skeleton must NOT pre-fill semantics — that's the runner's job
	if pm.Episodes[0].Intent != "" {
		t.Error("skeleton must leave Intent empty for the runner")
	}
}

// TestProcessModelSaveLoad round-trips a runner-completed model.
func TestProcessModelSaveLoad(t *testing.T) {
	withTempScreenlogDir(t)
	// the session dir must exist for the model path
	if _, err := screenlogSessionDir("slog-pm2"); err != nil {
		t.Fatal(err)
	}
	pm := &ProcessModel{
		SessionID: "slog-pm2",
		Role:      "order-entry-clerk",
		System:    "Logo Tiger ERP",
		Episodes: []ProcessEpisode{{
			Index: 0, System: "erp", Intent: "enter sales order",
			FieldsTouched: []string{"Cari Kod", "Stok Kodu"},
			DecisionRules: []string{"if customer ACME → 5% discount"},
			Confidence:    0.9,
		}},
		Machinery: []MachineryUse{{Machine: "CST18D", ObservedUse: "48 crimp cycles", Params: map[string]string{"applicator": "RKES-27"}}},
		Summary:   "operator entered 1 sales order",
	}
	if err := saveProcessModel(pm); err != nil {
		t.Fatal(err)
	}
	got, err := loadProcessModel("slog-pm2")
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != "order-entry-clerk" || got.System != "Logo Tiger ERP" {
		t.Errorf("model fields lost: %+v", got)
	}
	if len(got.Episodes) != 1 || got.Episodes[0].Intent != "enter sales order" {
		t.Errorf("episode lost: %+v", got.Episodes)
	}
	if len(got.Machinery) != 1 || got.Machinery[0].Params["applicator"] != "RKES-27" {
		t.Errorf("machinery lost: %+v", got.Machinery)
	}
	if got.CreatedAt == 0 {
		t.Error("CreatedAt should be stamped on save")
	}
}

// TestScreenlogLatestFrameFallback verifies the live view falls back to the
// newest stored session's last frame when nothing is actively recording.
func TestScreenlogLatestFrameFallback(t *testing.T) {
	withTempScreenlogDir(t)
	sess := &ScreenlogSession{
		ID:     "slog-live1",
		Config: defaultScreenlogConfig(),
		Frames: []ScreenlogFrame{
			{Idx: 1, CapturedAt: 10, File: "a.jpg", ActiveApp: "x"},
			{Idx: 2, CapturedAt: 20, File: "b.jpg", ActiveApp: "y"},
		},
	}
	if err := saveScreenlogSession(sess); err != nil {
		t.Fatal(err)
	}
	id, fr, ok := screenlogLatestFrame()
	if !ok || id != "slog-live1" || fr.File != "b.jpg" {
		t.Fatalf("latest frame should be slog-live1/b.jpg, got ok=%v id=%q file=%q", ok, id, fr.File)
	}
}
