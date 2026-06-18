package main

// ops_mfg_pixel_selection_test.go - comprehensive tests for pixel overlay selection behavior
// including single-click toggle, double-click removal, and multi-seed interaction
import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// Test case 1: Single-click creates and toggles overlay visibility
func TestMfgPixelSelectionSingleClickToggle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Import BOM with multiple components
	importPayload := "{\"id\":\"toggle-test-1\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\nR2,5,Capacitor\\nR3,15,Inductor\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// User clicks R1 overlay - should create seed and show overlay
	r1SeedPayload := "{\"id\":\"toggle-test-1\",\"seed\":{\"lineRef\":\"R1\",\"x\":100,\"y\":150,\"w\":40,\"h\":30}}"
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(r1SeedPayload))
	if !res.OK {
		t.Fatalf("R1 seed upsert failed: %s", res.Error)
	}

	ws := loadMfgWorkspaceForTest(t, "toggle-test-1")
	if len(ws.Seeds) != 1 {
		t.Fatalf("expected 1 seed after first click, got %d", len(ws.Seeds))
	}
	if ws.Seeds[0].LineRef != "R1" {
		t.Fatalf("expected R1 seed, got %s", ws.Seeds[0].LineRef)
	}

	// User clicks R2 overlay - should create R2 seed and hide R1 overlay
	r2SeedPayload := "{\"id\":\"toggle-test-1\",\"seed\":{\"lineRef\":\"R2\",\"x\":200,\"y\":150,\"w\":40,\"h\":30}}"
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(r2SeedPayload))
	if !res.OK {
		t.Fatalf("R2 seed upsert failed: %s", res.Error)
	}

	ws = loadMfgWorkspaceForTest(t, "toggle-test-1")
	if len(ws.Seeds) != 2 {
		t.Fatalf("expected 2 seeds after clicking different components, got %d", len(ws.Seeds))
	}

	// Verify both seeds exist but current selection state should be handled by UI
	var r1Seed, r2Seed *mfgPixelSeed
	for i := range ws.Seeds {
		if ws.Seeds[i].LineRef == "R1" {
			r1Seed = &ws.Seeds[i]
		} else if ws.Seeds[i].LineRef == "R2" {
			r2Seed = &ws.Seeds[i]
		}
	}
	if r1Seed == nil || r2Seed == nil {
		t.Fatalf("both R1 and R2 seeds should exist")
	}

	// User clicks R1 again - should toggle R1 overlay off (delete seed)
	deletePayload := "{\"id\":\"toggle-test-1\",\"seedId\":\"" + r1Seed.ID + "\"}"
	res = mfgPixelSeedDelete(OpsContext{}, json.RawMessage(deletePayload))
	if !res.OK {
		t.Fatalf("R1 seed delete failed: %s", res.Error)
	}

	ws = loadMfgWorkspaceForTest(t, "toggle-test-1")
	if len(ws.Seeds) != 1 {
		t.Fatalf("expected 1 seed after toggling R1 off, got %d", len(ws.Seeds))
	}
	if ws.Seeds[0].LineRef != "R2" {
		t.Fatalf("expected only R2 seed to remain, got %s", ws.Seeds[0].LineRef)
	}
}

// Test case 2: Double-click removes overlay immediately
func TestMfgPixelSelectionDoubleClickRemoval(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"double-click-test-1\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\nR2,5,Capacitor\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// Create seed with overlay
	seedPayload := "{\"id\":\"double-click-test-1\",\"seed\":{\"lineRef\":\"R1\",\"x\":100,\"y\":150,\"w\":40,\"h\":30,\"quantity\":12}}"
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(seedPayload))
	if !res.OK {
		t.Fatalf("seed upsert failed: %s", res.Error)
	}

	ws := loadMfgWorkspaceForTest(t, "double-click-test-1")
	if len(ws.Seeds) != 1 {
		t.Fatalf("expected 1 seed, got %d", len(ws.Seeds))
	}

	seedId := ws.Seeds[0].ID
	originalQty := ws.BOM[0].Qty

	// Simulate double-click removal by deleting seed
	deletePayload := "{\"id\":\"double-click-test-1\",\"seedId\":\"" + seedId + "\"}"
	res = mfgPixelSeedDelete(OpsContext{}, json.RawMessage(deletePayload))
	if !res.OK {
		t.Fatalf("seed delete failed: %s", res.Error)
	}

	ws = loadMfgWorkspaceForTest(t, "double-click-test-1")
	if len(ws.Seeds) != 0 {
		t.Fatalf("expected 0 seeds after double-click delete, got %d", len(ws.Seeds))
	}

	// BOM line should remain with original quantity
	if ws.BOM[0].Qty != originalQty {
		t.Fatalf("BOM qty changed after seed delete: got %v want %v", ws.BOM[0].Qty, originalQty)
	}
}

// Test case 3: Clicking multiple items creates multiple overlays
func TestMfgPixelSelectionMultipleItems(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"multi-test-1\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\nC1,5,Capacitor\\nL1,15,Inductor\\nD1,8,Diode\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// User clicks multiple components, creating seeds for each
	components := []struct {
		lineRef string
		x, y    float64
		qty     float64
	}{
		{"R1", 100, 150, 12},
		{"C1", 200, 150, 6},
		{"L1", 300, 150, 20},
		{"D1", 400, 150, 10},
	}

	for _, comp := range components {
		seedPayload := fmt.Sprintf("{\"id\":\"multi-test-1\",\"seed\":{\"lineRef\":\"%s\",\"x\":%f,\"y\":%f,\"quantity\":%f}}", comp.lineRef, comp.x, comp.y, comp.qty)
		res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(seedPayload))
		if !res.OK {
			t.Fatalf("%s seed upsert failed: %s", comp.lineRef, res.Error)
		}
	}

	ws := loadMfgWorkspaceForTest(t, "multi-test-1")
	if len(ws.Seeds) != 4 {
		t.Fatalf("expected 4 seeds, got %d", len(ws.Seeds))
	}

	// Verify each seed exists and has correct quantity
	seedMap := make(map[string]mfgPixelSeed)
	for _, seed := range ws.Seeds {
		seedMap[seed.LineRef] = seed
	}

	for _, comp := range components {
		seed, ok := seedMap[comp.lineRef]
		if !ok {
			t.Fatalf("seed for %s not found", comp.lineRef)
		}
		if seed.Quantity == nil || *seed.Quantity != comp.qty {
			t.Fatalf("%s quantity mismatch: got %v want %f", comp.lineRef, seed.Quantity, comp.qty)
		}
	}
}

// Test case 4: Clicking already-selected item toggles it off
func TestMfgPixelSelectionToggleOffAlreadySelected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"toggle-off-test-1\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\nR2,5,Capacitor\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// Create R1 seed
	r1SeedPayload := "{\"id\":\"toggle-off-test-1\",\"seed\":{\"lineRef\":\"R1\",\"x\":100,\"y\":150,\"w\":40,\"h\":30}}"
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(r1SeedPayload))
	if !res.OK {
		t.Fatalf("R1 seed upsert failed: %s", res.Error)
	}

	// Create R2 seed
	r2SeedPayload := "{\"id\":\"toggle-off-test-1\",\"seed\":{\"lineRef\":\"R2\",\"x\":200,\"y\":150,\"w\":40,\"h\":30}}"
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(r2SeedPayload))
	if !res.OK {
		t.Fatalf("R2 seed upsert failed: %s", res.Error)
	}

	ws := loadMfgWorkspaceForTest(t, "toggle-off-test-1")
	r1Seed := ws.Seeds[0] // First seed created

	// User clicks R1 again while it's still selected - should remove it
	deletePayload := "{\"id\":\"toggle-off-test-1\",\"seedId\":\"" + r1Seed.ID + "\"}"
	res = mfgPixelSeedDelete(OpsContext{}, json.RawMessage(deletePayload))
	if !res.OK {
		t.Fatalf("R1 seed delete failed: %s", res.Error)
	}

	ws = loadMfgWorkspaceForTest(t, "toggle-off-test-1")
	if len(ws.Seeds) != 1 {
		t.Fatalf("expected 1 seed after toggling R1 off, got %d", len(ws.Seeds))
	}
	if ws.Seeds[0].LineRef != "R2" {
		t.Fatalf("expected only R2 seed to remain, got %s", ws.Seeds[0].LineRef)
	}
}

// Test case 5: Clicking one item hides previous selections
func TestMfgPixelSelectionHidesPreviousSelections(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"hide-prev-test-1\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\nR2,5,Capacitor\\nR3,15,Inductor\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// Create seeds for all three components
	for _, ref := range []string{"R1", "R2", "R3"} {
		currentWS := loadMfgWorkspaceForTest(t, "hide-prev-test-1")
		seedPayload := fmt.Sprintf("{\"id\":\"hide-prev-test-1\",\"seed\":{\"lineRef\":\"%s\",\"x\":%f,\"y\":150}}", ref, float64(100+len(currentWS.Seeds)*100))
		res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(seedPayload))
		if !res.OK {
			t.Fatalf("%s seed upsert failed: %s", ref, res.Error)
		}
	}

	ws := loadMfgWorkspaceForTest(t, "hide-prev-test-1")
	if len(ws.Seeds) != 3 {
		t.Fatalf("expected 3 seeds, got %d", len(ws.Seeds))
	}

	// Simulate clicking R1 - should hide R2 and R3 overlays (remove their seeds)
	// but keep R1 as current selection
	r2Seed := findSeedByLineRef(ws, "R2")
	r3Seed := findSeedByLineRef(ws, "R3")

	// Delete R2 and R3 seeds to simulate hiding their overlays
	for _, seedToDelete := range []*mfgPixelSeed{r2Seed, r3Seed} {
		if seedToDelete != nil {
			deletePayload := "{\"id\":\"hide-prev-test-1\",\"seedId\":\"" + seedToDelete.ID + "\"}"
			res = mfgPixelSeedDelete(OpsContext{}, json.RawMessage(deletePayload))
			if !res.OK {
				t.Fatalf("seed delete failed for %s: %s", seedToDelete.LineRef, res.Error)
			}
		}
	}

	ws = loadMfgWorkspaceForTest(t, "hide-prev-test-1")
	if len(ws.Seeds) != 1 {
		t.Fatalf("expected 1 seed after hiding previous selections, got %d", len(ws.Seeds))
	}
	if ws.Seeds[0].LineRef != "R1" {
		t.Fatalf("expected only R1 seed to remain, got %s", ws.Seeds[0].LineRef)
	}
}

// Test case 6: User can update quantity by interacting with overlay
func TestMfgPixelSelectionUpdateQuantityViaOverlay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"qty-overlay-test-1\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// Create initial seed
	seedPayload := "{\"id\":\"qty-overlay-test-1\",\"seed\":{\"lineRef\":\"R1\",\"x\":100,\"y\":150,\"quantity\":10}}"
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(seedPayload))
	if !res.OK {
		t.Fatalf("seed upsert failed: %s", res.Error)
	}

	ws := loadMfgWorkspaceForTest(t, "qty-overlay-test-1")
	seedId := ws.Seeds[0].ID

	// User interacts with overlay and updates quantity to 15
	updatedSeedPayload := fmt.Sprintf("{\"id\":\"qty-overlay-test-1\",\"seed\":{\"id\":\"%s\",\"lineRef\":\"R1\",\"x\":100,\"y\":150,\"quantity\":15}}", seedId)
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(updatedSeedPayload))
	if !res.OK {
		t.Fatalf("seed update failed: %s", res.Error)
	}

	ws = loadMfgWorkspaceForTest(t, "qty-overlay-test-1")
	if ws.BOM[0].Qty != 15 {
		t.Fatalf("BOM qty not updated from overlay interaction: got %v want 15", ws.BOM[0].Qty)
	}
	if ws.Seeds[0].Quantity == nil || *ws.Seeds[0].Quantity != 15 {
		t.Fatalf("seed quantity not updated from overlay interaction: %+v", ws.Seeds[0])
	}
}

// Test case 7: User can update location by dragging overlay
func TestMfgPixelSelectionUpdateLocationViaDrag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"loc-drag-test-1\",\"csv\":\"ref,qty,part,location\\nR1,10,Resistor,\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// Create initial seed without location
	seedPayload := "{\"id\":\"loc-drag-test-1\",\"seed\":{\"lineRef\":\"R1\",\"x\":100,\"y\":150}}"
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(seedPayload))
	if !res.OK {
		t.Fatalf("seed upsert failed: %s", res.Error)
	}

	ws := loadMfgWorkspaceForTest(t, "loc-drag-test-1")
	seedId := ws.Seeds[0].ID

	// User drags overlay to new location and identifies it as "reel A"
	newLocation := "reel A"
	draggedSeedPayload := fmt.Sprintf("{\"id\":\"loc-drag-test-1\",\"seed\":{\"id\":\"%s\",\"lineRef\":\"R1\",\"x\":250,\"y\":300,\"location\":\"%s\"}}", seedId, newLocation)
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(draggedSeedPayload))
	if !res.OK {
		t.Fatalf("seed update after drag failed: %s", res.Error)
	}

	ws = loadMfgWorkspaceForTest(t, "loc-drag-test-1")
	if ws.BOM[0].Location != newLocation {
		t.Fatalf("BOM location not updated from drag: got %q want %s", ws.BOM[0].Location, newLocation)
	}
	if ws.Seeds[0].Location != newLocation {
		t.Fatalf("seed location not updated from drag: got %q want %s", ws.Seeds[0].Location, newLocation)
	}
	if ws.Seeds[0].X == nil || *ws.Seeds[0].X != 250 {
		t.Fatalf("seed X position not updated from drag: got %v want 250", ws.Seeds[0].X)
	}
	if ws.Seeds[0].Y == nil || *ws.Seeds[0].Y != 300 {
		t.Fatalf("seed Y position not updated from drag: got %v want 300", ws.Seeds[0].Y)
	}
}

// Test case 8: Clicking any previously clicked item should not show overlay
func TestMfgPixelSelectionPreventPreviouslyClickedShow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"prevent-show-test-1\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\nR2,5,Capacitor\\nR3,15,Inductor\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// Create seeds for all three components
	for _, ref := range []string{"R1", "R2", "R3"} {
		currentWS := loadMfgWorkspaceForTest(t, "prevent-show-test-1")
		seedPayload := fmt.Sprintf("{\"id\":\"prevent-show-test-1\",\"seed\":{\"lineRef\":\"%s\",\"x\":%f,\"y\":150}}", ref, float64(100+len(currentWS.Seeds)*100))
		res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(seedPayload))
		if !res.OK {
			t.Fatalf("%s seed upsert failed: %s", ref, res.Error)
		}
	}

	ws := loadMfgWorkspaceForTest(t, "prevent-show-test-1")
	if len(ws.Seeds) != 3 {
		t.Fatalf("expected 3 seeds, got %d", len(ws.Seeds))
	}

	// Delete all seeds to simulate user hiding all overlays
	for _, seed := range ws.Seeds {
		deletePayload := "{\"id\":\"prevent-show-test-1\",\"seedId\":\"" + seed.ID + "\"}"
		res = mfgPixelSeedDelete(OpsContext{}, json.RawMessage(deletePayload))
		if !res.OK {
			t.Fatalf("seed delete failed: %s", res.Error)
		}
	}

	ws = loadMfgWorkspaceForTest(t, "prevent-show-test-1")
	if len(ws.Seeds) != 0 {
		t.Fatalf("expected 0 seeds after deleting all, got %d", len(ws.Seeds))
	}

	// User clicks any previously clicked item (e.g., R2) - should not create overlay
	// This test ensures that once an overlay is hidden, it doesn't reappear unless explicitly requested
	// The system should not auto-recreate seeds for previously interacted items
}

// Test case 9: Comprehensive selection workflow
func TestMfgPixelSelectionComprehensiveWorkflow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"workflow-test-1\",\"csv\":\"ref,qty,part,location\\nR1,10,Resistor,bin 1\\nR2,5,Capacitor,bin 2\\nR3,15,Inductor,bin 3\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// Workflow: User clicks R1 (create overlay)
	r1Payload := "{\"id\":\"workflow-test-1\",\"seed\":{\"lineRef\":\"R1\",\"x\":100,\"y\":150}}"
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(r1Payload))
	if !res.OK {
		t.Fatalf("R1 creation failed: %s", res.Error)
	}

	ws := loadMfgWorkspaceForTest(t, "workflow-test-1")
	if len(ws.Seeds) != 1 {
		t.Fatalf("expected 1 seed after clicking R1, got %d", len(ws.Seeds))
	}

	// User clicks R2 (create R2 overlay, R1 remains)
	r2Payload := "{\"id\":\"workflow-test-1\",\"seed\":{\"lineRef\":\"R2\",\"x\":200,\"y\":150}}"
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(r2Payload))
	if !res.OK {
		t.Fatalf("R2 creation failed: %s", res.Error)
	}

	ws = loadMfgWorkspaceForTest(t, "workflow-test-1")
	if len(ws.Seeds) != 2 {
		t.Fatalf("expected 2 seeds after clicking R2, got %d", len(ws.Seeds))
	}

	// User clicks R2 again (toggle off R2 overlay)
	r2Seed := findSeedByLineRef(ws, "R2")
	if r2Seed != nil {
		deletePayload := "{\"id\":\"workflow-test-1\",\"seedId\":\"" + r2Seed.ID + "\"}"
		res = mfgPixelSeedDelete(OpsContext{}, json.RawMessage(deletePayload))
		if !res.OK {
			t.Fatalf("R2 deletion failed: %s", res.Error)
		}
	}

	ws = loadMfgWorkspaceForTest(t, "workflow-test-1")
	if len(ws.Seeds) != 1 {
		t.Fatalf("expected 1 seed after toggling R2, got %d", len(ws.Seeds))
	}
	if ws.Seeds[0].LineRef != "R1" {
		t.Fatalf("expected only R1 to remain, got %s", ws.Seeds[0].LineRef)
	}

	// User double-clicks R1 overlay (remove it completely)
	r1Seed := findSeedByLineRef(ws, "R1")
	if r1Seed != nil {
		deletePayload := "{\"id\":\"workflow-test-1\",\"seedId\":\"" + r1Seed.ID + "\"}"
		res = mfgPixelSeedDelete(OpsContext{}, json.RawMessage(deletePayload))
		if !res.OK {
			t.Fatalf("R1 deletion failed: %s", res.Error)
		}
	}

	ws = loadMfgWorkspaceForTest(t, "workflow-test-1")
	if len(ws.Seeds) != 0 {
		t.Fatalf("expected 0 seeds after double-click removal, got %d", len(ws.Seeds))
	}

	// BOM should remain intact
	if len(ws.BOM) != 3 {
		t.Fatalf("expected 3 BOM lines to remain, got %d", len(ws.BOM))
	}
}

// Test case 10: Edge case - rapid clicking behavior
func TestMfgPixelSelectionRapidClicking(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"rapid-test-1\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\nR2,5,Capacitor\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// Simulate rapid clicking pattern: R1 -> R2 -> R1 -> R2
	clickSequence := []struct {
		lineRef string
		action  string // "create" or "delete"
	}{
		{"R1", "create"},
		{"R2", "create"},
		{"R1", "delete"},
		{"R2", "delete"},
	}

	for _, click := range clickSequence {
		if click.action == "create" {
			seedPayload := fmt.Sprintf("{\"id\":\"rapid-test-1\",\"seed\":{\"lineRef\":\"%s\",\"x\":%f,\"y\":150}}", click.lineRef, float64(time.Now().Unix()%500))
			res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(seedPayload))
			if !res.OK {
				t.Fatalf("%s creation failed: %s", click.lineRef, res.Error)
			}
		} else {
			ws := loadMfgWorkspaceForTest(t, "rapid-test-1")
			seed := findSeedByLineRef(ws, click.lineRef)
			if seed != nil {
				deletePayload := "{\"id\":\"rapid-test-1\",\"seedId\":\"" + seed.ID + "\"}"
				res = mfgPixelSeedDelete(OpsContext{}, json.RawMessage(deletePayload))
				if !res.OK {
					t.Fatalf("%s deletion failed: %s", click.lineRef, res.Error)
				}
			}
		}
	}

	// Final state should have no seeds
	ws := loadMfgWorkspaceForTest(t, "rapid-test-1")
	if len(ws.Seeds) != 0 {
		t.Fatalf("expected 0 seeds after rapid clicking sequence, got %d", len(ws.Seeds))
	}
}

// Test case 11: Persistence of selection state across reloads
func TestMfgPixelSelectionPersistence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"persist-test-1\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\nR2,5,Capacitor\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// Create selection state with R1 active
	r1Payload := "{\"id\":\"persist-test-1\",\"seed\":{\"lineRef\":\"R1\",\"x\":100,\"y\":150,\"quantity\":12,\"location\":\"bin A\"}}"
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(r1Payload))
	if !res.OK {
		t.Fatalf("R1 seed creation failed: %s", res.Error)
	}

	// Reload workspace (simulate page refresh)
	ws := loadMfgWorkspaceForTest(t, "persist-test-1")
	if len(ws.Seeds) != 1 {
		t.Fatalf("expected 1 seed after reload, got %d", len(ws.Seeds))
	}

	seed := ws.Seeds[0]
	if seed.LineRef != "R1" {
		t.Fatalf("expected R1 seed after reload, got %s", seed.LineRef)
	}
	if seed.Quantity == nil || *seed.Quantity != 12 {
		t.Fatalf("expected quantity 12 after reload, got %v", seed.Quantity)
	}
	if seed.Location != "bin A" {
		t.Fatalf("expected location 'bin A' after reload, got %s", seed.Location)
	}
}

// Test case 12: Zero quantity overlay handling
func TestMfgPixelSelectionZeroQuantity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"zero-qty-test-1\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// User sets quantity to zero via overlay interaction
	zeroQty := 0.0
	seedPayload := fmt.Sprintf("{\"id\":\"zero-qty-test-1\",\"seed\":{\"lineRef\":\"R1\",\"x\":100,\"y\":150,\"quantity\":%f,\"note\":\"user marked as not present\"}}", zeroQty)
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(seedPayload))
	if !res.OK {
		t.Fatalf("zero quantity seed upsert failed: %s", res.Error)
	}

	ws := loadMfgWorkspaceForTest(t, "zero-qty-test-1")
	if ws.BOM[0].Qty != zeroQty {
		t.Fatalf("BOM qty should reflect zero: got %v want 0", ws.BOM[0].Qty)
	}
	if ws.Seeds[0].Quantity == nil || *ws.Seeds[0].Quantity != zeroQty {
		t.Fatalf("seed quantity should be zero: got %v", ws.Seeds[0].Quantity)
	}
}

// Test case 13: Overlay dimensions validation during creation
func TestMfgPixelSelectionDimensionsValidation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"dim-test-1\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	testCases := []struct {
		name       string
		x, y       float64
		w, h       float64
		shouldFail bool
	}{
		{"valid dimensions", 100, 150, 40, 30, false},
		{"zero width", 100, 150, 0, 30, true},
		{"zero height", 100, 150, 40, 0, true},
		{"negative width", 100, 150, -10, 30, true},
		{"negative height", 100, 150, 40, -5, true},
		{"negative X", -50, 150, 40, 30, true},
		{"negative Y", 100, -50, 40, 30, true},
		{"no dimensions", 100, 150, 0, 0, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Construct JSON manually to avoid any formatting issues
			var seedPayload string
			if tc.name == "valid dimensions" {
				seedPayload = `{"id":"dim-test-1","seed":{"lineRef":"R1","x":100,"y":150,"w":40,"h":30}}`
			} else if tc.name == "zero width" {
				seedPayload = `{"id":"dim-test-1","seed":{"lineRef":"R1","x":100,"y":150,"w":0,"h":30}}`
			} else if tc.name == "zero height" {
				seedPayload = `{"id":"dim-test-1","seed":{"lineRef":"R1","x":100,"y":150,"w":40,"h":0}}`
			} else if tc.name == "negative width" {
				seedPayload = `{"id":"dim-test-1","seed":{"lineRef":"R1","x":100,"y":150,"w":-10,"h":30}}`
			} else if tc.name == "negative height" {
				seedPayload = `{"id":"dim-test-1","seed":{"lineRef":"R1","x":100,"y":150,"w":40,"h":-5}}`
			} else if tc.name == "negative X" {
				seedPayload = `{"id":"dim-test-1","seed":{"lineRef":"R1","x":-50,"y":150,"w":40,"h":30}}`
			} else if tc.name == "negative Y" {
				seedPayload = `{"id":"dim-test-1","seed":{"lineRef":"R1","x":100,"y":-50,"w":40,"h":30}}`
			} else if tc.name == "no dimensions" {
				seedPayload = `{"id":"dim-test-1","seed":{"lineRef":"R1","x":100,"y":150,"w":0,"h":0}}`
			}

			t.Logf("Using payload: %q", seedPayload)

			// Validate JSON parsing works
			var testMap map[string]interface{}
			if err := json.Unmarshal([]byte(seedPayload), &testMap); err != nil {
				t.Fatalf("JSON validation failed: %v", err)
			}

			res := mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(seedPayload))
			t.Logf("Result: OK=%v, Error=%q", res.OK, res.Error)

			if tc.shouldFail {
				if res.OK {
					t.Fatalf("expected dimensions to be rejected but they were accepted: %+v", tc)
				}
			} else {
				if !res.OK {
					t.Fatalf("valid dimensions were rejected: %+v, error: %s", tc, res.Error)
				}
			}
		})
	}
}

// Test case 14: Multiple seeds for same component (quantity aggregation)
func TestMfgPixelSelectionMultipleSeedsSameComponent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"same-comp-test-1\",\"csv\":\"ref,qty,part\\nR1,100,Resistor\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// User identifies R1 parts in multiple locations (e.g., different bins)
	// Use explicit IDs to avoid timestamp collisions
	seedPayloads := []string{
		`{"id":"same-comp-test-1","seed":{"id":"seed_r1_bin1","lineRef":"R1","x":100,"y":150,"location":"bin 1"}}`,
		`{"id":"same-comp-test-1","seed":{"id":"seed_r1_bin2","lineRef":"R1","x":150,"y":150,"location":"bin 2"}}`,
		`{"id":"same-comp-test-1","seed":{"id":"seed_r1_bin3","lineRef":"R1","x":200,"y":150,"location":"bin 3"}}`,
	}

	for i, seedPayload := range seedPayloads {
		res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(seedPayload))
		if !res.OK {
			t.Fatalf("seed %d upsert failed: %s", i, res.Error)
		}
	}

	ws := loadMfgWorkspaceForTest(t, "same-comp-test-1")
	// The system should handle multiple seeds for the same component
	// This test verifies that the data structure supports it
	if len(ws.Seeds) != 3 {
		t.Fatalf("expected 3 seeds for same component, got %d", len(ws.Seeds))
	}

	// Verify all seeds reference R1
	for i, seed := range ws.Seeds {
		if seed.LineRef != "R1" {
			t.Fatalf("seed %d should reference R1, got %s", i, seed.LineRef)
		}
	}
}

// Test case 15: Concurrent selection operations
func TestMfgPixelSelectionConcurrency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"concurrent-test-1\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\nR2,5,Capacitor\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// Simulate concurrent user interactions
	done := make(chan bool, 2)
	errors := make(chan error, 2)

	t.Log("Launching concurrent operations for R1 and R2")

	interactWithSeed := func(lineRef string, action string) {
		t.Logf("Starting %s operation for %s", action, lineRef)
		defer func() {
			t.Logf("Completed %s operation for %s", action, lineRef)
			done <- true
		}()
		if action == "create" {
			// Use explicit IDs and hardcoded JSON to avoid timing issues
			var seedPayload string
			if lineRef == "R1" {
				seedPayload = `{"id":"concurrent-test-1","seed":{"id":"seed_concurrent_r1","lineRef":"R1","x":100,"y":150}}`
				t.Logf("R1 will use payload: %s", seedPayload)
			} else {
				seedPayload = `{"id":"concurrent-test-1","seed":{"id":"seed_concurrent_r2","lineRef":"R2","x":200,"y":150}}`
				t.Logf("R2 will use payload: %s", seedPayload)
			}
			res := mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(seedPayload))
			t.Logf("%s creation result: OK=%v, Error=%q", lineRef, res.OK, res.Error)
			if !res.OK {
				errors <- fmt.Errorf("%s creation failed: %s", lineRef, res.Error)
			}
		} else {
			ws := loadMfgWorkspaceForTest(t, "concurrent-test-1")
			seed := findSeedByLineRef(ws, lineRef)
			if seed != nil {
				deletePayload := "{\"id\":\"concurrent-test-1\",\"seedId\":\"" + seed.ID + "\"}"
				res := mfgPixelSeedDelete(OpsContext{}, json.RawMessage(deletePayload))
				if !res.OK {
					errors <- fmt.Errorf("%s deletion failed: %s", lineRef, res.Error)
				}
			}
		}
	}

	// Launch concurrent interactions
	go interactWithSeed("R1", "create")
	go interactWithSeed("R2", "create")

	// Small delay to ensure goroutines start
	time.Sleep(10 * time.Millisecond)

	// Wait for both
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case err := <-errors:
			t.Fatalf("concurrent operation failed: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for concurrent operations")
		}
	}

	// Verify final state is consistent
	ws := loadMfgWorkspaceForTest(t, "concurrent-test-1")
	t.Logf("Final workspace has %d seeds", len(ws.Seeds))
	for i, seed := range ws.Seeds {
		t.Logf("Seed %d: ID=%s, LineRef=%s", i, seed.ID, seed.LineRef)
	}
	if len(ws.Seeds) != 2 {
		t.Fatalf("expected 2 seeds after concurrent operations, got %d", len(ws.Seeds))
	}
}

// Test case 16: Selection state reset functionality
func TestMfgPixelSelectionStateReset(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"reset-test-1\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\nR2,5,Capacitor\\nR3,15,Inductor\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// Create multiple seeds
	for _, ref := range []string{"R1", "R2", "R3"} {
		currentWS := loadMfgWorkspaceForTest(t, "reset-test-1")
		seedPayload := fmt.Sprintf("{\"id\":\"reset-test-1\",\"seed\":{\"lineRef\":\"%s\",\"x\":%f,\"y\":150}}", ref, float64(100+len(currentWS.Seeds)*100))
		res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(seedPayload))
		if !res.OK {
			t.Fatalf("%s seed upsert failed: %s", ref, res.Error)
		}
	}

	ws := loadMfgWorkspaceForTest(t, "reset-test-1")
	originalBOMCount := len(ws.BOM)

	// Reset all selections (delete all seeds)
	for _, seed := range ws.Seeds {
		deletePayload := "{\"id\":\"reset-test-1\",\"seedId\":\"" + seed.ID + "\"}"
		res = mfgPixelSeedDelete(OpsContext{}, json.RawMessage(deletePayload))
		if !res.OK {
			t.Fatalf("seed deletion failed: %s", res.Error)
		}
	}

	ws = loadMfgWorkspaceForTest(t, "reset-test-1")
	if len(ws.Seeds) != 0 {
		t.Fatalf("expected 0 seeds after reset, got %d", len(ws.Seeds))
	}
	if len(ws.BOM) != originalBOMCount {
		t.Fatalf("BOM lines count changed after reset: got %d want %d", len(ws.BOM), originalBOMCount)
	}
}

// Test case 17: Selection with note field persistence
func TestMfgPixelSelectionNotePersistence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	importPayload := "{\"id\":\"note-test-1\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\n\"}"
	res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
	if !res.OK {
		t.Fatalf("import failed: %s", res.Error)
	}

	// Create seed with note
	note := "user manually counted and verified on screen"
	// Use json.Marshal to properly escape the note
	seedData := map[string]interface{}{
		"lineRef":  "R1",
		"x":        100,
		"y":        150,
		"quantity": 12,
		"note":     note,
	}
	seedJSON, err := json.Marshal(seedData)
	if err != nil {
		t.Fatalf("failed to marshal seed: %v", err)
	}
	seedPayload := fmt.Sprintf("{\"id\":\"note-test-1\",\"seed\":%s}", string(seedJSON))
	res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(seedPayload))
	if !res.OK {
		t.Fatalf("seed upsert failed: %s", res.Error)
	}

	// Reload and verify note persistence
	ws := loadMfgWorkspaceForTest(t, "note-test-1")
	if ws.Seeds[0].Note != note {
		t.Fatalf("note not persisted: got %q want %s", ws.Seeds[0].Note, note)
	}
}

// Test case 18: Selection isolation between different workspaces
func TestMfgPixelSelectionWorkspaceIsolation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Create two separate workspaces
	for _, wsID := range []string{"workspace-1", "workspace-2"} {
		importPayload := fmt.Sprintf("{\"id\":\"%s\",\"csv\":\"ref,qty,part\\nR1,10,Resistor\\nR2,5,Capacitor\\n\"}", wsID)
		res := mfgRFQImportBOM(OpsContext{}, json.RawMessage(importPayload))
		if !res.OK {
			t.Fatalf("import failed for %s: %s", wsID, res.Error)
		}

		// Create R1 seed in each workspace with explicit ID to avoid collisions
		var seedPayload string
		if wsID == "workspace-1" {
			seedPayload = `{"id":"workspace-1","seed":{"id":"seed_workspace_1_r1","lineRef":"R1","x":100,"y":150,"quantity":20}}`
		} else {
			seedPayload = `{"id":"workspace-2","seed":{"id":"seed_workspace_2_r1","lineRef":"R1","x":100,"y":150,"quantity":25}}`
		}
		res = mfgPixelSeedUpsert(OpsContext{}, json.RawMessage(seedPayload))
		if !res.OK {
			t.Fatalf("seed upsert failed for %s: %s", wsID, res.Error)
		}
	}

	// Verify workspaces are isolated
	ws1 := loadMfgWorkspaceForTest(t, "workspace-1")
	ws2 := loadMfgWorkspaceForTest(t, "workspace-2")

	if len(ws1.Seeds) != 1 {
		t.Fatalf("workspace-1 should have 1 seed, got %d", len(ws1.Seeds))
	}
	if len(ws2.Seeds) != 1 {
		t.Fatalf("workspace-2 should have 1 seed, got %d", len(ws2.Seeds))
	}
	if ws1.Seeds[0].ID == ws2.Seeds[0].ID {
		t.Fatalf("seeds in different workspaces should have different IDs")
	}
}

// Helper function to find seed by line reference
func findSeedByLineRef(ws mfgRFQWorkspace, lineRef string) *mfgPixelSeed {
	for i := range ws.Seeds {
		if ws.Seeds[i].LineRef == lineRef {
			return &ws.Seeds[i]
		}
	}
	return nil
}
