package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeBoxControl emulates the ESP32 line-control port (:8347).
func fakeBoxControl(t *testing.T) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case cmd == "PING":
						c.Write([]byte("PONG\n"))
					case cmd == "INFO":
						c.Write([]byte("INFO fw=test id=DEADBEEF link=wifi bus=rs485 baud=9600\n"))
					case cmd == "SENSE":
						c.Write([]byte("S cur=12 force=0 tq=0 vin=23900 ibus=12\n"))
					case strings.HasPrefix(cmd, "ABSWAP"), strings.HasPrefix(cmd, "TERM"),
						strings.HasPrefix(cmd, "BIAS"), strings.HasPrefix(cmd, "LED"),
						strings.HasPrefix(cmd, "BAUD"), strings.HasPrefix(cmd, "BUS"):
						c.Write([]byte("OK\n"))
					default:
						c.Write([]byte("ERR unknown\n"))
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

// fakeModbusTCP emulates a Modbus-TCP slave (the box gateway side).
func fakeModbusTCP(t *testing.T, vals []uint16) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				hdr := make([]byte, 7)
				if _, err := io.ReadFull(c, hdr); err != nil {
					return
				}
				pdu := make([]byte, 5)
				if _, err := io.ReadFull(c, pdu); err != nil {
					return
				}
				unit, fc := hdr[6], pdu[0]
				bc := byte(len(vals) * 2)
				resp := []byte{hdr[0], hdr[1], 0, 0, 0, byte(3 + int(bc)), unit, fc, bc}
				for _, v := range vals {
					resp = append(resp, byte(v>>8), byte(v))
				}
				c.Write(resp)
			}(c)
		}
	}()
	return ln.Addr().String(), func() { ln.Close() }
}

func TestBoxControlCmd(t *testing.T) {
	addr, stop := fakeBoxControl(t)
	defer stop()
	if got, err := boxControlCmd(addr, "PING", time.Second); err != nil || got != "PONG" {
		t.Fatalf("PING -> %q, %v", got, err)
	}
	if got, err := boxControlCmd(addr, "ABSWAP 1", time.Second); err != nil || got != "OK" {
		t.Fatalf("ABSWAP -> %q, %v", got, err)
	}
	info, _ := boxControlCmd(addr, "INFO", time.Second)
	if !strings.HasPrefix(info, "INFO ") {
		t.Fatalf("INFO -> %q", info)
	}
}

func TestModbusReadTCP(t *testing.T) {
	want := []uint16{0x1234, 0x00FF, 1250}
	addr, stop := fakeModbusTCP(t, want)
	defer stop()
	got, err := modbusReadTCP(addr, 1, 3, 0, len(want), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("len %d != %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reg[%d]=%#x want %#x", i, got[i], want[i])
		}
	}
}

func TestParseKV(t *testing.T) {
	if v := parseKV("S cur=12 force=0 vin=23900 ibus=12", "vin"); v != 23900 {
		t.Fatalf("vin=%d", v)
	}
	if v := parseKV("S cur=12", "vin"); v != 0 {
		t.Fatalf("missing key should be 0, got %d", v)
	}
}

// modbus header length sanity: the gateway encodes len = unit + pdu.
func TestModbusHeaderLen(t *testing.T) {
	hdr := make([]byte, 7)
	binary.BigEndian.PutUint16(hdr[4:6], 6)
	if binary.BigEndian.Uint16(hdr[4:6]) != 6 {
		t.Fatal("len encode")
	}
}

func TestBoxProfilesIndustrialIoTPositioning(t *testing.T) {
	res := boxProfilesHandler(OpsContext{}, json.RawMessage(`{"domain":"manufacturing"}`))
	if !res.OK {
		t.Fatalf("box_profiles failed: %#v", res)
	}
	initial, ok := res.Initial.(map[string]interface{})
	if !ok {
		t.Fatalf("initial shape = %T", res.Initial)
	}
	if !strings.Contains(initial["positioning"].(string), "industrial IoT edge box") {
		t.Fatalf("positioning = %q", initial["positioning"])
	}
	if !strings.Contains(initial["interoperability"].(string), "Yaver-alone") {
		t.Fatalf("interoperability = %q", initial["interoperability"])
	}
	profiles := initial["profiles"].([]boxProfile)
	if len(profiles) == 0 {
		t.Fatal("expected manufacturing profiles")
	}
	for _, p := range profiles {
		if p.Domain != "manufacturing" {
			t.Fatalf("unexpected domain %q in filtered profile %#v", p.Domain, p)
		}
	}
}

func TestBoxProfileAliasesResolveToPlans(t *testing.T) {
	cases := map[string]string{
		"kalkan":  "kalkan-ocpp-load-balancer",
		"ocpp":    "kalkan-ocpp-load-balancer",
		"talos":   "talos-screwdriver-cell",
		"ender":   "ender-marlin-robotics",
		"fairino": "fairino-cobot-cell",
		"simkab":  "simkab-robotics-machine-cell",
		"jcwelec": "jcwelec-cst18d",
		"yh8030h": "yuanhan-yh8030h",
	}
	for alias, wantID := range cases {
		res := boxProfilePlanHandler(OpsContext{}, json.RawMessage(`{"profile":"`+alias+`"}`))
		if !res.OK {
			t.Fatalf("%s plan failed: %#v", alias, res)
		}
		initial := res.Initial.(map[string]interface{})
		profile := initial["profile"].(boxProfile)
		if profile.ID != wantID {
			t.Fatalf("%s resolved to %s, want %s", alias, profile.ID, wantID)
		}
		steps := initial["steps"].([]boxPlanStep)
		if len(steps) == 0 {
			t.Fatalf("%s returned no steps", alias)
		}
	}
}

func TestBoxProfilePlanUnknown(t *testing.T) {
	res := boxProfilePlanHandler(OpsContext{}, json.RawMessage(`{"profile":"does-not-exist"}`))
	if res.OK || res.Code != "not_found" {
		t.Fatalf("unknown profile result = %#v", res)
	}
}

func TestBoxBOMCatalogHasRecommendedBuilds(t *testing.T) {
	res := boxBOMHandler(OpsContext{}, json.RawMessage(`{}`))
	if !res.OK {
		t.Fatalf("box_bom failed: %#v", res)
	}
	initial := res.Initial.(map[string]interface{})
	if initial["currency"] != "USD" {
		t.Fatalf("currency = %#v", initial["currency"])
	}
	boms := initial["boms"].([]boxBOM)
	if len(boms) < 5 {
		t.Fatalf("expected Lite/Pro/Max/reference/china BOMs, got %d", len(boms))
	}
	seen := map[string]bool{}
	for _, b := range boms {
		seen[b.SKU] = true
		if b.TotalLowUSD <= 0 || b.TotalHighUSD < b.TotalLowUSD {
			t.Fatalf("bad cost range for %#v", b)
		}
	}
	for _, sku := range []string{"lite", "pro", "max", "reference", "china"} {
		if !seen[sku] {
			t.Fatalf("missing BOM sku %s", sku)
		}
	}
}

func TestBoxBOMFilter(t *testing.T) {
	res := boxBOMHandler(OpsContext{}, json.RawMessage(`{"sku":"pro"}`))
	if !res.OK {
		t.Fatalf("box_bom pro failed: %#v", res)
	}
	initial := res.Initial.(map[string]interface{})
	bom := initial["bom"].(boxBOM)
	if bom.SKU != "pro" {
		t.Fatalf("sku = %s", bom.SKU)
	}
	if bom.TotalLowUSD >= bom.TotalHighUSD {
		t.Fatalf("bad pro cost range: %d-%d", bom.TotalLowUSD, bom.TotalHighUSD)
	}
}
