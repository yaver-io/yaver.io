# Yaver — genuine-app attestation (design)

Status: design (2026-07-11). Answers "how do we authenticate the *genuine Yaver
app* — not a fork or malware — when the entire codebase is open source, and
without shipping a private key inside the app?"

## 1. The fundamental theorem

**In a fully open-source system, no secret embedded in the software can prove
the software is genuine.** A private key in the repo, or baked into the
distributed binary, is extractable — from source directly or by reversing the
binary. So "sign a credential with a GitHub-secrets key, ship it in the app,
verify with the public key" is **not** a runtime authenticity control: an
attacker reads the code, extracts/re-derives the credential, and speaks the
protocol perfectly.

Two questions get conflated; separate them:

| Question | Open-source answerable? | Mechanism |
|---|---|---|
| Is the client a **device/user I trust**? | ✅ | Per-device keypair, private key **generated on-device, in the keychain, never in source** (the ed25519 device-signature — `docs/yaver-relay-asymmetric-auth.md`). |
| Is the client the **genuine Yaver app**? | ⚠️ only via hardware | **App Attest** (iOS) / **Play Integrity** (Android) — proof comes from device hardware + the store identity, neither of which is in the repo. |

This doc is about the second question.

## 2. What GitHub-secrets signing is (and isn't) for

Keep it — for **distribution integrity**: it code-signs the release (Go agent =
Developer ID + notarization; app = App Store / Play signing), and the **OS**
refuses to run a tampered binary. It does **not** let the agent verify at
runtime that a peer is running that signed binary — the peer just sends bytes
and can claim anything. Do not repurpose it for runtime auth.

## 3. iOS — App Attest

The Secure Enclave holds a key the app can never read. Flow:

1. App generates an App Attest key (`DCAppAttestService.generateKey`).
2. App attests it against a server nonce (`attestKey`) → Apple co-signs an
   assertion that this is bundle `io.yaver.mobile`, unmodified, on a genuine,
   non-jailbroken device.
3. Backend verifies the attestation object against Apple's root + the expected
   app id + a fresh challenge; stores the attested public key.
4. Subsequent requests carry an **assertion** (`generateAssertion`) over a
   server challenge; backend verifies with the stored key + checks the counter.

The app holds **no long-lived secret** — the private key lives in the Secure
Enclave and is non-exportable.

## 4. Android — Play Integrity

1. App requests an integrity token over a server nonce
   (`IntegrityManager.requestIntegrityToken`).
2. Backend decrypts/verifies the token with Google → a verdict:
   `appRecognitionVerdict` (matches the Play-signed Yaver APK),
   `deviceRecognitionVerdict` (genuine device), `appLicensingVerdict`.
3. Accept only `PLAY_RECOGNIZED` app + `MEETS_DEVICE_INTEGRITY` (policy-tunable).

Same property: no app-managed secret; the trust root is Google + your Play
listing identity.

## 5. The architecture — attestation gates TOKEN ISSUANCE

Attestation is not sent to the agent on every call. It gates the **mint** of a
Yaver session/relay credential:

```
app → attest (App Attest / Play Integrity) → Yaver backend verifies with
      Apple/Google + your store identity → issues a short-lived session token
      (and/or registers the app's device signing key) → agent/relay trust the
      token / signature, which trace back to a genuine-app attestation.
```

A malicious re-implementation can't attest as `io.yaver.mobile`, so it never
obtains a token, so it never reaches the agent as an authorized caller. Compose
with the device-signature layer: attestation proves *genuine app*, the device
key proves *this device*, the token proves *this user*.

## 6. The honest limits (do not paper over)

1. **Rooted/jailbroken devices bypass attestation.** The verdicts *report*
   device integrity, so refuse or downgrade degraded devices — but a determined
   attacker on their own rooted device with their own credentials is inside the
   boundary. Model it that way.
2. **You cannot and should not stop the USER from using their own client.** An
   open-source dev tool must let a user drive *their own* agent with *their own*
   credentials from a client they built. Attestation gates the *official app's*
   privileged issuance path; it is not a wall against the user themselves.
3. **The real boundary is USER + CONSENT, not brand.** Design the agent to be
   safe when driven by *any* client: authenticated identity + per-action consent
   on dangerous ops (the confirm gates) + capability scoping. Then "non-Yaver
   software attacking the agent" is just "an unauthorized request" — already
   refused. Attestation and device-signatures *raise the bar* for obtaining
   authorization; consent gates bound what an authorized-but-misused session can
   do.

## 7. Where this leaves the Go agent

The agent's protection against "non-Yaver software" is layered, none of it a
brand secret:
- **Auth** — ed25519 device signature / token: random malware with no registered
  device key and no valid token gets `401`. (Shipped: relay-side + agent.)
- **Attestation-gated issuance** — a malicious app on the phone can't get a
  Yaver token even with the user's OAuth, because it can't attest as the genuine
  app. (This doc.)
- **Token in the OS keychain** — app-sandboxed, so a malicious app can't steal
  the genuine app's token.
- **Consent gates** — destructive ops need explicit approval regardless of
  caller.

## 8. Implementation plan

1. **Backend**: `POST /attest/challenge` (nonce) + `POST /attest/verify` for
   iOS (App Attest object/assertion, verify vs Apple root) and Android (Play
   Integrity token, verify vs Google). On success, register the app's device
   signing key and/or mint a short-lived session token. Store the App Attest key
   + counter per install.
2. **Mobile iOS**: `DCAppAttestService` generate/attest/assert; send with the
   device sign-key registration.
3. **Mobile Android**: Play Integrity request; send verdict.
4. **Policy**: env-tunable minimum verdict (allow debug/dev builds a bypass flag
   ONLY in non-prod). Refuse/downgrade non-integral devices per your risk
   appetite.
5. **Compose**: the attested app registers its device ed25519 signing key
   (`docs/yaver-relay-asymmetric-auth.md`) — attestation proves genuineness,
   the signature proves per-request device identity.

## 9. Non-goals

- Proving an arbitrary desktop/CLI client is "genuine Yaver" — impossible
  open-source and unnecessary (the user owns the box). Desktop/CLI trust rests
  on the device key + the user's login.
- DRM / anti-fork. The goal is to protect the *user* from impersonation of the
  official app, not to prevent forks.
</content>
