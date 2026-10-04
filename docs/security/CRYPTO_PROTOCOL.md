# Yaver endpoint crypto protocol v1

Status: endpoint foundation implemented in `desktop/agent/e2ee`. The relay must
not import that package.

## Suite

- Ed25519: long-term device identity signatures.
- X25519: fresh ephemeral session agreement.
- HKDF-SHA256: transcript-bound, purpose-separated key derivation.
- XChaCha20-Poly1305: authenticated application envelopes.

Implementations must use maintained platform/library primitives. No custom
cipher, MAC, KDF or timestamp-derived nonce is permitted.

## Authenticated handshake

Each endpoint creates a fresh X25519 key and signs a versioned hello containing
the opaque session ID, opaque device ID, ephemeral public key, issue time and
expiry. A peer verifies this signature against a key authorized out-of-band by
an existing trusted endpoint. A public key returned by the server alone is not
trusted.

The X25519 secret and ordered client/agent hello transcript feed HKDF. Separate
keys are derived for client-to-agent, agent-to-client, file, control and rekey
purposes. Session keys are memory-only and expire with the session.

## Envelope

Cleartext routing metadata is limited to version, opaque session ID, opaque
sender/destination IDs, opaque packet ID/sequence and ciphertext length. The
version/session/sender/sequence tuple is authenticated as AEAD associated data.
Message type and every semantic field remain inside ciphertext.

Receivers authenticate before advancing a 64-packet replay window. Duplicate,
stale, reordered-outside-window, modified or incorrectly bound envelopes fail.

## Rekey

Rekey at device membership changes, worker replacement, manual request, 24
hours or 1 GiB, whichever comes first. V1 uses explicit authenticated ephemeral
handshakes. A future ratchet must use a reviewed standard such as MLS/Noise or
the Signal Double Ratchet; do not invent one.

