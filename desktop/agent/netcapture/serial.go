package netcapture

import (
	"fmt"
	"strings"
	"time"
)

// Serial (RS232/RS485) ingestion. Bytes arrive either from a Linux tty open
// (serial_linux.go) or — the primary path on a phone-as-host / the IoT connector
// box — mirrored in from the Android USB-serial layer via Engine.Feed. Either
// way they land in FeedSerial, which keeps a rolling buffer and runs the
// selected decoder. Decoders: modbus_rtu (CRC-self-delimited), marlin (G-code
// ok/err lines), ascii (line-oriented), or auto (RTU framing).

const serialIdleGapMs = 1500

// FeedSerial ingests raw bytes from a serial link at time ts (unix millis).
func (a *Analyzer) FeedSerial(b []byte, ts int64) {
	if len(b) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.packets++ // count chunks as "packets" for the top-line counter
	a.bytes += len(b)
	a.updatedAt = ts
	ss := a.serialStats()
	ss.Bytes += len(b)
	if ss.Decoder == "" {
		ss.Decoder = a.serDecoder
	}

	if a.serLastTS > 0 && ts-a.serLastTS > serialIdleGapMs && len(a.serBuf) == 0 {
		a.disc = append(a.disc, DisconnectEvent{TS: ts, Flow: "serial:" + a.source, Cause: "serial_idle",
			Note: fmt.Sprintf("%dms silence", ts-a.serLastTS)})
	}
	a.serLastTS = ts

	switch a.serDecoder {
	case "marlin":
		a.feedSerialLines(b, ts, "marlin")
	case "ascii":
		a.feedSerialLines(b, ts, "ascii")
	default: // modbus_rtu | auto
		a.feedSerialRTU(b, ts)
	}
}

func (a *Analyzer) feedSerialRTU(b []byte, ts int64) {
	ss := a.serialStats()
	a.serBuf = append(a.serBuf, b...)
	frames, crcErrs, tail := scanRTU(a.serBuf)
	a.serBuf = tail
	ss.CRCErrors += crcErrs
	for _, fr := range frames {
		ss.Frames++
		a.protos["modbus_rtu"]++
		name := modbusFuncName(fr.fn)
		dir := "req"
		if fr.resp {
			dir = "resp"
		}
		sev := "info"
		summary := fmt.Sprintf("RTU unit %d %s %s addr %d×%d", fr.unit, name, dir, fr.addr, fr.count)
		if fr.fn&0x80 != 0 {
			sev = "error"
			en := modbusExceptName(fr.excpt)
			ss.ByException[en]++
			summary = fmt.Sprintf("RTU unit %d EXCEPTION %s", fr.unit, en)
		} else {
			ss.ByFunc[name]++
		}
		a.emit(Event{
			TS: ts, Proto: "modbus_rtu", Src: "bus", Dst: fmt.Sprintf("unit%d", fr.unit), Severity: sev,
			Summary: summary,
			Detail:  map[string]interface{}{"unit": fr.unit, "func": name, "addr": fr.addr, "count": fr.count, "resp": fr.resp, "hex": fr.raw},
		})
	}
}

func (a *Analyzer) feedSerialLines(b []byte, ts int64, decoder string) {
	ss := a.serialStats()
	a.serBuf = append(a.serBuf, b...)
	for {
		idx := indexByte(a.serBuf, '\n')
		if idx < 0 {
			if len(a.serBuf) > 4096 { // avoid unbounded growth on a no-newline stream
				a.serBuf = a.serBuf[len(a.serBuf)-4096:]
			}
			break
		}
		line := strings.TrimRight(string(a.serBuf[:idx]), "\r")
		a.serBuf = a.serBuf[idx+1:]
		if line == "" {
			continue
		}
		ss.Frames++
		a.protos[decoder]++
		sev := "info"
		low := strings.ToLower(line)
		if decoder == "marlin" {
			switch {
			case strings.HasPrefix(line, "ok"):
				sev = "info"
			case strings.HasPrefix(low, "error") || strings.HasPrefix(line, "!!") || strings.Contains(low, "halt"):
				sev = "error"
			case strings.HasPrefix(low, "echo:busy"):
				sev = "warn"
			}
		}
		a.emit(Event{
			TS: ts, Proto: decoder, Src: "device", Severity: sev,
			Summary: line,
		})
	}
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// nowMs is the millis clock used by the live tty reader (tests feed explicit ts).
func nowMs() int64 { return time.Now().UnixMilli() }
