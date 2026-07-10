// Speech.swift — the wrist's OTHER output channel besides haptics.
//
// AVSpeechSynthesizer is available on watchOS (VERIFIED against the watchOS
// 10.0 SDK — docs/yaver-smartwatch-voice-terminal.md §1.4). Until now it was
// unused: the watch showed ONE line of text + a haptic, but stayed silent.
// Speaking the one-sentence summary out loud is the single highest-value
// addition on this surface (docs/yaver-watch-surface.md §6 / §8 build order #2):
// a wrist that answers aloud needs no screen at all.
//
// Mirrors Haptics.swift's shape so the reduce path in WatchStore can fire both
// side-by-side: Haptics.forReply(reply) + Speech.forReply(reply). The spoken
// line is exactly the `spoken` field the protocol already carries — no new
// agent contract, no new wire shape.
//
// Design choices:
//   • One shared synthesizer instance (cheap, avoids the audio session churn
//     of alloc/init per utterance).
//   • A new speak() INTERRUPTS any in-flight utterance — a stale sentence must
//     never block the latest reply from being heard.
//   • Rate is slightly below default (.45 vs .5) for clarity on a tiny speaker.
//   • Empty / whitespace-only lines are no-ops (the UI still shows the last
//     non-empty line; silence is better than a placeholder beep).
//   • No audio-session category juggling: watchOS defaults to the ambient
//     category, which is correct for a wrist that should respect silent mode.

import Foundation
#if canImport(AVFoundation)
import AVFoundation
#endif

enum Speech {
    #if canImport(AVFoundation)
    /// Shared synthesizer. AVSpeechSynthesizer is safe to hold long-term and
    /// reuse across utterances; allocating per-speak() adds latency + audio
    /// session overhead for no benefit on a 215MHz wrist.
    private static let synthesizer = AVSpeechSynthesizer()
    #endif

    /// Speak a sentence. Interrupts anything currently being spoken so the
    /// newest reply is always the one heard.
    static func speak(_ text: String) {
        let cleaned = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cleaned.isEmpty else { return }
        #if canImport(AVFoundation)
        // Stop any in-flight utterance BEFORE queuing the new one. Without this
        // a rapid ack→summary pair would queue two sentences and the user would
        // hear "On it. Done. Tests pass." back-to-back, which defeats the
        // glance-free goal.
        if synthesizer.isSpeaking {
            synthesizer.stopSpeaking(at: .immediate)
        }
        let utterance = AVSpeechUtterance(string: cleaned)
        utterance.rate = 0.45
        utterance.pitchMultiplier = 1.0
        utterance.volume = 1.0
        // Default voice is the system voice for the user's locale — no need to
        // pick one explicitly and risk a missing-locale fallback.
        synthesizer.speak(utterance)
        #endif
    }

    /// Stop any in-flight speech (e.g. the user tapped Cancel, or a handoff
    /// means the phone will take over).
    static func stop() {
        #if canImport(AVFoundation)
        synthesizer.stopSpeaking(at: .immediate)
        #endif
    }

    /// Pick the right line to speak for a protocol reply, mirroring
    /// Haptics.forReply. Same decision shape: speak the `spoken` field when
    /// present, fall back to a per-kind default, and stay silent for `working`
    /// (a background task shouldn't talk over the user's next command).
    static func forReply(_ reply: WatchReply) {
        switch reply.kind {
        case .ack:
            speak(reply.spoken ?? "On it.")
        case .working:
            // "Working…" is a transient state; speaking it would talk over
            // the user. The haptic (.start) is enough. The terminal summary
            // is what gets spoken.
            break
        case .confirmNeeded:
            // Speak the PROMPT (the question), not a generic "confirm?".
            speak(reply.prompt ?? "Confirm?")
        case .summary:
            speak(reply.spoken ?? "Done.")
        case .error:
            speak(reply.spoken ?? "Something went wrong.")
        case .handoff:
            speak(reply.spoken ?? "Sent it to your phone.")
        }
    }
}
