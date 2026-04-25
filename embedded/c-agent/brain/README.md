# brain — Go-side wire codec

Companion Go module to the c-agent C runtime. Mirrors the wire
layer (frame header + CBOR + body schemas) so the cloud brain —
which is Go-shaped, like the existing `desktop/agent/` and
`relay/` services — can talk to a c-agent device using
byte-identical encodings.

> **Status.** Pair-of c-agent's C codec. Byte-for-byte parity is
> the contract; both sides are tested against the same vectors.
> This module is a thin codec, not a full brain — the LLM layer,
> retrieval, build farm, etc. live elsewhere.

## What's in scope

| File | Purpose |
|---|---|
| `frame.go` | 9-byte HTTP/2-style frame header encode + decode |
| `cbor.go` | CBOR (RFC 8949) deterministic-CTAP2 subset, mirrors `core/src/cbor.c` |
| `body.go` | Phase-0 frame body codecs (HELLO, HEARTBEAT, INVOKE so far) |

## Cross-language parity

Every encoder in this module is tested against byte vectors that
the C codec also produces, so a regression in either side is
caught at CI.

```bash
go test ./...
```

## What's NOT in scope (yet)

- TLS / Noise crypto (lives in the brain service, not this codec)
- Module signing / signature verify (lives in the build farm)
- LLM / retrieval / brain orchestration (lives in iot-brain/)
- All Phase-0 body types — currently HELLO, HEARTBEAT, INVOKE.
  Adding the rest (AUTH, AUTHRSP, ATTEST, ERROR, TOOL_RSP,
  STREAM_CHUNK, NEED, MODULE) is a follow-up slice.

## Layout

```
embedded/c-agent/brain/
├── go.mod
├── frame.go
├── frame_test.go
├── cbor.go
├── cbor_test.go
├── body.go
└── body_test.go
```
