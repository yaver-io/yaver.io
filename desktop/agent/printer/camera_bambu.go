package printer

// camera_bambu.go — the P1/P1S/A1 chamber camera. Unlike the X1 (RTSP), the P-
// and A-series expose a bespoke JPEG-push stream on TCP 6000 over TLS: connect,
// send an 80-byte auth packet (user "bblp" + the LAN access code), then read
// length-prefixed JPEG frames forever. We wrap it as a robot.Camera so the
// printer reuses the exact shared-eye path — printer_snapshot, the MJPEG stream,
// and the mobile/web viewers — that the robot and arm cells already use.

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/yaver-io/agent/robot"
)

// BambuCamera is a robot.Camera backed by the chamber stream.
type BambuCamera struct {
	addr string
	port int
	code string

	mu     sync.Mutex
	conn   net.Conn
	reader *bufio.Reader
}

// compile-time check: BambuCamera satisfies the shared camera contract.
var _ robot.Camera = (*BambuCamera)(nil)

func NewBambuCamera(addr string, port int, accessCode string) *BambuCamera {
	if port <= 0 {
		port = 6000
	}
	return &BambuCamera{addr: addr, port: port, code: accessCode}
}

func (c *BambuCamera) Available() bool { return c.addr != "" && c.code != "" }

// authPacket is the 80-byte handshake the chamber stream expects: a 16-byte
// header (0x40, 0x3000, 0, 0 as little-endian u32s) followed by 32-byte
// null-padded username and 32-byte null-padded access code.
func authPacket(user, code string) []byte {
	buf := make([]byte, 16, 80)
	binary.LittleEndian.PutUint32(buf[0:], 0x40)
	binary.LittleEndian.PutUint32(buf[4:], 0x3000)
	// buf[8:16] stays zero
	u := make([]byte, 32)
	copy(u, user)
	p := make([]byte, 32)
	copy(p, code)
	buf = append(buf, u...)
	buf = append(buf, p...)
	return buf
}

func (c *BambuCamera) dial(ctx context.Context) (net.Conn, *bufio.Reader, error) {
	d := &net.Dialer{Timeout: 6 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", fmt.Sprintf("%s:%d", c.addr, c.port), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return nil, nil, fmt.Errorf("bambu camera dial: %w", err)
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(authPacket("bblp", c.code)); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("bambu camera auth: %w", err)
	}
	return conn, bufio.NewReaderSize(conn, 1<<16), nil
}

// readFrame reads one length-prefixed JPEG from the chamber stream.
func readFrame(r *bufio.Reader) ([]byte, error) {
	header := make([]byte, 16)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(header[0:4])
	if size == 0 || size > 8<<20 {
		return nil, fmt.Errorf("bambu camera: implausible frame size %d", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	if !looksJPEGFrame(payload) {
		return nil, fmt.Errorf("bambu camera: frame is not a JPEG")
	}
	return payload, nil
}

func looksJPEGFrame(b []byte) bool {
	return len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF
}

// Snapshot opens a fresh connection and returns the first frame, then closes.
func (c *BambuCamera) Snapshot(ctx context.Context) ([]byte, error) {
	conn, r, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	return readFrame(r)
}

// Grab satisfies robot.Camera (one-shot frame).
func (c *BambuCamera) Grab(ctx context.Context) ([]byte, error) { return c.Snapshot(ctx) }

// StreamMJPEG opens the chamber stream and re-emits frames as
// multipart/x-mixed-replace, matching every other Yaver camera. The caller sets
// Content-Type to "multipart/x-mixed-replace; boundary=" + robot.MJPEGBoundary.
func (c *BambuCamera) StreamMJPEG(ctx context.Context, w io.Writer, flush func(), fps int) error {
	conn, r, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		frame, err := readFrame(r)
		if err != nil {
			return err
		}
		hdr := fmt.Sprintf("\r\n--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", robot.MJPEGBoundary, len(frame))
		if _, err := io.WriteString(w, hdr); err != nil {
			return err
		}
		if _, err := w.Write(frame); err != nil {
			return err
		}
		if flush != nil {
			flush()
		}
	}
}

func (c *BambuCamera) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// httpSnapshot pulls a single JPEG from an override URL (IP camera / phone-as-
// webcam) — used when CameraOverride is set instead of the chamber camera.
func httpSnapshot(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("camera override %s: status %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if !looksJPEGFrame(b) {
		return nil, fmt.Errorf("camera override %s: not a JPEG", url)
	}
	return b, nil
}
