// Side-effect-only module: prepare the global crypto surface so that
// libraries which check `self.crypto.getRandomValues` at import time
// (most notably tweetnacl) actually find it.
//
// Why this exists:
//   Tweetnacl runs an IIFE at the bottom of nacl.js that does
//     var crypto = typeof self !== 'undefined'
//       ? (self.crypto || self.msCrypto)
//       : null;
//     if (crypto && crypto.getRandomValues) { nacl.setPRNG(...) }
//   In Hermes / React Native the global `self` is undefined, so the
//   check returns null and tweetnacl falls through to the Node-only
//   `require('crypto')` branch which is also unavailable. End result:
//   no PRNG is ever set, and every later call to nacl.box.keyPair()
//   throws `Error('no PRNG')` — which is exactly what the device-
//   attention banner kept surfacing as the recovery failure subtitle
//   ("no PRNG") on every reclaim/re-auth attempt today.
//
// The fix is two steps, both required in this exact order:
//   1) `globalThis.self = globalThis` so the `typeof self !==
//      'undefined'` check in tweetnacl evaluates true.
//   2) Load `react-native-get-random-values`, which mutates
//      `globalThis.crypto.getRandomValues` to call SecRandomCopyBytes
//      (iOS) / SecureRandom (Android) via a native module.
// After both run, tweetnacl's IIFE — which fires when nacl is first
// imported transitively (DeviceContext -> encryptedPair -> tweetnacl)
// — sees `self.crypto.getRandomValues`, sets up its PRNG against it,
// and `nacl.box.keyPair()` works for every subsequent caller.
//
// THIS FILE MUST BE THE FIRST IMPORT in mobile/app/_layout.tsx, ahead
// of `react-native-get-random-values` and ahead of any context that
// transitively pulls in tweetnacl.

// Step 1 — bridge self to globalThis. Wrapped in `typeof` so we don't
// shadow a real `self` on platforms (web) where it already exists.
if (typeof (globalThis as any).self === "undefined") {
  (globalThis as any).self = globalThis;
}

// Step 2 — actually polyfill crypto.getRandomValues via the
// react-native-get-random-values module's native bridge.
// Side-effect import; nothing to consume here.
// eslint-disable-next-line @typescript-eslint/no-require-imports
require("react-native-get-random-values");

export {};
