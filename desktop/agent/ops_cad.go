package main

// ops_cad.go — remote CAD: an AI agent (or the user) writes OpenSCAD on a remote
// dev box, the box renders it to an STL + a PNG preview, and the preview streams
// back over the mesh so a phone or the web dashboard SEES the model — then the
// same box slices the STL to G-code and hands it to printer_print.
//
// OpenSCAD is script-first and headless, so it is the natural CAD engine for an
// agent: text in → solid out, fully reproducible. This pairs with
// robot_jig_generate (which emits parametric OpenSCAD) and the printer verbs to
// close the loop: describe → code → render → preview → slice → print.
//
// Artifacts live under ~/.yaver/cad/<id>/. cad_get streams an artifact back as
// base64 so a mobile three.js viewer can load the STL without a separate HTTP
// route. Tools (openscad / a slicer) may be absent on a box; cad_tools reports
// availability and every verb degrades to a clear, actionable error.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

func cadRoot() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".yaver", "cad")
}

// cadTool returns the first available executable from candidates, or "".
func cadTool(candidates ...string) string {
	for _, name := range candidates {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

const (
	cadMaxArtifactBytes = 24 << 20 // 24 MB cap for base64-over-mesh transfer
)

type cadPayload struct {
	Scad   string            `json:"scad"`   // OpenSCAD source
	Name   string            `json:"name"`   // friendly name (sanitized for filenames)
	Params map[string]string `json:"params"` // -D name=value overrides
	STL    *bool             `json:"stl"`    // render an STL too (default true)
	ImgW   int               `json:"imgW"`
	ImgH   int               `json:"imgH"`

	// slice inputs
	ModelPath string `json:"modelPath"` // STL/3MF path from a prior cad_render
	Profile   string `json:"profile"`   // slicer profile/config path or name
	Slicer    string `json:"slicer"`    // "orca" | "prusa" | "" (auto)

	// cad_get
	Path string `json:"path"` // artifact path to fetch
}

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

	reg("cad_tools", "Report which CAD/slicer tools are installed on this box (openscad, orca/prusa slicer) + install hints.", func(c OpsContext, _ json.RawMessage) OpsResult {
		osc := cadTool("openscad", "openscad-nightly")
		slicer := cadTool("orca-slicer", "orcaslicer", "OrcaSlicer", "prusa-slicer", "prusa-slicer-console", "PrusaSlicer", "superslicer")
		return OpsResult{OK: true, Initial: map[string]any{
			"openscad":     osc != "",
			"openscadPath": osc,
			"slicer":       slicer != "",
			"slicerPath":   slicer,
			"hints": map[string]string{
				"openscad": "apt install openscad  (or brew install openscad)",
				"slicer":   "install OrcaSlicer or PrusaSlicer; the CLI ships in the app bundle",
			},
		}}
	})

	reg("cad_render", "Render OpenSCAD source to an STL + a PNG preview — payload {scad, name?, params?, stl?, imgW?, imgH?}. Returns the PNG inline as a data: URL plus artifact paths; fetch the STL with cad_get.", func(c OpsContext, payload json.RawMessage) OpsResult {
		var p cadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		if strings.TrimSpace(p.Scad) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "scad source required"}
		}
		osc := cadTool("openscad", "openscad-nightly")
		if osc == "" {
			return OpsResult{OK: false, Code: "no_openscad", Error: "openscad not installed on this box (apt install openscad / brew install openscad)"}
		}
		dir, base, err := cadWriteSource(p)
		if err != nil {
			return OpsResult{OK: false, Code: "io_error", Error: err.Error()}
		}
		scadPath := filepath.Join(dir, base+".scad")
		pngPath := filepath.Join(dir, base+".png")
		imgW, imgH := p.ImgW, p.ImgH
		if imgW <= 0 {
			imgW = 1024
		}
		if imgH <= 0 {
			imgH = 768
		}

		out := map[string]any{"dir": dir, "scadPath": scadPath}

		// PNG preview (rendered, with colors + view-all).
		pngArgs := append(cadDefines(p.Params),
			"-o", pngPath, "--render",
			fmt.Sprintf("--imgsize=%d,%d", imgW, imgH),
			"--viewall", "--autocenter", "--colorscheme=Tomorrow", scadPath)
		if log, err := cadRun(c.Ctx, osc, 90, pngArgs...); err != nil {
			return OpsResult{OK: false, Code: "render_failed", Error: err.Error(), Initial: map[string]any{"log": log}}
		}
		if b, err := os.ReadFile(pngPath); err == nil {
			out["pngPath"] = pngPath
			out["preview"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
		}

		// STL solid (default on).
		if p.STL == nil || *p.STL {
			stlPath := filepath.Join(dir, base+".stl")
			stlArgs := append(cadDefines(p.Params), "-o", stlPath, scadPath)
			if log, err := cadRun(c.Ctx, osc, 180, stlArgs...); err != nil {
				return OpsResult{OK: false, Code: "render_failed", Error: err.Error(), Initial: map[string]any{"log": log}}
			}
			if fi, err := os.Stat(stlPath); err == nil {
				out["stlPath"] = stlPath
				out["stlBytes"] = fi.Size()
			}
		}
		return OpsResult{OK: true, Initial: out}
	})

	reg("cad_preview", "Fast PNG preview of OpenSCAD source (no STL) — payload {scad, params?, imgW?, imgH?}. Returns a data: URL.", func(c OpsContext, payload json.RawMessage) OpsResult {
		var p cadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		if strings.TrimSpace(p.Scad) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "scad source required"}
		}
		osc := cadTool("openscad", "openscad-nightly")
		if osc == "" {
			return OpsResult{OK: false, Code: "no_openscad", Error: "openscad not installed on this box"}
		}
		dir, base, err := cadWriteSource(p)
		if err != nil {
			return OpsResult{OK: false, Code: "io_error", Error: err.Error()}
		}
		scadPath := filepath.Join(dir, base+".scad")
		pngPath := filepath.Join(dir, base+".png")
		imgW, imgH := p.ImgW, p.ImgH
		if imgW <= 0 {
			imgW = 800
		}
		if imgH <= 0 {
			imgH = 600
		}
		args := append(cadDefines(p.Params), "-o", pngPath,
			fmt.Sprintf("--imgsize=%d,%d", imgW, imgH),
			"--viewall", "--autocenter", "--colorscheme=Tomorrow", scadPath)
		if log, err := cadRun(c.Ctx, osc, 60, args...); err != nil {
			return OpsResult{OK: false, Code: "render_failed", Error: err.Error(), Initial: map[string]any{"log": log}}
		}
		b, err := os.ReadFile(pngPath)
		if err != nil {
			return OpsResult{OK: false, Code: "render_failed", Error: "no preview produced"}
		}
		return OpsResult{OK: true, Initial: map[string]any{
			"preview": "data:image/png;base64," + base64.StdEncoding.EncodeToString(b),
			"pngPath": pngPath,
		}}
	})

	reg("cad_slice", "Slice an STL/3MF to printer-ready G-code/3MF — payload {modelPath, slicer?, profile?}. Uses OrcaSlicer/PrusaSlicer CLI on the box. Returns the output path for printer_upload + printer_print.", func(c OpsContext, payload json.RawMessage) OpsResult {
		var p cadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		if strings.TrimSpace(p.ModelPath) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "modelPath required (run cad_render first)"}
		}
		if _, err := os.Stat(p.ModelPath); err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: "modelPath not found: " + err.Error()}
		}
		orca := cadTool("orca-slicer", "orcaslicer", "OrcaSlicer")
		prusa := cadTool("prusa-slicer", "prusa-slicer-console", "PrusaSlicer", "superslicer")
		dir := filepath.Dir(p.ModelPath)
		stem := strings.TrimSuffix(filepath.Base(p.ModelPath), filepath.Ext(p.ModelPath))

		var bin, outPath string
		var args []string
		switch {
		case (p.Slicer == "orca" || p.Slicer == "") && orca != "":
			bin = orca
			outPath = filepath.Join(dir, stem+".gcode.3mf")
			args = []string{"--export-3mf", outPath, "--slice", "0"}
			if p.Profile != "" {
				args = append(args, "--load-settings", p.Profile)
			}
			args = append(args, p.ModelPath)
		case prusa != "":
			bin = prusa
			outPath = filepath.Join(dir, stem+".gcode")
			args = []string{"--export-gcode", "-o", outPath}
			if p.Profile != "" {
				args = append(args, "--load", p.Profile)
			}
			args = append(args, p.ModelPath)
		default:
			return OpsResult{OK: false, Code: "no_slicer", Error: "no slicer CLI on this box (install OrcaSlicer or PrusaSlicer)"}
		}
		log, err := cadRun(c.Ctx, bin, 240, args...)
		if err != nil {
			return OpsResult{OK: false, Code: "slice_failed", Error: err.Error(), Initial: map[string]any{"log": log}}
		}
		fi, statErr := os.Stat(outPath)
		if statErr != nil {
			return OpsResult{OK: false, Code: "slice_failed", Error: "slicer produced no output", Initial: map[string]any{"log": log}}
		}
		return OpsResult{OK: true, Initial: map[string]any{"outputPath": outPath, "bytes": fi.Size(), "slicer": filepath.Base(bin)}}
	})

	reg("cad_get", "Fetch a CAD artifact (STL/PNG/3MF/gcode) as base64 over the mesh — payload {path}. Path must be under ~/.yaver/cad/. Capped at 24 MB.", func(c OpsContext, payload json.RawMessage) OpsResult {
		var p cadPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		clean := filepath.Clean(p.Path)
		root := cadRoot()
		if !strings.HasPrefix(clean, root+string(os.PathSeparator)) {
			return OpsResult{OK: false, Code: "forbidden", Error: "path must be under " + root}
		}
		fi, err := os.Stat(clean)
		if err != nil {
			return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
		}
		if fi.Size() > cadMaxArtifactBytes {
			return OpsResult{OK: false, Code: "too_large", Error: fmt.Sprintf("artifact is %d bytes (> %d cap)", fi.Size(), cadMaxArtifactBytes)}
		}
		b, err := os.ReadFile(clean)
		if err != nil {
			return OpsResult{OK: false, Code: "io_error", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{
			"name":   filepath.Base(clean),
			"bytes":  len(b),
			"base64": base64.StdEncoding.EncodeToString(b),
			"mime":   cadMime(clean),
		}}
	})
}

// cadWriteSource creates a per-render dir and writes the .scad source.
func cadWriteSource(p cadPayload) (dir, base string, err error) {
	base = sanitizeName(p.Name)
	if base == "" {
		base = "model"
	}
	dir = filepath.Join(cadRoot(), uuid.NewString())
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	if err = os.WriteFile(filepath.Join(dir, base+".scad"), []byte(p.Scad), 0o644); err != nil {
		return "", "", err
	}
	return dir, base, nil
}

func cadDefines(params map[string]string) []string {
	var args []string
	for k, v := range params {
		args = append(args, "-D", k+"="+v)
	}
	return args
}

func cadRun(ctx context.Context, bin string, timeoutSec int, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	out, err := cmd.CombinedOutput()
	log := strings.TrimSpace(string(out))
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return log, fmt.Errorf("%s timed out after %ds", filepath.Base(bin), timeoutSec)
		}
		return log, fmt.Errorf("%s failed: %v", filepath.Base(bin), err)
	}
	return log, nil
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}

func cadMime(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".stl":
		return "model/stl"
	case ".3mf":
		return "model/3mf"
	case ".gcode":
		return "text/x.gcode"
	default:
		return "application/octet-stream"
	}
}
