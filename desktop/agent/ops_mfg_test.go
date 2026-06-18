package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMfgPixelSeedUpdatesBOMQuantityAndLocation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := json.RawMessage(`{
		"id":"quote-1",
		"name":"Harness quote",
		"csv":"ref,qty,part,description,package,unit_usd,location\nJ1,1,USB-C,Type-C receptacle,SMD,0.30,A\nR1,2,10k,Pull-up,0402,0.01,B\n"
	}`)
	res := mfgRFQImportBOM(OpsContext{}, importPayload)
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	qty := 12.0
	upsertPayload, _ := json.Marshal(map[string]any{
		"id": "quote-1",
		"seed": map[string]any{
			"id":       "seed-j1",
			"lineRef":  "J1",
			"x":        120,
			"y":        240,
			"quantity": qty,
			"location": "top-left reel bin",
			"note":     "manual assist correction from screen",
		},
	})
	res = mfgPixelSeedUpsert(OpsContext{}, upsertPayload)
	if !res.OK {
		t.Fatalf("seed upsert failed: %s", res.Error)
	}

	ws := loadMfgWorkspaceForTest(t, "quote-1")
	if got := ws.BOM[0].Qty; got != 12 {
		t.Fatalf("BOM qty not synced from seed: got %v want 12", got)
	}
	if got := ws.BOM[0].Location; got != "top-left reel bin" {
		t.Fatalf("BOM location not synced from seed: got %q", got)
	}
	if len(ws.Seeds) != 1 || ws.Seeds[0].Quantity == nil || *ws.Seeds[0].Quantity != 12 {
		t.Fatalf("seed not persisted with quantity: %+v", ws.Seeds)
	}
}

func TestMfgBOMLineUpdateSyncsSeedAndSeedDeleteKeepsBOM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(`{
		"id":"quote-2",
		"csv":"ref,qty,part\nU1,1,ESP32\n"
	}`))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(`{
		"id":"quote-2",
		"seed":{"id":"seed-u1","lineRef":"U1","quantity":3,"location":"old"}
	}`))
	if !res.OK {
		t.Fatalf("seed upsert failed: %s", res.Error)
	}

	res = mfgBOMLineUpdate(OpsContext{}, json.RawMessage(`{
		"id":"quote-2",
		"lineRef":"U1",
		"quantity":8,
		"location":"manual-assist bench"
	}`))
	if !res.OK {
		t.Fatalf("line update failed: %s", res.Error)
	}
	ws := loadMfgWorkspaceForTest(t, "quote-2")
	if got := ws.BOM[0].Qty; got != 8 {
		t.Fatalf("BOM qty = %v, want 8", got)
	}
	if ws.Seeds[0].Quantity == nil || *ws.Seeds[0].Quantity != 8 {
		t.Fatalf("seed quantity not synced from BOM: %+v", ws.Seeds[0])
	}
	if got := ws.Seeds[0].Location; got != "manual-assist bench" {
		t.Fatalf("seed location = %q, want manual-assist bench", got)
	}

	res = mfgPixelSeedDelete(OpsContext{}, json.RawMessage(`{"id":"quote-2","seedId":"seed-u1"}`))
	if !res.OK {
		t.Fatalf("seed delete failed: %s", res.Error)
	}
	ws = loadMfgWorkspaceForTest(t, "quote-2")
	if len(ws.Seeds) != 0 {
		t.Fatalf("seed should be removed: %+v", ws.Seeds)
	}
	if got := ws.BOM[0].Qty; got != 8 {
		t.Fatalf("seed delete should not roll back BOM qty: got %v want 8", got)
	}
}

func loadMfgWorkspaceForTest(t *testing.T, id string) mfgRFQWorkspace {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".yaver", "mfg-rfq", id+".json"))
	if err != nil {
		t.Fatalf("read workspace: %v", err)
	}
	var ws mfgRFQWorkspace
	if err := json.Unmarshal(b, &ws); err != nil {
		t.Fatalf("parse workspace: %v", err)
	}
	return ws
}
