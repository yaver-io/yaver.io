import nacl from "tweetnacl";

/**
 * TweetNaCl discovers randomness at module-evaluation time. Expo Router may
 * evaluate a route that imports TweetNaCl before the root layout's polyfills,
 * leaving TweetNaCl permanently wired to its `no PRNG` stub for that process.
 *
 * Install the PRNG explicitly from Expo Crypto instead. This is backed by
 * SecRandomCopyBytes on Apple platforms and SecureRandom on Android. There is
 * no insecure fallback: if the platform CSPRNG is unavailable, credential
 * handoff fails closed.
 */
nacl.setPRNG((target, length) => {
  const random = new Uint8Array(length);
  try {
    const platformCrypto = (globalThis as { crypto?: { getRandomValues?: (value: Uint8Array) => Uint8Array } }).crypto;
    if (typeof platformCrypto?.getRandomValues === "function") {
      platformCrypto.getRandomValues(random);
    } else {
      // Lazy loading keeps the shared protocol runnable in Node tests while
      // still reaching Expo's native CSPRNG if route discovery imported this
      // module before the root layout installed its global crypto facade.
      // eslint-disable-next-line @typescript-eslint/no-require-imports
      const expoCrypto = require("expo-crypto") as typeof import("expo-crypto");
      expoCrypto.getRandomValues(random);
    }
    target.set(random);
  } catch {
    throw new Error("Secure encryption is unavailable in this Yaver build. Update the app and try again.");
  } finally {
    random.fill(0);
  }
});

export default nacl;
