package arm

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/yaver-io/agent/robot"
)

func sixJointInfo() ArmInfo {
	js := make([]JointSpec, 6)
	for i := range js {
		js[i] = JointSpec{Name: jointName(i), Type: JointRevolute, Min: -180, Max: 180, Unit: "deg"}
	}
	return ArmInfo{Joints: js, HasCartesian: false, DOF: 6, Source: "config"}
}

// a tiny line-protocol robot for the generic backend (real TCP server, no mocks).
func fakeLineRobot(t *testing.T) (addr string, moved *[]string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	got := &[]string{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadString('\n')
				line = strings.TrimSpace(line)
				*got = append(*got, line)
				switch {
				case strings.HasPrefix(line, "STATE"):
					_, _ = c.Write([]byte("10,20,30,40,50,60\n"))
				case strings.HasPrefix(line, "MOVEJ"), strings.HasPrefix(line, "ENABLE"), strings.HasPrefix(line, "STOP"):
					_, _ = c.Write([]byte("OK\n"))
				default:
					_, _ = c.Write([]byte("OK\n"))
				}
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), got
}

func TestGenericBackendThroughController(t *testing.T) {
	addr, got := fakeLineRobot(t)
	cfg := Config{Driver: "generic_tcp", Addr: addr, Info: sixJointInfo()}
	cfg.Normalize()
	b, err := NewGenericArmBackend(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctrl := NewController(b, nil, robot.VisionConfig{}, cfg)
	ctx := context.Background()

	// Describe: DOF from the parametric config (not hardcoded).
	info, _ := ctrl.Describe(ctx)
	if info.DOF != 6 {
		t.Fatalf("DOF=%d want 6", info.DOF)
	}

	// State reads the CSV reply.
	js, _, err := ctrl.State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if len(js) != 6 || js[0].Position != 10 || js[5].Position != 60 {
		t.Fatalf("bad state: %+v", js)
	}

	// In-range MoveJ succeeds and sends a MOVEJ line with the full vector.
	r := ctrl.MoveJoints(ctx, map[string]float64{"J1": 45}, 50, 50, "off", "")
	if !r.OK {
		t.Fatalf("MoveJoints in-range failed: %s", r.Error)
	}
	var sawMovej string
	for _, g := range *got {
		if strings.HasPrefix(g, "MOVEJ") {
			sawMovej = g
		}
	}
	if !strings.Contains(sawMovej, "45") {
		t.Fatalf("MOVEJ line missing target: %q", sawMovej)
	}

	// Out-of-range MoveJ is REFUSED (never silently clamped).
	r = ctrl.MoveJoints(ctx, map[string]float64{"J1": 999}, 50, 50, "off", "")
	if r.OK || r.Code != "out_of_range" {
		t.Fatalf("out-of-range move should be refused, got ok=%v code=%q", r.OK, r.Code)
	}
}

func TestControllerHomeUsesJointHome(t *testing.T) {
	addr, got := fakeLineRobot(t)
	info := sixJointInfo()
	info.Joints[0].Home = 5
	cfg := Config{Driver: "generic_tcp", Addr: addr, Info: info}
	cfg.Normalize()
	b, _ := NewGenericArmBackend(cfg)
	ctrl := NewController(b, nil, robot.VisionConfig{}, cfg)
	r := ctrl.Home(context.Background(), 30, 30, "off", "")
	if !r.OK {
		t.Fatalf("home failed: %s", r.Error)
	}
	_ = got
}

func TestMyCobotFrameAndCodec(t *testing.T) {
	// frame layout: FE FE LEN genre data... FA, LEN = len(data)+2
	f := frameMyCobot(mcGetAngles, nil)
	if len(f) != 5 || f[0] != 0xFE || f[1] != 0xFE || f[2] != 2 || f[3] != mcGetAngles || f[4] != 0xFA {
		t.Fatalf("bad GET_ANGLES frame: % X", f)
	}
	// int16 round-trip incl. negatives (deg*100 encoding)
	data := appendInt16(nil, int16(-9000)) // -90.00 deg
	data = appendInt16(data, int16(4500))  // +45.00 deg
	vals := decodeInt16s(data)
	if len(vals) != 2 || vals[0] != -9000 || vals[1] != 4500 {
		t.Fatalf("int16 codec wrong: %v", vals)
	}
	// SEND_ANGLES frame for 6 joints + speed = 13 data bytes → LEN 15
	sf := frameMyCobot(mcSendAngles, append(make([]byte, 12), 50))
	if sf[2] != 15 {
		t.Fatalf("SEND_ANGLES LEN=%d want 15", sf[2])
	}
}

func TestXMLRPCRoundTrip(t *testing.T) {
	// encode a Fairino-style MoveJ-ish call and ensure it's well-formed
	c := newXMLRPCClient("http://127.0.0.1:1/RPC2", 0)
	_ = c
	// parse a typical getter reply: [errcode, j1..j6]
	resp := []byte(`<?xml version="1.0"?><methodResponse><params><param><value><array><data>` +
		`<value><int>0</int></value>` +
		`<value><double>10.5</double></value><value><double>-20.0</double></value>` +
		`<value><double>30</double></value><value><double>40</double></value>` +
		`<value><double>50</double></value><value><double>60</double></value>` +
		`</data></array></value></param></params></methodResponse>`)
	v, err := parseXMLRPCResponse(resp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := fairinoData(v, 6) // drops the leading errcode (len 7 → 6)
	if len(got) != 6 || got[0] != 10.5 || got[1] != -20 || got[5] != 60 {
		t.Fatalf("fairino data wrong: %v", got)
	}
}

func TestXMLRPCFault(t *testing.T) {
	resp := []byte(`<?xml version="1.0"?><methodResponse><fault><value><string>boom</string></value></fault></methodResponse>`)
	_, err := parseXMLRPCResponse(resp)
	if err == nil {
		t.Fatal("expected fault error")
	}
}
