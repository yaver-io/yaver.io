# Yaver zero-knowledge threat model

Status: migration target. Code and adversarial tests are authoritative.

## Promise

Yaver-operated infrastructure routes ciphertext and account/routing metadata.
It must not possess keys capable of decrypting session content or user-owned
provider credentials. Authorized endpoints and the external service selected by
the user are the only plaintext processors.

## Trusted endpoints

- Signed iOS, Android, macOS, Windows and Linux clients.
- Yaver CLI/local daemon and user-controlled execution agents.
- A user-controlled provisioner, including a native phone acting directly.

The browser is a weaker endpoint because the serving origin can change its
JavaScript. Do not give the web surface a stronger trust label than signed
native clients.

## Untrusted infrastructure

- Cloudflare Workers, Durable Objects, Queues, D1, KV, R2 and logs.
- Convex data and functions for application content.
- Relay Free and its operators.
- Analytics, crash reporting and support exports.

These systems may observe opaque account/device/session IDs, public keys,
presence, timestamps, sizes, IP addresses, billing and pairing metadata.

## Protected content

Prompts, model responses, terminal data, tools and arguments/results, source,
filenames, files, screenshots, voice transcripts, clipboard, environment,
provider/Git/SSH credentials and session keys must be encrypted before crossing
an endpoint boundary.

## Adversaries and expected result

| Threat | Expected property |
|---|---|
| Database/log/storage disclosure | Metadata and ciphertext only |
| Malicious relay/Worker | May drop, delay, reorder or replay; cannot decrypt or forge |
| Server key substitution | Rejected because peers trust endpoint-signed keys, not bare directory keys |
| Cross-tenant routing attempt | Rejected before forwarding and again by E2EE authentication |
| Endpoint compromise | Plaintext on that endpoint is compromised; E2EE cannot prevent this |
| Remote VM/root compromise | Plaintext processed by that VM may be compromised |

## Current blockers

The product must not claim full zero knowledge until all sensitive `/d/...`
HTTP relay traffic uses application E2EE or WireGuard mesh, plaintext diagnostic
and feedback fields are removed/encrypted, artifact upload is client-encrypted,
and every shipped surface verifies endpoint-authorized device membership.

