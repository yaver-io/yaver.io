// Dictation.swift — get a transcript string from the wrist.
//
// IMPORTANT (docs/yaver-smartwatch-voice-terminal.md §8): the watch mic is the
// WORST place to do STT. In the DEFAULT phone-paired mode, prefer letting the
// PHONE transcribe (better mic models, no watch battery hit) — or use paired
// AirPods. This file is the fallback for when the watch itself must capture
// speech: it uses WatchKit's built-in text-input controller, which already wraps
// Apple's dictation UI (and Scribble) and returns a finished string. We do NOT
// run whisper.rn on the watch — too heavy.
//
// presentTextInputController is the simplest, most battery-friendly path and is
// the one Apple actually wants watch apps to use for free-form input. A raw
// SFSpeechRecognizer streaming path is intentionally avoided here for the
// scaffold; if a future build wants continuous recognition it would live behind
// this same `dictate()` async facade so callers don't change.

import Foundation
#if canImport(WatchKit)
import WatchKit
#endif

enum Dictation {
    /// Present the watch dictation UI and resolve with the spoken text, or nil
    /// if the user cancelled / said nothing. Never throws — a cancelled
    /// dictation is a normal outcome, not an error.
    @MainActor
    static func dictate() async -> String? {
        #if canImport(WatchKit)
        await withCheckedContinuation { (continuation: CheckedContinuation<String?, Never>) in
            guard let controller = currentController else {
                continuation.resume(returning: nil)
                return
            }
            // .plain suggestions list is empty: we want free-form voice, with the
            // dictation/Scribble affordances WatchKit provides for free.
            controller.presentTextInputController(withSuggestions: nil,
                                                  allowedInputMode: .plain) { results in
                let text = (results?.first as? String)?
                    .trimmingCharacters(in: .whitespacesAndNewlines)
                continuation.resume(returning: (text?.isEmpty == false) ? text : nil)
            }
        }
        #else
        return nil
        #endif
    }

    #if canImport(WatchKit)
    /// The root interface controller to present the input UI from. On modern
    /// SwiftUI watch apps there may be no WKInterfaceController; in that case a
    /// SwiftUI `TextFieldLink` / `.textInputAutocapitalization` button is the
    /// supported entry point and the View layer wires it directly. This helper
    /// covers the WatchKit-controller path for completeness.
    private static var currentController: WKInterfaceController? {
        WKExtension.shared().rootInterfaceController
    }
    #endif
}
