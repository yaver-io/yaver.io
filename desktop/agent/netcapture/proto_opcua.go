package netcapture

import (
	"encoding/binary"
	"fmt"
)

// OPC-UA binary transport decoder (TCP 4840). We read the message header
// (HEL/ACK/OPN/MSG/CLO/ERR + chunk type) and surface ERR messages / abort
// chunks with their status code — the common modern PLC/SCADA failure surface.

func init() {
	registerProto(&protoDecoder{name: "opcua", ports: []int{4840}, fn: decodeOPCUA})
}

var opcuaStatus = map[uint32]string{
	0x80AE0000: "BadConnectionClosed",
	0x800A0000: "BadTimeout",
	0x80250000: "BadSessionIdInvalid",
	0x80130000: "BadSecurityChecksFailed",
	0x80AC0000: "BadServerNotConnected",
	0x80020000: "BadInternalError",
	0x80050000: "BadShutdown",
	0x80580000: "BadServiceUnsupported",
}

func opcuaStatusName(code uint32) string {
	if n, ok := opcuaStatus[code]; ok {
		return fmt.Sprintf("%s(0x%08x)", n, code)
	}
	return fmt.Sprintf("0x%08x", code)
}

func decodeOPCUA(p *packet, f *Flow, a *Analyzer) []Event {
	b := p.payload
	if len(b) < 8 {
		return nil
	}
	msg := string(b[0:3])
	chunk := b[3]
	o := a.opcuaStats()
	o.ByMsg[msg]++

	switch msg {
	case "HEL":
		o.Hello++
		return []Event{{TS: p.ts, Proto: "opcua", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "info", Summary: "OPC-UA Hello"}}
	case "ACK":
		return []Event{{TS: p.ts, Proto: "opcua", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "info", Summary: "OPC-UA Ack"}}
	case "ERR":
		o.Errors++
		code := uint32(0)
		if len(b) >= 12 {
			code = binary.LittleEndian.Uint32(b[8:12])
		}
		sn := opcuaStatusName(code)
		o.ByStatus[sn]++
		return []Event{{
			TS: p.ts, Proto: "opcua", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "error",
			Summary: "OPC-UA ERROR " + sn,
			Detail:  map[string]interface{}{"status": sn},
		}}
	case "OPN", "MSG", "CLO":
		o.Messages++
		if chunk == 'A' { // abort chunk
			o.Errors++
			code := uint32(0)
			if len(b) >= 16 {
				code = binary.LittleEndian.Uint32(b[12:16])
			}
			sn := opcuaStatusName(code)
			o.ByStatus[sn]++
			return []Event{{
				TS: p.ts, Proto: "opcua", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "warn",
				Summary: "OPC-UA abort " + sn,
				Detail:  map[string]interface{}{"status": sn},
			}}
		}
		return []Event{{TS: p.ts, Proto: "opcua", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "info", Summary: "OPC-UA " + msg}}
	}
	return nil
}
