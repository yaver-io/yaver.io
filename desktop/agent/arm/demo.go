package arm

// demo.go — record DEMONSTRATIONS for imitation learning. This is the data source
// of the video→policy pipeline: the practical way to teach a wire-harness step is
// to hand-guide the arm (free-drive) or teleop it while Yaver densely records
// synchronized {camera frame, joint state, pose} at a fixed rate. Those episodes
// are what you ship to a rented GPU to train an ACT / Diffusion-Policy / SmolVLA
// model in LeRobot — the model that later runs via policy.go.
//
// This differs from program.go's teach-and-repeat, which records SPARSE waypoints
// for deterministic replay. A learned policy needs DENSE trajectories (10–30 Hz of
// frame+state+action), so this writes a per-episode dataset:
//
//   <dir>/<name>/episode_000/
//       meta.json            {name, prompt, fps, dof, joints, frames, createdAt}
//       frames/000001.jpg …  one JPEG per tick (the camera obs)
//       states.jsonl         one line per tick: {t, joints{name:val}, pose?}
//
// LeRobot's own format (parquet + encoded video) is the training target; this
// jpg+jsonl layout is the lossless, dependency-free intermediate a small converter
// (documented) turns into a LeRobotDataset. Local-first, never leaves the box
// except as a training upload the user initiates.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// DemoMeta describes one recorded episode.
type DemoMeta struct {
	Name       string      `json:"name"`
	Prompt     string      `json:"prompt,omitempty"`
	FPS        int         `json:"fps"`
	DOF        int         `json:"dof"`
	Joints     []JointSpec `json:"joints,omitempty"`
	Frames     int         `json:"frames"`
	Episode    string      `json:"episode"`
	CreatedAt  int64       `json:"createdAt"`
	DurationMs int64       `json:"durationMs,omitempty"`
}

// demoTick is one row of states.jsonl.
type demoTick struct {
	T      float64            `json:"t"` // seconds since episode start
	Joints map[string]float64 `json:"joints"`
	Pose   *Pose              `json:"pose,omitempty"`
}

// DemoRecorder owns the dataset dir and at most one active recording (one arm).
type DemoRecorder struct {
	dir string
	mu  sync.Mutex
	cur *demoSession
}

type demoSession struct {
	meta    DemoMeta
	epDir   string
	start   time.Time
	cancel  context.CancelFunc
	done    chan struct{}
	mu      sync.Mutex
	frames  int
	stopErr error
}

// DefaultDemoRecorder stores under ~/.yaver/arm-demos.
func DefaultDemoRecorder() *DemoRecorder {
	home, _ := os.UserHomeDir()
	return &DemoRecorder{dir: filepath.Join(home, ".yaver", "arm-demos")}
}

// NewDemoRecorder is for tests / custom locations.
func NewDemoRecorder(dir string) *DemoRecorder { return &DemoRecorder{dir: dir} }

// Active reports the in-progress recording, if any.
func (r *DemoRecorder) Active() (bool, string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur == nil {
		return false, "", 0
	}
	r.cur.mu.Lock()
	n := r.cur.frames
	r.cur.mu.Unlock()
	return true, r.cur.meta.Name, n
}

// Start begins a dense recording from the controller's camera + joint state. It
// auto-allocates the next episode_NNN under <dir>/<name>. fps defaults to 10.
func (r *DemoRecorder) Start(ctx context.Context, c *Controller, name, prompt string, fps int) error {
	if name == "" {
		return fmt.Errorf("demo: name required")
	}
	if c.Camera == nil || !c.Camera.Available() {
		return fmt.Errorf("demo: a camera is required (the policy is vision-conditioned)")
	}
	if fps <= 0 {
		fps = 10
	}
	if fps > 60 {
		fps = 60
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur != nil {
		return fmt.Errorf("demo: already recording %q — stop it first", r.cur.meta.Name)
	}
	epDir, ep, err := r.nextEpisode(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(epDir, "frames"), 0o755); err != nil {
		return err
	}
	info, _ := c.Describe(ctx)
	rctx, cancel := context.WithCancel(context.Background())
	sess := &demoSession{
		meta:   DemoMeta{Name: name, Prompt: prompt, FPS: fps, DOF: info.DOF, Joints: info.Joints, Episode: ep, CreatedAt: time.Now().UnixMilli()},
		epDir:  epDir,
		start:  time.Now(),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	r.cur = sess
	go sess.run(rctx, c, fps)
	return nil
}

// Stop ends the active recording and finalizes meta.json.
func (r *DemoRecorder) Stop() (DemoMeta, error) {
	r.mu.Lock()
	sess := r.cur
	r.cur = nil
	r.mu.Unlock()
	if sess == nil {
		return DemoMeta{}, fmt.Errorf("demo: not recording")
	}
	sess.cancel()
	<-sess.done
	sess.mu.Lock()
	sess.meta.Frames = sess.frames
	sess.meta.DurationMs = time.Since(sess.start).Milliseconds()
	meta := sess.meta
	serr := sess.stopErr
	sess.mu.Unlock()
	b, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(sess.epDir, "meta.json"), b, 0o644); err != nil && serr == nil {
		serr = err
	}
	return meta, serr
}

func (s *demoSession) run(ctx context.Context, c *Controller, fps int) {
	defer close(s.done)
	ticker := time.NewTicker(time.Duration(float64(time.Second) / float64(fps)))
	defer ticker.Stop()
	jsonlPath := filepath.Join(s.epDir, "states.jsonl")
	f, err := os.Create(jsonlPath)
	if err != nil {
		s.mu.Lock()
		s.stopErr = err
		s.mu.Unlock()
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			grabCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			frame, ferr := c.Camera.Grab(grabCtx)
			js, pose, serr := c.State(grabCtx)
			cancel()
			if ferr != nil || serr != nil {
				continue // skip a bad tick rather than abort the whole demo
			}
			s.mu.Lock()
			idx := s.frames + 1
			s.frames = idx
			s.mu.Unlock()
			_ = os.WriteFile(filepath.Join(s.epDir, "frames", fmt.Sprintf("%06d.jpg", idx)), frame, 0o644)
			_ = enc.Encode(demoTick{
				T:      time.Since(s.start).Seconds(),
				Joints: namedJoints(js),
				Pose:   pose,
			})
		}
	}
}

// nextEpisode allocates <dir>/<name>/episode_NNN.
func (r *DemoRecorder) nextEpisode(name string) (string, string, error) {
	base := filepath.Join(r.dir, name)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", "", err
	}
	entries, _ := os.ReadDir(base)
	n := 0
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) > 8 && e.Name()[:8] == "episode_" {
			n++
		}
	}
	ep := fmt.Sprintf("episode_%03d", n)
	return filepath.Join(base, ep), ep, nil
}

// List returns all recorded episodes (by reading each meta.json).
func (r *DemoRecorder) List() ([]DemoMeta, error) {
	var out []DemoMeta
	names, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, nd := range names {
		if !nd.IsDir() {
			continue
		}
		eps, _ := os.ReadDir(filepath.Join(r.dir, nd.Name()))
		for _, ed := range eps {
			if !ed.IsDir() {
				continue
			}
			b, err := os.ReadFile(filepath.Join(r.dir, nd.Name(), ed.Name(), "meta.json"))
			if err != nil {
				continue
			}
			var m DemoMeta
			if json.Unmarshal(b, &m) == nil {
				out = append(out, m)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}
