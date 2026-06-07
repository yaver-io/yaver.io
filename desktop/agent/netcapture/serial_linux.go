//go:build linux

package netcapture

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// openSerialTTY opens a serial device read-only in raw 8N1 mode for passive
// tapping. Same termios setup as the machine package; kept self-contained.
//
// GOTCHA (from hard-won experience): never open a port another process already
// holds — toggling DTR resets many boards and the held owner sees EIO. On a
// phone-as-host the USB-serial bytes come in via Feed instead, sidestepping this.
func openSerialTTY(dev string, baud int) (io.ReadCloser, error) {
	if baud <= 0 {
		baud = 9600
	}
	f, err := os.OpenFile(dev, os.O_RDONLY|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd())
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		f.Close()
		return nil, err
	}
	bc := baudConst(baud)
	t.Cflag = unix.CS8 | unix.CREAD | unix.CLOCAL | bc
	t.Iflag = 0
	t.Oflag = 0
	t.Lflag = 0
	t.Cc[unix.VMIN] = 0
	t.Cc[unix.VTIME] = 1 // 0.1s read timeout
	t.Ispeed = bc
	t.Ospeed = bc
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, t); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func baudConst(baud int) uint32 {
	switch baud {
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
