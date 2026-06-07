//go:build !linux

package netcapture

import "io"

// openSerialTTY is unsupported off Linux. The manual/Feed path (phone-as-host,
// the IoT connector box, pcap replay) still works on every platform.
func openSerialTTY(dev string, baud int) (io.ReadCloser, error) {
	return nil, ErrUnsupported
}
