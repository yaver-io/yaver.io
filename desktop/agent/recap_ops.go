package main

import (
	"context"
	"encoding/json"
	"strings"
)

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "recap_list",
		Description: "List recaps by autorun, slot, tag, and recency. Returns metadata plus HTTP routes for the bytes; never returns local paths or artifact bytes.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"autorun": map[string]interface{}{"type": "string"},
				"slot":    map[string]interface{}{"type": "string"},
				"tag":     map[string]interface{}{"type": "string"},
				"limit":   map[string]interface{}{"type": "integer", "minimum": 0},
			},
			"additionalProperties": false,
		},
		Handler:    opsRecapListHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "recap_show",
		Description: "Show one recap's metadata and the HTTP routes to fetch its video, poster, and subtitles.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"recapId"},
			"properties": map[string]interface{}{
				"recapId": map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsRecapShowHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "recap_build",
		Description: "Build one recap now. Owner-only because encoding spends CPU, disk, and optional narration tokens. Returns recap metadata and the HTTP routes for artifacts.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": true,
		},
		Handler:    opsRecapBuildHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "recap_delete",
		Description: "Delete one recap by id.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"recapId"},
			"properties": map[string]interface{}{
				"recapId": map[string]interface{}{"type": "string"},
			},
			"additionalProperties": false,
		},
		Handler:    opsRecapDeleteHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "recap_config_get",
		Description: "Read recap auto-build and retention config.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsRecapConfigGetHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "recap_config_set",
		Description: "Update recap auto-build and retention config.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"additionalProperties": true,
		},
		Handler:    opsRecapConfigSetHandler,
		AllowGuest: false,
	})
}

type recapOpsListPayload struct {
	Autorun string `json:"autorun"`
	Slot    string `json:"slot"`
	Tag     string `json:"tag"`
	Limit   int    `json:"limit"`
}

func opsRecapListHandler(_ OpsContext, raw json.RawMessage) OpsResult {
	var p recapOpsListPayload
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	recs, err := listRecaps(RecapFilter{
		AutorunID: strings.TrimSpace(p.Autorun),
		Slot:      strings.TrimSpace(p.Slot),
		Tag:       strings.TrimSpace(p.Tag),
		Limit:     p.Limit,
	})
	if err != nil {
		return OpsResult{OK: false, Code: "list_failed", Error: err.Error()}
	}
	out := make([]map[string]interface{}, 0, len(recs))
	for _, rec := range recs {
		out = append(out, recapOpsView(rec))
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"recaps": out}}
}

func opsRecapShowHandler(_ OpsContext, raw json.RawMessage) OpsResult {
	var p struct {
		RecapID string `json:"recapId"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	rec, err := loadRecap(strings.TrimSpace(p.RecapID))
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: recapOpsView(rec)}
}

func opsRecapBuildHandler(_ OpsContext, raw json.RawMessage) OpsResult {
	var opts RecapBuildOpts
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &opts); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	rec, err := BuildRecap(context.Background(), opts)
	if err != nil {
		code := "build_failed"
		if strings.Contains(err.Error(), "ffmpeg not found") {
			code = "dependency_missing"
		}
		return OpsResult{OK: false, Code: code, Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: recapOpsView(rec)}
}

func opsRecapDeleteHandler(_ OpsContext, raw json.RawMessage) OpsResult {
	var p struct {
		RecapID string `json:"recapId"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if err := deleteRecap(strings.TrimSpace(p.RecapID)); err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"deleted": true, "recapId": strings.TrimSpace(p.RecapID)}}
}

func opsRecapConfigGetHandler(_ OpsContext, _ json.RawMessage) OpsResult {
	return OpsResult{OK: true, Initial: map[string]interface{}{"config": loadRecapConfig()}}
}

func opsRecapConfigSetHandler(_ OpsContext, raw json.RawMessage) OpsResult {
	var cfg RecapConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if err := saveRecapConfig(cfg); err != nil {
		return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"config": loadRecapConfig()}}
}

func recapOpsView(rec *RecapRecord) map[string]interface{} {
	out := map[string]interface{}{
		"recapId":             rec.ID,
		"autorunId":           rec.AutorunID,
		"slot":                recapSlotLabel(rec.Slot),
		"task":                rec.Task,
		"tag":                 rec.Tag,
		"title":               rec.Title,
		"status":              rec.Status,
		"createdAt":           rec.CreatedAt,
		"durationSec":         rec.DurationSec,
		"sizeBytes":           rec.SizeBytes,
		"frames":              rec.Frames,
		"display":             rec.Display,
		"hasAudio":            rec.HasAudio,
		"hasSubtitles":        rec.HasSubtitles,
		"finishReason":        rec.FinishReason,
		"iterations":          rec.Iterations,
		"commits":             rec.Commits,
		"finalCommit":         rec.FinalCommit,
		"landed":              rec.Landed,
		"complete":            rec.Complete,
		"priorityCount":       rec.PriorityCount,
		"evidencedPriorities": rec.EvidencedPriorities,
		"heals":               rec.Heals,
		"route":               "/recap/" + rec.ID,
		"videoRoute":          "/recap/" + rec.ID + "/video",
		"posterRoute":         "/recap/" + rec.ID + "/poster",
	}
	if rec.Error != "" {
		out["error"] = rec.Error
	}
	if rec.HasSubtitles {
		out["subtitlesRoute"] = "/recap/" + rec.ID + "/subtitles.vtt"
	}
	if rec.Voice != "" {
		out["voice"] = rec.Voice
	}
	return out
}
