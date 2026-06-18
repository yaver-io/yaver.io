package main

// ops_mfg.go - local-first manufacturing RFQ/BOM assist verbs.
//
// Talos remains the long-term record/UI plane for quotes, supplier RFQs, and
// orders. These verbs give Yaver a concrete, shared contract today: web, mobile,
// CLI, and MCP can all edit a BOM-backed RFQ assist workspace through /ops.

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// mfgRFQMutex protects concurrent access to workspace files
var mfgRFQMutex sync.Mutex

type mfgRFQWorkspace struct {
	ID        string            `json:"id"`
	Name      string            `json:"name,omitempty"`
	BOM       []mfgBOMLine      `json:"bom"`
	Seeds     []mfgPixelSeed    `json:"seeds"`
	UpdatedAt int64             `json:"updatedAt"`
	Meta      map[string]string `json:"meta,omitempty"`
}

type mfgBOMLine struct {
	Ref         string            `json:"ref"`
	Qty         float64           `json:"qty"`
	Part        string            `json:"part,omitempty"`
	Description string            `json:"description,omitempty"`
	Package     string            `json:"package,omitempty"`
	SupplierPN  string            `json:"supplierPn,omitempty"`
	LCSC        string            `json:"lcsc,omitempty"`
	UnitUSD     float64           `json:"unitUsd,omitempty"`
	Location    string            `json:"location,omitempty"`
	Attrs       map[string]string `json:"attrs,omitempty"`
}

type mfgPixelSeed struct {
	ID        string   `json:"id"`
	LineRef   string   `json:"lineRef"`
	X         *float64 `json:"x,omitempty"`
	Y         *float64 `json:"y,omitempty"`
	W         *float64 `json:"w,omitempty"`
	H         *float64 `json:"h,omitempty"`
	Location  string   `json:"location,omitempty"`
	Quantity  *float64 `json:"quantity,omitempty"`
	Note      string   `json:"note,omitempty"`
	UpdatedAt int64    `json:"updatedAt"`
}

type mfgPayload struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	CSV      string            `json:"csv"`
	Path     string            `json:"path"`
	LineRef  string            `json:"lineRef"`
	SeedID   string            `json:"seedId"`
	Line     *mfgBOMLine       `json:"line"`
	Seed     *mfgPixelSeed     `json:"seed"`
	Quantity *float64          `json:"quantity"`
	Location *string           `json:"location"`
	Meta     map[string]string `json:"meta"`
}

func init() {
	reg := func(name, desc string, schema map[string]interface{}, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Schema: atvSchema(schema), Handler: h, AllowGuest: false})
	}
	reg("mfg_rfq_import_bom", "Create/update a local RFQ workspace from BOM CSV. Payload {id?, name?, csv? | path?, meta?}.", map[string]interface{}{
		"id":   map[string]interface{}{"type": "string"},
		"name": map[string]interface{}{"type": "string"},
		"csv":  map[string]interface{}{"type": "string", "description": "BOM CSV text."},
		"path": map[string]interface{}{"type": "string", "description": "Local BOM CSV path on this machine."},
	}, mfgRFQImportBOM)
	reg("mfg_rfq_get", "Read a local RFQ workspace with BOM lines and manual pixel seeds. Payload {id}.", map[string]interface{}{
		"id": map[string]interface{}{"type": "string"},
	}, mfgRFQGet)
	reg("mfg_bom_line_update", "Update one BOM line. Quantity/location edits are canonical for RFQ assist. Payload {id,lineRef,line? | quantity?,location?}.", map[string]interface{}{
		"id":       map[string]interface{}{"type": "string"},
		"lineRef":  map[string]interface{}{"type": "string"},
		"quantity": map[string]interface{}{"type": "number"},
		"location": map[string]interface{}{"type": "string"},
	}, mfgBOMLineUpdate)
	reg("mfg_pixel_seed_upsert", "Add/update a manual-assist pixel seed and sync quantity/location into its BOM line. Payload {id,seed:{id?,lineRef,x,y,w,h,quantity,location,note}}.", map[string]interface{}{
		"id":   map[string]interface{}{"type": "string"},
		"seed": map[string]interface{}{"type": "object"},
	}, mfgPixelSeedUpsert)
	reg("mfg_pixel_seed_delete", "Remove a manual-assist pixel seed. Payload {id,seedId}. Does not delete the BOM line.", map[string]interface{}{
		"id":     map[string]interface{}{"type": "string"},
		"seedId": map[string]interface{}{"type": "string"},
	}, mfgPixelSeedDelete)
}

func mfgRFQImportBOM(_ OpsContext, raw json.RawMessage) OpsResult {
	p, err := parseMfgPayload(raw)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	id := mfgSafeID(firstNonEmptyStr(p.ID, p.Name, "default"))
	if id == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "id or name required"}
	}
	text := strings.TrimSpace(p.CSV)
	if text == "" && strings.TrimSpace(p.Path) != "" {
		b, err := os.ReadFile(p.Path)
		if err != nil {
			return OpsResult{OK: false, Code: "read_failed", Error: err.Error()}
		}
		text = string(b)
	}
	if strings.TrimSpace(text) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "csv or path required"}
	}
	lines, err := parseMfgBOMCSV(text)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	existing, _ := mfgRFQLoad(id)
	ws := mfgRFQWorkspace{ID: id, Name: p.Name, BOM: lines, Seeds: existing.Seeds, Meta: p.Meta, UpdatedAt: time.Now().UnixMilli()}
	syncAllSeedsToBOM(&ws)
	if err := mfgRFQSave(ws); err != nil {
		return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]any{"workspace": ws, "bomCount": len(ws.BOM), "seedCount": len(ws.Seeds)}}
}

func mfgRFQGet(_ OpsContext, raw json.RawMessage) OpsResult {
	p, err := parseMfgPayload(raw)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	ws, err := mfgRFQLoad(mfgSafeID(p.ID))
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]any{"workspace": ws, "bomCount": len(ws.BOM), "seedCount": len(ws.Seeds)}}
}

func mfgBOMLineUpdate(_ OpsContext, raw json.RawMessage) OpsResult {
	p, err := parseMfgPayload(raw)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	ws, err := mfgRFQLoad(mfgSafeID(p.ID))
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	idx := findBOMLine(ws.BOM, p.LineRef)
	if idx < 0 {
		return OpsResult{OK: false, Code: "not_found", Error: "BOM line not found: " + p.LineRef}
	}
	if p.Line != nil {
		p.Line.Ref = firstNonEmptyStr(p.Line.Ref, ws.BOM[idx].Ref)
		ws.BOM[idx] = *p.Line
	}
	if p.Quantity != nil {
		if *p.Quantity < 0 {
			return OpsResult{OK: false, Code: "bad_payload", Error: "quantity must be >= 0"}
		}
		ws.BOM[idx].Qty = *p.Quantity
		syncBOMQuantityToSeeds(&ws, ws.BOM[idx].Ref, *p.Quantity)
	}
	if p.Location != nil {
		ws.BOM[idx].Location = strings.TrimSpace(*p.Location)
		syncBOMLocationToSeeds(&ws, ws.BOM[idx].Ref, ws.BOM[idx].Location)
	}
	ws.UpdatedAt = time.Now().UnixMilli()
	if err := mfgRFQSave(ws); err != nil {
		return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]any{"workspace": ws, "line": ws.BOM[idx]}}
}

func mfgPixelSeedUpsert(_ OpsContext, raw json.RawMessage) OpsResult {
	p, err := parseMfgPayload(raw)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Seed == nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: "seed required"}
	}
	ws, err := mfgRFQLoad(mfgSafeID(p.ID))
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	seed := *p.Seed
	seed.LineRef = strings.TrimSpace(seed.LineRef)
	if seed.LineRef == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "seed.lineRef required"}
	}
	lineIdx := findBOMLine(ws.BOM, seed.LineRef)
	if lineIdx < 0 {
		return OpsResult{OK: false, Code: "not_found", Error: "BOM line not found: " + seed.LineRef}
	}
	if seed.Quantity != nil && *seed.Quantity < 0 {
		return OpsResult{OK: false, Code: "bad_payload", Error: "seed.quantity must be >= 0"}
	}
	if strings.TrimSpace(seed.ID) == "" {
		seed.ID = "seed_" + strings.ToLower(mfgSafeID(seed.LineRef)) + "_" + strconv.FormatInt(time.Now().UnixMilli(), 36)
	}
	seed.UpdatedAt = time.Now().UnixMilli()
	seedIdx := findPixelSeed(ws.Seeds, seed.ID)
	if seedIdx >= 0 {
		ws.Seeds[seedIdx] = seed
	} else {
		ws.Seeds = append(ws.Seeds, seed)
	}
	syncSeedToBOM(&ws, seed)
	ws.UpdatedAt = seed.UpdatedAt
	if err := mfgRFQSave(ws); err != nil {
		return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]any{"workspace": ws, "seed": seed, "line": ws.BOM[lineIdx]}}
}

func mfgPixelSeedDelete(_ OpsContext, raw json.RawMessage) OpsResult {
	p, err := parseMfgPayload(raw)
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	ws, err := mfgRFQLoad(mfgSafeID(p.ID))
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	idx := findPixelSeed(ws.Seeds, p.SeedID)
	if idx < 0 {
		return OpsResult{OK: false, Code: "not_found", Error: "seed not found: " + p.SeedID}
	}
	removed := ws.Seeds[idx]
	ws.Seeds = append(ws.Seeds[:idx], ws.Seeds[idx+1:]...)
	ws.UpdatedAt = time.Now().UnixMilli()
	if err := mfgRFQSave(ws); err != nil {
		return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]any{"workspace": ws, "removed": removed}}
}

func parseMfgPayload(raw json.RawMessage) (mfgPayload, error) {
	var p mfgPayload
	if len(raw) == 0 {
		return p, fmt.Errorf("payload required")
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

func parseMfgBOMCSV(text string) ([]mfgBOMLine, error) {
	r := csv.NewReader(strings.NewReader(text))
	r.TrimLeadingSpace = true
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("BOM CSV needs a header and at least one row")
	}
	header := map[string]int{}
	for i, h := range rows[0] {
		header[normBOMHeader(h)] = i
	}
	get := func(row []string, names ...string) string {
		for _, name := range names {
			if idx, ok := header[normBOMHeader(name)]; ok && idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
		}
		return ""
	}
	out := make([]mfgBOMLine, 0, len(rows)-1)
	for _, row := range rows[1:] {
		ref := get(row, "ref", "reference", "designator")
		if ref == "" {
			continue
		}
		qty := parseBOMFloat(get(row, "qty", "quantity"))
		unit := parseBOMFloat(get(row, "unit_usd", "unit usd", "price", "unit_price"))
		line := mfgBOMLine{
			Ref:         ref,
			Qty:         qty,
			Part:        get(row, "part", "value/part", "value", "mpn"),
			Description: get(row, "description", "desc"),
			Package:     get(row, "package", "footprint"),
			SupplierPN:  get(row, "supplier_pn", "supplier pn", "supplier"),
			LCSC:        get(row, "lcsc", "lcsc_part"),
			UnitUSD:     unit,
			Location:    get(row, "location", "block"),
			Attrs:       map[string]string{},
		}
		for key, idx := range header {
			if idx < len(row) && line.Attrs[key] == "" {
				line.Attrs[key] = strings.TrimSpace(row[idx])
			}
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no BOM lines parsed")
	}
	return out, nil
}

func normBOMHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

func mfgSafeID(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), ".-_")
}

func parseBOMFloat(s string) float64 {
	s = strings.TrimSpace(strings.TrimPrefix(s, "~"))
	if s == "" || s == "-" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func findBOMLine(lines []mfgBOMLine, ref string) int {
	ref = strings.TrimSpace(ref)
	for i, line := range lines {
		if strings.EqualFold(line.Ref, ref) {
			return i
		}
	}
	return -1
}

func findPixelSeed(seeds []mfgPixelSeed, id string) int {
	id = strings.TrimSpace(id)
	for i, seed := range seeds {
		if seed.ID == id {
			return i
		}
	}
	return -1
}

func syncSeedToBOM(ws *mfgRFQWorkspace, seed mfgPixelSeed) {
	idx := findBOMLine(ws.BOM, seed.LineRef)
	if idx < 0 {
		return
	}
	if seed.Quantity != nil {
		ws.BOM[idx].Qty = *seed.Quantity
	}
	if strings.TrimSpace(seed.Location) != "" {
		ws.BOM[idx].Location = strings.TrimSpace(seed.Location)
	}
}

func syncAllSeedsToBOM(ws *mfgRFQWorkspace) {
	for _, seed := range ws.Seeds {
		syncSeedToBOM(ws, seed)
	}
}

func syncBOMQuantityToSeeds(ws *mfgRFQWorkspace, ref string, qty float64) {
	for i := range ws.Seeds {
		if strings.EqualFold(ws.Seeds[i].LineRef, ref) {
			ws.Seeds[i].Quantity = &qty
			ws.Seeds[i].UpdatedAt = time.Now().UnixMilli()
		}
	}
}

func syncBOMLocationToSeeds(ws *mfgRFQWorkspace, ref, location string) {
	for i := range ws.Seeds {
		if strings.EqualFold(ws.Seeds[i].LineRef, ref) {
			ws.Seeds[i].Location = location
			ws.Seeds[i].UpdatedAt = time.Now().UnixMilli()
		}
	}
}

func mfgRFQDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yaver", "mfg-rfq")
}

func mfgRFQPath(id string) string {
	return filepath.Join(mfgRFQDir(), mfgSafeID(id)+".json")
}

func mfgRFQLoad(id string) (mfgRFQWorkspace, error) {
	var ws mfgRFQWorkspace
	if strings.TrimSpace(id) == "" {
		return ws, fmt.Errorf("id required")
	}
	b, err := os.ReadFile(mfgRFQPath(id))
	if err != nil {
		return ws, fmt.Errorf("RFQ workspace %q not found", id)
	}
	return ws, json.Unmarshal(b, &ws)
}

func mfgRFQSave(ws mfgRFQWorkspace) error {
	if strings.TrimSpace(ws.ID) == "" {
		return fmt.Errorf("workspace id required")
	}
	if err := os.MkdirAll(mfgRFQDir(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(ws, "", "  ")
	return os.WriteFile(mfgRFQPath(ws.ID), b, 0o600)
}
