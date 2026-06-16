// Complications.swift — watch-face quick actions. The cheapest possible
// interaction (docs/yaver-smartwatch-voice-terminal.md §4 #2): tap a
// complication to dispatch a FIXED intent without speaking.
//
// On modern watchOS, complications are WidgetKit widgets. A widget's tap simply
// launches the app (you can't run arbitrary code from the widget process), so a
// complication "quick action" is implemented as:
//   1) a WidgetKit complication that, when tapped, deep-links into the app via a
//      `widgetURL` like  yaverwatch://intent/run-tests
//   2) the app's `.onOpenURL` handler maps that URL to a WatchIntent and calls
//      store.sendIntent(...)  — which routes through the same phone-paired /
//      standalone transport as a spoken command.
//
// This file provides (a) the URL <-> WatchIntent mapping (single source of
// truth) and (b) a minimal WidgetKit complication scaffold. It is heavily
// commented because the widget target wiring (a separate Widget Extension
// target) is a follow-up; for the scaffold we keep the mapping + a Timeline
// provider that renders the two shipped quick-actions (run-tests, status).
//
// NOTE: to actually show on a face, these widgets must live in a *Widget
// Extension* target (added in Xcode / project.yml). Kept here, minimal, so the
// intent surface and deep-link contract are pinned now.

import Foundation
import SwiftUI
#if canImport(WidgetKit)
import WidgetKit
#endif

// MARK: - Deep-link contract (app side reads this)

enum WatchDeepLink {
    static let scheme = "yaverwatch"
    static let intentHost = "intent"

    /// URL a complication carries; the app maps it back to a WatchIntent.
    /// e.g. yaverwatch://intent/run-tests
    static func url(for intent: WatchIntent) -> URL {
        URL(string: "\(scheme)://\(intentHost)/\(intent.rawValue)")!
    }

    /// Parse a deep link opened via `.onOpenURL` back into an intent.
    static func intent(from url: URL) -> WatchIntent? {
        guard url.scheme == scheme, url.host == intentHost else { return nil }
        let raw = url.lastPathComponent
        return WatchIntent(rawValue: raw)
    }
}

// MARK: - Quick-action catalog (which intents get a complication)

/// The complication quick-actions we ship. Keep this to 2–3 (the wrist's
/// "buttons"). Voice covers everything else.
enum WatchQuickActions {
    static let shipped: [WatchIntent] = [.runTests, .status]

    static func label(_ intent: WatchIntent) -> String {
        switch intent {
        case .runTests: return "Tests"
        case .deploy: return "Deploy"
        case .status: return "Status"
        }
    }

    static func symbol(_ intent: WatchIntent) -> String {
        switch intent {
        case .runTests: return "checkmark.seal"
        case .deploy: return "arrow.up.circle"
        case .status: return "bolt.heart"
        }
    }
}

#if canImport(WidgetKit)

// MARK: - Minimal WidgetKit complication scaffold
//
// A single-entry timeline (static quick-action button). Real builds would add a
// Widget Extension target and list these in its @main WidgetBundle. The view is
// intentionally tiny — an SF Symbol + label that deep-links on tap.

struct QuickActionEntry: TimelineEntry {
    let date: Date
    let intent: WatchIntent
}

struct QuickActionProvider: TimelineProvider {
    let intent: WatchIntent

    func placeholder(in context: Context) -> QuickActionEntry {
        QuickActionEntry(date: Date(), intent: intent)
    }
    func getSnapshot(in context: Context, completion: @escaping (QuickActionEntry) -> Void) {
        completion(QuickActionEntry(date: Date(), intent: intent))
    }
    func getTimeline(in context: Context, completion: @escaping (Timeline<QuickActionEntry>) -> Void) {
        // Static action — never needs refreshing.
        completion(Timeline(entries: [QuickActionEntry(date: Date(), intent: intent)], policy: .never))
    }
}

struct QuickActionComplicationView: View {
    let entry: QuickActionEntry

    var body: some View {
        VStack(spacing: 2) {
            Image(systemName: WatchQuickActions.symbol(entry.intent))
            Text(WatchQuickActions.label(entry.intent))
                .font(.system(size: 11, weight: .semibold))
                .minimumScaleFactor(0.6)
        }
        // Tapping the complication opens the app at this deep link, which
        // .onOpenURL turns into store.sendIntent(entry.intent).
        .widgetURL(WatchDeepLink.url(for: entry.intent))
    }
}

// Example widget definitions. In a Widget Extension target these would be
// registered in a WidgetBundle; here they document the shape.
//
// struct RunTestsComplication: Widget {
//     var body: some WidgetConfiguration {
//         StaticConfiguration(kind: "io.yaver.watch.runTests",
//                             provider: QuickActionProvider(intent: .runTests)) { entry in
//             QuickActionComplicationView(entry: entry)
//         }
//         .configurationDisplayName("Run tests")
//         .supportedFamilies([.accessoryCircular, .accessoryCorner, .accessoryInline])
//     }
// }

#endif
