package netcapture

import (
	"encoding/binary"
	"fmt"
)

// Modbus-TCP (MBAP) decoder. Requests and responses are paired by
// (flow, transaction-id) to derive per-transaction latency and to attribute
// exception responses. PDU semantics intentionally mirror machine/modbus.go but
// are kept self-contained so this package has no cross-package coupling.

func init() {
	registerProto(&protoDecoder{name: "modbus", ports: []int{502}, fn: decodeModbusTCP})
}

var modbusFunc = map[byte]string{
	0x01: "read_coils", 0x02: "read_discrete", 0x03: "read_holding", 0x04: "read_input",
	0x05: "write_coil", 0x06: "write_register", 0x0F: "write_coils", 0x10: "write_registers",
	0x16: "mask_write", 0x17: "rw_multiple",
}

var modbusExcept = map[byte]string{
	0x01: "illegal_function", 0x02: "illegal_address", 0x03: "illegal_value",
	0x04: "device_failure", 0x05: "ack", 0x06: "slave_busy", 0x08: "parity_error",
	0x0A: "gateway_path", 0x0B: "gateway_target",
}

func modbusFuncName(fn byte) string {
	if n, ok := modbusFunc[fn&0x7f]; ok {
		return n
	}
	return fmt.Sprintf("fc_0x%02x", fn&0x7f)
}

func modbusExceptName(code byte) string {
	if n, ok := modbusExcept[code]; ok {
		return fmt.Sprintf("0x%02x_%s", code, n)
	}
	return fmt.Sprintf("0x%02x", code)
}

func decodeModbusTCP(p *packet, f *Flow, a *Analyzer) []Event {
	b := p.payload
	if len(b) < 8 {
		return nil
	}
	txid := binary.BigEndian.Uint16(b[0:2])
	if binary.BigEndian.Uint16(b[2:4]) != 0 { // protocol id must be 0 for Modbus
		return nil
	}
	unit := b[6]
	fn := b[7]
	isExc := fn&0x80 != 0
	m := a.modbusStats()
	key := fmt.Sprintf("modbus|%s|%d|%d", f.Key, unit, txid)

	if lat, ok := a.takeReq(key, p.ts); ok {
		// response leg
		m.Transactions++
		a.mbLatSum += lat
		if lat > a.mbLatMax {
			a.mbLatMax = lat
		}
		if isExc {
			m.Exceptions++
			code := byte(0)
			if len(b) >= 9 {
				code = b[8]
			}
			en := modbusExceptName(code)
			m.ByException[en]++
			return []Event{{
				TS: p.ts, Proto: "modbus", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "error",
				Summary: fmt.Sprintf("Modbus EXCEPTION %s on %s (unit %d, %.0fms)", en, modbusFuncName(fn), unit, lat),
				Detail:  map[string]interface{}{"txid": txid, "unit": unit, "func": modbusFuncName(fn), "exception": en, "latencyMs": lat},
			}}
		}
		return []Event{{
			TS: p.ts, Proto: "modbus", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "info",
			Summary: fmt.Sprintf("Modbus %s OK unit %d (%.0fms)", modbusFuncName(fn), unit, lat),
			Detail:  map[string]interface{}{"txid": txid, "unit": unit, "func": modbusFuncName(fn), "latencyMs": lat},
		}}
	}

	// request leg
	a.markReq(key, p.ts)
	m.ByFunc[modbusFuncName(fn)]++
	m.Units[fmt.Sprintf("%d", unit)]++
	var addr, count int = -1, 0
	if len(b) >= 12 {
		addr = int(binary.BigEndian.Uint16(b[8:10]))
		count = int(binary.BigEndian.Uint16(b[10:12]))
	}
	return []Event{{
		TS: p.ts, Proto: "modbus", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "info",
		Summary: fmt.Sprintf("Modbus %s req unit %d addr %d×%d", modbusFuncName(fn), unit, addr, count),
		Detail:  map[string]interface{}{"txid": txid, "unit": unit, "func": modbusFuncName(fn), "addr": addr, "count": count},
	}}
}
