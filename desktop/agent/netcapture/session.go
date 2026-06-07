package netcapture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// ErrUnsupported is returned when live serial tty capture is requested on a
// platform without an implementation. The manual/Feed path (phone-as-host, the
// connector box, or pcap replay) works everywhere regardless.
var ErrUnsupported = errors.New("netcapture: live serial tty capture not supported on this platform")

// Engine owns active capture sessions. Constructed lazily by the ops layer;
// safe for concurrent use. Mirrors machine.Engine.
type Engine struct {
	mu       sync.Mutex
	sessions map[string]*Session
	seq      int
	dir      string // ~/.yaver/netcapture (pcap + tty-log artifacts live here, local only)
}

// New constructs an Engine and ensures its artifact dir exists.
func New() (*Engine, error) {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".yaver", "netcapture")
	_ = os.MkdirAll(dir, 0o700)
	return &Engine{sessions: map[string]*Session{}, dir: dir}, nil
}

// Session is one running (or stopped) capture.
type Session struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	Filter    string `json:"filter,omitempty"`
	Decoder   string `json:"decoder,omitempty"`
	StartedAt int64  `json:"startedAt"`
	PcapPath  string `json:"pcapPath,omitempty"`

	a      *Analyzer
	cancel context.CancelFunc
	cmd    *exec.Cmd
	mu     sync.Mutex
	closer io.Closer
	done   bool
}

// NetOptions configures a network capture.
type NetOptions struct {
	Iface          string
	Filter         string
	CapturePayload bool
	OnEvent        func(Event)
}

// SerialOptions configures a serial capture. Device="" → manual/Feed session.
type SerialOptions struct {
	Device  string
	Baud    int
	Decoder string // modbus_rtu|marlin|ascii|auto (default auto)
	OnEvent func(Event)
}

func (e *Engine) nextID(prefix string) string {
	e.seq++
	return fmt.Sprintf("%s-%d", prefix, e.seq)
}

// StartNet begins a live network capture via tcpdump and pure-Go decode.
func (e *Engine) StartNet(opts NetOptions) (string, error) {
	iface := opts.Iface
	if iface == "" {
		iface = "any"
	}
	e.mu.Lock()
	id := e.nextID("net")
	now := nowMs()
	a := newAnalyzer(id, "net", iface, now)
	a.capturePayload = opts.CapturePayload
	a.onEvent = opts.OnEvent
	pcapPath := filepath.Join(e.dir, id+".pcap")
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{ID: id, Kind: "net", Source: iface, Filter: opts.Filter, StartedAt: now, PcapPath: pcapPath, a: a, cancel: cancel}
	e.sessions[id] = s
	e.mu.Unlock()

	go s.runNet(ctx, iface, opts.Filter, pcapPath)
	return id, nil
}

// StartSerial begins a serial capture. With a device it opens the tty (Linux);
// without one it's a manual session you drive with Feed.
func (e *Engine) StartSerial(opts SerialOptions) (string, error) {
	decoder := opts.Decoder
	if decoder == "" {
		decoder = "auto"
	}
	e.mu.Lock()
	id := e.nextID("ser")
	now := nowMs()
	src := opts.Device
	if src == "" {
		src = "fed:" + decoder
	}
	a := newAnalyzer(id, "serial", src, now)
	a.serDecoder = decoder
	a.onEvent = opts.OnEvent
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{ID: id, Kind: "serial", Source: src, Decoder: decoder, StartedAt: now, a: a, cancel: cancel}
	e.sessions[id] = s
	e.mu.Unlock()

	if opts.Device == "" {
		return id, nil // manual: bytes arrive via Feed
	}
	rc, err := openSerialTTY(opts.Device, opts.Baud)
	if err != nil {
		a.setError(err.Error())
		return id, err
	}
	s.mu.Lock()
	s.closer = rc
	s.mu.Unlock()
	go s.runSerial(ctx, rc)
	return id, nil
}

// Feed injects bytes into a serial session (the phone USB-serial / connector-box
// / replay path).
func (e *Engine) Feed(id string, b []byte) error {
	s := e.get(id)
	if s == nil || s.Kind != "serial" {
		return errors.New("no such serial session")
	}
	s.a.FeedSerial(b, nowMs())
	return nil
}

// FeedAt is Feed with an explicit timestamp (tests / precise replay).
func (e *Engine) FeedAt(id string, b []byte, ts int64) error {
	s := e.get(id)
	if s == nil || s.Kind != "serial" {
		return errors.New("no such serial session")
	}
	s.a.FeedSerial(b, ts)
	return nil
}

func (e *Engine) get(id string) *Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sessions[id]
}

// PcapPath returns the on-disk pcap artifact path for a net session.
func (e *Engine) PcapPath(id string) (string, bool) {
	s := e.get(id)
	if s == nil || s.PcapPath == "" {
		return "", false
	}
	return s.PcapPath, true
}

// Sessions lists active session summaries.
func (e *Engine) Sessions() []map[string]interface{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]map[string]interface{}, 0, len(e.sessions))
	for _, s := range e.sessions {
		snap := s.a.Snapshot(0)
		out = append(out, map[string]interface{}{
			"id": s.ID, "kind": s.Kind, "source": s.Source, "status": snap.Status,
			"packets": snap.Packets, "startedAt": s.StartedAt,
		})
	}
	return out
}

// Analyze returns the full structured analysis for a session.
func (e *Engine) Analyze(id string, topFlows int) (Analysis, bool) {
	s := e.get(id)
	if s == nil {
		return Analysis{}, false
	}
	return s.a.Snapshot(topFlows), true
}

// Tail returns the last n decoded events for a session.
func (e *Engine) Tail(id string, n int) ([]Event, bool) {
	s := e.get(id)
	if s == nil {
		return nil, false
	}
	return s.a.Tail(n), true
}

// Stop ends a session and returns its final analysis.
func (e *Engine) Stop(id string) (Analysis, bool) {
	s := e.get(id)
	if s == nil {
		return Analysis{}, false
	}
	s.mu.Lock()
	if !s.done {
		s.done = true
		if s.cancel != nil {
			s.cancel()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		if s.closer != nil {
			_ = s.closer.Close()
		}
	}
	s.mu.Unlock()
	s.a.setStatus("stopped")
	return s.a.Snapshot(0), true
}

// runNet pipes tcpdump's pcap stream through the pure-Go decoder, teeing the raw
// pcap to a local file for later offline decode/download.
func (s *Session) runNet(ctx context.Context, iface, filter, pcapPath string) {
	rc, cmd, err := startTcpdump(ctx, iface, filter)
	if err != nil {
		s.a.setError(err.Error())
		return
	}
	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()
	defer rc.Close()

	var rdr io.Reader = rc
	if f, ferr := os.Create(pcapPath); ferr == nil {
		defer f.Close()
		rdr = io.TeeReader(rc, f)
	}
	pr, err := newPcapReader(rdr)
	if err != nil {
		s.a.setError("pcap: " + err.Error() + " (is tcpdump permitted? needs root / CAP_NET_RAW)")
		return
	}
	for {
		select {
		case <-ctx.Done():
			s.a.setStatus("stopped")
			return
		default:
		}
		ts, data, rerr := pr.next()
		if rerr != nil {
			break
		}
		if pkt, ok := decodePacket(pr.linkType, data, ts); ok {
			s.a.Feed(pkt)
		}
	}
	s.a.setStatus("stopped")
}

// runSerial reads the tty in chunks and feeds the decoder.
func (s *Session) runSerial(ctx context.Context, rc io.ReadCloser) {
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			s.a.setStatus("stopped")
			return
		default:
		}
		n, err := rc.Read(buf)
		if n > 0 {
			s.a.FeedSerial(append([]byte(nil), buf[:n]...), nowMs())
		}
		if err != nil {
			if err != io.EOF {
				s.a.disc = append(s.a.disc, DisconnectEvent{TS: nowMs(), Flow: "serial:" + s.Source, Cause: "serial_idle", Note: "read error: " + err.Error()})
			}
			break
		}
	}
	s.a.setStatus("stopped")
}

// DecodePcapFile decodes an existing pcap file with the pure-Go decoders (the
// netcapture_pcap_decode verb — no tshark needed). Bounded by maxPackets.
func DecodePcapFile(path string, maxPackets int) (Analysis, error) {
	f, err := os.Open(path)
	if err != nil {
		return Analysis{}, err
	}
	defer f.Close()
	pr, err := newPcapReader(f)
	if err != nil {
		return Analysis{}, err
	}
	a := newAnalyzer("file:"+filepath.Base(path), "net", path, nowMs())
	count := 0
	for {
		ts, data, rerr := pr.next()
		if rerr != nil {
			break
		}
		if pkt, ok := decodePacket(pr.linkType, data, ts); ok {
			a.Feed(pkt)
		}
		count++
		if maxPackets > 0 && count >= maxPackets {
			break
		}
	}
	a.setStatus("stopped")
	an := a.Snapshot(0)
	an.UpdatedAt = nowMs()
	return an, nil
}
