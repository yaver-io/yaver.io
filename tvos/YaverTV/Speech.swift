// Speech.swift — TV's voice output channel.
//
// AVSpeechSynthesizer is available on tvOS (VERIFIED — docs/yaver-tvos-surface.md
// §1.3) and was unused. Speaking the session summary aloud turns the TV into a
// genuine "lean-back" coding surface: send a prompt from the Siri Remote, hear
// the result spoken back without reading a wall of text.
//
// Mirrors watch/YaverWatch/Speech.swift's shape. One difference: the TV has
// room to render the pane visually, so TTS is complementary (speak a summary
// while showing the full pane), not the sole output channel.
//
// No audio-session category juggling: tvOS defaults are correct for a living-
// room device that should respect the system volume.

import Foundation
#if canImport(AVFoundation)
import AVFoundation
#endif

enum Speech {
    #if canImport(AVFoundation)
    private static let synthesizer = AVSpeechSynthesizer()
    #endif

    /// Speak a sentence. Interrupts anything currently being spoken.
    static func speak(_ text: String) {
        let cleaned = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cleaned.isEmpty else { return }
        #if canImport(AVFoundation)
        if synthesizer.isSpeaking {
            synthesizer.stopSpeaking(at: .immediate)
        }
        let utterance = AVSpeechUtterance(string: cleaned)
        utterance.rate = 0.45
        utterance.volume = 1.0
        synthesizer.speak(utterance)
        #endif
    }

    /// Stop any in-flight speech.
    static func stop() {
        #if canImport(AVFoundation)
        synthesizer.stopSpeaking(at: .immediate)
        #endif
    }

    /// Speak a one-sentence summary of a pane tail. Mirrors
    /// watch_risk.go::summarizeForWatch / watchFirstStatusClause: first clean
    /// non-code line, clamped to 120 chars. The TV shows the full pane; this
    /// is the spoken companion.
    static func speakSummary(of pane: String) {
        speak(summarize(pane))
    }

    // MARK: - Pane summarization (mirrors watch_risk.go::watchFirstStatusClause)

    private static let codePattern = try! NSRegularExpression(
        pattern: #"[{}<>;=]|```|\b(function|const|class|def|import|return)\b|/\w+/"#
    )
    private static let sentencePattern = try! NSRegularExpression(
        pattern: #"^(.{1,120}?[.!?])(\s|$)"#
    )
    private static let markdownPattern = try! NSRegularExpression(
        pattern: "[#*`_~]"
    )

    static func summarize(_ pane: String) -> String {
        let lines = pane.split(separator: "\n", omittingEmptySubsequences: true)
        for line in lines {
            let l = String(line).trimmingCharacters(in: .whitespaces)
            if l.isEmpty { continue }
            let r = NSRange(l.startIndex..., in: l)
            if codePattern.firstMatch(in: l, range: r) == nil {
                return clampSentence(stripMarkdown(l))
            }
        }
        return "Done."
    }

    private static func clampSentence(_ s: String) -> String {
        let r = NSRange(s.startIndex..., in: s)
        if let m = sentencePattern.firstMatch(in: s, range: r),
           let sentenceRange = Range(m.range(at: 1), in: s) {
            let clause = String(s[sentenceRange])
            return clause.count <= 120 ? clause : String(clause.prefix(119)) + "…"
        }
        if s.count <= 120 { return s }
        return String(s.prefix(119)) + "…"
    }

    private static func stripMarkdown(_ s: String) -> String {
        let r = NSRange(s.startIndex..., in: s)
        return markdownPattern.stringByReplacingMatches(
            in: s, range: r, withTemplate: ""
        ).trimmingCharacters(in: .whitespaces)
    }
}
