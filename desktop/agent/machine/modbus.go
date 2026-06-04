package machine

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"time"
)

// ── Modbus CRC-16 (poly 0xA001, the standard RTU CRC) ──────────────────────

func crc16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// crcOK checks that the trailing 2 bytes of frame are a valid little-endian CRC
// over the rest. RTU sends CRC low-byte first.
func crcOK(frame []byte) bool {
	if len(frame) < 4 {
		return false
	}
	want := binary.LittleEndian.Uint16(frame[len(frame)-2:])
	return crc16(frame[:len(frame)-2]) == want
}

// ── RTU frame extraction (sniffer core) ────────────────────────────────────
//
// On a passive bus tap we don't have request/response markers or reliable
// inter-frame gaps, so we self-delimit by CRC: at each offset we try every
// plausible total frame length implied by the function code + embedded byte
// counts, and accept the first whose CRC validates. This is the standard
// "CRC-validated" sniffing technique. Returns decoded frames and the trailing
// bytes that couldn't (yet) be decoded — feed those back in next time.

// candidateLengths returns plausible total RTU frame lengths starting at buf[0].
func candidateLengths(buf []byte) []int {
	if len(buf) < 2 {
		return nil
	}
	fn := buf[1]
	var out []int
	// Exception response: addr, func|0x80, code, crc(2) = 5
	if fn&0x80 != 0 {
		return []int{5}
	}
	switch fn {
	case 0x01, 0x02, 0x03, 0x04:
		// request: addr,func,startHi,startLo,cntHi,cntLo,crc(2) = 8
		out = append(out, 8)
		// response: addr,func,byteCount,data...,crc(2) = 3+bc+2
		if len(buf) >= 3 {
			bc := int(buf[2])
			out = append(out, 3+bc+2)
		}
	case 0x05, 0x06:
		// write single coil/register (req and resp are both 8 bytes)
		out = append(out, 8)
	case 0x0F, 0x10:
		// write-multiple request: addr,func,startHi,startLo,cntHi,cntLo,byteCount,data,crc(2)
		if len(buf) >= 7 {
			bc := int(buf[6])
			out = append(out, 7+bc+2)
		}
		// write-multiple response: addr,func,startHi,startLo,cntHi,cntLo,crc(2) = 8
		out = append(out, 8)
	default:
		// unknown function — try a few short lengths
		out = append(out, 5, 8)
	}
	return out
}

// scanFrames extracts CRC-valid frames from buf. Returns frames + leftover tail.
func scanFrames(buf []byte) ([]Frame, []byte) {
	var frames []Frame
	i := 0
	now := time.Now().UnixMilli()
	for i < len(buf) {
		matched := false
		for _, n := range candidateLengths(buf[i:]) {
			if n >= 4 && i+n <= len(buf) && crcOK(buf[i:i+n]) {
				if f, ok := decodeFrame(buf[i:i+n], now); ok {
					frames = append(frames, f)
					i += n
					matched = true
					break
				}
			}
		}
		if !matched {
			// If even the longest candidate would exceed buf, keep the tail
			// (it may be a partial frame still arriving).
			maxNeed := 0
			for _, n := range candidateLengths(buf[i:]) {
				if n > maxNeed {
					maxNeed = n
				}
			}
			if maxNeed > 0 && i+maxNeed > len(buf) {
				break
			}
			i++ // resync: drop one byte and retry
		}
	}
	tail := buf[i:]
	// bound the retained tail so a noisy bus can't grow it unbounded
	if len(tail) > 512 {
		tail = tail[len(tail)-512:]
	}
	return frames, append([]byte(nil), tail...)
}

// decodeFrame best-effort decodes a CRC-valid RTU frame.
func decodeFrame(b []byte, ts int64) (Frame, bool) {
	if len(b) < 4 {
		return Frame{}, false
	}
	f := Frame{TS: ts, Unit: b[0], Func: b[1], Addr: -1, Raw: hex.EncodeToString(b)}
	fn := b[1]
	body := b[:len(b)-2] // strip CRC
	if fn&0x80 != 0 {
		f.Excpt = b[2]
		f.IsResp = true
		return f, true
	}
	switch fn {
	case 0x01, 0x02, 0x03, 0x04:
		if len(body) == 6 { // request
			f.Addr = int(binary.BigEndian.Uint16(body[2:4]))
			f.Count = int(binary.BigEndian.Uint16(body[4:6]))
		} else if len(body) >= 3 && int(body[2]) == len(body)-3 { // response
			f.IsResp = true
			bc := int(body[2])
			f.Count = bc / 2
			for j := 0; j+1 < bc; j += 2 {
				f.Values = append(f.Values, binary.BigEndian.Uint16(body[3+j:3+j+2]))
			}
		}
	case 0x05, 0x06:
		f.IsWrite = true
		f.Addr = int(binary.BigEndian.Uint16(body[2:4]))
		f.Values = []uint16{binary.BigEndian.Uint16(body[4:6])}
		f.Count = 1
	case 0x0F, 0x10:
		f.IsWrite = true
		if len(body) >= 6 {
			f.Addr = int(binary.BigEndian.Uint16(body[2:4]))
			f.Count = int(binary.BigEndian.Uint16(body[4:6]))
		}
		if len(body) >= 7 && int(body[6]) == len(body)-7 && fn == 0x10 {
			for j := 0; j+1 < int(body[6]); j += 2 {
				f.Values = append(f.Values, binary.BigEndian.Uint16(body[7+j:7+j+2]))
			}
		}
	}
	return f, true
}

// ── Modbus-TCP client (active scan/read/write; no external dep) ────────────

// TCPClient is a minimal Modbus-TCP master over a single connection.
type TCPClient struct {
	conn net.Conn
	unit byte
	txid uint16
}

// DialTCP connects to a Modbus-TCP slave (default port 502).
func DialTCP(addr string, unit byte, timeout time.Duration) (*TCPClient, error) {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	return &TCPClient{conn: c, unit: unit}, nil
}

func (c *TCPClient) Close() error { return c.conn.Close() }

func (c *TCPClient) txn(pdu []byte, timeout time.Duration) ([]byte, error) {
	c.txid++
	hdr := make([]byte, 7)
	binary.BigEndian.PutUint16(hdr[0:2], c.txid)
	binary.BigEndian.PutUint16(hdr[2:4], 0) // protocol id
	binary.BigEndian.PutUint16(hdr[4:6], uint16(len(pdu)+1))
	hdr[6] = c.unit
	_ = c.conn.SetDeadline(time.Now().Add(timeout))
	if _, err := c.conn.Write(append(hdr, pdu...)); err != nil {
		return nil, err
	}
	rh := make([]byte, 7)
	if _, err := readFull(c.conn, rh); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(rh[4:6])) - 1
	if n < 1 || n > 260 {
		return nil, fmt.Errorf("modbus: bad length %d", n)
	}
	resp := make([]byte, n)
	if _, err := readFull(c.conn, resp); err != nil {
		return nil, err
	}
	if resp[0]&0x80 != 0 {
		code := byte(0)
		if len(resp) > 1 {
			code = resp[1]
		}
		return nil, fmt.Errorf("modbus exception 0x%02x", code)
	}
	return resp, nil
}

// ReadRegisters reads `count` 16-bit registers from `start` using fc (3=holding,
// 4=input). Returns the decoded values.
func (c *TCPClient) ReadRegisters(fc byte, start, count int, timeout time.Duration) ([]uint16, error) {
	if fc != 3 && fc != 4 {
		return nil, fmt.Errorf("modbus: ReadRegisters fc must be 3 or 4")
	}
	pdu := []byte{fc, byte(start >> 8), byte(start), byte(count >> 8), byte(count)}
	resp, err := c.txn(pdu, timeout)
	if err != nil {
		return nil, err
	}
	if len(resp) < 2 {
		return nil, fmt.Errorf("modbus: short response")
	}
	bc := int(resp[1])
	out := make([]uint16, 0, bc/2)
	for j := 0; j+1 < bc && 2+j+1 < len(resp); j += 2 {
		out = append(out, binary.BigEndian.Uint16(resp[2+j:2+j+2]))
	}
	return out, nil
}

// WriteSingleRegister writes one holding register (fc 6). Caller must clamp.
func (c *TCPClient) WriteSingleRegister(addr int, val uint16, timeout time.Duration) error {
	pdu := []byte{0x06, byte(addr >> 8), byte(addr), byte(val >> 8), byte(val)}
	_, err := c.txn(pdu, timeout)
	return err
}

func readFull(c net.Conn, buf []byte) (int, error) {
	got := 0
	for got < len(buf) {
		n, err := c.Read(buf[got:])
		got += n
		if err != nil {
			return got, err
		}
	}
	return got, nil
}
