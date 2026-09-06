// YouTubeAnimation.swift — the couch's "something is happening" visual language.
//
// YouTube-style animated equalizer + mic ripple for the tvOS task/vibe surfaces:
// a task is running (bars dance in the task list + live console), the Siri
// Remote mic is ready to dictate (pulsing mic ripple on a focused prompt), a
// vibe turn is in flight (equalizer while sending/working).
//
// Design notes:
// - The bars are driven by TimelineView(.animation(minimumInterval:)) from
//   ABSOLUTE time, so every bar has its own phase and the shape is
//   deterministic across re-renders — no @State flip churn, no
//   repeatForever-delay phase quirks. `paused:` (tvOS 17) freezes the timer
//   when a surface is idle so a long "Done" list doesn't burn GPU.
// - The ripple uses the classic repeatForever ring: two stroked circles scale
//   out and fade, offset by a delay, so it reads as "listening" (YouTube
//   voice-search style) rather than a spinner.
// - Determinism is deliberate: only the bars that mean LIVE work animate —
//   completed/failed tasks render a plain dot, never a dancing equalizer.

import SwiftUI

// MARK: - Equalizer bars (YouTube activity indicator)

struct EqualizerBars: View {
    var barCount: Int = 5
    var color: Color = .blue
    var active: Bool = true

    var body: some View {
        TimelineView(.animation(minimumInterval: 1.0 / 20.0, paused: !active)) { context in
            let t = context.date.timeIntervalSinceReferenceDate
            HStack(alignment: .bottom, spacing: 3) {
                ForEach(0..<barCount, id: \.self) { i in
                    Capsule()
                        .fill(color)
                        .frame(width: 3)
                        .frame(maxHeight: .infinity)
                        .scaleEffect(y: height(at: t, bar: i), anchor: .bottom)
                }
            }
            .animation(active ? .easeInOut(duration: 0.16) : nil, value: t)
        }
        .frame(height: 16)
        .accessibilityHidden(true)
    }

    /// Deterministic per-bar height in (0.2…1.0]. Sum of two sines with an
    /// index-scrambled phase so neighbouring bars never move in lockstep —
    /// the "someone is working" equalizer, not a ruler.
    private func height(at t: TimeInterval, bar i: Int) -> CGFloat {
        guard active else { return 0.35 }
        let phase = t * 2.6 + Double(i) * 0.9
        let scramble = Double((i * 37) % 11) / 11.0
        let h = 0.35 + 0.35 * abs(sin(phase)) + 0.15 * abs(sin(phase * 1.7 + scramble))
        return CGFloat(min(max(h, 0.2), 1.0))
    }
}

// MARK: - Mic listening ripple (Siri Remote dictation ready)

/// A mic in a filled circle with two expanding, fading rings — the cue that
/// the prompt is focused and one press of the Siri Remote mic starts dictation.
struct MicListeningIndicator: View {
    var color: Color = .blue

    @State private var ripple = false

    var body: some View {
        ZStack {
            if ripple {
                Circle()
                    .stroke(color.opacity(0.35), lineWidth: 2)
                    .scaleEffect(ripple ? 1.6 : 1.0)
                    .opacity(ripple ? 0 : 0.8)
                    .animation(.easeOut(duration: 1.3).repeatForever(autoreverses: false), value: ripple)
                Circle()
                    .stroke(color.opacity(0.18), lineWidth: 2)
                    .scaleEffect(ripple ? 2.0 : 1.0)
                    .opacity(ripple ? 0 : 0.8)
                    .animation(.easeOut(duration: 1.3).repeatForever(autoreverses: false).delay(0.45), value: ripple)
            }
            Image(systemName: "mic.fill")
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(.white)
                .frame(width: 30, height: 30)
                .background(color, in: Circle())
        }
        .frame(width: 46, height: 46)
        .onAppear { ripple = true }
        .accessibilityLabel("Mic ready")
    }
}

// MARK: - Task status glyph (static dot → live equalizer)

/// The status indicator for a task row. Active states (queued/running/review)
/// render the animated equalizer so the couch sees life without reading a word;
/// terminal states stay a plain dot — no dancing bars for finished work.
struct TaskStatusGlyph: View {
    let status: String?

    private var color: Color {
        switch (status ?? "").lowercased() {
        case "running": return .green
        case "queued": return .blue
        case "review": return .purple
        case "completed": return .gray
        case "failed", "stopped": return .red
        default: return .secondary
        }
    }

    var body: some View {
        switch (status ?? "").lowercased() {
        case "running", "queued", "review":
            EqualizerBars(barCount: 4, color: color, active: true)
        default:
            Circle().fill(color).frame(width: 14, height: 14)
        }
    }
}
