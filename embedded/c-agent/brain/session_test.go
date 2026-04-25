package brain

import (
	"bytes"
	"net"
	"testing"
	"time"
)

// fakeDevice runs in a goroutine, plays the device side of the
// protocol against the brain. Useful for end-to-end tests
// without spinning up the full c-agent binary.
type fakeDevice struct {
	t        *testing.T
	conn     *Conn
	wifiResp ToolRsp // canned response to wifi_client_count
}

func (d *fakeDevice) run() {
	// Receive brain's HELLO + send our own.
	hdr, _, err := d.conn.RecvFrame(4096)
	if err != nil {
		d.t.Errorf("device recv brain hello: %v", err)
		return
	}
	if hdr.Type != FrameHello {
		d.t.Errorf("device: expected HELLO, got 0x%02x", uint8(hdr.Type))
		return
	}
	deviceHello := Hello{
		ProtocolVersion: ProtocolVersion,
		Role:            "device",
		AgentVersion:    "yvr-cagent/0.0.1",
	}
	body := make([]byte, 128)
	n, _ := deviceHello.Encode(body)
	if err := d.conn.SendFrame(FrameHeader{Type: FrameHello}, body[:n]); err != nil {
		d.t.Errorf("device send hello: %v", err)
		return
	}

	// Loop: handle invokes, send canned responses.
	for {
		hdr, payload, err := d.conn.RecvFrame(65536)
		if err != nil {
			return
		}
		switch hdr.Type {
		case FrameInvoke:
			req, err := DecodeInvoke(payload)
			if err != nil {
				d.t.Errorf("device decode invoke: %v", err)
				return
			}
			rsp := d.wifiResp
			rsp.ProtocolVersion = ProtocolVersion
			rsp.ToolHash = req.ToolHash
			rspBody := make([]byte, 4096)
			rn, err := rsp.Encode(rspBody)
			if err != nil {
				d.t.Errorf("device encode tool_rsp: %v", err)
				return
			}
			respHdr := FrameHeader{
				Type:     FrameToolRsp,
				StreamID: hdr.StreamID,
			}
			if err := d.conn.SendFrame(respHdr, rspBody[:rn]); err != nil {
				d.t.Errorf("device send tool_rsp: %v", err)
				return
			}
		case FrameHeartbeat:
			// Echo back so the brain knows we're alive.
			if err := d.conn.SendFrame(FrameHeader{Type: FrameHeartbeat, Flags: FlagAck}, payload); err != nil {
				return
			}
		default:
			// Quietly ignore other frames in the test harness.
		}
	}
}

func TestSession_HelloAndInvoke(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	cannedResult := []byte{0x01, 0x02, 0x03}
	cannedHash := []byte{0xAB, 0xAB, 0xAB, 0xAB}

	deviceDone := make(chan struct{})
	go func() {
		defer close(deviceDone)
		nc, err := listener.Accept()
		if err != nil {
			return
		}
		device := &fakeDevice{
			t:    t,
			conn: Wrap(nc),
			wifiResp: ToolRsp{
				Status:     0,
				Result:     cannedResult,
				DurationMs: 42,
			},
		}
		device.run()
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	conn, err := Dial("127.0.0.1", port, 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	sess := NewSession(conn, SessionConfig{
		AgentVersion:  "test-brain/0.1",
		InvokeTimeout: 2 * time.Second,
	})
	defer sess.Close()

	if err := sess.HandleHello(); err != nil {
		t.Fatalf("HandleHello: %v", err)
	}
	if sess.PeerHello.Role != "device" {
		t.Fatalf("PeerHello.Role = %q", sess.PeerHello.Role)
	}
	if sess.PeerHello.AgentVersion != "yvr-cagent/0.0.1" {
		t.Fatalf("PeerHello.AgentVersion = %q", sess.PeerHello.AgentVersion)
	}

	rsp, err := sess.Invoke(Invoke{
		ToolHash: cannedHash,
		Method:   "wifi_client_count",
		Args:     []byte{},
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if rsp.Status != 0 {
		t.Fatalf("Status = %d, want 0", rsp.Status)
	}
	if !bytes.Equal(rsp.Result, cannedResult) {
		t.Fatalf("Result mismatch: got %x", rsp.Result)
	}
	if rsp.DurationMs != 42 {
		t.Fatalf("DurationMs = %d, want 42", rsp.DurationMs)
	}
	if !bytes.Equal(rsp.ToolHash, cannedHash) {
		t.Fatalf("ToolHash echo mismatch")
	}

	sess.Close()
	<-deviceDone
}

func TestSession_Heartbeat(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	deviceDone := make(chan struct{})
	go func() {
		defer close(deviceDone)
		nc, err := listener.Accept()
		if err != nil {
			return
		}
		conn := Wrap(nc)
		// Skip HELLO exchange — this test only checks heartbeat.
		hdr, payload, err := conn.RecvFrame(4096)
		if err != nil || hdr.Type != FrameHello {
			return
		}
		// Send fake device HELLO back.
		dh := Hello{ProtocolVersion: ProtocolVersion, Role: "device"}
		body := make([]byte, 64)
		n, _ := dh.Encode(body)
		conn.SendFrame(FrameHeader{Type: FrameHello}, body[:n])
		_ = payload

		// Now expect a HEARTBEAT and decode it.
		hdr, payload, err = conn.RecvFrame(4096)
		if err != nil {
			t.Errorf("device recv heartbeat: %v", err)
			return
		}
		if hdr.Type != FrameHeartbeat {
			t.Errorf("expected HEARTBEAT, got 0x%02x", uint8(hdr.Type))
			return
		}
		hb, err := DecodeHeartbeat(payload)
		if err != nil {
			t.Errorf("decode heartbeat: %v", err)
			return
		}
		if hb.NowMs == 0 {
			t.Errorf("NowMs is 0")
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	conn, _ := Dial("127.0.0.1", port, 1*time.Second)
	sess := NewSession(conn, SessionConfig{})
	defer sess.Close()
	if err := sess.HandleHello(); err != nil {
		t.Fatalf("HandleHello: %v", err)
	}
	if err := sess.SendHeartbeat(); err != nil {
		t.Fatalf("SendHeartbeat: %v", err)
	}
	<-deviceDone
}
