package main

// machine_driver_vision.go — THE MOAT: wrap a screen-only machine (no Modbus, no
// OPC-UA, no API) by pointing a camera at its HMI and reading the displayed
// counter / state / alarm with a VLM. Kepware/HighByte/Ignition can't wrap a
// machine that only has a screen; Yaver can. Read-only by design — vision drives
// the WATCH surface, never control (writing setpoints by simulated touch stays a
// frontier we don't ship until a hardware interlock backs it).
//
// Lives in package main to reuse robot.Camera (GstCamera) + the OpenAI-compatible
// VLM call, keeping the machine/ package transport-pure. Caps: status, read,
// subscribe, vision.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yaver-io/agent/machine"
	"github.com/yaver-io/agent/robot"
)

// visionDriver reads a machine's HMI through a camera + VLM.
type visionDriver struct {
	name    string
	kind    string
	cam     robot.Camera
	fields  []machine.Tag // expected HMI fields (name + unit2) to read off the screen
	baseURL string
	apiKey  string
	model   string

	mu        sync.Mutex
	lastState string
	lastTS    int64
}

// visionDriverConfig configures a visionDriver.
type visionDriverConfig struct {
	Name    string
	Kind    string
	Camera  robot.Camera
	Fields  []machine.Tag
	BaseURL string
	APIKey  string
	Model   string
}

func newVisionDriver(cfg visionDriverConfig) *visionDriver {
	if cfg.Kind == "" {
		cfg.Kind = "vision"
	}
	return &visionDriver{
		name: cfg.Name, kind: cfg.Kind, cam: cfg.Camera, fields: cfg.Fields,
		baseURL: cfg.BaseURL, apiKey: cfg.APIKey, model: cfg.Model,
	}
}

func (d *visionDriver) Name() string { return d.name }
func (d *visionDriver) Kind() string { return d.kind }

func (d *visionDriver) Capabilities() machine.CapSet {
	return machine.Caps(machine.CapStatus, machine.CapRead, machine.CapSubscribe, machine.CapVision)
}

func (d *visionDriver) Connect(ctx context.Context) error {
	if d.cam == nil || !d.cam.Available() {
		return fmt.Errorf("vision driver: no camera available")
	}
	return nil
}
func (d *visionDriver) Close() error { return nil }

func (d *visionDriver) Browse(ctx context.Context) ([]machine.Tag, error) {
	out := make([]machine.Tag, len(d.fields))
	copy(out, d.fields)
	return out, nil
}

// Read captures one HMI frame and asks the VLM to read the configured fields +
// the machine's run state, returning a Sample per recognized field.
func (d *visionDriver) Read(ctx context.Context, refs []machine.TagRef) ([]machine.Sample, error) {
	if d.cam == nil {
		return nil, fmt.Errorf("vision driver: no camera")
	}
	jpg, err := d.cam.Grab(ctx)
	if err != nil {
		return nil, fmt.Errorf("vision driver: capture: %w", err)
	}
	state, vals, err := d.askVLM(ctx, jpg)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.lastState = state
	d.lastTS = time.Now().UnixMilli()
	d.mu.Unlock()

	unit2 := map[string]string{}
	want := map[string]bool{}
	for _, f := range d.fields {
		unit2[f.Name] = f.Unit2
	}
	for _, r := range refs {
		if r.Name != "" {
			want[r.Name] = true
		}
	}
	now := time.Now().UnixMilli()
	out := make([]machine.Sample, 0, len(vals))
	for name, v := range vals {
		if len(want) > 0 && !want[name] {
			continue
		}
		out = append(out, machine.Sample{Tag: name, Value: v, Unit2: unit2[name], TS: now})
	}
	return out, nil
}

func (d *visionDriver) Subscribe(ctx context.Context, refs []machine.TagRef, opts machine.SubOpts) (<-chan machine.Sample, error) {
	interval := time.Duration(opts.IntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = 3 * time.Second // VLM reads are slower; default 3s
	}
	ch := make(chan machine.Sample, 16)
	go func() {
		defer close(ch)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			if samples, err := d.Read(ctx, refs); err == nil {
				for _, s := range samples {
					select {
					case ch <- s:
					case <-ctx.Done():
						return
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return ch, nil
}

func (d *visionDriver) Status(ctx context.Context) (machine.MachineStatus, error) {
	d.mu.Lock()
	state := d.lastState
	ts := d.lastTS
	d.mu.Unlock()
	if state == "" {
		state = "unknown"
	}
	return machine.MachineStatus{
		Name: d.name, Kind: d.kind, Driver: "vision_hmi",
		Connected: d.cam != nil && d.cam.Available(),
		State:     state, Caps: d.Capabilities().List(),
		Detail: map[string]any{"lastReadTs": ts}, TS: time.Now().UnixMilli(),
	}, nil
}

// Control is not supported — vision is read-only (see file header).
func (d *visionDriver) Write(ctx context.Context, w []machine.TagWrite) error {
	return machine.ErrNotSupported
}
func (d *visionDriver) Recall(ctx context.Context, program string) error {
	return machine.ErrNotSupported
}
func (d *visionDriver) SubmitJob(ctx context.Context, job machine.Job) error {
	return machine.ErrNotSupported
}

const visionHMISystemPrompt = `You read an industrial machine's HMI/control panel from a photo. Report the machine's run state and the numeric value of each requested field exactly as shown on screen. Respond ONLY with compact JSON: {"state":"running|idle|fault|setup|off|unknown","fields":{"<name>":<number>},"alarm":"<text or empty>"}. Use the field names given. If a field is not visible, omit it. Numbers only for field values (no units).`

// askVLM sends the frame + field list to an OpenAI-compatible vision chat and
// parses the JSON verdict. Provider ladder mirrors machineUnderstandLLM.
func (d *visionDriver) askVLM(ctx context.Context, jpeg []byte) (string, map[string]float64, error) {
	baseURL := firstNonEmptyStr(d.baseURL, os.Getenv("GHOST_VISION_BASE_URL"), os.Getenv("OPENAI_BASE_URL"), localOllamaV1)
	apiKey := firstNonEmptyStr(d.apiKey, os.Getenv("GHOST_VISION_API_KEY"), os.Getenv("OPENAI_API_KEY"))
	model := d.model
	if model == "" {
		model = firstNonEmptyStr(os.Getenv("GHOST_VISION_MODEL"), os.Getenv("OPENAI_MODEL"))
		if model == "" {
			if strings.Contains(baseURL, "11434") {
				model = "llama3.2-vision"
			} else {
				model = "gpt-4o-mini"
			}
		}
	}
	baseURL = strings.TrimRight(baseURL, "/")

	names := make([]string, 0, len(d.fields))
	for _, f := range d.fields {
		names = append(names, f.Name)
	}
	dataURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg)
	body := map[string]any{
		"model":       model,
		"temperature": 0,
		"messages": []any{
			map[string]any{"role": "system", "content": visionHMISystemPrompt},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "Fields to read: " + strings.Join(names, ", ")},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
			}},
		},
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	cl := &http.Client{Timeout: 90 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", nil, err
	}
	if out.Error != nil {
		return "", nil, fmt.Errorf("vision: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", nil, fmt.Errorf("vision: empty response")
	}
	return parseVisionVerdict(out.Choices[0].Message.Content)
}

// parseVisionVerdict extracts {state, fields{}} from the model's JSON reply,
// tolerating markdown fences and surrounding prose.
func parseVisionVerdict(content string) (string, map[string]float64, error) {
	content = strings.TrimSpace(content)
	if i := strings.Index(content, "{"); i > 0 {
		content = content[i:]
	}
	if j := strings.LastIndex(content, "}"); j >= 0 && j < len(content)-1 {
		content = content[:j+1]
	}
	var parsed struct {
		State  string             `json:"state"`
		Fields map[string]float64 `json:"fields"`
		Alarm  string             `json:"alarm"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return "", nil, fmt.Errorf("vision: parse verdict: %w", err)
	}
	if parsed.State == "" {
		parsed.State = "unknown"
	}
	if parsed.Fields == nil {
		parsed.Fields = map[string]float64{}
	}
	return parsed.State, parsed.Fields, nil
}
