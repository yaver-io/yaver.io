# Device pairing and authorization

The account service discovers devices; it does not establish cryptographic
trust between them.

Every installation owns an Ed25519 signing key and X25519 agreement identity.
Private material stays in Secure Enclave/Keychain, Android Keystore, Windows
Credential Manager, macOS Keychain, Linux Secret Service or an owner-only local
encrypted store. Only public keys may be registered centrally.

An existing trusted endpoint authorizes a new device using a short-lived QR or
local handoff containing public keys, an unpredictable nonce, expiry and an
ephemeral challenge. Both endpoints sign the resulting authorization. Display
a safety fingerprint for optional manual comparison.

Session membership changes require an authorization signed by an already
trusted endpoint. Endpoints reject server-added keys that lack that chain. On
revocation the control plane stops routing, surviving endpoints rotate active
session keys, and the revoked endpoint cannot join future sessions.

Recovery uses a user-held recovery secret that encrypts account authorization
material client-side. The recovery secret is never uploaded or emailed.

