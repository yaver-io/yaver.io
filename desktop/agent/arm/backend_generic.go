package arm

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GenericArmBackend drives ANY robot with a line protocol — a TCP socket
// ("ip:port") or a serial tty ("/dev/...") — purely from Config.CommandTemplates.
// No robot-specific code: each logical op is a template string with
// {placeholders}, and replies to state/pose are parsed by regex. This is the
// "wire a new robot by parameters" path; DOF + limits come from Config.Info.
//
// Placeholders: {joints} (comma-joined joint values, in Info order), {jN}
// (1-based), {x}{y}{z}{roll}{pitch}{yaw}, {vel}{acc}, {on} (1/0 for enable).
type GenericArmBackend struct {
	cfg     Config
	info    ArmInfo
	stateRe *regexp.Regexp
	poseRe  *regexp.Regexp
	serial  bool
}

func NewGenericArmBackend(cfg Config) (*GenericArmBackend, error) {
	info := cfg.Info
	info.Normalize()
	b := &GenericArmBackend{cfg: cfg, info: info, serial: strings.HasPrefix(cfg.Addr, "/dev/")}
	if cfg.StateParse != "" {
		re, err := regexp.Compile(cfg.StateParse)
		if err != nil {
			return nil, fmt.Errorf("bad stateParse regex: %w", err)
		}
		b.stateRe = re
	}
	if cfg.PoseParse != "" {
		re, err := regexp.Compile(cfg.PoseParse)
		if err != nil {
			return nil, fmt.Errorf("bad poseParse regex: %w", err)
		}
		b.poseRe = re
	}
	return b, nil
}

// DefaultGenericTemplates is a sensible CSV line protocol so a simple robot/sim
// works out of the box: commands are uppercase verbs, joint state is a CSV reply.
func DefaultGenericTemplates() (map[string]string, string) {
	return map[string]string{
		"enable":     "ENABLE {on}",
		"stop":       "STOP",
		"estop":      "ESTOP",
		"state":      "STATE",
		"pose":       "POSE",
		"moveJoints": "MOVEJ {joints} {vel} {acc}",
		"movePose":   "MOVEL {x} {y} {z} {roll} {pitch} {yaw} {vel} {acc}",
	}, ""
}

func (b *GenericArmBackend) Name() string { return "generic" }

func (b *GenericArmBackend) tmpl(op string) string {
	if b.cfg.CommandTemplates != nil {
		if t, ok := b.cfg.CommandTemplates[op]; ok {
			return t
		}
	}
	def, _ := DefaultGenericTemplates()
	return def[op]
}

func (b *GenericArmBackend) Connect(ctx context.Context) error {
	if strings.TrimSpace(b.cfg.Addr) == "" {
		return fmt.Errorf("generic arm: no addr (set ip:port or /dev/tty...)")
	}
	if _, err := b.send(ctx, b.tmpl("state")); err != nil {
		return fmt.Errorf("generic arm connect (%s): %w", b.cfg.Addr, err)
	}
	return nil
}

func (b *GenericArmBackend) Close() error { return nil }

func (b *GenericArmBackend) Describe(ctx context.Context) (ArmInfo, error) {
	info := b.info
	if info.Source == "" {
		info.Source = "config" // generic robots can't introspect DOF — the UI defines it
	}
	info.Normalize()
	return info, nil
}

func (b *GenericArmBackend) Status(ctx context.Context) (ArmStatus, error) {
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

func (b *GenericArmBackend) Enable(ctx context.Context, on bool) error {
	t := b.tmpl("enable")
	if t == "" {
		return nil
	}
	_, err := b.send(ctx, b.expand(t, nil, nil, 0, 0, on))
	return err
}

func (b *GenericArmBackend) JointState(ctx context.Context) ([]JointState, error) {
	reply, err := b.send(ctx, b.tmpl("state"))
	if err != nil {
		return nil, err
	}
	vals := b.parseNumbers(reply, b.stateRe, len(b.info.Joints))
	out := make([]JointState, 0, len(b.info.Joints))
	for i, j := range b.info.Joints {
		pos := 0.0
		if i < len(vals) {
			pos = vals[i]
		}
		out = append(out, JointState{Name: j.Name, Position: pos, Unit: j.unit()})
	}
	return out, nil
}

func (b *GenericArmBackend) Pose(ctx context.Context) (Pose, error) {
	if !b.info.HasCartesian {
		return Pose{}, ErrNoCartesian
	}
	t := b.tmpl("pose")
	if t == "" {
		return Pose{}, ErrNoCartesian
	}
	reply, err := b.send(ctx, t)
	if err != nil {
		return Pose{}, err
	}
	d := b.parseNumbers(reply, b.poseRe, 6)
	if len(d) < 6 {
		return Pose{}, ErrNoCartesian
	}
	return Pose{X: d[0], Y: d[1], Z: d[2], Roll: d[3], Pitch: d[4], Yaw: d[5]}, nil
}

func (b *GenericArmBackend) MoveJoints(ctx context.Context, targets map[string]float64, velPct, accPct int) error {
	// Full ordered vector: current overlaid with targets.
	cur, err := b.JointState(ctx)
	if err != nil {
		return err
	}
	vec := make([]float64, len(b.info.Joints))
	for i, j := range b.info.Joints {
		if i < len(cur) {
			vec[i] = cur[i].Position
		}
		for name, v := range targets {
			if strings.EqualFold(name, j.Name) {
				vec[i] = v
			}
		}
	}
	_, err = b.send(ctx, b.expand(b.tmpl("moveJoints"), vec, nil, velPct, accPct, false))
	return err
}

func (b *GenericArmBackend) MoveLinear(ctx context.Context, p Pose, velPct, accPct int) error {
	if !b.info.HasCartesian {
		return ErrNoCartesian
	}
	_, err := b.send(ctx, b.expand(b.tmpl("movePose"), nil, &p, velPct, accPct, false))
	return err
}

func (b *GenericArmBackend) WaitIdle(ctx context.Context) error {
	if t := b.tmpl("wait"); t != "" {
		_, err := b.send(ctx, t)
		return err
	}
	return nil
}

func (b *GenericArmBackend) Stop(ctx context.Context) error {
	if t := b.tmpl("stop"); t != "" {
		_, err := b.send(ctx, t)
		return err
	}
	return nil
}

func (b *GenericArmBackend) EStop(ctx context.Context) error {
	if t := b.tmpl("estop"); t != "" {
		_, err := b.send(ctx, t)
		return err
	}
	return b.Stop(ctx)
}

func (b *GenericArmBackend) FreeDrive(ctx context.Context, on bool) error {
	t := b.tmpl("freedrive")
	if t == "" {
		return ErrNoFreeDrive
	}
	_, err := b.send(ctx, b.expand(t, nil, nil, 0, 0, on))
	return err
}

func (b *GenericArmBackend) Raw(ctx context.Context, cmd string) (string, error) {
	return b.send(ctx, cmd)
}

// expand fills a template's placeholders.
func (b *GenericArmBackend) expand(t string, joints []float64, p *Pose, velPct, accPct int, on bool) string {
	if t == "" {
		return ""
	}
	rep := func(s, k, v string) string { return strings.ReplaceAll(s, k, v) }
	if joints != nil {
		parts := make([]string, len(joints))
		for i, v := range joints {
			parts[i] = num(v)
			t = rep(t, "{j"+itoa(i+1)+"}", num(v))
		}
		t = rep(t, "{joints}", strings.Join(parts, ","))
	}
	if p != nil {
		t = rep(t, "{x}", num(p.X))
		t = rep(t, "{y}", num(p.Y))
		t = rep(t, "{z}", num(p.Z))
		t = rep(t, "{roll}", num(p.Roll))
		t = rep(t, "{pitch}", num(p.Pitch))
		t = rep(t, "{yaw}", num(p.Yaw))
	}
	t = rep(t, "{vel}", itoa(clampPct(orDefault(velPct, b.cfg.DefaultVelPct))))
	t = rep(t, "{acc}", itoa(clampPct(orDefault(accPct, b.cfg.DefaultAccPct))))
	if on {
		t = rep(t, "{on}", "1")
	} else {
		t = rep(t, "{on}", "0")
	}
	return t
}

// parseNumbers extracts ordered float values from a reply line. With a regex
// (named groups j1,j2,… or x,y,z,…) it uses those; otherwise it scans every
// number in order.
func (b *GenericArmBackend) parseNumbers(reply string, re *regexp.Regexp, want int) []float64 {
	if re != nil {
		m := re.FindStringSubmatch(reply)
		if m != nil {
			names := re.SubexpNames()
			byName := map[string]float64{}
			for i, n := range names {
				if i == 0 || n == "" || i >= len(m) {
					continue
				}
				if f, err := strconv.ParseFloat(strings.TrimSpace(m[i]), 64); err == nil {
					byName[n] = f
				}
			}
			// ordered j1..jN if present
			var out []float64
			for i := 1; ; i++ {
				v, ok := byName["j"+itoa(i)]
				if !ok {
					break
				}
				out = append(out, v)
			}
			if len(out) > 0 {
				return out
			}
			// else pose order
			for _, n := range []string{"x", "y", "z", "roll", "pitch", "yaw"} {
				if v, ok := byName[n]; ok {
					out = append(out, v)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return scanFloats(reply)
}

var floatRe = regexp.MustCompile(`-?\d+\.?\d*`)

func scanFloats(s string) []float64 {
	var out []float64
	for _, m := range floatRe.FindAllString(s, -1) {
		if f, err := strconv.ParseFloat(m, 64); err == nil {
			out = append(out, f)
		}
	}
	return out
}

// send writes one line and reads one reply line (TCP dial-per-call, or serial).
func (b *GenericArmBackend) send(ctx context.Context, line string) (string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", nil
	}
	if b.serial {
		return b.sendSerial(line)
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", b.cfg.Addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	}
	if _, err := conn.Write([]byte(line + "\n")); err != nil {
		return "", err
	}
	reply, err := bufio.NewReader(conn).ReadString('\n')
	return strings.TrimRight(reply, "\r\n"), err
}

func (b *GenericArmBackend) sendSerial(line string) (string, error) {
	f, err := os.OpenFile(b.cfg.Addr, os.O_RDWR, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write([]byte(line + "\n")); err != nil {
		return "", err
	}
	r := bufio.NewReader(f)
	_ = f.SetDeadline(time.Now().Add(3 * time.Second))
	reply, _ := r.ReadString('\n')
	return strings.TrimRight(reply, "\r\n"), nil
}

func num(f float64) string  { return strconv.FormatFloat(f, 'f', -1, 64) }
func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
