//go:build linux

package machine

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

const machineSerialSupported = true

func baudConst(b int) uint32 {
	switch b {
	case 1200:
		return unix.B1200
	case 2400:
		return unix.B2400
	case 4800:
		return unix.B4800
	case 9600:
		return unix.B9600
	case 19200:
		return unix.B19200
	case 38400:
		return unix.B38400
	case 57600:
		return unix.B57600
	case 115200:
		return unix.B115200
	case 230400:
		return unix.B230400
	default:
		return unix.B9600
	}
}

// openSerial opens a serial device in raw 8N1 mode at the given baud. The port
// is opened O_RDWR so the same handle drives both passive sniffing (read-only
// use) and the active RTU master (read+write). VMIN=0/VTIME=1 gives a 0.1s read
// timeout so reader loops can check their stop channel and so the RTU master's
// framed reader makes forward progress without blocking forever.
func openSerial(dev string, baud int) (io.ReadWriteCloser, error) {
	if baud == 0 {
		baud = 9600
	}
	dev = resolveSerialDevice(dev)
	fd, err := unix.Open(dev, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	// raw mode
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB
	t.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL
	// baud (encoded in CBAUD bits of c_cflag)
	t.Cflag &^= unix.CBAUD
	t.Cflag |= baudConst(baud)
	t.Cc[unix.VMIN] = 0
	t.Cc[unix.VTIME] = 1
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, t); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	_ = unix.SetNonblock(fd, false)
	return os.NewFile(uintptr(fd), dev), nil
}
