# Key recovery

Default recovery is user-controlled. Generate a random 256-bit recovery secret
on a trusted endpoint and present it as a printable code or reviewed word
encoding. Derive a wrapping key locally and upload only authenticated encrypted
account-key material plus non-secret KDF parameters.

Yaver never stores the recovery secret, a server-decryptable copy, or a reset
path that silently replaces trusted device keys. Losing every authorized device
and the recovery secret means historical ciphertext is unrecoverable; the UI
must state this before setup completes.

Recovery authorizes a new device identity but does not reuse old session keys.
After recovery, revoke lost devices and establish fresh sessions. Recovery
events may be logged as opaque account/device IDs and timestamps only.

## Tenant-owned Hetzner credential

The mobile Hetzner up/down feature stores the token under the stable native
credential service `works.yaver.credentials.v1`. Normal application and OS
updates retain it. iOS Keychain commonly retains it across reinstall with the
same bundle identity, but this is not the only recovery mechanism. Android
uninstall removes the Keystore-backed value.

For reinstall/reset recovery, the phone can create a versioned NaCl secretbox
backup. It generates a fresh 256-bit recovery key, encrypts and authenticates
the token locally, and exposes the ciphertext and recovery key separately. The
app never uploads either value to Yaver. Restore decrypts locally, validates the
token directly against Hetzner, then writes it back to native secure storage.
Anyone holding both values can recover the token, so the UI requires the user
to keep them in separate locations.
