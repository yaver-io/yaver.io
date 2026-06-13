package printer

// backend_bambu.go — the Bambu Lab driver. Control + telemetry ride MQTT/TLS
// (mqtt.go); file upload rides implicit FTPS (curl); the camera rides the
// chamber stream (camera_bambu.go). All command shapes follow Bambu's published
// LAN JSON protocol (device/<serial>/request ← commands, device/<serial>/report
// → telemetry).
//
// Connection model: one short-lived MQTT session per operation. Status connects,
// asks for a full "pushall" report, parses the freshest one, and disconnects —
// cheap, stateless, and robust to the printer dropping idle sessions. A
// long-lived telemetry subscription can be layered on later for the live UI.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// BambuBackend implements Backend over the Bambu LAN protocol.
type BambuBackend struct {
	cfg Config
	seq atomic.Int64
	cam *BambuCamera
}

// NewBambuBackend builds a driver from config. It does not connect yet.
func NewBambuBackend(cfg Config) *BambuBackend {
	cfg.Normalize()
	return &BambuBackend{cfg: cfg}
}

func (b *BambuBackend) Name() string { return "bambu" }

func (b *BambuBackend) host() string {
	return fmt.Sprintf("%s:%d", b.cfg.Addr, b.cfg.MQTTPort)
}

func (b *BambuBackend) reportTopic() string  { return "device/" + b.cfg.Serial + "/report" }
func (b *BambuBackend) requestTopic() string { return "device/" + b.cfg.Serial + "/request" }

func (b *BambuBackend) clientID() string {
	return fmt.Sprintf("yaver-%d-%d", os.Getpid(), b.seq.Add(1))
}

func (b *BambuBackend) nextSeq() string {
	return strconv.FormatInt(b.seq.Add(1), 10)
}

// Connect validates we can reach + authenticate the printer (a CONNECT round-trip).
func (b *BambuBackend) Connect(ctx context.Context) error {
	if b.cfg.Addr == "" || b.cfg.Serial == "" || b.cfg.AccessCode == "" {
		return errString("bambu: addr, serial and accessCode are all required")
	}
	c, err := dialMQTT(b.host(), b.clientID(), "bblp", b.cfg.AccessCode, 8*time.Second)
	if err != nil {
		return err
	}
	return c.Close()
}

func (b *BambuBackend) Close() error {
	if b.cam != nil {
		b.cam.Close()
	}
	return nil
}

func (b *BambuBackend) Info(ctx context.Context) (Info, error) {
	return Info{
		Vendor:    "Bambu Lab",
		Model:     b.cfg.Model,
		Serial:    b.cfg.Serial,
		Name:      b.cfg.Name,
		IP:        b.cfg.Addr,
		HasCamera: true,
	}, nil
}

// Status connects, requests a full report (pushall), and parses the freshest
// "print" object it sees within a short window.
func (b *BambuBackend) Status(ctx context.Context) (Status, error) {
	c, err := dialMQTT(b.host(), b.clientID(), "bblp", b.cfg.AccessCode, 8*time.Second)
	if err != nil {
		return Status{Online: false}, err
	}
	defer c.Close()
	if err := c.Subscribe(b.reportTopic(), 5*time.Second); err != nil {
		return Status{Online: false}, err
	}
	// Ask for a complete snapshot.
	_ = c.Publish(b.requestTopic(), []byte(`{"pushing":{"sequence_id":"`+b.nextSeq()+`","command":"pushall"}}`), 5*time.Second)

	deadline := time.Now().Add(4 * time.Second)
	var best bambuPrint
	got := false
	for time.Now().Before(deadline) {
		msg, err := c.ReadMessage(time.Until(deadline))
		if err != nil {
			break
		}
		var rep bambuReport
		if json.Unmarshal(msg.Payload, &rep) != nil || rep.Print == nil {
			continue
		}
		best.merge(*rep.Print)
		got = true
		if best.GcodeState != "" && best.NozzleTemper != 0 {
			break // a full report has arrived
		}
	}
	st := best.toStatus()
	st.Online = true
	if !got {
		st.State = "unknown"
	}
	return st, nil
}

func (b *BambuBackend) publishCmd(ctx context.Context, payload string) error {
	c, err := dialMQTT(b.host(), b.clientID(), "bblp", b.cfg.AccessCode, 8*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Publish(b.requestTopic(), []byte(payload), 5*time.Second)
}

func (b *BambuBackend) Pause(ctx context.Context) error {
	return b.publishCmd(ctx, `{"print":{"sequence_id":"`+b.nextSeq()+`","command":"pause"}}`)
}
func (b *BambuBackend) Resume(ctx context.Context) error {
	return b.publishCmd(ctx, `{"print":{"sequence_id":"`+b.nextSeq()+`","command":"resume"}}`)
}
func (b *BambuBackend) Stop(ctx context.Context) error {
	return b.publishCmd(ctx, `{"print":{"sequence_id":"`+b.nextSeq()+`","command":"stop"}}`)
}

func (b *BambuBackend) SetTemp(ctx context.Context, which string, c float64) error {
	var line string
	switch strings.ToLower(which) {
	case "nozzle", "extruder", "hotend":
		line = fmt.Sprintf("M104 S%d", int(c))
	case "bed", "plate":
		line = fmt.Sprintf("M140 S%d", int(c))
	case "chamber":
		line = fmt.Sprintf("M141 S%d", int(c))
	default:
		return fmt.Errorf("bambu: unknown heater %q (nozzle|bed|chamber)", which)
	}
	return b.Gcode(ctx, line)
}

func (b *BambuBackend) Light(ctx context.Context, on bool) error {
	mode := "off"
	if on {
		mode = "on"
	}
	payload := fmt.Sprintf(`{"system":{"sequence_id":"%s","command":"ledctrl","led_node":"chamber_light","led_mode":"%s","led_on_time":500,"led_off_time":500,"loop_times":0,"interval_time":0}}`, b.nextSeq(), mode)
	return b.publishCmd(ctx, payload)
}

func (b *BambuBackend) Gcode(ctx context.Context, line string) error {
	line = strings.TrimRight(line, "\n") + "\n"
	param, _ := json.Marshal(line) // JSON-escape the newline + quotes
	payload := fmt.Sprintf(`{"print":{"sequence_id":"%s","command":"gcode_line","param":%s}}`, b.nextSeq(), string(param))
	return b.publishCmd(ctx, payload)
}

// Upload pushes a local file to the printer root over implicit FTPS. Bambu's FTP
// uses TLS-from-connect on 990 with user "bblp" + the access code and a
// self-signed cert; curl handles all of that with --ftp-ssl --insecure. We shell
// to curl (ubiquitous, same posture as the openscad/gst shellouts elsewhere).
func (b *BambuBackend) Upload(ctx context.Context, localPath, remoteName string) (string, error) {
	if remoteName == "" {
		remoteName = baseName(localPath)
	}
	if _, err := os.Stat(localPath); err != nil {
		return "", fmt.Errorf("bambu upload: %w", err)
	}
	url := fmt.Sprintf("ftps://%s:%d/%s", b.cfg.Addr, b.cfg.FTPPort, remoteName)
	cmd := exec.CommandContext(ctx, "curl", "--silent", "--show-error",
		"--ftp-ssl", "--insecure", "--ftp-pasv",
		"--user", "bblp:"+b.cfg.AccessCode,
		"-T", localPath, url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("bambu ftps upload failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return remoteName, nil
}

// StartPrint begins a print of an already-uploaded file. DESTRUCTIVE — the ops
// layer gates this behind confirm:true. NOTE: this physically runs the machine.
func (b *BambuBackend) StartPrint(ctx context.Context, req PrintRequest) error {
	if req.RemoteFile == "" {
		return errString("bambu: StartPrint needs an uploaded RemoteFile")
	}
	plate := req.Plate
	if plate <= 0 {
		plate = 1
	}
	subtask := req.Subtask
	if subtask == "" {
		subtask = strings.TrimSuffix(baseName(req.RemoteFile), ".3mf")
	}
	payload := map[string]any{
		"print": map[string]any{
			"sequence_id":    b.nextSeq(),
			"command":        "project_file",
			"param":          fmt.Sprintf("Metadata/plate_%d.gcode", plate),
			"url":            "file:///sdcard/" + req.RemoteFile,
			"subtask_name":   subtask,
			"use_ams":        req.UseAMS,
			"bed_leveling":   req.BedLevel,
			"flow_cali":      req.FlowCalib,
			"vibration_cali": true,
			"layer_inspect":  false,
			"timelapse":      false,
		},
	}
	buf, _ := json.Marshal(payload)
	return b.publishCmd(ctx, string(buf))
}

// SnapshotJPEG grabs one frame from the chamber camera (lazy-opening the stream).
func (b *BambuBackend) SnapshotJPEG(ctx context.Context) ([]byte, error) {
	if b.cfg.CameraOverride != "" {
		return httpSnapshot(ctx, b.cfg.CameraOverride)
	}
	if b.cam == nil {
		b.cam = NewBambuCamera(b.cfg.Addr, b.cfg.CameraPort, b.cfg.AccessCode)
	}
	return b.cam.Snapshot(ctx)
}

func baseName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// --- Bambu report JSON parsing ---

type bambuReport struct {
	Print *bambuPrint `json:"print"`
}

// bambuPrint mirrors the subset of device report fields we surface. Reports are
// often partial deltas; merge() keeps the last non-empty value for each field.
type bambuPrint struct {
	NozzleTemper       float64      `json:"nozzle_temper"`
	NozzleTargetTemper float64      `json:"nozzle_target_temper"`
	BedTemper          float64      `json:"bed_temper"`
	BedTargetTemper    float64      `json:"bed_target_temper"`
	ChamberTemper      float64      `json:"chamber_temper"`
	McPercent          int          `json:"mc_percent"`
	McRemainingTime    int          `json:"mc_remaining_time"`
	LayerNum           int          `json:"layer_num"`
	TotalLayerNum      int          `json:"total_layer_num"`
	GcodeState         string       `json:"gcode_state"`
	StgCur             int          `json:"stg_cur"`
	SubtaskName        string       `json:"subtask_name"`
	SpdLevel           int          `json:"spd_lvl"`
	CoolingFanSpeed    string       `json:"cooling_fan_speed"`
	NozzleDiameter     string       `json:"nozzle_diameter"`
	Lights             []bambuLight `json:"lights_report"`
	HMS                []bambuHMS   `json:"hms"`
}

type bambuLight struct {
	Node string `json:"node"`
	Mode string `json:"mode"`
}
type bambuHMS struct {
	Attr int64 `json:"attr"`
	Code int64 `json:"code"`
}

func (p *bambuPrint) merge(o bambuPrint) {
	if o.NozzleTemper != 0 {
		p.NozzleTemper = o.NozzleTemper
	}
	if o.NozzleTargetTemper != 0 {
		p.NozzleTargetTemper = o.NozzleTargetTemper
	}
	if o.BedTemper != 0 {
		p.BedTemper = o.BedTemper
	}
	if o.BedTargetTemper != 0 {
		p.BedTargetTemper = o.BedTargetTemper
	}
	if o.ChamberTemper != 0 {
		p.ChamberTemper = o.ChamberTemper
	}
	if o.McPercent != 0 {
		p.McPercent = o.McPercent
	}
	if o.McRemainingTime != 0 {
		p.McRemainingTime = o.McRemainingTime
	}
	if o.LayerNum != 0 {
		p.LayerNum = o.LayerNum
	}
	if o.TotalLayerNum != 0 {
		p.TotalLayerNum = o.TotalLayerNum
	}
	if o.GcodeState != "" {
		p.GcodeState = o.GcodeState
	}
	if o.StgCur != 0 {
		p.StgCur = o.StgCur
	}
	if o.SubtaskName != "" {
		p.SubtaskName = o.SubtaskName
	}
	if o.SpdLevel != 0 {
		p.SpdLevel = o.SpdLevel
	}
	if o.CoolingFanSpeed != "" {
		p.CoolingFanSpeed = o.CoolingFanSpeed
	}
	if o.NozzleDiameter != "" {
		p.NozzleDiameter = o.NozzleDiameter
	}
	if len(o.Lights) > 0 {
		p.Lights = o.Lights
	}
	if len(o.HMS) > 0 {
		p.HMS = o.HMS
	}
}

func (p bambuPrint) toStatus() Status {
	st := Status{
		State:        normalizeState(p.GcodeState),
		Stage:        bambuStageName(p.StgCur),
		Nozzle:       TempPair{Cur: p.NozzleTemper, Target: p.NozzleTargetTemper},
		Bed:          TempPair{Cur: p.BedTemper, Target: p.BedTargetTemper},
		Chamber:      TempPair{Cur: p.ChamberTemper},
		Progress:     float64(p.McPercent),
		LayerNum:     p.LayerNum,
		TotalLayers:  p.TotalLayerNum,
		RemainingMin: p.McRemainingTime,
		SpeedLevel:   p.SpdLevel,
		SubtaskName:  p.SubtaskName,
		UpdatedAt:    time.Now().UnixMilli(),
	}
	if p.CoolingFanSpeed != "" {
		if v, err := strconv.Atoi(p.CoolingFanSpeed); err == nil {
			st.FanSpeed = int(float64(v) / 15.0 * 100.0) // Bambu reports 0..15
		}
	}
	if p.NozzleDiameter != "" {
		st.NozzleDiameter, _ = strconv.ParseFloat(p.NozzleDiameter, 64)
	}
	for _, l := range p.Lights {
		if l.Node == "chamber_light" {
			on := strings.EqualFold(l.Mode, "on")
			st.LightOn = &on
		}
	}
	for _, h := range p.HMS {
		if h.Code != 0 || h.Attr != 0 {
			st.Errors = append(st.Errors, fmt.Sprintf("HMS_%04X_%04X", uint32(h.Attr), uint32(h.Code)))
		}
	}
	return st
}

// normalizeState maps Bambu gcode_state to the cross-driver vocabulary.
func normalizeState(s string) string {
	switch strings.ToUpper(s) {
	case "IDLE":
		return "idle"
	case "PREPARE", "SLICING":
		return "prepare"
	case "RUNNING":
		return "printing"
	case "PAUSE":
		return "paused"
	case "FINISH":
		return "finished"
	case "FAILED":
		return "failed"
	case "":
		return "unknown"
	default:
		return strings.ToLower(s)
	}
}

// bambuStageName maps the numeric mc stage to a short label (common subset).
func bambuStageName(stg int) string {
	switch stg {
	case 0:
		return "printing"
	case 1:
		return "auto bed leveling"
	case 2:
		return "heating bed"
	case 3:
		return "scanning bed surface"
	case 4:
		return "inspecting first layer"
	case 6:
		return "homing toolhead"
	case 7:
		return "cleaning nozzle"
	case 14:
		return "cooling chamber"
	default:
		if stg == 255 || stg == -1 {
			return ""
		}
		return fmt.Sprintf("stage %d", stg)
	}
}
