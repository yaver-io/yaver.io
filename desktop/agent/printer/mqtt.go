package printer

// mqtt.go — a tiny, dependency-free MQTT 3.1.1 client, just enough to drive a
// Bambu printer: TLS CONNECT (user "bblp" + access code), SUBSCRIBE to the
// report topic, PUBLISH commands to the request topic, and read QoS-0 PUBLISH
// frames back. We hand-roll it (rather than pull in paho) to keep the agent's
// dependency surface minimal — the same posture as the rest of the repo, where
// tests run against real sockets with no third-party mocks.
//
// Only the packet types Bambu needs are implemented: CONNECT/CONNACK,
// SUBSCRIBE/SUBACK, PUBLISH (QoS 0 both ways), PINGREQ/PINGRESP, DISCONNECT.

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	pktCONNECT    = 0x10
	pktCONNACK    = 0x20
	pktPUBLISH    = 0x30
	pktSUBSCRIBE  = 0x82 // includes required flags 0b0010
	pktSUBACK     = 0x90
	pktPINGREQ    = 0xC0
	pktPINGRESP   = 0xD0
	pktDISCONNECT = 0xE0
)

// mqttClient is a minimal synchronous MQTT-over-TLS client.
type mqttClient struct {
	conn   net.Conn
	r      *bufio.Reader
	wmu    sync.Mutex
	nextID uint16
}

// mqttMessage is a received PUBLISH.
type mqttMessage struct {
	Topic   string
	Payload []byte
}

// dialMQTT opens a TLS MQTT connection and performs CONNECT. host is "ip:port".
// Bambu uses a self-signed cert, so verification is skipped (LAN-only, the access
// code is the real credential). clientID must be unique-ish per connection.
func dialMQTT(host, clientID, user, pass string, timeout time.Duration) (*mqttClient, error) {
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	d := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(d, "tcp", host, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return nil, fmt.Errorf("mqtt dial %s: %w", host, err)
	}
	c := &mqttClient{conn: conn, r: bufio.NewReader(conn), nextID: 1}
	if err := c.connect(clientID, user, pass, timeout); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *mqttClient) connect(clientID, user, pass string, timeout time.Duration) error {
	var vh []byte
	vh = appendString(vh, "MQTT")
	vh = append(vh, 0x04)       // protocol level 3.1.1
	vh = append(vh, 0xC2)       // flags: username+password+clean session
	vh = append(vh, 0x00, 0x3C) // keepalive 60s
	var payload []byte
	payload = appendString(payload, clientID)
	payload = appendString(payload, user)
	payload = appendString(payload, pass)

	if err := c.writePacket(pktCONNECT, append(vh, payload...), timeout); err != nil {
		return err
	}
	hdr, body, err := c.readPacket(timeout)
	if err != nil {
		return fmt.Errorf("mqtt connack read: %w", err)
	}
	if hdr&0xF0 != pktCONNACK || len(body) < 2 {
		return fmt.Errorf("mqtt: expected CONNACK, got 0x%02x", hdr)
	}
	if body[1] != 0x00 {
		return fmt.Errorf("mqtt connect refused (code %d — wrong access code?)", body[1])
	}
	return nil
}

// Subscribe subscribes to one topic at QoS 0 and waits for SUBACK.
func (c *mqttClient) Subscribe(topic string, timeout time.Duration) error {
	id := c.allocID()
	var body []byte
	body = append(body, byte(id>>8), byte(id))
	body = appendString(body, topic)
	body = append(body, 0x00) // QoS 0
	if err := c.writePacket(pktSUBSCRIBE, body, timeout); err != nil {
		return err
	}
	hdr, sbody, err := c.readUntil(pktSUBACK, timeout)
	if err != nil {
		return err
	}
	if hdr&0xF0 != pktSUBACK || len(sbody) < 3 || sbody[2] == 0x80 {
		return fmt.Errorf("mqtt subscribe failed for %s", topic)
	}
	return nil
}

// Publish sends a QoS-0 PUBLISH.
func (c *mqttClient) Publish(topic string, payload []byte, timeout time.Duration) error {
	var body []byte
	body = appendString(body, topic)
	body = append(body, payload...)
	return c.writePacket(pktPUBLISH, body, timeout)
}

// ReadMessage blocks for the next PUBLISH, answering PINGRESP/other control
// packets transparently, until deadline.
func (c *mqttClient) ReadMessage(timeout time.Duration) (mqttMessage, error) {
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return mqttMessage{}, io.EOF
		}
		hdr, body, err := c.readPacket(remaining)
		if err != nil {
			return mqttMessage{}, err
		}
		if hdr&0xF0 != pktPUBLISH {
			continue // CONNACK/SUBACK/PINGRESP — ignore, keep waiting for data
		}
		msg, err := decodePublish(hdr, body)
		if err != nil {
			return mqttMessage{}, err
		}
		return msg, nil
	}
}

// Ping sends PINGREQ (keepalive).
func (c *mqttClient) Ping(timeout time.Duration) error {
	return c.writePacket(pktPINGREQ, nil, timeout)
}

func (c *mqttClient) Close() error {
	_ = c.writePacket(pktDISCONNECT, nil, 2*time.Second)
	return c.conn.Close()
}

func (c *mqttClient) allocID() uint16 {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	id := c.nextID
	c.nextID++
	if c.nextID == 0 {
		c.nextID = 1
	}
	return id
}

func (c *mqttClient) writePacket(typeAndFlags byte, body []byte, timeout time.Duration) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(timeout))
	frame := make([]byte, 0, len(body)+5)
	frame = append(frame, typeAndFlags)
	frame = appendRemainingLen(frame, len(body))
	frame = append(frame, body...)
	_, err := c.conn.Write(frame)
	return err
}

func (c *mqttClient) readUntil(want byte, timeout time.Duration) (byte, []byte, error) {
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, nil, io.EOF
		}
		hdr, body, err := c.readPacket(remaining)
		if err != nil {
			return 0, nil, err
		}
		if hdr&0xF0 == want&0xF0 {
			return hdr, body, nil
		}
	}
}

func (c *mqttClient) readPacket(timeout time.Duration) (byte, []byte, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
	hdr, err := c.r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	n, err := readRemainingLen(c.r)
	if err != nil {
		return 0, nil, err
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return 0, nil, err
	}
	return hdr, body, nil
}

// decodePublish extracts topic+payload from a QoS-0 PUBLISH body.
func decodePublish(hdr byte, body []byte) (mqttMessage, error) {
	if len(body) < 2 {
		return mqttMessage{}, fmt.Errorf("mqtt: short PUBLISH")
	}
	tlen := int(binary.BigEndian.Uint16(body[:2]))
	if 2+tlen > len(body) {
		return mqttMessage{}, fmt.Errorf("mqtt: bad topic length")
	}
	topic := string(body[2 : 2+tlen])
	rest := body[2+tlen:]
	// QoS 1/2 would carry a 2-byte packet id here; Bambu uses QoS 0, but be
	// defensive in case a broker upgrades QoS.
	if hdr&0x06 != 0 && len(rest) >= 2 {
		rest = rest[2:]
	}
	return mqttMessage{Topic: topic, Payload: rest}, nil
}

// --- wire helpers (exported-free, unit-tested) ---

func appendString(b []byte, s string) []byte {
	b = append(b, byte(len(s)>>8), byte(len(s)))
	return append(b, s...)
}

// appendRemainingLen encodes the MQTT variable-length "remaining length".
func appendRemainingLen(b []byte, n int) []byte {
	for {
		digit := byte(n % 128)
		n /= 128
		if n > 0 {
			digit |= 0x80
		}
		b = append(b, digit)
		if n == 0 {
			return b
		}
	}
}

func readRemainingLen(r *bufio.Reader) (int, error) {
	value, mult := 0, 1
	for i := 0; i < 4; i++ {
		bb, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		value += int(bb&0x7F) * mult
		if bb&0x80 == 0 {
			return value, nil
		}
		mult *= 128
	}
	return 0, fmt.Errorf("mqtt: malformed remaining length")
}
