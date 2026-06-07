package netcapture

import (
	"sort"
	"sync"
)

// protoDecoder is a pluggable application-protocol decoder. Each proto_*.go
// registers one in its init(); the Analyzer dispatches by well-known port. This
// is the generic extension seam — new industrial protocols drop in here without
// touching the engine, UI, or wiring.
type protoDecoder struct {
	name  string
	ports []int
	fn    func(p *packet, f *Flow, a *Analyzer) []Event
}

var portDecoders = map[int]*protoDecoder{}

func registerProto(d *protoDecoder) {
	for _, p := range d.ports {
		portDecoders[p] = d
	}
}

// Analyzer is the stateful aggregator. One per session. Safe for concurrent
// Feed + Snapshot (a single mutex; capture is not hot enough to need sharding).
type Analyzer struct {
	mu sync.Mutex

	session   string
	kind      string
	source    string
	startedAt int64
	updatedAt int64
	status    string
	errMsg    string

	packets int
	bytes   int
	protos  map[string]int
	flows   map[string]*Flow
	disc    []DisconnectEvent

	modbus *ModbusStats
	http   *HTTPStats
	dns    *DNSStats
	s7     *S7Stats
	tds    *TDSStats
	opcua  *OPCUAStats
	serial *SerialStats

	// latency pairing: "proto|flow|id" -> request unix-millis
	pending map[string]int64
	// modbus latency accumulators
	mbLatSum, mbLatMax float64

	ring    []Event
	ringMax int

	// serial-path state
	serBuf     []byte
	serDecoder string
	serLastTS  int64

	capturePayload bool
	onEvent        func(Event)
}

func newAnalyzer(session, kind, source string, startedAt int64) *Analyzer {
	return &Analyzer{
		session:   session,
		kind:      kind,
		source:    source,
		startedAt: startedAt,
		updatedAt: startedAt,
		status:    "running",
		protos:    map[string]int{},
		flows:     map[string]*Flow{},
		pending:   map[string]int64{},
		ringMax:   2000,
	}
}

// Feed ingests one decoded packet (net path). Holds the lock for the whole call;
// app decoders run inside it and must not re-lock.
func (a *Analyzer) Feed(p packet) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.packets++
	a.bytes += p.size
	a.updatedAt = p.ts

	switch p.l4 {
	case 6:
		a.protos["tcp"]++
	case 17:
		a.protos["udp"]++
	case 1, 58:
		a.protos["icmp"]++
	default:
		a.protos["other"]++
	}

	if p.l4 != 6 && p.l4 != 17 {
		return // ICMP/other: counted, no flow/app decode
	}

	key, src, dst, proto := p.flowKey()
	f := a.flows[key]
	if f == nil {
		f = &Flow{Key: key, Proto: proto, Src: src, Dst: dst, FirstTS: p.ts, State: "active"}
		a.flows[key] = f
	}
	f.Packets++
	f.Bytes += p.size
	f.LastTS = p.ts

	if p.l4 == 6 {
		a.tcpState(p, f)
	}

	if len(p.payload) > 0 {
		if d := decoderFor(&p); d != nil {
			f.AppProto = d.name
			a.protos[d.name]++
			for _, ev := range d.fn(&p, f, a) {
				a.emit(ev)
			}
		}
	}
}

func decoderFor(p *packet) *protoDecoder {
	if d := portDecoders[p.dstPort]; d != nil {
		return d
	}
	if d := portDecoders[p.srcPort]; d != nil {
		return d
	}
	return nil
}

// tcpState tracks the connection lifecycle + retransmits + RTT, and records
// disconnect-timeline entries.
func (a *Analyzer) tcpState(p packet, f *Flow) {
	switch {
	case p.flags&tcpSYN != 0 && p.flags&tcpACK == 0:
		f.sawSYN = true
		f.synTS = p.ts
		if f.State == "active" {
			f.State = "syn_sent"
		}
	case p.flags&tcpSYN != 0 && p.flags&tcpACK != 0:
		f.sawSYNAK = true
		if f.synTS > 0 && f.RTTms == 0 {
			f.RTTms = float64(p.ts - f.synTS)
		}
		f.State = "established"
	}
	if p.flags&tcpRST != 0 {
		f.Resets++
		cause := "tcp_reset"
		if f.sawSYN && !f.sawSYNAK {
			cause = "conn_refused"
			f.State = "refused"
		} else {
			f.State = "reset"
		}
		a.disc = append(a.disc, DisconnectEvent{TS: p.ts, Flow: f.Key, Cause: cause})
	}
	if p.flags&tcpFIN != 0 {
		if f.State != "reset" && f.State != "refused" {
			f.State = "closed"
		}
		a.disc = append(a.disc, DisconnectEvent{TS: p.ts, Flow: f.Key, Cause: "fin"})
	}
	// crude retransmit detection on payload-bearing segments
	if len(p.payload) > 0 {
		if f.hadSeq && p.seq < f.lastSeq {
			f.Retransmits++
		}
		f.lastSeq = p.seq
		f.hadSeq = true
	}
}

// emit appends to the ring buffer and fans out to the live subscriber.
func (a *Analyzer) emit(ev Event) {
	a.ring = append(a.ring, ev)
	if len(a.ring) > a.ringMax {
		a.ring = a.ring[len(a.ring)-a.ringMax:]
	}
	if a.onEvent != nil {
		a.onEvent(ev)
	}
}

// emitLocked is for the serial path which holds the lock around byte ingestion.
func (a *Analyzer) emitLocked(ev Event) { a.emit(ev) }

// markReq records a request timestamp for latency pairing.
func (a *Analyzer) markReq(key string, ts int64) { a.pending[key] = ts }

// takeReq pops a request timestamp; returns latency-ms if paired.
func (a *Analyzer) takeReq(key string, ts int64) (float64, bool) {
	if r, ok := a.pending[key]; ok {
		delete(a.pending, key)
		return float64(ts - r), true
	}
	return 0, false
}

// ── lazy rollup accessors (called inside the held lock) ────────────────────

func (a *Analyzer) modbusStats() *ModbusStats {
	if a.modbus == nil {
		a.modbus = &ModbusStats{ByFunc: map[string]int{}, ByException: map[string]int{}, Units: map[string]int{}}
	}
	return a.modbus
}
func (a *Analyzer) httpStats() *HTTPStats {
	if a.http == nil {
		a.http = &HTTPStats{ByStatus: map[string]int{}, ByMethod: map[string]int{}}
	}
	return a.http
}
func (a *Analyzer) dnsStats() *DNSStats {
	if a.dns == nil {
		a.dns = &DNSStats{ByName: map[string]int{}}
	}
	return a.dns
}
func (a *Analyzer) s7Stats() *S7Stats {
	if a.s7 == nil {
		a.s7 = &S7Stats{ByFunc: map[string]int{}, ByError: map[string]int{}}
	}
	return a.s7
}
func (a *Analyzer) tdsStats() *TDSStats {
	if a.tds == nil {
		a.tds = &TDSStats{ByErrorNo: map[string]int{}}
	}
	return a.tds
}
func (a *Analyzer) opcuaStats() *OPCUAStats {
	if a.opcua == nil {
		a.opcua = &OPCUAStats{ByMsg: map[string]int{}, ByStatus: map[string]int{}}
	}
	return a.opcua
}
func (a *Analyzer) serialStats() *SerialStats {
	if a.serial == nil {
		a.serial = &SerialStats{ByFunc: map[string]int{}, ByException: map[string]int{}}
	}
	return a.serial
}

func (a *Analyzer) setStatus(s string) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
}

func (a *Analyzer) setError(e string) {
	a.mu.Lock()
	a.status = "error"
	a.errMsg = e
	a.mu.Unlock()
}

// Tail returns the last n decoded events (for netcapture_tail / AI reads).
func (a *Analyzer) Tail(n int) []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n <= 0 || n > len(a.ring) {
		n = len(a.ring)
	}
	out := make([]Event, n)
	copy(out, a.ring[len(a.ring)-n:])
	return out
}

// Snapshot builds the structured Analysis (top flows by bytes), and runs the
// deterministic Diagnose() pass.
func (a *Analyzer) Snapshot(topFlows int) Analysis {
	a.mu.Lock()
	defer a.mu.Unlock()
	if topFlows <= 0 {
		topFlows = 25
	}
	flows := make([]Flow, 0, len(a.flows))
	for _, f := range a.flows {
		fc := *f
		flows = append(flows, fc)
	}
	sort.Slice(flows, func(i, j int) bool { return flows[i].Bytes > flows[j].Bytes })
	if len(flows) > topFlows {
		flows = flows[:topFlows]
	}
	if a.modbus != nil && a.modbus.Transactions > 0 {
		a.modbus.AvgLatencyMs = a.mbLatSum / float64(a.modbus.Transactions)
		a.modbus.MaxLatencyMs = a.mbLatMax
	}
	disc := append([]DisconnectEvent(nil), a.disc...)
	an := Analysis{
		Session:     a.session,
		Kind:        a.kind,
		Source:      a.source,
		Status:      a.status,
		StartedAt:   a.startedAt,
		UpdatedAt:   a.updatedAt,
		Packets:     a.packets,
		Bytes:       a.bytes,
		Protocols:   copyIntMap(a.protos),
		Flows:       flows,
		Disconnects: disc,
		Modbus:      a.modbus,
		HTTP:        a.http,
		DNS:         a.dns,
		S7:          a.s7,
		TDS:         a.tds,
		OPCUA:       a.opcua,
		Serial:      a.serial,
		Error:       a.errMsg,
	}
	an.Findings = diagnose(&an)
	return an
}

func copyIntMap(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
