package brain

// Phase-0 frame body codecs. Layout mirrors core/include/yvr/body.h
// exactly — same field order, same key ordering on the wire,
// same forward-compat skip-unknown decoder semantics.
//
// Currently covered: HELLO, HEARTBEAT, INVOKE. Adding the rest
// (AUTH/AUTHRSP/ATTEST/ERROR/TOOL_RSP/STREAM_CHUNK/NEED/MODULE)
// is a follow-up slice once the parity-test pattern is locked
// in.

// ProtocolVersion mirrors YVR_PROTOCOL_VERSION in body.h.
const ProtocolVersion = 1

// ── HELLO ─────────────────────────────────────────────────────

// Hello is the first frame on every session. Each peer sends
// its own; both sides must arrive before the session proceeds.
type Hello struct {
	ProtocolVersion uint32
	Role            string // "brain" | "device"
	AgentVersion    string // optional — empty string omits the field
}

// Encode writes h into buf in CTAP2 deterministic order. Returns
// the number of bytes written.
func (h Hello) Encode(buf []byte) (int, error) {
	if h.Role == "" {
		return 0, ErrInvalidArg
	}
	w := NewCBORWriter(buf)

	// CTAP2 order: "v" (0x61) < "role" (0x64) < "agent_version" (0x6d).
	hasAV := h.AgentVersion != ""
	if hasAV {
		w.BeginMap(3)
	} else {
		w.BeginMap(2)
	}

	w.WriteText("v")
	w.WriteUint(uint64(h.ProtocolVersion))

	w.WriteText("role")
	w.WriteText(h.Role)

	if hasAV {
		w.WriteText("agent_version")
		w.WriteText(h.AgentVersion)
	}

	if w.Err != nil {
		return 0, w.Err
	}
	return w.Len(), nil
}

// DecodeHello parses one HELLO body from buf. Unknown fields
// are skipped for forward compatibility.
func DecodeHello(buf []byte) (Hello, error) {
	r := NewCBORReader(buf)
	kv, err := r.ReadMapBegin()
	if err != nil {
		return Hello{}, err
	}

	var out Hello
	var seenV, seenRole bool

	for i := 0; i < kv; i++ {
		key, err := r.ReadText()
		if err != nil {
			return Hello{}, err
		}
		switch key {
		case "v":
			v, err := r.ReadUint()
			if err != nil {
				return Hello{}, err
			}
			if v > 0xFFFFFFFF {
				return Hello{}, ErrBadFrame
			}
			out.ProtocolVersion = uint32(v)
			seenV = true
		case "role":
			s, err := r.ReadText()
			if err != nil {
				return Hello{}, err
			}
			out.Role = s
			seenRole = true
		case "agent_version":
			s, err := r.ReadText()
			if err != nil {
				return Hello{}, err
			}
			out.AgentVersion = s
		default:
			if err := r.Skip(); err != nil {
				return Hello{}, err
			}
		}
	}
	if !seenV || !seenRole {
		return Hello{}, ErrBadFrame
	}
	return out, nil
}

// ── HEARTBEAT ─────────────────────────────────────────────────

// Heartbeat is a periodic liveness ping. Brain → device may
// carry a signed wall clock so the device can correct its time.
type Heartbeat struct {
	ProtocolVersion uint32
	NowMs           uint64
	Signature       []byte // optional
}

func (h Heartbeat) Encode(buf []byte) (int, error) {
	w := NewCBORWriter(buf)

	hasSig := len(h.Signature) > 0
	if hasSig {
		w.BeginMap(3)
	} else {
		w.BeginMap(2)
	}

	// CTAP2 order: "v" (0x61) < "now_ms" (0x66) < "signature" (0x69).
	w.WriteText("v")
	w.WriteUint(uint64(h.ProtocolVersion))

	w.WriteText("now_ms")
	w.WriteUint(h.NowMs)

	if hasSig {
		w.WriteText("signature")
		w.WriteBytes(h.Signature)
	}

	if w.Err != nil {
		return 0, w.Err
	}
	return w.Len(), nil
}

func DecodeHeartbeat(buf []byte) (Heartbeat, error) {
	r := NewCBORReader(buf)
	kv, err := r.ReadMapBegin()
	if err != nil {
		return Heartbeat{}, err
	}

	var out Heartbeat
	var seenV, seenNow bool

	for i := 0; i < kv; i++ {
		key, err := r.ReadText()
		if err != nil {
			return Heartbeat{}, err
		}
		switch key {
		case "v":
			v, err := r.ReadUint()
			if err != nil {
				return Heartbeat{}, err
			}
			if v > 0xFFFFFFFF {
				return Heartbeat{}, ErrBadFrame
			}
			out.ProtocolVersion = uint32(v)
			seenV = true
		case "now_ms":
			v, err := r.ReadUint()
			if err != nil {
				return Heartbeat{}, err
			}
			out.NowMs = v
			seenNow = true
		case "signature":
			b, err := r.ReadBytes()
			if err != nil {
				return Heartbeat{}, err
			}
			// Copy because the reader's slice aliases buf,
			// which the caller may mutate after Decode returns.
			out.Signature = append([]byte(nil), b...)
		default:
			if err := r.Skip(); err != nil {
				return Heartbeat{}, err
			}
		}
	}
	if !seenV || !seenNow {
		return Heartbeat{}, ErrBadFrame
	}
	return out, nil
}

// ── INVOKE ────────────────────────────────────────────────────

// Invoke is brain → device: run a module by hash with vendor-
// defined args.
type Invoke struct {
	ProtocolVersion uint32
	ToolHash        []byte
	Method          string
	Args            []byte // opaque CBOR; vendor-defined
	Approval        []byte // optional signed approval token
}

func (iv Invoke) Encode(buf []byte) (int, error) {
	if len(iv.ToolHash) == 0 {
		return 0, ErrInvalidArg
	}
	if iv.Method == "" {
		return 0, ErrInvalidArg
	}

	w := NewCBORWriter(buf)
	hasApproval := len(iv.Approval) > 0
	if hasApproval {
		w.BeginMap(5)
	} else {
		w.BeginMap(4)
	}

	// CTAP2 order: v < args < method < approval < tool_hash.
	w.WriteText("v")
	w.WriteUint(uint64(iv.ProtocolVersion))

	w.WriteText("args")
	w.WriteBytes(iv.Args)

	w.WriteText("method")
	w.WriteText(iv.Method)

	if hasApproval {
		w.WriteText("approval")
		w.WriteBytes(iv.Approval)
	}

	w.WriteText("tool_hash")
	w.WriteBytes(iv.ToolHash)

	if w.Err != nil {
		return 0, w.Err
	}
	return w.Len(), nil
}

func DecodeInvoke(buf []byte) (Invoke, error) {
	r := NewCBORReader(buf)
	kv, err := r.ReadMapBegin()
	if err != nil {
		return Invoke{}, err
	}

	var out Invoke
	var seenV, seenArgs, seenMethod, seenHash bool

	for i := 0; i < kv; i++ {
		key, err := r.ReadText()
		if err != nil {
			return Invoke{}, err
		}
		switch key {
		case "v":
			v, err := r.ReadUint()
			if err != nil {
				return Invoke{}, err
			}
			if v > 0xFFFFFFFF {
				return Invoke{}, ErrBadFrame
			}
			out.ProtocolVersion = uint32(v)
			seenV = true
		case "args":
			b, err := r.ReadBytes()
			if err != nil {
				return Invoke{}, err
			}
			out.Args = append([]byte(nil), b...)
			seenArgs = true
		case "method":
			s, err := r.ReadText()
			if err != nil {
				return Invoke{}, err
			}
			out.Method = s
			seenMethod = true
		case "approval":
			b, err := r.ReadBytes()
			if err != nil {
				return Invoke{}, err
			}
			out.Approval = append([]byte(nil), b...)
		case "tool_hash":
			b, err := r.ReadBytes()
			if err != nil {
				return Invoke{}, err
			}
			out.ToolHash = append([]byte(nil), b...)
			seenHash = true
		default:
			if err := r.Skip(); err != nil {
				return Invoke{}, err
			}
		}
	}
	if !seenV || !seenArgs || !seenMethod || !seenHash {
		return Invoke{}, ErrBadFrame
	}
	return out, nil
}
