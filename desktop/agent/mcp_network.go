package main

import (
	"fmt"
	"net"
	osexec "os/exec"
	"runtime"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Packet capture & analysis
// ---------------------------------------------------------------------------

func mcpTcpdump(iface string, count int, filter string) interface{} {
	if count <= 0 {
		count = 20
	}
	args := []string{"tcpdump", "-c", strconv.Itoa(count), "-nn"}
	if iface != "" {
		args = append(args, "-i", iface)
	} else {
		args = append(args, "-i", "any")
	}
	if filter != "" {
		args = append(args, strings.Fields(filter)...)
	}
	out, err := runCmd("sudo", args...)
	if err != nil {
		// Try without sudo
		out, err = runCmd("tcpdump", args[1:]...)
		if err != nil {
			return map[string]interface{}{"error": fmt.Sprintf("tcpdump: %s — %s", err, out)}
		}
	}
	return map[string]interface{}{"packets": out, "count": count}
}

func mcpTcpdumpHTTP(iface string, count int) interface{} {
	return mcpTcpdump(iface, count, "tcp port 80 or tcp port 443")
}

func mcpTcpdumpDNS(iface string, count int) interface{} {
	return mcpTcpdump(iface, count, "udp port 53")
}

func mcpTshark(iface string, count int, filter, fields string) interface{} {
	if count <= 0 {
		count = 20
	}
	args := []string{"-c", strconv.Itoa(count)}
	if iface != "" {
		args = append(args, "-i", iface)
	}
	if filter != "" {
		args = append(args, "-Y", filter)
	}
	if fields != "" {
		args = append(args, "-T", "fields")
		for _, f := range strings.Split(fields, ",") {
			args = append(args, "-e", strings.TrimSpace(f))
		}
	}
	out, err := runCmd("tshark", args...)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("tshark: %s (install: brew install wireshark) — %s", err, out)}
	}
	return map[string]interface{}{"packets": out, "count": count}
}

func mcpPcapAnalyze(file, filter string) interface{} {
	args := []string{"-r", file, "-q", "-z", "conv,tcp"}
	if filter != "" {
		args = append(args, "-Y", filter)
	}
	out, err := runCmd("tshark", args...)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("tshark: %s", out)}
	}
	return map[string]interface{}{"analysis": out, "file": file}
}

func mcpPcapStats(file string) interface{} {
	out, err := runCmd("capinfos", file)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("capinfos: %s (part of wireshark) — %s", err, out)}
	}
	return map[string]interface{}{"stats": out, "file": file}
}

// ---------------------------------------------------------------------------
// Network tools — nc, arp, nmap, traceroute, mtr, netcat
// ---------------------------------------------------------------------------

func mcpNetcat(host string, port int, data string) interface{} {
	args := []string{"-zv", "-w", "3", host, strconv.Itoa(port)}
	if data != "" {
		// Send data mode
		cmd := osexec.Command("nc", "-w", "3", host, strconv.Itoa(port))
		cmd.Stdin = strings.NewReader(data)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return map[string]interface{}{"error": err.Error(), "output": string(out)}
		}
		return map[string]interface{}{"output": string(out), "host": host, "port": port}
	}
	out, err := runCmd("nc", args...)
	if err != nil {
		return map[string]interface{}{"host": host, "port": port, "open": false, "output": out}
	}
	return map[string]interface{}{"host": host, "port": port, "open": true, "output": out}
}

func mcpPortScan(host string, ports string) interface{} {
	if ports == "" {
		ports = "22,80,443,3000,3306,5432,6379,8080,8443,9090,27017"
	}
	var results []map[string]interface{}
	for _, ps := range strings.Split(ports, ",") {
		p, err := strconv.Atoi(strings.TrimSpace(ps))
		if err != nil {
			continue
		}
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, p), 2e9)
		open := err == nil
		if open {
			conn.Close()
		}
		results = append(results, map[string]interface{}{"port": p, "open": open})
	}
	return map[string]interface{}{"host": host, "ports": results}
}

func mcpArpTable() interface{} {
	out, err := runCmd("arp", "-a")
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"arp_table": out}
}

func mcpArpScan(subnet string) interface{} {
	if subnet == "" {
		subnet = "192.168.1.0/24"
	}
	out, err := runCmd("arp-scan", "--localnet")
	if err != nil {
		// Fallback: use nmap
		out, err = runCmd("nmap", "-sn", subnet)
		if err != nil {
			return map[string]interface{}{"error": fmt.Sprintf("arp-scan/nmap: %s — %s", err, out)}
		}
	}
	return map[string]interface{}{"devices": out, "subnet": subnet}
}

func mcpNmapScan(target, scanType string, ports string) interface{} {
	args := []string{}
	switch scanType {
	case "quick":
		args = []string{"-F", target}
	case "services":
		args = []string{"-sV", target}
	case "os":
		args = []string{"-O", target}
	case "full":
		args = []string{"-A", target}
	case "udp":
		args = []string{"-sU", "--top-ports", "20", target}
	case "ping":
		args = []string{"-sn", target}
	default:
		args = []string{"-F", target}
	}
	if ports != "" {
		args = append(args, "-p", ports)
	}
	out, err := runCmd("nmap", args...)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("nmap: %s — %s", err, out)}
	}
	return map[string]interface{}{"scan": out, "target": target, "type": scanType}
}

func mcpTraceroute(host string, maxHops int) interface{} {
	if maxHops <= 0 {
		maxHops = 30
	}
	var out string
	var err error
	if runtime.GOOS == "darwin" {
		out, err = runCmd("traceroute", "-m", strconv.Itoa(maxHops), host)
	} else {
		out, err = runCmd("traceroute", "-m", strconv.Itoa(maxHops), host)
	}
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": out}
	}
	return map[string]interface{}{"route": out, "host": host}
}

func mcpMtr(host string, count int) interface{} {
	if count <= 0 {
		count = 5
	}
	out, err := runCmd("mtr", "--report", "--report-cycles", strconv.Itoa(count), host)
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("mtr: %s (install: brew install mtr) — %s", err, out)}
	}
	return map[string]interface{}{"report": out, "host": host}
}

func mcpNetworkInterfaces() interface{} {
	ifaces, err := net.Interfaces()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	var result []map[string]interface{}
	for _, i := range ifaces {
		addrs, _ := i.Addrs()
		var addrStrs []string
		for _, a := range addrs {
			addrStrs = append(addrStrs, a.String())
		}
		result = append(result, map[string]interface{}{
			"name":       i.Name,
			"mac":        i.HardwareAddr.String(),
			"mtu":        i.MTU,
			"flags":      i.Flags.String(),
			"addresses":  addrStrs,
		})
	}
	return map[string]interface{}{"interfaces": result, "count": len(result)}
}

func mcpIPRoute() interface{} {
	var out string
	var err error
	if runtime.GOOS == "darwin" {
		out, err = runCmd("netstat", "-rn")
	} else {
		out, err = runCmd("ip", "route", "show")
	}
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"routes": out}
}

func mcpListenPortsDetailed() interface{} {
	var out string
	var err error
	if runtime.GOOS == "darwin" {
		out, err = runCmd("lsof", "-i", "-P", "-n", "-sTCP:LISTEN")
	} else {
		out, err = runCmd("ss", "-tlnp")
	}
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"listening": out}
}

func mcpNetworkConnections(state string) interface{} {
	var out string
	var err error
	if runtime.GOOS == "darwin" {
		args := []string{"-an", "-p", "tcp"}
		out, err = runCmd("netstat", args...)
	} else {
		args := []string{"-tunap"}
		if state != "" {
			args = append(args, "state", state)
		}
		out, err = runCmd("ss", args...)
	}
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"connections": out}
}

func mcpBandwidthTest(host string) interface{} {
	// Try iperf3
	out, err := runCmd("iperf3", "-c", host, "-t", "5", "--json")
	if err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("iperf3: %s (install: brew install iperf3, run server: iperf3 -s) — %s", err, out)}
	}
	return map[string]interface{}{"results": out}
}

func mcpCurlTimings(urlStr string) interface{} {
	if err := guardOutboundHTTPURL(urlStr); err != nil { // A3: no metadata/link-local SSRF
		return map[string]interface{}{"error": err.Error()}
	}
	format := `{"time_namelookup": %{time_namelookup}, "time_connect": %{time_connect}, "time_appconnect": %{time_appconnect}, "time_pretransfer": %{time_pretransfer}, "time_starttransfer": %{time_starttransfer}, "time_total": %{time_total}, "http_code": %{http_code}, "size_download": %{size_download}, "speed_download": %{speed_download}}`
	out, err := runCmd("curl", "-so", "/dev/null", "-w", format, urlStr)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"timings": out, "url": urlStr}
}
