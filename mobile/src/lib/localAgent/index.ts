// localAgent — deterministic core for the on-device voice helper.
//
// This barrel re-exports the PURE, RN-free logic layer that sits between
// local STT and real actions:
//   - resolver: free-form/spoken device reference → concrete device (tie-safe)
//   - catalog:  action catalog + safety tiers (auto / confirm / blocked)
//   - tiers:    model-tier selection + the in-app model picker
//
// The native model runtime (llama.rn), the GitHub-Releases model download,
// and the STT/TTS wiring live in separate adapter modules that consume this
// core. Keeping the decision logic pure means it's unit-tested and the
// security policy (what may auto-run vs needs confirmation) is auditable in
// one place. See SPIKE-local-voice-helper-and-nicknames-2026-06-01.md.

export * from "./resolver";
export * from "./catalog";
export * from "./tiers";
export * from "./brain";
export * from "./connectivity";
