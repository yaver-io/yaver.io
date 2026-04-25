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

// ── AUTH ──────────────────────────────────────────────────────

// Auth is brain → device challenge.
type Auth struct {
	ProtocolVersion uint32
	Nonce           []byte
	SignedNowMs     uint64
}

func (a Auth) Encode(buf []byte) (int, error) {
	if len(a.Nonce) == 0 {
		return 0, ErrInvalidArg
	}
	w := NewCBORWriter(buf)
	w.BeginMap(3)
	// CTAP2 order: v < nonce < signed_now_ms.
	w.WriteText("v")
	w.WriteUint(uint64(a.ProtocolVersion))
	w.WriteText("nonce")
	w.WriteBytes(a.Nonce)
	w.WriteText("signed_now_ms")
	w.WriteUint(a.SignedNowMs)
	if w.Err != nil {
		return 0, w.Err
	}
	return w.Len(), nil
}

func DecodeAuth(buf []byte) (Auth, error) {
	r := NewCBORReader(buf)
	kv, err := r.ReadMapBegin()
	if err != nil {
		return Auth{}, err
	}
	var out Auth
	var seenV, seenNonce, seenNow bool
	for i := 0; i < kv; i++ {
		key, err := r.ReadText()
		if err != nil {
			return Auth{}, err
		}
		switch key {
		case "v":
			v, err := r.ReadUint()
			if err != nil {
				return Auth{}, err
			}
			if v > 0xFFFFFFFF {
				return Auth{}, ErrBadFrame
			}
			out.ProtocolVersion = uint32(v)
			seenV = true
		case "nonce":
			b, err := r.ReadBytes()
			if err != nil {
				return Auth{}, err
			}
			out.Nonce = append([]byte(nil), b...)
			seenNonce = true
		case "signed_now_ms":
			v, err := r.ReadUint()
			if err != nil {
				return Auth{}, err
			}
			out.SignedNowMs = v
			seenNow = true
		default:
			if err := r.Skip(); err != nil {
				return Auth{}, err
			}
		}
	}
	if !seenV || !seenNonce || !seenNow {
		return Auth{}, ErrBadFrame
	}
	return out, nil
}

// ── AUTHRSP ───────────────────────────────────────────────────

// AuthRsp is device → brain response to AUTH.
type AuthRsp struct {
	ProtocolVersion uint32
	Sig             []byte
	Nonce           []byte
	DeviceCert      []byte
}

func (a AuthRsp) Encode(buf []byte) (int, error) {
	if len(a.Sig) == 0 || len(a.Nonce) == 0 || len(a.DeviceCert) == 0 {
		return 0, ErrInvalidArg
	}
	w := NewCBORWriter(buf)
	w.BeginMap(4)
	// CTAP2 order: v < sig < nonce < device_cert.
	w.WriteText("v")
	w.WriteUint(uint64(a.ProtocolVersion))
	w.WriteText("sig")
	w.WriteBytes(a.Sig)
	w.WriteText("nonce")
	w.WriteBytes(a.Nonce)
	w.WriteText("device_cert")
	w.WriteBytes(a.DeviceCert)
	if w.Err != nil {
		return 0, w.Err
	}
	return w.Len(), nil
}

func DecodeAuthRsp(buf []byte) (AuthRsp, error) {
	r := NewCBORReader(buf)
	kv, err := r.ReadMapBegin()
	if err != nil {
		return AuthRsp{}, err
	}
	var out AuthRsp
	var sV, sSig, sNonce, sCert bool
	for i := 0; i < kv; i++ {
		key, err := r.ReadText()
		if err != nil {
			return AuthRsp{}, err
		}
		switch key {
		case "v":
			v, err := r.ReadUint()
			if err != nil {
				return AuthRsp{}, err
			}
			if v > 0xFFFFFFFF {
				return AuthRsp{}, ErrBadFrame
			}
			out.ProtocolVersion = uint32(v)
			sV = true
		case "sig":
			b, err := r.ReadBytes()
			if err != nil {
				return AuthRsp{}, err
			}
			out.Sig = append([]byte(nil), b...)
			sSig = true
		case "nonce":
			b, err := r.ReadBytes()
			if err != nil {
				return AuthRsp{}, err
			}
			out.Nonce = append([]byte(nil), b...)
			sNonce = true
		case "device_cert":
			b, err := r.ReadBytes()
			if err != nil {
				return AuthRsp{}, err
			}
			out.DeviceCert = append([]byte(nil), b...)
			sCert = true
		default:
			if err := r.Skip(); err != nil {
				return AuthRsp{}, err
			}
		}
	}
	if !sV || !sSig || !sNonce || !sCert {
		return AuthRsp{}, ErrBadFrame
	}
	return out, nil
}

// ── ToolRsp ───────────────────────────────────────────────────

// ToolRsp is device → brain: result of an INVOKE.
type ToolRsp struct {
	ProtocolVersion uint32
	Error           string
	Result          []byte
	Status          int32
	ToolHash        []byte
	DurationMs      uint32
}

func (r ToolRsp) Encode(buf []byte) (int, error) {
	if len(r.ToolHash) == 0 {
		return 0, ErrInvalidArg
	}
	w := NewCBORWriter(buf)
	hasErr := r.Error != ""
	hasDur := r.DurationMs != 0
	mapN := 4
	if hasErr {
		mapN++
	}
	if hasDur {
		mapN++
	}
	w.BeginMap(mapN)
	// CTAP2 order: v < error < result < status < tool_hash < duration_ms.
	w.WriteText("v")
	w.WriteUint(uint64(r.ProtocolVersion))
	if hasErr {
		w.WriteText("error")
		w.WriteText(r.Error)
	}
	w.WriteText("result")
	w.WriteBytes(r.Result)
	w.WriteText("status")
	w.WriteInt(int64(r.Status))
	w.WriteText("tool_hash")
	w.WriteBytes(r.ToolHash)
	if hasDur {
		w.WriteText("duration_ms")
		w.WriteUint(uint64(r.DurationMs))
	}
	if w.Err != nil {
		return 0, w.Err
	}
	return w.Len(), nil
}

func DecodeToolRsp(buf []byte) (ToolRsp, error) {
	rd := NewCBORReader(buf)
	kv, err := rd.ReadMapBegin()
	if err != nil {
		return ToolRsp{}, err
	}
	var out ToolRsp
	var sV, sResult, sStatus, sHash bool
	for i := 0; i < kv; i++ {
		key, err := rd.ReadText()
		if err != nil {
			return ToolRsp{}, err
		}
		switch key {
		case "v":
			v, err := rd.ReadUint()
			if err != nil {
				return ToolRsp{}, err
			}
			if v > 0xFFFFFFFF {
				return ToolRsp{}, ErrBadFrame
			}
			out.ProtocolVersion = uint32(v)
			sV = true
		case "error":
			s, err := rd.ReadText()
			if err != nil {
				return ToolRsp{}, err
			}
			out.Error = s
		case "result":
			b, err := rd.ReadBytes()
			if err != nil {
				return ToolRsp{}, err
			}
			out.Result = append([]byte(nil), b...)
			sResult = true
		case "status":
			v, err := rd.ReadInt()
			if err != nil {
				return ToolRsp{}, err
			}
			if v < -2147483648 || v > 2147483647 {
				return ToolRsp{}, ErrBadFrame
			}
			out.Status = int32(v)
			sStatus = true
		case "tool_hash":
			b, err := rd.ReadBytes()
			if err != nil {
				return ToolRsp{}, err
			}
			out.ToolHash = append([]byte(nil), b...)
			sHash = true
		case "duration_ms":
			v, err := rd.ReadUint()
			if err != nil {
				return ToolRsp{}, err
			}
			if v > 0xFFFFFFFF {
				return ToolRsp{}, ErrBadFrame
			}
			out.DurationMs = uint32(v)
		default:
			if err := rd.Skip(); err != nil {
				return ToolRsp{}, err
			}
		}
	}
	if !sV || !sResult || !sStatus || !sHash {
		return ToolRsp{}, ErrBadFrame
	}
	return out, nil
}

// ── StreamChunk ───────────────────────────────────────────────

// StreamChunk is device → brain: one chunk of a long-running probe.
type StreamChunk struct {
	ProtocolVersion uint32
	Seq             uint32
	Data            []byte
	StreamID        uint32
	EndStream       bool
}

func (c StreamChunk) Encode(buf []byte) (int, error) {
	w := NewCBORWriter(buf)
	w.BeginMap(5)
	// CTAP2 order: v < seq < data < stream_id < end_stream.
	w.WriteText("v")
	w.WriteUint(uint64(c.ProtocolVersion))
	w.WriteText("seq")
	w.WriteUint(uint64(c.Seq))
	w.WriteText("data")
	w.WriteBytes(c.Data)
	w.WriteText("stream_id")
	w.WriteUint(uint64(c.StreamID))
	w.WriteText("end_stream")
	w.WriteBool(c.EndStream)
	if w.Err != nil {
		return 0, w.Err
	}
	return w.Len(), nil
}

func DecodeStreamChunk(buf []byte) (StreamChunk, error) {
	r := NewCBORReader(buf)
	kv, err := r.ReadMapBegin()
	if err != nil {
		return StreamChunk{}, err
	}
	var out StreamChunk
	var sV, sSeq, sData, sSID, sEnd bool
	for i := 0; i < kv; i++ {
		key, err := r.ReadText()
		if err != nil {
			return StreamChunk{}, err
		}
		switch key {
		case "v":
			v, err := r.ReadUint()
			if err != nil {
				return StreamChunk{}, err
			}
			if v > 0xFFFFFFFF {
				return StreamChunk{}, ErrBadFrame
			}
			out.ProtocolVersion = uint32(v)
			sV = true
		case "seq":
			v, err := r.ReadUint()
			if err != nil {
				return StreamChunk{}, err
			}
			if v > 0xFFFFFFFF {
				return StreamChunk{}, ErrBadFrame
			}
			out.Seq = uint32(v)
			sSeq = true
		case "data":
			b, err := r.ReadBytes()
			if err != nil {
				return StreamChunk{}, err
			}
			out.Data = append([]byte(nil), b...)
			sData = true
		case "stream_id":
			v, err := r.ReadUint()
			if err != nil {
				return StreamChunk{}, err
			}
			if v > 0xFFFFFFFF {
				return StreamChunk{}, ErrBadFrame
			}
			out.StreamID = uint32(v)
			sSID = true
		case "end_stream":
			b, err := r.ReadBool()
			if err != nil {
				return StreamChunk{}, err
			}
			out.EndStream = b
			sEnd = true
		default:
			if err := r.Skip(); err != nil {
				return StreamChunk{}, err
			}
		}
	}
	if !sV || !sSeq || !sData || !sSID || !sEnd {
		return StreamChunk{}, ErrBadFrame
	}
	return out, nil
}

// ── Need ──────────────────────────────────────────────────────

// Need is device → brain: cache miss for a module hash.
type Need struct {
	ProtocolVersion uint32
	ToolHash        []byte
}

func (n Need) Encode(buf []byte) (int, error) {
	if len(n.ToolHash) == 0 {
		return 0, ErrInvalidArg
	}
	w := NewCBORWriter(buf)
	w.BeginMap(2)
	w.WriteText("v")
	w.WriteUint(uint64(n.ProtocolVersion))
	w.WriteText("tool_hash")
	w.WriteBytes(n.ToolHash)
	if w.Err != nil {
		return 0, w.Err
	}
	return w.Len(), nil
}

func DecodeNeed(buf []byte) (Need, error) {
	r := NewCBORReader(buf)
	kv, err := r.ReadMapBegin()
	if err != nil {
		return Need{}, err
	}
	var out Need
	var sV, sHash bool
	for i := 0; i < kv; i++ {
		key, err := r.ReadText()
		if err != nil {
			return Need{}, err
		}
		switch key {
		case "v":
			v, err := r.ReadUint()
			if err != nil {
				return Need{}, err
			}
			if v > 0xFFFFFFFF {
				return Need{}, ErrBadFrame
			}
			out.ProtocolVersion = uint32(v)
			sV = true
		case "tool_hash":
			b, err := r.ReadBytes()
			if err != nil {
				return Need{}, err
			}
			out.ToolHash = append([]byte(nil), b...)
			sHash = true
		default:
			if err := r.Skip(); err != nil {
				return Need{}, err
			}
		}
	}
	if !sV || !sHash {
		return Need{}, ErrBadFrame
	}
	return out, nil
}

// ── ModuleBody ────────────────────────────────────────────────

// ModuleBody is brain → device: signed module shipment.
type ModuleBody struct {
	ProtocolVersion uint32
	Wasm            []byte
	Descriptor      []byte
}

func (m ModuleBody) Encode(buf []byte) (int, error) {
	if len(m.Wasm) == 0 || len(m.Descriptor) == 0 {
		return 0, ErrInvalidArg
	}
	w := NewCBORWriter(buf)
	w.BeginMap(3)
	// CTAP2 order: v < wasm < descriptor.
	w.WriteText("v")
	w.WriteUint(uint64(m.ProtocolVersion))
	w.WriteText("wasm")
	w.WriteBytes(m.Wasm)
	w.WriteText("descriptor")
	w.WriteBytes(m.Descriptor)
	if w.Err != nil {
		return 0, w.Err
	}
	return w.Len(), nil
}

func DecodeModuleBody(buf []byte) (ModuleBody, error) {
	r := NewCBORReader(buf)
	kv, err := r.ReadMapBegin()
	if err != nil {
		return ModuleBody{}, err
	}
	var out ModuleBody
	var sV, sWasm, sDesc bool
	for i := 0; i < kv; i++ {
		key, err := r.ReadText()
		if err != nil {
			return ModuleBody{}, err
		}
		switch key {
		case "v":
			v, err := r.ReadUint()
			if err != nil {
				return ModuleBody{}, err
			}
			if v > 0xFFFFFFFF {
				return ModuleBody{}, ErrBadFrame
			}
			out.ProtocolVersion = uint32(v)
			sV = true
		case "wasm":
			b, err := r.ReadBytes()
			if err != nil {
				return ModuleBody{}, err
			}
			out.Wasm = append([]byte(nil), b...)
			sWasm = true
		case "descriptor":
			b, err := r.ReadBytes()
			if err != nil {
				return ModuleBody{}, err
			}
			out.Descriptor = append([]byte(nil), b...)
			sDesc = true
		default:
			if err := r.Skip(); err != nil {
				return ModuleBody{}, err
			}
		}
	}
	if !sV || !sWasm || !sDesc {
		return ModuleBody{}, ErrBadFrame
	}
	return out, nil
}
