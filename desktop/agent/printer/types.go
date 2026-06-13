// Package printer drives 3D printers as a first-class Yaver device cell, the
// sibling of the robot (Cartesian/Marlin) and arm (multi-DOF cobot) packages.
//
// The first driver is Bambu Lab (P1/P1S/P1P/A1/X1) over its LAN protocols:
//
//   - SSDP discovery on UDP 2021 — finds printers + their serial/model/version
//     with NO credentials (see discovery.go).
//   - MQTT over TLS on 8883 — live telemetry (temps, job %, stage) and control
//     (pause/resume/stop, gcode, chamber light, start-print). Auth: user "bblp",
//     password = the printer's 8-digit LAN access code (mqtt.go + backend_bambu.go).
//   - Implicit FTPS on 990 — upload sliced .3mf/.gcode before a print (backend_bambu.go).
//   - Chamber camera on 6000 — TLS JPEG push stream (camera_bambu.go). It feeds
//     the same robot.Camera path as every other Yaver cell, so printer_snapshot /
//     the MJPEG stream / mobile + web all reuse the existing eye.
//
// Everything is parametric and vault-backed (Config), mirroring arm.Config: a new
// printer is wired by parameters (driver + addr + access code + serial), not code.
// The Backend interface lets future drivers (OctoPrint, Klipper/Moonraker,
// PrusaLink) slot in without touching the verbs or UI.
package printer

import "context"

// TempPair is a (current, target) temperature in °C.
type TempPair struct {
	Cur    float64 `json:"cur"`
	Target float64 `json:"target"`
}

// Status is one live snapshot of a printer, normalized across drivers. Fields a
// driver cannot supply are left zero/empty — the UI degrades, it does not crash.
type Status struct {
	Online bool `json:"online"`

	// State is the normalized job state: "idle" | "printing" | "paused" |
	// "finished" | "failed" | "prepare" | "unknown".
	State string `json:"state"`
	// Stage is the printer's fine-grained stage text when known (Bambu mc_stage /
	// stg_cur), e.g. "heating bed", "auto bed leveling", "printing".
	Stage string `json:"stage,omitempty"`

	Nozzle  TempPair `json:"nozzle"`
	Bed     TempPair `json:"bed"`
	Chamber TempPair `json:"chamber,omitempty"`

	// Progress 0..100; LayerNum/TotalLayers and RemainingMin when the driver
	// reports them.
	Progress     float64 `json:"progress"`
	LayerNum     int     `json:"layerNum,omitempty"`
	TotalLayers  int     `json:"totalLayers,omitempty"`
	RemainingMin int     `json:"remainingMin,omitempty"`

	// SpeedLevel: Bambu speed profile (1 silent .. 4 ludicrous), 0 if unknown.
	SpeedLevel int `json:"speedLevel,omitempty"`
	FanSpeed   int `json:"fanSpeed,omitempty"` // part-cooling fan %, 0 unknown

	// SubtaskName is the current job/file name when printing.
	SubtaskName string `json:"subtaskName,omitempty"`
	// LightOn reports the chamber light state when known.
	LightOn *bool `json:"lightOn,omitempty"`

	// Nozzle diameter (mm) and HMS (health-management) error codes when present.
	NozzleDiameter float64  `json:"nozzleDiameter,omitempty"`
	Errors         []string `json:"errors,omitempty"`

	UpdatedAt int64 `json:"updatedAt,omitempty"` // unix ms of this snapshot
}

// Info is the static identity of a printer, mostly from SSDP discovery + the
// configured serial. Stable across the device's lifetime.
type Info struct {
	Vendor    string `json:"vendor"`   // "Bambu Lab"
	Model     string `json:"model"`    // human model, e.g. "P1S"
	ModelKey  string `json:"modelKey"` // wire code, e.g. "C12"
	Serial    string `json:"serial"`   // printer serial (USN), drives MQTT topics
	Name      string `json:"name"`     // friendly device name (DevName)
	Firmware  string `json:"firmware"` // e.g. "01.09.01.00"
	IP        string `json:"ip"`
	HasCamera bool   `json:"hasCamera"`
	HasAMS    bool   `json:"hasAMS"`
}

// Discovered is one SSDP hit. Credential-free — the access code is never on the
// wire, so this is safe to surface in a picker before the user enters it.
type Discovered struct {
	IP       string `json:"ip"`
	Serial   string `json:"serial"`   // USN
	Model    string `json:"model"`    // human ("P1S")
	ModelKey string `json:"modelKey"` // DevModel.bambu.com ("C12")
	Name     string `json:"name"`     // DevName.bambu.com
	Firmware string `json:"firmware"` // DevVersion.bambu.com
	SignalDB int    `json:"signalDb"` // DevSignal (RSSI, dBm)
	Connect  string `json:"connect"`  // DevConnect ("cloud"/"lan")
	Bind     string `json:"bind"`     // DevBind ("free"/"occupied")
}

// Backend is the per-printer driver. Telemetry verbs (Status/Snapshot) are
// read-only and safe; control verbs mutate the machine and a print can run for
// hours, so the ops layer gates the destructive ones (StartPrint) behind
// confirm:true. A driver returns ErrUnsupported for a capability it lacks.
type Backend interface {
	Name() string
	Connect(ctx context.Context) error
	Close() error

	Info(ctx context.Context) (Info, error)
	Status(ctx context.Context) (Status, error)

	// Pause/Resume/Stop the running job. Stop is the safe abort.
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	Stop(ctx context.Context) error

	// SetTemp targets a heater: which = "nozzle" | "bed" | "chamber". c<=0 cools.
	SetTemp(ctx context.Context, which string, c float64) error
	// Light toggles the chamber light.
	Light(ctx context.Context, on bool) error
	// Gcode sends one raw G-code line (M-codes, jogs). Power-user / homing.
	Gcode(ctx context.Context, line string) error

	// Upload pushes a local sliced file to the printer's storage, returning the
	// on-printer path to reference in StartPrint.
	Upload(ctx context.Context, localPath, remoteName string) (string, error)
	// StartPrint begins a print of an already-uploaded file. DESTRUCTIVE — the
	// ops layer requires confirm:true before calling this.
	StartPrint(ctx context.Context, req PrintRequest) error

	// Camera returns a snapshot source (robot.Camera-compatible) or nil if the
	// driver has no camera. The ops layer adapts it to the shared eye path.
	SnapshotJPEG(ctx context.Context) ([]byte, error)
}

// PrintRequest names an uploaded file + plate to print and the AMS choice.
type PrintRequest struct {
	RemoteFile string `json:"remoteFile"` // path returned by Upload (e.g. "model.3mf")
	Plate      int    `json:"plate"`      // plate index (default 1)
	UseAMS     bool   `json:"useAMS"`
	BedLevel   bool   `json:"bedLevel"`  // run auto bed leveling first (default true)
	FlowCalib  bool   `json:"flowCalib"` // flow-rate calibration
	Subtask    string `json:"subtask"`   // display name
}

// errString is a tiny constant error helper (mirrors arm.errConst).
type errString string

func (e errString) Error() string { return string(e) }

// ErrUnsupported marks a capability a driver does not implement.
const ErrUnsupported = errString("printer: capability not supported by this driver")
