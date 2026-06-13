package main

// ops_printer.go — 3D printers as native ops verbs, the third device cell after
// ops_robot.go (Cartesian/Marlin) and ops_armcell.go (multi-DOF cobots). One
// parametric driver layer (printer.Backend) drives Bambu Lab today and
// OctoPrint/Klipper/PrusaLink tomorrow; DOF-of-a-printer is the job state, not
// motion axes, so the verbs are status/control/camera/print rather than jog/move.
//
// Discovery is credential-free (SSDP, printer_discover). Everything else needs
// the config: driver + addr + serial + the LAN access code, stored ENCRYPTED in
// the vault (project "printer", name "config"). The access code is a secret and
// never leaves the box — it is redacted out of printer_config_get.
//
// Destructive verbs (printer_print, printer_gcode, printer_home, printer_set_temp)
// physically run the machine; printer_print additionally requires confirm:true.
// The camera reuses the shared robot.Camera path so printer_snapshot works over
// the mesh exactly like robot_snapshot.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yaver-io/agent/printer"
)

const printerVaultProject = "printer"
const printerVaultConfigName = "config"

func printerConfigFilePath() string {
	home, _ := os.UserHomeDir()
	return home + "/.yaver/printer-config.json"
}

var (
	printerCfgMu     sync.Mutex
	printerCfgCached *printer.Config
)

func printerConfigDefault() printer.Config {
	c := printer.Config{
		Driver:     strings.ToLower(strings.TrimSpace(os.Getenv("YAVER_PRINTER_DRIVER"))),
		Addr:       os.Getenv("YAVER_PRINTER_ADDR"),
		Serial:     os.Getenv("YAVER_PRINTER_SERIAL"),
		AccessCode: os.Getenv("YAVER_PRINTER_ACCESS_CODE"),
	}
	c.Normalize()
	return c
}

func printerConfigGet() printer.Config {
	printerCfgMu.Lock()
	defer printerCfgMu.Unlock()
	if printerCfgCached != nil {
		return *printerCfgCached
	}
	def := printerConfigDefault()
	cfg := def
	found := false
	if vs, err := openVaultOptional(); err == nil {
		if e, gerr := vs.Get(printerVaultProject, printerVaultConfigName); gerr == nil && e != nil && e.Value != "" {
			var c printer.Config
			if json.Unmarshal([]byte(e.Value), &c) == nil {
				cfg, found = c, true
			}
		}
	}
	if !found {
		if b, err := os.ReadFile(printerConfigFilePath()); err == nil {
			var c printer.Config
			if json.Unmarshal(b, &c) == nil {
				cfg, found = c, true
			}
		}
	}
	cfg.Normalize()
	if cfg.Addr == "" {
		cfg.Addr = def.Addr
	}
	if cfg.AccessCode == "" {
		cfg.AccessCode = def.AccessCode
	}
	printerCfgCached = &cfg
	return cfg
}

func printerConfigSave(c printer.Config) error {
	c.Normalize()
	c.UpdatedAt = time.Now().UnixMilli()
	b, _ := json.Marshal(c)
	var vaultErr error
	if vs, err := openVaultOptional(); err == nil {
		vaultErr = vs.Set(VaultEntry{Project: printerVaultProject, Name: printerVaultConfigName, Category: "custom", Value: string(b), Notes: "Yaver printer cell config (driver + addr + serial; access code is a secret)"})
	} else {
		vaultErr = err
	}
	if vaultErr != nil {
		// Vault locked/unavailable — fall back to a 0600 local file so the cell
		// still works; the access code is then file-protected, not vault-encrypted.
		if ferr := os.WriteFile(printerConfigFilePath(), b, 0o600); ferr != nil {
			return ferr
		}
	}
	printerCfgMu.Lock()
	printerCfgCached = &c
	printerCfgMu.Unlock()
	return nil
}

func printerEnabled() bool {
	return printerConfigGet().Enabled()
}

var (
	printerOnce    sync.Once
	printerBackend printer.Backend
)

func ensurePrinter() printer.Backend {
	printerOnce.Do(func() {
		cfg := printerConfigGet()
		switch cfg.Driver {
		default: // "bambu" and unknown → bambu
			printerBackend = printer.NewBambuBackend(cfg)
		}
	})
	return printerBackend
}

// printerForOps resolves the configured backend or a typed deny result.
func printerForOps() (printer.Backend, *OpsResult) {
	if !printerEnabled() {
		return nil, &OpsResult{OK: false, Code: "unauthorized", Error: "no printer configured; run printer_discover then printer_config_set with {driver:'bambu', addr, serial, accessCode}"}
	}
	b := ensurePrinter()
	if b == nil {
		return nil, &OpsResult{OK: false, Code: "unsupported", Error: "printer engine unavailable"}
	}
	return b, nil
}

type printerCmdPayload struct {
	Which   string  `json:"which"`   // set_temp heater
	Celsius float64 `json:"celsius"` // set_temp target
	On      *bool   `json:"on"`      // light
	Line    string  `json:"line"`    // gcode
	Confirm bool    `json:"confirm"` // gate destructive verbs

	// upload + print
	LocalPath  string `json:"localPath"`
	RemoteName string `json:"remoteName"`
	RemoteFile string `json:"remoteFile"`
	Plate      int    `json:"plate"`
	UseAMS     bool   `json:"useAMS"`
	BedLevel   bool   `json:"bedLevel"`
	FlowCalib  bool   `json:"flowCalib"`
	Subtask    string `json:"subtask"`

	TimeoutSec int `json:"timeoutSec"`
}

func init() {
	reg := func(name, desc string, h VerbHandler) {
		registerOpsVerb(opsVerbSpec{Name: name, Description: desc, Handler: h, AllowGuest: false})
	}

	reg("printer_discover", "Find 3D printers on the LAN via SSDP (Bambu broadcast on UDP 2021). Credential-free — returns ip/serial/model/firmware/signal for a picker. No config required.", func(c OpsContext, payload json.RawMessage) OpsResult {
		secs := 6
		var p struct {
			Seconds int `json:"seconds"`
		}
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &p)
			if p.Seconds > 0 && p.Seconds <= 20 {
				secs = p.Seconds
			}
		}
		ctx, cancel := context.WithTimeout(c.Ctx, time.Duration(secs+2)*time.Second)
		defer cancel()
		found, err := printer.Discover(ctx, time.Duration(secs)*time.Second)
		if err != nil {
			return OpsResult{OK: false, Code: "discover_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"printers": found, "count": len(found)}}
	})

	reg("printer_drivers", "List supported printer drivers + known Bambu model codes for the UI picker.", func(c OpsContext, _ json.RawMessage) OpsResult {
		return OpsResult{OK: true, Initial: map[string]any{
			"drivers": []map[string]string{
				{"driver": "bambu", "label": "Bambu Lab (P1/P1S/P1P/A1/X1)", "transport": "mqtt+ftps+chamber-cam"},
			},
			"bambuModels": map[string]string{
				"C11": "P1P", "C12": "P1S", "BL-P001": "X1 Carbon", "N1": "A1 mini", "N2": "A1",
			},
		}}
	})

	reg("printer_config_get", "Get the printer cell config (access code REDACTED) + enabled state.", func(c OpsContext, _ json.RawMessage) OpsResult {
		cfg := printerConfigGet()
		return OpsResult{OK: true, Initial: map[string]any{"config": cfg.Redacted(), "enabled": cfg.Enabled()}}
	})

	reg("printer_config_set", "Set the printer cell config — {driver:'bambu', addr, serial, accessCode, model?, name?}. Saved ENCRYPTED in the vault; the access code never leaves this box. Pass an empty accessCode to keep the existing one.", func(c OpsContext, payload json.RawMessage) OpsResult {
		var cfg printer.Config
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &cfg); err != nil {
				return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
			}
		}
		// Preserve an existing access code when the caller omits it (so a UI can
		// edit addr/name without re-typing the secret).
		if strings.TrimSpace(cfg.AccessCode) == "" {
			cfg.AccessCode = printerConfigGet().AccessCode
		}
		cfg.Normalize()
		if err := printerConfigSave(cfg); err != nil {
			return OpsResult{OK: false, Code: "save_failed", Error: err.Error()}
		}
		// Reset the lazy singleton so the next call uses the new config.
		printerOnce = sync.Once{}
		printerBackend = nil
		return OpsResult{OK: true, Initial: map[string]any{"config": cfg.Redacted(), "enabled": cfg.Enabled()}}
	})

	reg("printer_info", "Static printer identity (vendor/model/serial/ip).", func(c OpsContext, _ json.RawMessage) OpsResult {
		b, deny := printerForOps()
		if deny != nil {
			return *deny
		}
		ctx, cancel := context.WithTimeout(c.Ctx, 8*time.Second)
		defer cancel()
		info, err := b.Info(ctx)
		if err != nil {
			return OpsResult{OK: false, Code: "info_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"info": info}}
	})

	reg("printer_status", "Live printer status — state, temps (nozzle/bed/chamber), progress %, layer, ETA, stage. Read-only.", func(c OpsContext, _ json.RawMessage) OpsResult {
		b, deny := printerForOps()
		if deny != nil {
			return *deny
		}
		ctx, cancel := context.WithTimeout(c.Ctx, 15*time.Second)
		defer cancel()
		st, err := b.Status(ctx)
		if err != nil {
			return OpsResult{OK: false, Code: "status_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"status": st}}
	})

	reg("printer_snapshot", "Single chamber-camera JPEG as a data: URL (over the mesh, like robot_snapshot).", func(c OpsContext, _ json.RawMessage) OpsResult {
		b, deny := printerForOps()
		if deny != nil {
			return *deny
		}
		ctx, cancel := context.WithTimeout(c.Ctx, 20*time.Second)
		defer cancel()
		jpg, err := b.SnapshotJPEG(ctx)
		if err != nil {
			return OpsResult{OK: false, Code: "no_camera", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{
			"image": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpg),
			"bytes": len(jpg),
		}}
	})

	reg("printer_light", "Toggle the chamber light — payload {on: true|false}.", func(c OpsContext, payload json.RawMessage) OpsResult {
		b, deny := printerForOps()
		if deny != nil {
			return *deny
		}
		var p printerCmdPayload
		_ = json.Unmarshal(payload, &p)
		on := p.On == nil || *p.On
		ctx, cancel := context.WithTimeout(c.Ctx, 8*time.Second)
		defer cancel()
		if err := b.Light(ctx, on); err != nil {
			return OpsResult{OK: false, Code: "command_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"lightOn": on}}
	})

	reg("printer_pause", "Pause the running print.", func(c OpsContext, _ json.RawMessage) OpsResult {
		return printerSimpleCmd(c, func(ctx context.Context, b printer.Backend) error { return b.Pause(ctx) })
	})
	reg("printer_resume", "Resume a paused print.", func(c OpsContext, _ json.RawMessage) OpsResult {
		return printerSimpleCmd(c, func(ctx context.Context, b printer.Backend) error { return b.Resume(ctx) })
	})
	reg("printer_stop", "Stop/abort the running print (safe to call anytime).", func(c OpsContext, _ json.RawMessage) OpsResult {
		return printerSimpleCmd(c, func(ctx context.Context, b printer.Backend) error { return b.Stop(ctx) })
	})

	reg("printer_set_temp", "Set a heater target — payload {which:'nozzle'|'bed'|'chamber', celsius}. celsius<=0 cools. Physically heats the machine.", func(c OpsContext, payload json.RawMessage) OpsResult {
		b, deny := printerForOps()
		if deny != nil {
			return *deny
		}
		var p printerCmdPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		if p.Which == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "which (nozzle|bed|chamber) required"}
		}
		ctx, cancel := context.WithTimeout(c.Ctx, 8*time.Second)
		defer cancel()
		if err := b.SetTemp(ctx, p.Which, p.Celsius); err != nil {
			return OpsResult{OK: false, Code: "command_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"which": p.Which, "target": p.Celsius}}
	})

	reg("printer_gcode", "Send one raw G-code line — payload {line, confirm:true}. Power-user; physically moves the machine, so confirm is required.", func(c OpsContext, payload json.RawMessage) OpsResult {
		b, deny := printerForOps()
		if deny != nil {
			return *deny
		}
		var p printerCmdPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		if !p.Confirm {
			return OpsResult{OK: false, Code: "confirm_required", Error: "raw gcode moves the machine; pass confirm:true"}
		}
		if strings.TrimSpace(p.Line) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "line required"}
		}
		ctx, cancel := context.WithTimeout(c.Ctx, 8*time.Second)
		defer cancel()
		if err := b.Gcode(ctx, p.Line); err != nil {
			return OpsResult{OK: false, Code: "command_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"sent": p.Line}}
	})

	reg("printer_upload", "Upload a local sliced file (.3mf/.gcode) to the printer over FTPS — payload {localPath, remoteName?}. Returns the on-printer path for printer_print.", func(c OpsContext, payload json.RawMessage) OpsResult {
		b, deny := printerForOps()
		if deny != nil {
			return *deny
		}
		var p printerCmdPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		if strings.TrimSpace(p.LocalPath) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "localPath required"}
		}
		to := 120
		if p.TimeoutSec > 0 && p.TimeoutSec <= 600 {
			to = p.TimeoutSec
		}
		ctx, cancel := context.WithTimeout(c.Ctx, time.Duration(to)*time.Second)
		defer cancel()
		remote, err := b.Upload(ctx, p.LocalPath, p.RemoteName)
		if err != nil {
			return OpsResult{OK: false, Code: "upload_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"remoteFile": remote}}
	})

	reg("printer_print", "START A PRINT of an already-uploaded file — payload {remoteFile, plate?, useAMS?, bedLevel?, confirm:true}. DESTRUCTIVE: runs the machine for hours. Requires confirm:true AND an idle printer with a clear bed.", func(c OpsContext, payload json.RawMessage) OpsResult {
		b, deny := printerForOps()
		if deny != nil {
			return *deny
		}
		var p printerCmdPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
		if !p.Confirm {
			return OpsResult{OK: false, Code: "confirm_required", Error: "starting a print is destructive; pass confirm:true and make sure the bed is clear"}
		}
		if strings.TrimSpace(p.RemoteFile) == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "remoteFile required (upload first)"}
		}
		// Safety interlock: refuse to start over a busy/printing machine.
		sctx, scancel := context.WithTimeout(c.Ctx, 15*time.Second)
		st, serr := b.Status(sctx)
		scancel()
		if serr == nil && (st.State == "printing" || st.State == "paused" || st.State == "prepare") {
			return OpsResult{OK: false, Code: "busy", Error: "printer is " + st.State + "; stop the current job before starting a new one"}
		}
		ctx, cancel := context.WithTimeout(c.Ctx, 20*time.Second)
		defer cancel()
		err := b.StartPrint(ctx, printer.PrintRequest{
			RemoteFile: p.RemoteFile, Plate: p.Plate, UseAMS: p.UseAMS,
			BedLevel: p.BedLevel, FlowCalib: p.FlowCalib, Subtask: p.Subtask,
		})
		if err != nil {
			return OpsResult{OK: false, Code: "command_failed", Error: err.Error()}
		}
		return OpsResult{OK: true, Initial: map[string]any{"started": p.RemoteFile}}
	})
}

func printerSimpleCmd(c OpsContext, fn func(ctx context.Context, b printer.Backend) error) OpsResult {
	b, deny := printerForOps()
	if deny != nil {
		return *deny
	}
	ctx, cancel := context.WithTimeout(c.Ctx, 8*time.Second)
	defer cancel()
	if err := fn(ctx, b); err != nil {
		return OpsResult{OK: false, Code: "command_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]any{"ok": true}}
}
