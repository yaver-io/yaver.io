package netcapture

import "fmt"

// diagnose runs the deterministic findings pass over a snapshot. It is the
// "deep analysis" that does NOT need an LLM — pattern-matched, explainable
// findings the UI renders and the netcapture_analyze verb feeds to the model as
// grounding. Severity: error > warn > info.
func diagnose(an *Analysis) []Finding {
	var f []Finding

	// ── connectivity ──────────────────────────────────────────────────────
	refused, resets, fins := 0, 0, 0
	for _, d := range an.Disconnects {
		switch d.Cause {
		case "conn_refused":
			refused++
		case "tcp_reset":
			resets++
		case "fin":
			fins++
		}
	}
	if refused > 0 {
		f = append(f, Finding{
			Severity: "error", Code: "conn_refused",
			Title: fmt.Sprintf("%d connection(s) refused", refused),
			Detail: "SYN met an immediate RST — the target port is closed or the service is down. Check the PLC/server is listening and the port/firewall is correct.",
		})
	}
	if resets > 0 {
		f = append(f, Finding{
			Severity: "warn", Code: "tcp_resets",
			Title: fmt.Sprintf("%d mid-session TCP reset(s)", resets),
			Detail: "An established connection was torn down with RST — often a device reboot, watchdog, idle timeout on the controller, or a cable/PSU glitch.",
		})
	}

	// ── retransmit storms (link quality) ──────────────────────────────────
	for _, fl := range an.Flows {
		if fl.Retransmits >= 5 {
			sev := "warn"
			if fl.Retransmits >= 20 {
				sev = "error"
			}
			f = append(f, Finding{
				Severity: sev, Code: "retransmits",
				Title: fmt.Sprintf("%d retransmissions on %s", fl.Retransmits, fl.Key),
				Detail: "Heavy retransmission means packet loss — bad cabling/connector, duplex mismatch, EMI on the line, or an overloaded switch. This is the usual cause of 'the HMI is laggy / times out'.",
			})
		}
	}

	// ── Modbus ────────────────────────────────────────────────────────────
	if m := an.Modbus; m != nil {
		if m.Exceptions > 0 {
			f = append(f, Finding{
				Severity: "warn", Code: "modbus_exceptions",
				Title: fmt.Sprintf("%d Modbus exception response(s)", m.Exceptions),
				Detail: "The slave rejected requests: " + topMap(m.ByException) + ". 0x02 illegal-address = wrong register map; 0x06 slave-busy = controller overloaded; 0x04 device-failure = a fault on the PLC.",
			})
		}
		if m.MaxLatencyMs >= 500 {
			f = append(f, Finding{
				Severity: "warn", Code: "modbus_slow",
				Title: fmt.Sprintf("Modbus latency up to %.0f ms", m.MaxLatencyMs),
				Detail: "Slow request/response turnaround — the controller scan cycle is long, the link is congested, or polling is too aggressive.",
			})
		}
	}

	// ── HTTP ──────────────────────────────────────────────────────────────
	if h := an.HTTP; h != nil && h.Errors > 0 {
		f = append(f, Finding{
			Severity: "warn", Code: "http_errors",
			Title: fmt.Sprintf("%d HTTP error response(s)", h.Errors),
			Detail: "Status breakdown: " + topMap(h.ByStatus) + ". 5xx = server/gateway fault; 4xx = bad request/auth.",
		})
	}

	// ── DNS ───────────────────────────────────────────────────────────────
	if d := an.DNS; d != nil && (d.NXDomain > 0 || d.ServFail > 0) {
		f = append(f, Finding{
			Severity: "warn", Code: "dns_failures",
			Title: fmt.Sprintf("DNS failures (NXDOMAIN=%d, SERVFAIL=%d)", d.NXDomain, d.ServFail),
			Detail: "Name resolution is failing — a hostname is wrong/unregistered or the resolver is unreachable. ERP/cloud links break here before any TCP is attempted.",
		})
	}

	// ── S7 / LOGO! ────────────────────────────────────────────────────────
	if s := an.S7; s != nil && s.Errors > 0 {
		f = append(f, Finding{
			Severity: "warn", Code: "s7_errors",
			Title: fmt.Sprintf("%d S7 error(s)", s.Errors),
			Detail: "S7comm errors: " + topMap(s.ByError) + ". Usually an out-of-range DB/area access or the CPU rejecting the read/write.",
		})
	}

	// ── MS-SQL / TDS (ERP) ────────────────────────────────────────────────
	if t := an.TDS; t != nil {
		if t.LoginFailures > 0 {
			f = append(f, Finding{
				Severity: "error", Code: "tds_login_failed",
				Title: fmt.Sprintf("%d SQL Server login failure(s)", t.LoginFailures),
				Detail: "ERP↔database authentication is failing — wrong credentials, expired password, or the login lacks access to the database. (SQL text/credentials are redacted by default.)",
			})
		} else if t.Errors > 0 {
			f = append(f, Finding{
				Severity: "warn", Code: "tds_errors",
				Title: fmt.Sprintf("%d SQL Server error token(s)", t.Errors),
				Detail: "The database returned errors (numbers: " + topMap(t.ByErrorNo) + ") — query/permission/timeout issues between the ERP and the DB.",
			})
		}
	}

	// ── OPC-UA ────────────────────────────────────────────────────────────
	if o := an.OPCUA; o != nil && o.Errors > 0 {
		f = append(f, Finding{
			Severity: "warn", Code: "opcua_errors",
			Title: fmt.Sprintf("%d OPC-UA error/fault(s)", o.Errors),
			Detail: "OPC-UA service faults: " + topMap(o.ByStatus) + ". Common: BadSessionIdInvalid (session dropped), BadSecurityChecksFailed (cert/trust), BadTimeout.",
		})
	}

	// ── Serial ────────────────────────────────────────────────────────────
	if s := an.Serial; s != nil {
		if s.CRCErrors > 0 {
			f = append(f, Finding{
				Severity: "warn", Code: "serial_crc",
				Title: fmt.Sprintf("%d serial CRC error(s)", s.CRCErrors),
				Detail: "Corrupt frames on the RS232/RS485 line — wrong baud/parity, missing termination/bias resistors on the 485 bus, or EMI. Confirm both ends agree on baud and framing.",
			})
		}
		if s.Frames == 0 && s.Bytes > 0 {
			f = append(f, Finding{
				Severity: "warn", Code: "serial_undecoded",
				Title: "bytes seen but no frames decoded",
				Detail: "Traffic is present but the selected decoder found no valid frames — likely the wrong protocol/baud, or A/B (D+/D−) swapped on the RS485 pair.",
			})
		}
	}

	if len(f) == 0 {
		f = append(f, Finding{Severity: "info", Code: "healthy", Title: "No anomalies detected in this capture", Detail: "Traffic looks healthy across the decoded protocols. Capture longer or during the fault window to catch intermittent issues."})
	}
	return f
}

// topMap renders the largest few entries of a count map for finding detail.
func topMap(m map[string]int) string {
	if len(m) == 0 {
		return "(none)"
	}
	type kv struct {
		k string
		v int
	}
	var s []kv
	for k, v := range m {
		s = append(s, kv{k, v})
	}
	// simple insertion sort by count desc (maps are tiny)
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].v > s[j-1].v; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	out := ""
	for i, e := range s {
		if i >= 4 {
			out += ", …"
			break
		}
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%s×%d", e.k, e.v)
	}
	return out
}
