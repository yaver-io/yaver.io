//go:build !linux

package machine

// Serial enumeration / auto-baud are Linux-only (they need /dev + termios).
// resolveSerialDevice lives in serial_other.go.

func listSerialPorts() ([]SerialPortInfo, error) { return nil, ErrUnsupported }

func autoBaud(dev string, perBaud ...int) (AutoBaudResult, error) {
	return AutoBaudResult{}, ErrUnsupported
}
