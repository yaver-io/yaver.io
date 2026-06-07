package netcapture

import (
	"encoding/binary"
	"encoding/hex"
)

// Modbus-RTU framing for the serial path. Ported (self-contained) from the
// machine package's CRC-validated sniffing technique so netcapture has no
// cross-package coupling to a fast-moving package: on a passive bus tap there
// are no request/response markers, so frames are self-delimited by CRC-16.

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

func crcOK(frame []byte) bool {
	if len(frame) < 4 {
		return false
	}
	want := binary.LittleEndian.Uint16(frame[len(frame)-2:])
	return crc16(frame[:len(frame)-2]) == want
}

func candidateLengths(buf []byte) []int {
	if len(buf) < 2 {
		return nil
	}
	fn := buf[1]
	var out []int
	if fn&0x80 != 0 {
		return []int{5}
	}
	switch fn {
	case 0x01, 0x02, 0x03, 0x04:
		out = append(out, 8)
		if len(buf) >= 3 {
			out = append(out, 3+int(buf[2])+2)
		}
	case 0x05, 0x06:
		out = append(out, 8)
	case 0x0F, 0x10:
		if len(buf) >= 7 {
			out = append(out, 7+int(buf[6])+2)
		}
		out = append(out, 8)
	default:
		out = append(out, 5, 8)
	}
	return out
}

type rtuFrame struct {
	unit   byte
	fn     byte
	addr   int
	count  int
	values []uint16
	write  bool
	resp   bool
	excpt  byte
	raw    string
}

// scanRTU extracts CRC-valid frames; returns frames, the count of definite CRC
// failures (a fully-present candidate that did not validate), and the leftover
// tail to feed back next time.
func scanRTU(buf []byte) (frames []rtuFrame, crcErrs int, tail []byte) {
	i := 0
	for i < len(buf) {
		matched := false
		cands := candidateLengths(buf[i:])
		for _, n := range cands {
			if n >= 4 && i+n <= len(buf) && crcOK(buf[i:i+n]) {
				frames = append(frames, decodeRTU(buf[i:i+n]))
				i += n
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		// Is the smallest candidate fully present yet failed? Then this offset
		// is a definite bad/corrupt frame, not a partial still arriving.
		minNeed, maxNeed := 1<<30, 0
		for _, n := range cands {
			if n < minNeed {
				minNeed = n
			}
			if n > maxNeed {
				maxNeed = n
			}
		}
		if maxNeed > 0 && i+maxNeed > len(buf) && i+minNeed > len(buf) {
			break // genuinely partial — keep as tail
		}
		if minNeed != 1<<30 && i+minNeed <= len(buf) {
			crcErrs++
		}
		i++ // resync
	}
	tail = append([]byte(nil), buf[i:]...)
	if len(tail) > 512 {
		tail = tail[len(tail)-512:]
	}
	return
}

func decodeRTU(b []byte) rtuFrame {
	f := rtuFrame{unit: b[0], fn: b[1], addr: -1, raw: hex.EncodeToString(b)}
	fn := b[1]
	body := b[:len(b)-2]
	if fn&0x80 != 0 {
		if len(b) > 2 {
			f.excpt = b[2]
		}
		f.resp = true
		return f
	}
	switch fn {
	case 0x01, 0x02, 0x03, 0x04:
		if len(body) == 6 {
			f.addr = int(binary.BigEndian.Uint16(body[2:4]))
			f.count = int(binary.BigEndian.Uint16(body[4:6]))
		} else if len(body) >= 3 && int(body[2]) == len(body)-3 {
			f.resp = true
			bc := int(body[2])
			f.count = bc / 2
			for j := 0; j+1 < bc; j += 2 {
				f.values = append(f.values, binary.BigEndian.Uint16(body[3+j:3+j+2]))
			}
		}
	case 0x05, 0x06:
		f.write = true
		if len(body) >= 6 {
			f.addr = int(binary.BigEndian.Uint16(body[2:4]))
			f.values = []uint16{binary.BigEndian.Uint16(body[4:6])}
			f.count = 1
		}
	case 0x0F, 0x10:
		f.write = true
		if len(body) >= 6 {
			f.addr = int(binary.BigEndian.Uint16(body[2:4]))
			f.count = int(binary.BigEndian.Uint16(body[4:6]))
		}
	}
	return f
}
