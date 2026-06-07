package netcapture

import (
	"encoding/binary"
	"fmt"
)

// Siemens S7comm decoder (TPKT/COTP/S7 over TCP 102) — covers S7-1200/300/400
// and LOGO! 0BA7/0BA8. Jobs are paired with ack-data by PDU reference for
// latency, and CPU error class/code is surfaced.

func init() {
	registerProto(&protoDecoder{name: "s7", ports: []int{102}, fn: decodeS7})
}

var s7Func = map[byte]string{
	0xf0: "setup_comm", 0x04: "read_var", 0x05: "write_var",
	0x1a: "request_download", 0x1b: "download_block", 0x1c: "download_ended",
	0x1d: "start_upload", 0x1e: "upload", 0x1f: "end_upload", 0x28: "plc_control", 0x29: "plc_stop",
}

func s7FuncName(fn byte) string {
	if n, ok := s7Func[fn]; ok {
		return n
	}
	return fmt.Sprintf("fn_0x%02x", fn)
}

func decodeS7(p *packet, f *Flow, a *Analyzer) []Event {
	b := p.payload
	// TPKT (4) + COTP DT (3) then S7 (0x32). Tolerate small COTP variation.
	if len(b) < 8 || b[0] != 0x03 {
		return nil
	}
	s := b[7:]
	if len(s) < 12 || s[0] != 0x32 {
		// scan a couple offsets for the S7 protocol id
		found := -1
		for off := 5; off <= 11 && off < len(b); off++ {
			if b[off] == 0x32 {
				found = off
				break
			}
		}
		if found < 0 || len(b)-found < 12 {
			return nil
		}
		s = b[found:]
	}
	rosctr := s[1]
	pduRef := binary.BigEndian.Uint16(s[4:6])
	st := a.s7Stats()
	key := fmt.Sprintf("s7|%s|%d", f.Key, pduRef)

	switch rosctr {
	case 1: // job request
		st.Jobs++
		fn := byte(0)
		if len(s) > 10 {
			fn = s[10]
		}
		st.ByFunc[s7FuncName(fn)]++
		a.markReq(key, p.ts)
		return []Event{{
			TS: p.ts, Proto: "s7", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "info",
			Summary: "S7 job " + s7FuncName(fn),
			Detail:  map[string]interface{}{"func": s7FuncName(fn), "pduRef": pduRef},
		}}
	case 2, 3: // ack / ack_data
		st.AckData++
		errClass, errCode := byte(0), byte(0)
		if len(s) >= 12 {
			errClass, errCode = s[10], s[11]
		}
		lat, _ := a.takeReq(key, p.ts)
		if errClass != 0 || errCode != 0 {
			st.Errors++
			en := fmt.Sprintf("0x%02x%02x", errClass, errCode)
			st.ByError[en]++
			return []Event{{
				TS: p.ts, Proto: "s7", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "error",
				Summary: fmt.Sprintf("S7 error class/code %s (%.0fms)", en, lat),
				Detail:  map[string]interface{}{"errClass": errClass, "errCode": errCode, "pduRef": pduRef, "latencyMs": lat},
			}}
		}
		return []Event{{
			TS: p.ts, Proto: "s7", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "info",
			Summary: fmt.Sprintf("S7 ack-data OK (%.0fms)", lat),
			Detail:  map[string]interface{}{"pduRef": pduRef, "latencyMs": lat},
		}}
	}
	return nil
}
