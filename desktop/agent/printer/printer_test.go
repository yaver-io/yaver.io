package printer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"
)

func TestParseSSDP_P1S(t *testing.T) {
	// The exact NOTIFY a real P1S broadcast on the LAN (192.0.2.11).
	raw := "NOTIFY * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"Server: UPnP/1.0\r\n" +
		"Location: 192.0.2.11\r\n" +
		"NT: urn:bambulab-com:device:3dprinter:1\r\n" +
		"USN: 01P00X000000000\r\n" +
		"DevModel.bambu.com: C12\r\n" +
		"DevName.bambu.com: 3DP-01P-978\r\n" +
		"DevSignal.bambu.com: -41\r\n" +
		"DevConnect.bambu.com: cloud\r\n" +
		"DevBind.bambu.com: occupied\r\n" +
		"DevVersion.bambu.com: 01.09.01.00\r\n"
	d, ok := ParseSSDP([]byte(raw))
	if !ok {
		t.Fatal("expected ok")
	}
	if d.IP != "192.0.2.11" {
		t.Errorf("ip = %q", d.IP)
	}
	if d.Serial != "01P00X000000000" {
		t.Errorf("serial = %q", d.Serial)
	}
	if d.ModelKey != "C12" || d.Model != "P1S" {
		t.Errorf("model = %q/%q, want C12/P1S", d.ModelKey, d.Model)
	}
	if d.Firmware != "01.09.01.00" {
		t.Errorf("fw = %q", d.Firmware)
	}
	if d.SignalDB != -41 {
		t.Errorf("signal = %d", d.SignalDB)
	}
	if d.Connect != "cloud" || d.Bind != "occupied" {
		t.Errorf("connect/bind = %q/%q", d.Connect, d.Bind)
	}
}

func TestParseSSDP_NotBambu(t *testing.T) {
	if _, ok := ParseSSDP([]byte("NOTIFY * HTTP/1.1\r\nNT: upnp:rootdevice\r\n")); ok {
		t.Error("non-bambu datagram should be rejected")
	}
}

func TestModelName(t *testing.T) {
	cases := map[string]string{"C12": "P1S", "C11": "P1P", "N2": "A1", "N1": "A1 mini", "ZZZ": "ZZZ"}
	for code, want := range cases {
		if got := ModelName(code); got != want {
			t.Errorf("ModelName(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestRemainingLenRoundtrip(t *testing.T) {
	for _, n := range []int{0, 1, 127, 128, 16383, 16384, 2097151} {
		enc := appendRemainingLen(nil, n)
		r := bufio.NewReader(bytes.NewReader(enc))
		got, err := readRemainingLen(r)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if got != n {
			t.Errorf("remaining length roundtrip: got %d want %d", got, n)
		}
	}
}

func TestAppendStringAndPublishDecode(t *testing.T) {
	// Build a QoS-0 PUBLISH body and decode it.
	topic := "device/01P00X000000000/report"
	payload := []byte(`{"print":{"mc_percent":42}}`)
	var body []byte
	body = appendString(body, topic)
	body = append(body, payload...)
	msg, err := decodePublish(pktPUBLISH, body)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Topic != topic {
		t.Errorf("topic = %q", msg.Topic)
	}
	if !bytes.Equal(msg.Payload, payload) {
		t.Errorf("payload = %q", msg.Payload)
	}
}

func TestBambuReportToStatus(t *testing.T) {
	raw := `{"print":{
		"nozzle_temper":215.0,"nozzle_target_temper":220.0,
		"bed_temper":60.0,"bed_target_temper":60.0,"chamber_temper":31.0,
		"mc_percent":42,"mc_remaining_time":35,
		"layer_num":80,"total_layer_num":190,
		"gcode_state":"RUNNING","stg_cur":0,"subtask_name":"bracket",
		"spd_lvl":2,"cooling_fan_speed":"15","nozzle_diameter":"0.4",
		"lights_report":[{"node":"chamber_light","mode":"on"}],
		"hms":[]
	}}`
	var rep bambuReport
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Print == nil {
		t.Fatal("nil print")
	}
	st := rep.Print.toStatus()
	if st.State != "printing" {
		t.Errorf("state = %q", st.State)
	}
	if st.Nozzle.Cur != 215 || st.Nozzle.Target != 220 {
		t.Errorf("nozzle = %+v", st.Nozzle)
	}
	if st.Progress != 42 || st.RemainingMin != 35 {
		t.Errorf("progress=%v remaining=%v", st.Progress, st.RemainingMin)
	}
	if st.LayerNum != 80 || st.TotalLayers != 190 {
		t.Errorf("layers = %d/%d", st.LayerNum, st.TotalLayers)
	}
	if st.SubtaskName != "bracket" {
		t.Errorf("subtask = %q", st.SubtaskName)
	}
	if st.NozzleDiameter != 0.4 {
		t.Errorf("nozzleDiameter = %v", st.NozzleDiameter)
	}
	if st.LightOn == nil || !*st.LightOn {
		t.Errorf("lightOn = %v", st.LightOn)
	}
	if st.FanSpeed != 100 { // 15/15 → 100%
		t.Errorf("fanSpeed = %d", st.FanSpeed)
	}
}

func TestMergePartialReports(t *testing.T) {
	var p bambuPrint
	p.merge(bambuPrint{NozzleTemper: 200, GcodeState: "RUNNING"})
	p.merge(bambuPrint{McPercent: 10}) // partial delta — must not wipe nozzle/state
	if p.NozzleTemper != 200 || p.GcodeState != "RUNNING" {
		t.Errorf("merge lost prior fields: %+v", p)
	}
	if p.McPercent != 10 {
		t.Errorf("merge missed new field: %+v", p)
	}
}

func TestNormalizeStateAndStage(t *testing.T) {
	if normalizeState("PAUSE") != "paused" || normalizeState("FINISH") != "finished" {
		t.Error("state map")
	}
	if bambuStageName(2) != "heating bed" {
		t.Error("stage map")
	}
	if bambuStageName(255) != "" {
		t.Error("idle stage should be empty")
	}
}

func TestAuthPacketShape(t *testing.T) {
	pkt := authPacket("bblp", "12345678")
	if len(pkt) != 80 {
		t.Fatalf("auth packet len = %d, want 80", len(pkt))
	}
	if binary.LittleEndian.Uint32(pkt[0:4]) != 0x40 {
		t.Error("header[0] wrong")
	}
	if binary.LittleEndian.Uint32(pkt[4:8]) != 0x3000 {
		t.Error("header[1] wrong")
	}
	if string(bytes.TrimRight(pkt[16:48], "\x00")) != "bblp" {
		t.Errorf("user field = %q", pkt[16:48])
	}
	if string(bytes.TrimRight(pkt[48:80], "\x00")) != "12345678" {
		t.Errorf("code field = %q", pkt[48:80])
	}
}

func TestReadFrame(t *testing.T) {
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0xFF, 0xD9}
	var buf bytes.Buffer
	header := make([]byte, 16)
	binary.LittleEndian.PutUint32(header[0:4], uint32(len(jpeg)))
	buf.Write(header)
	buf.Write(jpeg)
	got, err := readFrame(bufio.NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, jpeg) {
		t.Errorf("frame = %x", got)
	}
}

func TestConfigNormalizeAndRedact(t *testing.T) {
	c := Config{Addr: " 192.0.2.11 ", Serial: "01P00X000000000", AccessCode: " abcd1234 "}
	c.Normalize()
	if c.Driver != "bambu" || c.MQTTPort != 8883 || c.CameraPort != 6000 || c.FTPPort != 990 {
		t.Errorf("defaults wrong: %+v", c)
	}
	if c.Addr != "192.0.2.11" || c.AccessCode != "abcd1234" {
		t.Errorf("trim failed: %q %q", c.Addr, c.AccessCode)
	}
	if c.Model != "P1S" {
		t.Errorf("model inference = %q", c.Model)
	}
	if !c.Enabled() {
		t.Error("should be enabled")
	}
	if c.Redacted().AccessCode == c.AccessCode {
		t.Error("redact failed")
	}
}

func TestStatusContextDeadlineRespected(t *testing.T) {
	// No printer reachable: Status must fail fast, not hang.
	b := NewBambuBackend(Config{Addr: "127.0.0.1", Serial: "x", AccessCode: "y", MQTTPort: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if _, err := b.Status(ctx); err == nil {
		t.Error("expected error connecting to dead port")
	}
}
