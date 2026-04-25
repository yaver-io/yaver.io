package brain

import (
	"bytes"
	"net"
	"testing"
	"time"
)

// echoServer accepts one connection, reads one frame, sends it
// back with type = HEARTBEAT and the ACK flag set. Mirrors the
// "echo first frame" behaviour the C TCP test uses, so a Go +
// C cross-language loopback test (when added) sees identical
// semantics on both sides.
func echoServer(t *testing.T, listener net.Listener, done chan<- struct{}) {
	t.Helper()
	defer close(done)
	nc, err := listener.Accept()
	if err != nil {
		t.Errorf("accept: %v", err)
		return
	}
	srv := Wrap(nc)
	defer srv.Close()

	hdr, payload, err := srv.RecvFrame(4096)
	if err != nil {
		t.Errorf("server recv: %v", err)
		return
	}
	resp := FrameHeader{
		Type:     FrameHeartbeat,
		Flags:    FlagAck,
		StreamID: hdr.StreamID,
	}
	if err := srv.SendFrame(resp, payload); err != nil {
		t.Errorf("server send: %v", err)
	}
}

func TestTransport_Loopback(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go echoServer(t, listener, done)

	port := listener.Addr().(*net.TCPAddr).Port
	client, err := Dial("127.0.0.1", port, 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	payload := []byte{0x11, 0x22, 0x33, 0x44}
	hdr := FrameHeader{Type: FrameHello, StreamID: 7}
	if err := client.SendFrame(hdr, payload); err != nil {
		t.Fatal(err)
	}

	gotHdr, gotPayload, err := client.RecvFrame(4096)
	if err != nil {
		t.Fatal(err)
	}
	if gotHdr.Type != FrameHeartbeat {
		t.Fatalf("type = 0x%02x, want HEARTBEAT", gotHdr.Type)
	}
	if gotHdr.Flags != FlagAck {
		t.Fatalf("flags = 0x%02x, want ACK", gotHdr.Flags)
	}
	if gotHdr.StreamID != 7 {
		t.Fatalf("stream id = %d, want 7", gotHdr.StreamID)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Fatalf("payload mismatch")
	}

	<-done
}

func TestTransport_FullHelloRoundTrip(t *testing.T) {
	// More realistic: encode a HELLO body, frame it, send it,
	// receive back, decode. Walks the full encode→frame→
	// transport→frame→decode path that the production loop uses.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go echoServer(t, listener, done)

	port := listener.Addr().(*net.TCPAddr).Port
	client, _ := Dial("127.0.0.1", port, 1*time.Second)
	defer client.Close()

	hello := Hello{
		ProtocolVersion: ProtocolVersion,
		Role:            "brain",
		AgentVersion:    "yvr-brain/0.0.1",
	}
	body := make([]byte, 64)
	n, err := hello.Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SendFrame(FrameHeader{Type: FrameHello}, body[:n]); err != nil {
		t.Fatal(err)
	}

	_, recvPayload, err := client.RecvFrame(4096)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeHello(recvPayload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Role != hello.Role || got.AgentVersion != hello.AgentVersion {
		t.Fatalf("decoded body mismatch: got %+v want %+v", got, hello)
	}

	<-done
}

func TestTransport_OversizedDrain(t *testing.T) {
	// Server sends a 200-byte frame; client provides a 32-byte
	// buffer. Expect ErrBufferTooSmall + the next frame
	// (HEARTBEAT) still arrives cleanly afterwards.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		nc, _ := listener.Accept()
		srv := Wrap(nc)
		defer srv.Close()

		big := make([]byte, 200)
		for i := range big {
			big[i] = byte(i)
		}
		if err := srv.SendFrame(FrameHeader{Type: FrameStreamChunk, StreamID: 1}, big); err != nil {
			t.Errorf("server send big: %v", err)
		}
		// Follow-up frame: a 4-byte HEARTBEAT.
		small := []byte{0xa, 0xb, 0xc, 0xd}
		if err := srv.SendFrame(FrameHeader{Type: FrameHeartbeat, StreamID: 1}, small); err != nil {
			t.Errorf("server send small: %v", err)
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	client, _ := Dial("127.0.0.1", port, 1*time.Second)
	defer client.Close()

	smallBuf := 32
	hdr, partial, err := client.RecvFrame(smallBuf)
	if err != ErrBufferTooSmall {
		t.Fatalf("got %v, want ErrBufferTooSmall", err)
	}
	if hdr.Length != 200 {
		t.Fatalf("hdr.Length = %d, want 200", hdr.Length)
	}
	if len(partial) != smallBuf {
		t.Fatalf("partial len = %d, want %d", len(partial), smallBuf)
	}

	// Next frame must arrive cleanly — drain worked.
	hdr2, payload2, err := client.RecvFrame(4096)
	if err != nil {
		t.Fatalf("second recv: %v", err)
	}
	if hdr2.Type != FrameHeartbeat {
		t.Fatalf("hdr2.Type = 0x%02x, want HEARTBEAT", hdr2.Type)
	}
	if !bytes.Equal(payload2, []byte{0xa, 0xb, 0xc, 0xd}) {
		t.Fatalf("payload2 mismatch")
	}

	<-done
}
