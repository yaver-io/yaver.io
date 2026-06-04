//go:build !linux

package machine

import "io"

const machineSerialSupported = false

// openSerial is unsupported off Linux; Modbus-TCP scan/read still works via the
// Engine's TCP methods. Use StartManual + FeedSession to replay a capture.
func openSerial(dev string, baud int) (io.ReadCloser, error) {
	return nil, ErrUnsupported
}
