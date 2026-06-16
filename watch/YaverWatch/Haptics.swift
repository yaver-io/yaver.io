// Haptics.swift — the wrist's other output channel. Voice in, ONE sentence + a
// HAPTIC out (docs/yaver-smartwatch-voice-terminal.md §0). The haptic is what
// lets the watch be glance-free: a tap on "On it", success on "Done", failure on
// "couldn't reach your box". Maps protocol reply kinds to WKHapticType.

import Foundation
#if canImport(WatchKit)
import WatchKit
#endif

enum Haptics {
    /// Dispatch accepted / "On it" — the early-ack tap.
    static func tap() { play(.click) }

    /// A long task started in the background.
    static func working() { play(.start) }

    /// Terminal success.
    static func success() { play(.success) }

    /// Terminal failure / unreachable / error.
    static func failure() { play(.failure) }

    /// A confirm-needed prompt arrived — the wrist should look.
    static func prompt() { play(.notification) }

    /// Pick the right haptic for a protocol reply so the UI layer doesn't have
    /// to know the WatchKit types. Mirrors the spoken-line decision.
    static func forReply(_ reply: WatchReply) {
        switch reply.kind {
        case .ack:
            tap()
        case .working:
            working()
        case .confirmNeeded:
            prompt()
        case .summary:
            // Success unless the task reported a non-completed terminal status.
            if let status = reply.status, status != "completed" {
                failure()
            } else {
                success()
            }
        case .error:
            failure()
        case .handoff:
            tap()
        }
    }

    #if canImport(WatchKit)
    private static func play(_ type: WKHapticType) {
        WKInterfaceDevice.current().play(type)
    }
    #else
    private static func play(_ type: Any) {}
    #endif
}
