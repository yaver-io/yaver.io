package netcapture

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Pure-Go reader for the classic libpcap file format — exactly what
// `tcpdump -w` writes. We parse it ourselves so deep analysis never needs
// tshark/Wireshark installed on the edge box. (pcapng is not emitted by plain
// tcpdump -w and is intentionally out of scope.)

const (
	pcapMagicMicroBE = 0xa1b2c3d4
	pcapMagicNanoBE  = 0xa1b23c4d
)

type pcapReader struct {
	r        io.Reader
	bo       binary.ByteOrder
	linkType int
	nano     bool
	hdr      [16]byte
}

// newPcapReader reads the 24-byte global header and prepares per-record reads.
func newPcapReader(r io.Reader) (*pcapReader, error) {
	var gh [24]byte
	if _, err := io.ReadFull(r, gh[:]); err != nil {
		return nil, fmt.Errorf("pcap header: %w", err)
	}
	pr := &pcapReader{r: r}
	magic := binary.BigEndian.Uint32(gh[0:4])
	switch magic {
	case pcapMagicMicroBE:
		pr.bo = binary.BigEndian
	case pcapMagicNanoBE:
		pr.bo, pr.nano = binary.BigEndian, true
	default:
		// little-endian variants (byte-swapped magic)
		lm := binary.LittleEndian.Uint32(gh[0:4])
		switch lm {
		case pcapMagicMicroBE:
			pr.bo = binary.LittleEndian
		case pcapMagicNanoBE:
			pr.bo, pr.nano = binary.LittleEndian, true
		default:
			return nil, fmt.Errorf("pcap: bad magic 0x%08x", magic)
		}
	}
	pr.linkType = int(pr.bo.Uint32(gh[20:24]))
	return pr, nil
}

// next reads one packet record. Returns (tsMillis, data, nil) or io.EOF.
// The returned slice is reused across calls; copy if you retain it.
func (pr *pcapReader) next() (int64, []byte, error) {
	if _, err := io.ReadFull(pr.r, pr.hdr[:]); err != nil {
		return 0, nil, err
	}
	tsSec := pr.bo.Uint32(pr.hdr[0:4])
	tsFrac := pr.bo.Uint32(pr.hdr[4:8])
	inclLen := pr.bo.Uint32(pr.hdr[8:12])
	if inclLen == 0 || inclLen > 1<<20 { // 1 MiB sanity cap
		return 0, nil, fmt.Errorf("pcap: implausible record length %d", inclLen)
	}
	buf := make([]byte, inclLen)
	if _, err := io.ReadFull(pr.r, buf); err != nil {
		return 0, nil, err
	}
	ms := int64(tsSec) * 1000
	if pr.nano {
		ms += int64(tsFrac) / 1_000_000
	} else {
		ms += int64(tsFrac) / 1000
	}
	return ms, buf, nil
}
