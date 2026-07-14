import SwiftUI
import WidgetKit

#if canImport(ActivityKit)
import ActivityKit

/// Shared presentation for every surface the Live Activity lands on: the Lock
/// Screen, the Dynamic Island, and — the reason this exists — the CarPlay
/// Dashboard. Written once so the car and the phone can never disagree.
@available(iOS 16.2, *)
enum YaverActivityStyle {
    /// Accent per coarse status. Deliberately high-contrast: this is read at a
    /// glance, at arm's length, in daylight, by someone who is driving.
    static func tint(_ status: String) -> Color {
        switch status {
        case "done": return Color(red: 0.30, green: 0.79, blue: 0.47)
        case "failed": return Color(red: 0.94, green: 0.35, blue: 0.35)
        case "listening", "speaking": return Color(red: 0.42, green: 0.62, blue: 0.98)
        default: return Color(red: 0.85, green: 0.72, blue: 0.35)
        }
    }

    static func glyph(_ status: String) -> String {
        switch status {
        case "done": return "checkmark.circle.fill"
        case "failed": return "exclamationmark.triangle.fill"
        case "listening": return "waveform"
        case "speaking": return "speaker.wave.2.fill"
        default: return "gearshape.2.fill"
        }
    }
}

/// The CarPlay Dashboard / Smart Stack view (`.small` activity family), and the
/// Lock Screen view. One compact row: glyph, headline, machine. No code, no
/// logs — a driver gets a state, not a document.
@available(iOS 16.2, *)
struct YaverActivityCompactView: View {
    let state: YaverActivityAttributes.ContentState
    let machine: String

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: YaverActivityStyle.glyph(state.status))
                .font(.system(size: 20, weight: .semibold))
                .foregroundStyle(YaverActivityStyle.tint(state.status))
                .frame(width: 26)

            VStack(alignment: .leading, spacing: 2) {
                Text(state.headline)
                    .font(.system(size: 15, weight: .semibold))
                    .lineLimit(1)
                    .minimumScaleFactor(0.85)

                Text(state.detail.isEmpty ? machine : state.detail)
                    .font(.system(size: 12, weight: .regular))
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer(minLength: 0)

            if let p = state.progress, p > 0, p < 1 {
                ProgressView(value: p)
                    .progressViewStyle(.circular)
                    .tint(YaverActivityStyle.tint(state.status))
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
        .activityBackgroundTint(Color.black.opacity(0.55))
        .activitySystemActionForegroundColor(.white)
    }
}
#endif
