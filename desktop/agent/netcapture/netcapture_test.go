package netcapture

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// mkpkt builds a decoded packet for direct analyzer feeding (skips the pcap/IP
// layer, which is covered separately by TestNetcapPcapIP).
func mkpkt(ts int64, l4 byte, srcIP string, srcPort int, dstIP string, dstPort int, flags byte, seq uint32, payload []byte) packet {
	return packet{ts: ts, l4: l4, srcIP: srcIP, srcPort: srcPort, dstIP: dstIP, dstPort: dstPort, flags: flags, seq: seq, payload: payload, size: 40 + len(payload)}
}

func findFinding(an Analysis, code string) *Finding {
	for i := range an.Findings {
		if an.Findings[i].Code == code {
			return &an.Findings[i]
		}
	}
	return nil
}

func TestNetcapModbusException(t *testing.T) {
	a := newAnalyzer("t", "net", "eth0", 0)
	// read_holding request: txid 1, proto 0, len 6, unit 1, fc 3, addr 0, count 5
	req := []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x06, 0x01, 0x03, 0x00, 0x00, 0x00, 0x05}
	a.Feed(mkpkt(1000, 6, "10.0.0.2", 50000, "10.0.0.50", 502, tcpPSH|tcpACK, 1, req))
	// exception response: fc 0x83, code 0x06 (slave busy)
	exc := []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x03, 0x01, 0x83, 0x06}
	a.Feed(mkpkt(1050, 6, "10.0.0.50", 502, "10.0.0.2", 50000, tcpPSH|tcpACK, 1, exc))

	an := a.Snapshot(0)
	if an.Modbus == nil || an.Modbus.Transactions != 1 || an.Modbus.Exceptions != 1 {
		t.Fatalf("modbus stats: %+v", an.Modbus)
	}
	if an.Modbus.ByException["0x06_slave_busy"] != 1 {
		t.Fatalf("exception map: %+v", an.Modbus.ByException)
	}
	if an.Modbus.AvgLatencyMs != 50 {
		t.Fatalf("latency: %v", an.Modbus.AvgLatencyMs)
	}
	if findFinding(an, "modbus_exceptions") == nil {
		t.Fatalf("missing modbus_exceptions finding: %+v", an.Findings)
	}
}

func TestNetcapS7Error(t *testing.T) {
	a := newAnalyzer("t", "net", "eth0", 0)
	job := []byte{0x03, 0x00, 0x00, 0x1c, 0x02, 0xf0, 0x80, // TPKT+COTP
		0x32, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x08, 0x00, 0x00, 0x04, 0x00} // S7 job, func read_var
	a.Feed(mkpkt(2000, 6, "10.0.0.2", 40000, "10.0.0.10", 102, tcpPSH|tcpACK, 1, job))
	ack := []byte{0x03, 0x00, 0x00, 0x1a, 0x02, 0xf0, 0x80,
		0x32, 0x03, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x81, 0x04} // ack_data, errclass 0x81 errcode 0x04
	a.Feed(mkpkt(2030, 6, "10.0.0.10", 102, "10.0.0.2", 40000, tcpPSH|tcpACK, 1, ack))

	an := a.Snapshot(0)
	if an.S7 == nil || an.S7.Jobs != 1 || an.S7.AckData != 1 || an.S7.Errors != 1 {
		t.Fatalf("s7 stats: %+v", an.S7)
	}
	if an.S7.ByFunc["read_var"] != 1 {
		t.Fatalf("s7 func map: %+v", an.S7.ByFunc)
	}
	if findFinding(an, "s7_errors") == nil {
		t.Fatalf("missing s7_errors finding")
	}
}

func TestNetcapTDSLoginFail(t *testing.T) {
	a := newAnalyzer("t", "net", "eth0", 0)
	login := []byte{0x10, 0x01, 0x00, 0x08, 0x00, 0x00, 0x01, 0x00} // LOGIN7 header only
	a.Feed(mkpkt(3000, 6, "10.0.0.2", 55000, "10.0.0.20", 1433, tcpPSH|tcpACK, 1, login))

	// response: 8-byte header + ERROR token (number 18456) + DONE
	var resp bytes.Buffer
	resp.Write([]byte{0x04, 0x01, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00}) // TDS response header
	errTok := []byte{0xAA, 0x0e, 0x00}                                  // ERROR token, len 14
	num := make([]byte, 4)
	binary.LittleEndian.PutUint32(num, 18456)
	errTok = append(errTok, num...)                            // number
	errTok = append(errTok, 0x01, 0x0e, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00) // state,class,msglen,serv,proc,line
	resp.Write(errTok)
	done := append([]byte{0xFD}, make([]byte, 12)...)
	resp.Write(done)
	a.Feed(mkpkt(3040, 6, "10.0.0.20", 1433, "10.0.0.2", 55000, tcpPSH|tcpACK, 1, resp.Bytes()))

	an := a.Snapshot(0)
	if an.TDS == nil || an.TDS.Logins != 1 || an.TDS.LoginFailures != 1 {
		t.Fatalf("tds stats: %+v", an.TDS)
	}
	if findFinding(an, "tds_login_failed") == nil {
		t.Fatalf("missing tds_login_failed finding: %+v", an.Findings)
	}
}

func TestNetcapOPCUAError(t *testing.T) {
	a := newAnalyzer("t", "net", "eth0", 0)
	err := []byte{'E', 'R', 'R', 'F', 0x10, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0A, 0x80} // status 0x800A0000 BadTimeout
	a.Feed(mkpkt(4000, 6, "10.0.0.10", 4840, "10.0.0.2", 60000, tcpPSH|tcpACK, 1, err))
	an := a.Snapshot(0)
	if an.OPCUA == nil || an.OPCUA.Errors != 1 {
		t.Fatalf("opcua stats: %+v", an.OPCUA)
	}
	if an.OPCUA.ByStatus["BadTimeout(0x800a0000)"] != 1 {
		t.Fatalf("opcua status map: %+v", an.OPCUA.ByStatus)
	}
	if findFinding(an, "opcua_errors") == nil {
		t.Fatalf("missing opcua_errors finding")
	}
}

func TestNetcapHTTPError(t *testing.T) {
	a := newAnalyzer("t", "net", "eth0", 0)
	a.Feed(mkpkt(5000, 6, "10.0.0.2", 51000, "10.0.0.30", 80, tcpPSH|tcpACK, 1, []byte("GET /x HTTP/1.1\r\nHost: plc.local\r\n\r\n")))
	a.Feed(mkpkt(5020, 6, "10.0.0.30", 80, "10.0.0.2", 51000, tcpPSH|tcpACK, 1, []byte("HTTP/1.1 500 Internal Server Error\r\n\r\n")))
	an := a.Snapshot(0)
	if an.HTTP == nil || an.HTTP.Requests != 1 || an.HTTP.Errors != 1 || an.HTTP.ByStatus["500"] != 1 {
		t.Fatalf("http stats: %+v", an.HTTP)
	}
	if findFinding(an, "http_errors") == nil {
		t.Fatalf("missing http_errors finding")
	}
}

func TestNetcapDNSNxdomain(t *testing.T) {
	a := newAnalyzer("t", "net", "eth0", 0)
	q := []byte{0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x03, 'p', 'l', 'c', 0x00, 0x00, 0x01, 0x00, 0x01}
	a.Feed(mkpkt(6000, 17, "10.0.0.2", 40000, "10.0.0.1", 53, 0, 0, q))
	r := []byte{0x00, 0x01, 0x81, 0x83, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x03, 'p', 'l', 'c', 0x00, 0x00, 0x01, 0x00, 0x01}
	a.Feed(mkpkt(6010, 17, "10.0.0.1", 53, "10.0.0.2", 40000, 0, 0, r))
	an := a.Snapshot(0)
	if an.DNS == nil || an.DNS.Queries != 1 || an.DNS.NXDomain != 1 {
		t.Fatalf("dns stats: %+v", an.DNS)
	}
	if findFinding(an, "dns_failures") == nil {
		t.Fatalf("missing dns_failures finding")
	}
}

func TestNetcapTCPHealth(t *testing.T) {
	a := newAnalyzer("t", "net", "eth0", 0)
	// SYN then RST (no SYN-ACK) = connection refused
	a.Feed(mkpkt(7000, 6, "10.0.0.2", 52000, "10.0.0.99", 9999, tcpSYN, 100, nil))
	a.Feed(mkpkt(7005, 6, "10.0.0.99", 9999, "10.0.0.2", 52000, tcpRST|tcpACK, 0, nil))
	an := a.Snapshot(0)
	if findFinding(an, "conn_refused") == nil {
		t.Fatalf("missing conn_refused finding: %+v", an.Findings)
	}
	refused := false
	for _, d := range an.Disconnects {
		if d.Cause == "conn_refused" {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("no conn_refused disconnect: %+v", an.Disconnects)
	}

	// RTT: SYN -> SYN-ACK on a separate flow
	b := newAnalyzer("t", "net", "eth0", 0)
	b.Feed(mkpkt(8000, 6, "10.0.0.2", 53000, "10.0.0.50", 502, tcpSYN, 1, nil))
	b.Feed(mkpkt(8012, 6, "10.0.0.50", 502, "10.0.0.2", 53000, tcpSYN|tcpACK, 1, nil))
	bn := b.Snapshot(0)
	if len(bn.Flows) == 0 || bn.Flows[0].RTTms != 12 {
		t.Fatalf("rtt: %+v", bn.Flows)
	}
}

func TestNetcapRetransmitStorm(t *testing.T) {
	a := newAnalyzer("t", "net", "eth0", 0)
	// strictly decreasing seq → every segment after the first looks like a
	// retransmit (8 packets → 7 retransmits, over the storm threshold)
	for i := 0; i < 8; i++ {
		seq := uint32(1000 - i*10)
		a.Feed(mkpkt(int64(9000+i), 6, "10.0.0.2", 54000, "10.0.0.50", 502, tcpPSH|tcpACK, seq, []byte{0xAA}))
	}
	an := a.Snapshot(0)
	if findFinding(an, "retransmits") == nil {
		t.Fatalf("missing retransmits finding: %+v", an.Findings)
	}
}

func TestNetcapSerialRTU(t *testing.T) {
	eng, _ := New()
	id, err := eng.StartSerial(SerialOptions{Decoder: "modbus_rtu"})
	if err != nil {
		t.Fatal(err)
	}
	// valid read_holding request
	req := []byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x05}
	req = appendCRC(req)
	// valid exception response
	exc := []byte{0x01, 0x83, 0x06}
	exc = appendCRC(exc)
	_ = eng.FeedAt(id, append(append([]byte{}, req...), exc...), 100)

	an, _ := eng.Analyze(id, 0)
	if an.Serial == nil || an.Serial.Frames != 2 {
		t.Fatalf("serial frames: %+v", an.Serial)
	}
	if an.Serial.ByFunc["read_holding"] != 1 || an.Serial.ByException["0x06_slave_busy"] != 1 {
		t.Fatalf("serial maps: %+v", an.Serial)
	}

	// corrupt frame → CRC error counted
	id2, _ := eng.StartSerial(SerialOptions{Decoder: "modbus_rtu"})
	bad := []byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x05, 0xFF, 0xFF} // wrong CRC
	good := appendCRC([]byte{0x01, 0x06, 0x00, 0x01, 0x00, 0x64})  // valid write to force resync
	_ = eng.FeedAt(id2, append(bad, good...), 200)
	an2, _ := eng.Analyze(id2, 0)
	if an2.Serial == nil || an2.Serial.CRCErrors == 0 {
		t.Fatalf("expected CRC errors: %+v", an2.Serial)
	}
}

func TestNetcapSerialMarlin(t *testing.T) {
	eng, _ := New()
	id, _ := eng.StartSerial(SerialOptions{Decoder: "marlin"})
	_ = eng.FeedAt(id, []byte("ok\nError:Printer halted\n"), 100)
	evs, _ := eng.Tail(id, 10)
	if len(evs) != 2 {
		t.Fatalf("marlin events: %d %+v", len(evs), evs)
	}
	sawErr := false
	for _, e := range evs {
		if e.Severity == "error" {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatalf("expected an error-severity marlin line: %+v", evs)
	}
}

func TestNetcapPcapIP(t *testing.T) {
	// Build a classic pcap (LE, Ethernet) with one TCP SYN to port 502.
	eth := []byte{0xaa, 0xaa, 0xaa, 0xaa, 0xaa, 0xaa, 0xbb, 0xbb, 0xbb, 0xbb, 0xbb, 0xbb, 0x08, 0x00}
	ip := []byte{0x45, 0x00, 0x00, 0x28, 0x00, 0x00, 0x00, 0x00, 0x40, 0x06, 0x00, 0x00,
		0x0a, 0x00, 0x00, 0x02, 0x0a, 0x00, 0x00, 0x32}
	tcp := []byte{0xc3, 0x50, 0x01, 0xf6, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00,
		0x50, 0x02, 0xff, 0xff, 0x00, 0x00, 0x00, 0x00}
	pkt := append(append(append([]byte{}, eth...), ip...), tcp...)

	var buf bytes.Buffer
	gh := make([]byte, 24)
	binary.LittleEndian.PutUint32(gh[0:4], pcapMagicMicroBE)
	binary.LittleEndian.PutUint16(gh[4:6], 2)
	binary.LittleEndian.PutUint16(gh[6:8], 4)
	binary.LittleEndian.PutUint32(gh[16:20], 262144)
	binary.LittleEndian.PutUint32(gh[20:24], linkEthernet)
	buf.Write(gh)
	rh := make([]byte, 16)
	binary.LittleEndian.PutUint32(rh[0:4], 1000)
	binary.LittleEndian.PutUint32(rh[8:12], uint32(len(pkt)))
	binary.LittleEndian.PutUint32(rh[12:16], uint32(len(pkt)))
	buf.Write(rh)
	buf.Write(pkt)

	pr, err := newPcapReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if pr.linkType != linkEthernet {
		t.Fatalf("linkType=%d", pr.linkType)
	}
	ts, data, err := pr.next()
	if err != nil {
		t.Fatal(err)
	}
	if ts != 1000000 { // 1000s in millis
		t.Fatalf("ts=%d", ts)
	}
	p, ok := decodePacket(pr.linkType, data, ts)
	if !ok || p.srcIP != "10.0.0.2" || p.dstIP != "10.0.0.50" || p.dstPort != 502 {
		t.Fatalf("decoded: %+v ok=%v", p, ok)
	}
	if p.flags&tcpSYN == 0 {
		t.Fatalf("SYN not detected: flags=%02x", p.flags)
	}
}

func TestNetcapDecodePcapFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.pcap")
	// reuse the LE/Ethernet writer from above via a minimal inline build
	eth := []byte{0xaa, 0xaa, 0xaa, 0xaa, 0xaa, 0xaa, 0xbb, 0xbb, 0xbb, 0xbb, 0xbb, 0xbb, 0x08, 0x00}
	ip := []byte{0x45, 0x00, 0x00, 0x28, 0x00, 0x00, 0x00, 0x00, 0x40, 0x06, 0x00, 0x00, 0x0a, 0x00, 0x00, 0x02, 0x0a, 0x00, 0x00, 0x32}
	tcp := []byte{0xc3, 0x50, 0x01, 0xf6, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x50, 0x02, 0xff, 0xff, 0x00, 0x00, 0x00, 0x00}
	pkt := append(append(append([]byte{}, eth...), ip...), tcp...)
	gh := make([]byte, 24)
	binary.LittleEndian.PutUint32(gh[0:4], pcapMagicMicroBE)
	binary.LittleEndian.PutUint32(gh[20:24], linkEthernet)
	rh := make([]byte, 16)
	binary.LittleEndian.PutUint32(rh[0:4], 1000)
	binary.LittleEndian.PutUint32(rh[8:12], uint32(len(pkt)))
	binary.LittleEndian.PutUint32(rh[12:16], uint32(len(pkt)))
	out := append(append(append([]byte{}, gh...), rh...), pkt...)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	an, err := DecodePcapFile(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if an.Packets != 1 || len(an.Flows) != 1 {
		t.Fatalf("decoded file: packets=%d flows=%d", an.Packets, len(an.Flows))
	}
}

func appendCRC(b []byte) []byte {
	c := crc16(b)
	return append(b, byte(c&0xff), byte(c>>8))
}
