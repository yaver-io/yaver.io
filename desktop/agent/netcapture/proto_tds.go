package netcapture

import (
	"encoding/binary"
	"fmt"
)

// MS-SQL / TDS decoder (TCP 1433) for ERP↔database links. PRIVACY: this decoder
// reads packet-type and response *token* metadata only — never SQL text,
// parameter values, or credentials. SQL/login bodies are skipped entirely unless
// the session was started with capturePayload (a high-risk opt-in), which still
// never surfaces raw bytes here. Login failures are the headline finding.

func init() {
	registerProto(&protoDecoder{name: "tds", ports: []int{1433}, fn: decodeTDS})
}

const (
	tdsSQLBatch  = 0x01
	tdsRPC       = 0x03
	tdsResponse  = 0x04
	tdsLogin7    = 0x10
	tdsPrelogin  = 0x12
)

func decodeTDS(p *packet, f *Flow, a *Analyzer) []Event {
	b := p.payload
	if len(b) < 8 {
		return nil
	}
	typ := b[0]
	t := a.tdsStats()
	loginKey := "tds_login|" + f.Key

	switch typ {
	case tdsLogin7:
		t.Logins++
		a.markReq(loginKey, p.ts)
		return []Event{{TS: p.ts, Proto: "tds", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "info", Summary: "SQL login attempt"}}
	case tdsPrelogin:
		return []Event{{TS: p.ts, Proto: "tds", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "info", Summary: "SQL prelogin"}}
	case tdsSQLBatch:
		t.Batches++
		return []Event{{TS: p.ts, Proto: "tds", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "info", Summary: "SQL batch (text redacted)"}}
	case tdsRPC:
		t.RPCs++
		return []Event{{TS: p.ts, Proto: "tds", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "info", Summary: "SQL RPC (args redacted)"}}
	case tdsResponse:
		return decodeTDSResponse(b[8:], p, f, a, loginKey)
	}
	return nil
}

// decodeTDSResponse walks the response token stream looking only for ERROR
// (0xAA), INFO (0xAB), LOGINACK (0xAD) and DONE tokens.
func decodeTDSResponse(s []byte, p *packet, f *Flow, a *Analyzer, loginKey string) []Event {
	t := a.tdsStats()
	var evs []Event
	sawLoginAck := false
	sawError := false
	var lastErrNo uint32
	i := 0
	for i < len(s) {
		tok := s[i]
		switch tok {
		case 0xAA, 0xAB: // ERROR / INFO — uint16 length prefix
			if i+3 > len(s) {
				return evs
			}
			ln := int(binary.LittleEndian.Uint16(s[i+1 : i+3]))
			body := i + 3
			if tok == 0xAA && body+4 <= len(s) {
				lastErrNo = binary.LittleEndian.Uint32(s[body : body+4])
				t.Errors++
				t.ByErrorNo[fmt.Sprintf("%d", lastErrNo)]++
				sawError = true
			}
			i = body + ln
		case 0xAD: // LOGINACK — uint16 length prefix
			if i+3 > len(s) {
				return evs
			}
			ln := int(binary.LittleEndian.Uint16(s[i+1 : i+3]))
			sawLoginAck = true
			i = i + 3 + ln
		case 0xE3, 0xA4, 0x81, 0xEE: // ENVCHANGE / others — uint16 length prefix
			if i+3 > len(s) {
				return evs
			}
			ln := int(binary.LittleEndian.Uint16(s[i+1 : i+3]))
			i = i + 3 + ln
		case 0x79: // RETURNSTATUS — fixed 4 bytes payload
			i += 5
		case 0xFD, 0xFE, 0xFF: // DONE family — 12-byte fixed payload
			i += 13
		default:
			// unknown token shape — stop walking, we have what we need
			i = len(s)
		}
	}

	if _, pending := a.takeReq(loginKey, p.ts); pending {
		if sawError && !sawLoginAck {
			t.LoginFailures++
			evs = append(evs, Event{
				TS: p.ts, Proto: "tds", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "error",
				Summary: fmt.Sprintf("SQL login FAILED (error %d)", lastErrNo),
				Detail:  map[string]interface{}{"errorNumber": lastErrNo},
			})
			return evs
		}
		if sawLoginAck {
			evs = append(evs, Event{TS: p.ts, Proto: "tds", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "info", Summary: "SQL login OK"})
		}
	}
	if sawError {
		evs = append(evs, Event{
			TS: p.ts, Proto: "tds", Src: p.srcIPPort(), Dst: p.dstIPPort(), Severity: "warn",
			Summary: fmt.Sprintf("SQL error token (number %d)", lastErrNo),
			Detail:  map[string]interface{}{"errorNumber": lastErrNo},
		})
	}
	return evs
}
