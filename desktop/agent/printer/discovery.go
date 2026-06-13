package printer

// discovery.go — Bambu Lab printers announce themselves on the LAN via an
// SSDP-style NOTIFY broadcast to 239.255.255.250:1900, received on UDP 2021 (and
// 1990). The announcement is credential-free and carries everything a picker
// needs: IP, serial (USN), model code, name, firmware, signal. We listen for a
// few seconds and de-dupe by serial.
//
// Example NOTIFY observed from a P1S (192.0.2.11):
//
//	NOTIFY * HTTP/1.1
//	NT: urn:bambulab-com:device:3dprinter:1
//	USN: 01P00X000000000
//	Location: 192.0.2.11
//	DevModel.bambu.com: C12
//	DevName.bambu.com: 3DP-01P-978
//	DevSignal.bambu.com: -41
//	DevConnect.bambu.com: cloud
//	DevBind.bambu.com: occupied
//	DevVersion.bambu.com: 01.09.01.00

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"time"
)

// ssdpPorts are the UDP ports Bambu printers broadcast their NOTIFY on.
var ssdpPorts = []int{2021, 1990}

// Discover listens for Bambu SSDP announcements for up to timeout and returns
// the de-duped set found. A zero/short timeout is clamped to a sane window —
// printers re-announce roughly every 5s, so ~6s catches an idle fleet.
func Discover(ctx context.Context, timeout time.Duration) ([]Discovered, error) {
	if timeout < time.Second {
		timeout = 6 * time.Second
	}
	deadline := time.Now().Add(timeout)
	found := map[string]Discovered{}

	for _, port := range ssdpPorts {
		pc, err := net.ListenPacket("udp4", ":"+strconv.Itoa(port))
		if err != nil {
			continue // port busy (another listener) — try the next
		}
		listenUntil(ctx, pc, deadline, found)
		_ = pc.Close()
	}

	out := make([]Discovered, 0, len(found))
	for _, d := range found {
		out = append(out, d)
	}
	return out, nil
}

func listenUntil(ctx context.Context, pc net.PacketConn, deadline time.Time, found map[string]Discovered) {
	buf := make([]byte, 2048)
	for {
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = pc.SetReadDeadline(time.Now().Add(750 * time.Millisecond))
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		d, ok := ParseSSDP(buf[:n])
		if !ok {
			continue
		}
		if d.IP == "" {
			if host, _, e := net.SplitHostPort(addr.String()); e == nil {
				d.IP = host
			}
		}
		key := d.Serial
		if key == "" {
			key = d.IP
		}
		found[key] = d
	}
}

// ParseSSDP parses one NOTIFY datagram into a Discovered. It is exported and
// pure so it can be unit-tested without a live printer. Returns ok=false for a
// datagram that is not a Bambu device announcement.
func ParseSSDP(raw []byte) (Discovered, bool) {
	text := string(raw)
	if !strings.Contains(text, "bambulab") && !strings.Contains(text, "bambu.com") {
		return Discovered{}, false
	}
	var d Discovered
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, val, ok := splitHeader(line)
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "location":
			d.IP = strings.TrimPrefix(strings.TrimPrefix(val, "http://"), "https://")
			d.IP = strings.Trim(strings.SplitN(d.IP, "/", 2)[0], "[]")
			if host, _, e := net.SplitHostPort(d.IP); e == nil {
				d.IP = host
			}
		case "usn":
			d.Serial = val
		case "devmodel.bambu.com":
			d.ModelKey = val
			d.Model = ModelName(val)
		case "devname.bambu.com":
			d.Name = val
		case "devversion.bambu.com":
			d.Firmware = val
		case "devsignal.bambu.com":
			d.SignalDB, _ = strconv.Atoi(strings.TrimSuffix(val, "dBm"))
		case "devconnect.bambu.com":
			d.Connect = val
		case "devbind.bambu.com":
			d.Bind = val
		}
	}
	if d.Serial == "" && d.ModelKey == "" {
		return Discovered{}, false
	}
	return d, true
}

func splitHeader(line string) (key, val string, ok bool) {
	i := strings.IndexByte(line, ':')
	if i <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
}

// ModelName maps Bambu wire model codes (DevModel.bambu.com) to human names.
// Unknown codes pass through unchanged so a new printer still shows *something*.
func ModelName(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "C11":
		return "P1P"
	case "C12":
		return "P1S"
	case "C13", "N2S":
		return "X1E"
	case "BL-P001":
		return "X1 Carbon"
	case "BL-P002":
		return "X1"
	case "N1":
		return "A1 mini"
	case "N2":
		return "A1"
	case "O1D", "O1E":
		return "H2D"
	default:
		return code
	}
}
