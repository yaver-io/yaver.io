package arm

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MyCobotBackend drives an Elephant Robotics myCobot (and protocol-compatible
// arms: mechArm, myPalletizer, …) over the pymycobot binary serial protocol —
// either a USB serial tty ("/dev/...") or TCP ("ip:port") for the Pro/socket
// models. Frame: [0xFE 0xFE LEN genre …data 0xFA], LEN = len(data)+2. Angles are
// signed BE int16 of deg*100; coords are BE int16 of value*10.
type MyCobotBackend struct {
	cfg  Config
	info ArmInfo
	dof  int
	tcp  bool

	mu   sync.Mutex
	conn io.ReadWriteCloser
	r    *bufio.Reader
}

const (
	mcHeader = 0xFE
	mcFooter = 0xFA

	mcPowerOn          = 0x10
	mcPowerOff         = 0x11
	mcReleaseAllServos = 0x13
	mcGetAngles        = 0x20
	mcSendAngles       = 0x22
	mcGetCoords        = 0x23
	mcSendCoords       = 0x25
	mcStop             = 0x29
	mcIsMoving         = 0x2B
)

func NewMyCobotBackend(cfg Config) *MyCobotBackend {
	info := cfg.Info
	if len(info.Joints) == 0 {
		info = MyCobotDefaults()
	}
	info.Normalize()
	return &MyCobotBackend{
		cfg:  cfg,
		info: info,
		dof:  len(info.Joints),
		tcp:  !strings.HasPrefix(cfg.Addr, "/dev/"),
	}
}

// MyCobotDefaults is the myCobot 280 6-DOF joint table (deg soft limits). Real
// per-model limits can be tightened in the UI; DOF is confirmed from GET_ANGLES.
func MyCobotDefaults() ArmInfo {
	lim := [][2]float64{{-168, 168}, {-135, 135}, {-150, 150}, {-145, 145}, {-165, 165}, {-175, 175}}
	js := make([]JointSpec, len(lim))
	for i, l := range lim {
		js[i] = JointSpec{Name: jointName(i), Type: JointRevolute, Min: l[0], Max: l[1], Unit: "deg", MaxVel: 120}
	}
	return ArmInfo{Model: "myCobot 280", Vendor: "Elephant Robotics", Joints: js, HasCartesian: true, PoseFrame: "base", DOF: 6, Source: "config"}
}

func (b *MyCobotBackend) Name() string { return "mycobot" }

func (b *MyCobotBackend) baud() int {
	if b.cfg.Baud > 0 {
		return b.cfg.Baud
	}
	return 115200 // myCobot M5 default; Pi models use 1000000 (set Baud)
}

func (b *MyCobotBackend) open() error {
	if b.tcp {
		addr := b.cfg.Addr
		if b.cfg.Port > 0 && !strings.Contains(addr, ":") {
			addr = fmt.Sprintf("%s:%d", addr, b.cfg.Port)
		}
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return err
		}
		b.conn, b.r = c, bufio.NewReader(c)
		return nil
	}
	// serial: raw mode at baud (stty + OpenFile, same minimal approach as robot)
	_ = exec.Command("stty", "-F", b.cfg.Addr, strconv.Itoa(b.baud()), "cs8", "-cstopb", "-parenb", "raw", "-echo", "clocal").Run()
	f, err := os.OpenFile(b.cfg.Addr, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	b.conn, b.r = f, bufio.NewReader(f)
	return nil
}

func (b *MyCobotBackend) ensure() error {
	if b.conn != nil {
		return nil
	}
	return b.open()
}

func (b *MyCobotBackend) Connect(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.ensure(); err != nil {
		return fmt.Errorf("mycobot open %s: %w", b.cfg.Addr, err)
	}
	return nil
}

func (b *MyCobotBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil {
		err := b.conn.Close()
		b.conn, b.r = nil, nil
		return err
	}
	return nil
}

// frame builds [FE FE LEN genre ...data FA].
func frameMyCobot(genre byte, data []byte) []byte {
	out := []byte{mcHeader, mcHeader, byte(len(data) + 2), genre}
	out = append(out, data...)
	out = append(out, mcFooter)
	return out
}

// txn writes a command; when wantReply, reads one reply frame's data bytes.
func (b *MyCobotBackend) txn(genre byte, data []byte, wantReply bool) ([]byte, error) {
	if err := b.ensure(); err != nil {
		return nil, err
	}
	if _, err := b.conn.Write(frameMyCobot(genre, data)); err != nil {
		b.reset()
		return nil, err
	}
	if !wantReply {
		return nil, nil
	}
	return b.readFrame(genre)
}

func (b *MyCobotBackend) reset() {
	if b.conn != nil {
		_ = b.conn.Close()
	}
	b.conn, b.r = nil, nil
}

// readFrame scans for FE FE, reads LEN, then the LEN bytes (genre+data+footer),
// validates the footer, and returns the data for the matching genre.
func (b *MyCobotBackend) readFrame(want byte) ([]byte, error) {
	deadline := time.Now().Add(3 * time.Second)
	if c, ok := b.conn.(net.Conn); ok {
		_ = c.SetReadDeadline(deadline)
	}
	for time.Now().Before(deadline) {
		// sync to header FE FE
		b0, err := b.r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b0 != mcHeader {
			continue
		}
		b1, err := b.r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b1 != mcHeader {
			continue
		}
		ln, err := b.r.ReadByte()
		if err != nil {
			return nil, err
		}
		body := make([]byte, int(ln))
		if _, err := io.ReadFull(b.r, body); err != nil {
			return nil, err
		}
		if len(body) < 2 || body[len(body)-1] != mcFooter {
			continue // bad frame, resync
		}
		genre := body[0]
		data := body[1 : len(body)-1]
		if genre == want {
			return data, nil
		}
	}
	return nil, fmt.Errorf("mycobot: timeout waiting for reply 0x%02x", want)
}

func (b *MyCobotBackend) Describe(ctx context.Context) (ArmInfo, error) {
	info := b.info
	b.mu.Lock()
	data, err := b.txn(mcGetAngles, nil, true)
	b.mu.Unlock()
	if err == nil {
		if n := len(data) / 2; n > 0 {
			info.Source = "robot"
			if n != len(info.Joints) {
				js := make([]JointSpec, n)
				for i := 0; i < n; i++ {
					if i < len(info.Joints) {
						js[i] = info.Joints[i]
					} else {
						js[i] = JointSpec{Name: jointName(i), Type: JointRevolute, Min: -180, Max: 180, Unit: "deg"}
					}
				}
				info.Joints = js
			}
		}
	}
	info.Normalize()
	b.info = info
	b.dof = len(info.Joints)
	return info, nil
}

func (b *MyCobotBackend) Status(ctx context.Context) (ArmStatus, error) {
	st := ArmStatus{Backend: b.Name()}
	js, err := b.JointState(ctx)
	if err != nil {
		st.Error = err.Error()
		return st, err
	}
	st.OK, st.Connected, st.Enabled, st.Joints = true, true, true, js
	if p, perr := b.Pose(ctx); perr == nil {
		st.Pose = &p
	}
	return st, nil
}

func (b *MyCobotBackend) Enable(ctx context.Context, on bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	genre := byte(mcPowerOff)
	if on {
		genre = mcPowerOn
	}
	_, err := b.txn(genre, nil, false)
	return err
}

func (b *MyCobotBackend) JointState(ctx context.Context) ([]JointState, error) {
	b.mu.Lock()
	data, err := b.txn(mcGetAngles, nil, true)
	b.mu.Unlock()
	if err != nil {
		return nil, err
	}
	vals := decodeInt16s(data)
	out := make([]JointState, 0, len(vals))
	for i, raw := range vals {
		name := jointName(i)
		if i < len(b.info.Joints) {
			name = b.info.Joints[i].Name
		}
		out = append(out, JointState{Name: name, Position: float64(raw) / 100.0, Unit: "deg"})
	}
	return out, nil
}

func (b *MyCobotBackend) Pose(ctx context.Context) (Pose, error) {
	b.mu.Lock()
	data, err := b.txn(mcGetCoords, nil, true)
	b.mu.Unlock()
	if err != nil {
		return Pose{}, err
	}
	d := decodeInt16s(data)
	if len(d) < 6 {
		return Pose{}, ErrNoCartesian
	}
	// xyz and rpy both scale by 10 in the myCobot protocol.
	return Pose{X: float64(d[0]) / 10, Y: float64(d[1]) / 10, Z: float64(d[2]) / 10,
		Roll: float64(d[3]) / 10, Pitch: float64(d[4]) / 10, Yaw: float64(d[5]) / 10}, nil
}

func (b *MyCobotBackend) MoveJoints(ctx context.Context, targets map[string]float64, velPct, accPct int) error {
	cur, err := b.JointState(ctx)
	if err != nil {
		return err
	}
	data := make([]byte, 0, len(cur)*2+1)
	for i, j := range cur {
		val := j.Position
		for name, v := range targets {
			if strings.EqualFold(name, j.Name) {
				val = v
			}
		}
		_ = i
		data = appendInt16(data, int16(math.Round(val*100)))
	}
	data = append(data, byte(clampSpeed(velPct))) // speed 0..100
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err = b.txn(mcSendAngles, data, false)
	return err
}

func (b *MyCobotBackend) MoveLinear(ctx context.Context, p Pose, velPct, accPct int) error {
	data := make([]byte, 0, 14)
	for _, v := range []float64{p.X, p.Y, p.Z, p.Roll, p.Pitch, p.Yaw} {
		data = appendInt16(data, int16(math.Round(v*10)))
	}
	data = append(data, byte(clampSpeed(velPct)))
	data = append(data, 1) // mode 1 = linear (MoveL); 0 = MoveJ-interp
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := b.txn(mcSendCoords, data, false)
	return err
}

// WaitIdle polls IS_MOVING until it reports stopped (or times out).
func (b *MyCobotBackend) WaitIdle(ctx context.Context) error {
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		b.mu.Lock()
		data, err := b.txn(mcIsMoving, nil, true)
		b.mu.Unlock()
		if err != nil {
			return nil // can't tell — don't block the pipeline
		}
		if len(data) > 0 && data[0] == 0 {
			return nil
		}
		time.Sleep(120 * time.Millisecond)
	}
	return nil
}

func (b *MyCobotBackend) Stop(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := b.txn(mcStop, nil, false)
	return err
}

func (b *MyCobotBackend) EStop(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, _ = b.txn(mcStop, nil, false)
	_, err := b.txn(mcReleaseAllServos, nil, false) // de-energize: strongest safe stop
	return err
}

// FreeDrive: release all servos → the arm is hand-movable (learning mode); off
// re-energizes (power on) so it holds position.
func (b *MyCobotBackend) FreeDrive(ctx context.Context, on bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	genre := byte(mcPowerOn)
	if on {
		genre = mcReleaseAllServos
	}
	_, err := b.txn(genre, nil, false)
	return err
}

// Raw runs "0xHH b0 b1 ..." — a genre byte then data bytes (decimal or 0x hex).
func (b *MyCobotBackend) Raw(ctx context.Context, cmd string) (string, error) {
	fields := strings.Fields(strings.TrimSpace(cmd))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty command")
	}
	genre, err := parseByte(fields[0])
	if err != nil {
		return "", err
	}
	data := make([]byte, 0, len(fields)-1)
	for _, f := range fields[1:] {
		bb, err := parseByte(f)
		if err != nil {
			return "", err
		}
		data = append(data, bb)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	reply, err := b.txn(genre, data, true)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%v", decodeInt16s(reply)), nil
}

func decodeInt16s(b []byte) []int16 {
	out := make([]int16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		out = append(out, int16(binary.BigEndian.Uint16(b[i:i+2])))
	}
	return out
}

func appendInt16(b []byte, v int16) []byte {
	return append(b, byte(uint16(v)>>8), byte(uint16(v)&0xFF))
}

func clampSpeed(p int) int {
	if p < 1 {
		return 1
	}
	if p > 100 {
		return 100
	}
	return p
}

func parseByte(s string) (byte, error) {
	s = strings.TrimSpace(s)
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		s, base = s[2:], 16
	}
	v, err := strconv.ParseUint(s, base, 8)
	return byte(v), err
}
