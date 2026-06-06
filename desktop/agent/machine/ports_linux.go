//go:build linux

package machine

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// resolveSerialDevice follows a /dev/serial/by-id/... (or any) symlink to its
// canonical /dev/tty* node so arbitration and opening agree on one identity.
// A by-id path is the stable handle for a durable worker (it survives the
// ttyUSB0→ttyUSB1 renumbering that happens on replug); we resolve it only for
// the bus key, callers may still open the symlink directly.
func resolveSerialDevice(dev string) string {
	if dev == "" {
		return dev
	}
	if real, err := filepath.EvalSymlinks(dev); err == nil && real != "" {
		return real
	}
	return dev
}

// listSerialPorts enumerates USB/ACM serial nodes and pairs each with its stable
// by-id symlink + kernel driver where discoverable.
func listSerialPorts() ([]SerialPortInfo, error) {
	byPath := map[string]*SerialPortInfo{}

	add := func(p string) {
		if _, ok := byPath[p]; !ok {
			byPath[p] = &SerialPortInfo{Path: p}
		}
	}
	// Primary nodes.
	for _, pat := range []string{"/dev/ttyUSB*", "/dev/ttyACM*"} {
		matches, _ := filepath.Glob(pat)
		for _, m := range matches {
			add(m)
		}
	}
	// Stable by-id symlinks → annotate the node they resolve to.
	if links, _ := filepath.Glob("/dev/serial/by-id/*"); len(links) > 0 {
		for _, link := range links {
			real := resolveSerialDevice(link)
			add(real)
			if info := byPath[real]; info != nil {
				info.ByID = link
				info.Description = describeByID(filepath.Base(link))
			}
		}
	}
	// Kernel driver (best-effort) from /sys.
	for path, info := range byPath {
		info.Driver = serialDriver(filepath.Base(path))
	}

	out := make([]SerialPortInfo, 0, len(byPath))
	for _, info := range byPath {
		out = append(out, *info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// describeByID turns an ftdi/ch340/cp210x by-id slug into a readable label.
func describeByID(slug string) string {
	s := strings.ReplaceAll(slug, "_", " ")
	if i := strings.Index(s, " if00"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(s, "usb ")
}

// serialDriver reads the bound kernel driver for a tty node from sysfs.
func serialDriver(node string) string {
	link, err := os.Readlink("/sys/class/tty/" + node + "/device/driver")
	if err != nil {
		return ""
	}
	return filepath.Base(link)
}

// autoBaud taps the bus at each candidate baud for a short window and keeps the
// baud that yields the most CRC-valid Modbus frames. It needs live traffic on
// the wire; a silent bus returns Best=0 with all-zero counts.
func autoBaud(dev string, perBaud ...int) (AutoBaudResult, error) {
	window := 1500 * time.Millisecond
	if len(perBaud) > 0 && perBaud[0] > 0 {
		window = time.Duration(perBaud[0]) * time.Millisecond
	}
	res := AutoBaudResult{Counts: map[int]int{}}
	best, bestN := 0, -1
	for _, baud := range commonBauds {
		n := countFramesAtBaud(dev, baud, window)
		res.Counts[baud] = n
		if n > bestN {
			bestN, best = n, baud
		}
		// A decisive winner (>= 8 clean frames) ends the probe early.
		if n >= 8 {
			break
		}
	}
	if bestN <= 0 {
		best = 0
	}
	res.Best = best
	return res, nil
}

// countFramesAtBaud opens the port at one baud, sniffs for `window`, and returns
// how many CRC-valid frames the extractor recovered.
func countFramesAtBaud(dev string, baud int, window time.Duration) int {
	rc, err := openSerial(dev, baud)
	if err != nil {
		return 0
	}
	defer rc.Close()
	sn := newSniffer("modbus_rtu")
	deadline := time.Now().Add(window)
	buf := make([]byte, 512)
	for time.Now().Before(deadline) {
		n, rerr := rc.Read(buf)
		if n > 0 {
			sn.Feed(buf[:n])
		}
		if rerr != nil {
			break
		}
	}
	sn.mu.Lock()
	frames := sn.frames
	sn.mu.Unlock()
	return frames
}
