package main

// ops_screwcell.go — Screw-driving cell: per terminal-block PASS/FAIL records +
// shop-floor analytics. The electrical/mechanical sibling of ops_circuit.go: the
// cell (Ender + BTS7960 screwdriver) records each block here, and the host model
// gets totals, a daily fail-rate trend, per-block + flagged-production-order
// breakdowns, and a per-order rollup that auto-counts driven screws against a
// work order.
//
// Like circuit netlists, runs are local work data — stored in the vault
// ("screw-cell"/"runs") with a ~/.yaver/screw-cell.json fallback. NEVER Convex.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const screwVaultProject = "screw-cell"
const screwVaultRunsName = "runs"

func screwCellFilePath() string {
	home, _ := os.UserHomeDir()
	return home + "/.yaver/screw-cell.json"
}

type screwRun struct {
	ID        string           `json:"id"`
	Label     string           `json:"label,omitempty"`
	Ficheno   string           `json:"ficheno,omitempty"`
	ProductID string           `json:"productId,omitempty"`
	Host      string           `json:"host,omitempty"`
	Screws    int              `json:"screws"`
	Passed    int              `json:"passed"`
	Flagged   bool             `json:"flagged"`
	Results   []map[string]any `json:"results,omitempty"`
	CreatedAt int64            `json:"createdAt"`
}

var screwMu sync.Mutex

func screwRunsLoad() []screwRun {
	// vault first, ~/.yaver fallback (mirrors circuitConfigGet)
	if vs, err := openVaultOptional(); err == nil {
		if e, gerr := vs.Get(screwVaultProject, screwVaultRunsName); gerr == nil && e != nil && e.Value != "" {
			var runs []screwRun
			if json.Unmarshal([]byte(e.Value), &runs) == nil {
				return runs
			}
		}
	}
	if b, err := os.ReadFile(screwCellFilePath()); err == nil {
		var runs []screwRun
		if json.Unmarshal(b, &runs) == nil {
			return runs
		}
	}
	return nil
}

func screwRunsSave(runs []screwRun) error {
	b, _ := json.Marshal(runs)
	if vs, err := openVaultOptional(); err == nil {
		if serr := vs.Set(VaultEntry{Project: screwVaultProject, Name: screwVaultRunsName, Category: "custom", Value: string(b), Notes: "Yaver screw-cell PASS/FAIL runs"}); serr == nil {
			return nil
		}
	}
	return os.WriteFile(screwCellFilePath(), b, 0o600)
}

func screwFailRate(passed, screws int) float64 {
	if screws == 0 {
		return 0
	}
	return float64(int((1-float64(passed)/float64(screws))*1000+0.5)) / 10
}

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

	reg("screw_cell_record", "Record a screw-driving cell run (a terminal block's PASS/FAIL). {label?, ficheno?, productId?, screws, passed, results?[], host?}. Flags the block if any screw failed.", func(c OpsContext, payload json.RawMessage) OpsResult {
		var in screwRun
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &in)
		}
		if in.Screws <= 0 {
			return OpsResult{OK: false, Code: "bad_payload", Error: "screws required"}
		}
		if in.Passed > in.Screws {
			in.Passed = in.Screws
		}
		in.ID = fmt.Sprintf("r_%d", time.Now().UnixNano())
		in.Flagged = in.Passed < in.Screws
		in.CreatedAt = time.Now().UnixMilli()
		screwMu.Lock()
		defer screwMu.Unlock()
		runs := append(screwRunsLoad(), in)
		if err := screwRunsSave(runs); err != nil {
			return OpsResult{OK: false, Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"id": in.ID, "flagged": in.Flagged, "failRate": screwFailRate(in.Passed, in.Screws)}}
	})

	reg("screw_cell_runs", "List recent screw-cell runs (PASS/FAIL per block). {limit?:50}", func(c OpsContext, payload json.RawMessage) OpsResult {
		var in struct {
			Limit int `json:"limit"`
		}
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &in)
		}
		if in.Limit <= 0 || in.Limit > 200 {
			in.Limit = 50
		}
		screwMu.Lock()
		runs := screwRunsLoad()
		screwMu.Unlock()
		sort.Slice(runs, func(a, b int) bool { return runs[a].CreatedAt > runs[b].CreatedAt })
		if len(runs) > in.Limit {
			runs = runs[:in.Limit]
		}
		out := make([]map[string]any, 0, len(runs))
		for _, r := range runs {
			out = append(out, map[string]any{"id": r.ID, "label": r.Label, "ficheno": r.Ficheno, "screws": r.Screws, "passed": r.Passed, "flagged": r.Flagged, "failRate": screwFailRate(r.Passed, r.Screws), "host": r.Host, "createdAt": r.CreatedAt})
		}
		return OpsResult{OK: true, Initial: map[string]any{"runs": out}}
	})

	reg("screw_cell_by_order", "Screw-cell rollup for one production order (ficheno): blocks done/flagged, screws driven/passed, fail rate — the auto-count against a work order. {ficheno}", func(c OpsContext, payload json.RawMessage) OpsResult {
		var in struct {
			Ficheno string `json:"ficheno"`
		}
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &in)
		}
		if strings.TrimSpace(in.Ficheno) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "ficheno required"}
		}
		screwMu.Lock()
		all := screwRunsLoad()
		screwMu.Unlock()
		screws, passed, flagged, blocks := 0, 0, 0, 0
		for _, r := range all {
			if r.Ficheno != in.Ficheno {
				continue
			}
			blocks++
			screws += r.Screws
			passed += r.Passed
			if r.Flagged {
				flagged++
			}
		}
		return OpsResult{OK: true, Initial: map[string]any{
			"ficheno": in.Ficheno, "blocks": blocks, "blocksFlagged": flagged,
			"screws": screws, "passed": passed, "failed": screws - passed, "failRate": screwFailRate(passed, screws),
		}}
	})

	reg("screw_cell_analytics", "Screw-cell shop-floor analytics over the last N days (default 30): totals, daily fail-rate trend, per-block breakdown (worst first), flagged production orders, recent runs. {days?:30}", func(c OpsContext, payload json.RawMessage) OpsResult {
		var in struct {
			Days int `json:"days"`
		}
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &in)
		}
		if in.Days <= 0 {
			in.Days = 30
		}
		since := time.Now().UnixMilli() - int64(in.Days)*86_400_000
		screwMu.Lock()
		all := screwRunsLoad()
		screwMu.Unlock()

		type agg struct {
			runs, screws, passed, flagged int
			productID                     string
			lastAt                        int64
		}
		var totS, totP, totRuns int
		byLabel := map[string]*agg{}
		byDay := map[string]*agg{}
		byFiche := map[string]*agg{}
		var recent []screwRun
		for _, r := range all {
			if r.CreatedAt < since {
				continue
			}
			totRuns++
			totS += r.Screws
			totP += r.Passed
			recent = append(recent, r)
			lab := r.Label
			if lab == "" {
				lab = "(unlabelled)"
			}
			if byLabel[lab] == nil {
				byLabel[lab] = &agg{}
			}
			byLabel[lab].runs++
			byLabel[lab].screws += r.Screws
			byLabel[lab].passed += r.Passed
			day := time.UnixMilli(r.CreatedAt).UTC().Format("2006-01-02")
			if byDay[day] == nil {
				byDay[day] = &agg{}
			}
			byDay[day].screws += r.Screws
			byDay[day].passed += r.Passed
			if r.Ficheno != "" {
				if byFiche[r.Ficheno] == nil {
					byFiche[r.Ficheno] = &agg{productID: r.ProductID}
				}
				f := byFiche[r.Ficheno]
				f.runs++
				f.screws += r.Screws
				f.passed += r.Passed
				if r.Flagged {
					f.flagged++
				}
				if r.CreatedAt > f.lastAt {
					f.lastAt = r.CreatedAt
				}
			}
		}
		dayKeys := make([]string, 0, len(byDay))
		for d := range byDay {
			dayKeys = append(dayKeys, d)
		}
		sort.Strings(dayKeys)
		trend := make([]map[string]any, 0, len(dayKeys))
		for _, d := range dayKeys {
			trend = append(trend, map[string]any{"date": d, "screws": byDay[d].screws, "failRate": screwFailRate(byDay[d].passed, byDay[d].screws)})
		}
		labels := make([]map[string]any, 0, len(byLabel))
		for k, v := range byLabel {
			labels = append(labels, map[string]any{"label": k, "runs": v.runs, "screws": v.screws, "passed": v.passed, "failRate": screwFailRate(v.passed, v.screws)})
		}
		sort.Slice(labels, func(a, b int) bool { return labels[a]["failRate"].(float64) > labels[b]["failRate"].(float64) })
		flaggedOrders := make([]map[string]any, 0)
		for k, v := range byFiche {
			if v.flagged > 0 {
				flaggedOrders = append(flaggedOrders, map[string]any{"ficheno": k, "productId": v.productID, "blocks": v.runs, "flaggedBlocks": v.flagged, "screws": v.screws, "failed": v.screws - v.passed, "failRate": screwFailRate(v.passed, v.screws), "lastAt": v.lastAt})
			}
		}
		sort.Slice(flaggedOrders, func(a, b int) bool { return flaggedOrders[a]["failRate"].(float64) > flaggedOrders[b]["failRate"].(float64) })
		sort.Slice(recent, func(a, b int) bool { return recent[a].CreatedAt > recent[b].CreatedAt })
		if len(recent) > 10 {
			recent = recent[:10]
		}
		recentOut := make([]map[string]any, 0, len(recent))
		for _, r := range recent {
			recentOut = append(recentOut, map[string]any{"id": r.ID, "label": r.Label, "ficheno": r.Ficheno, "screws": r.Screws, "passed": r.Passed, "failRate": screwFailRate(r.Passed, r.Screws), "createdAt": r.CreatedAt})
		}
		return OpsResult{OK: true, Initial: map[string]any{
			"window": map[string]any{"days": in.Days},
			"totals": map[string]any{"runs": totRuns, "screws": totS, "passed": totP, "failed": totS - totP, "failRate": screwFailRate(totP, totS)},
			"trend":  trend, "byLabel": labels, "flaggedOrders": flaggedOrders, "recent": recentOut,
		}}
	})
}
