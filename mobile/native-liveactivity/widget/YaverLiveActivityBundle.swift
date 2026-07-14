import SwiftUI
import WidgetKit

#if canImport(ActivityKit)
import ActivityKit

/// The widget extension's entry point — one Live Activity, drawn on the Lock
/// Screen, the Dynamic Island, the Watch Smart Stack, and the CarPlay Dashboard.
///
/// Deployment target is iOS 18.4, which is when `supplementalActivityFamilies`
/// arrived. That modifier is the ONLY thing that makes the activity eligible
/// for the CarPlay Dashboard, and it changes the configuration's concrete type
/// — so it cannot be applied behind an `if #available` inside a single `body`
/// (the type differs per branch), and `WidgetBundleBuilder` does not accept
/// availability control flow either. Rather than ship two near-identical widget
/// structs to buy iOS 16.2…18.3, we set the floor at 18.4: CarPlay Live
/// Activities need iOS 26 anyway, so the older band buys nothing for the car.
/// The main app still targets iOS 15.5 — only this extension is gated.
@available(iOS 18.4, *)
@main
struct YaverWidgetBundle: WidgetBundle {
    var body: some Widget {
        YaverActivityWidget()
    }
}

@available(iOS 18.4, *)
struct YaverActivityWidget: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: YaverActivityAttributes.self) { context in
            // Lock Screen + CarPlay Dashboard + Watch Smart Stack.
            YaverActivityCompactView(
                state: context.state,
                machine: context.attributes.machine
            )
        } dynamicIsland: { context in
            DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    Image(systemName: YaverActivityStyle.glyph(context.state.status))
                        .foregroundStyle(YaverActivityStyle.tint(context.state.status))
                }
                DynamicIslandExpandedRegion(.trailing) {
                    Text(context.attributes.machine)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                DynamicIslandExpandedRegion(.center) {
                    Text(context.state.headline)
                        .font(.system(size: 15, weight: .semibold))
                        .lineLimit(1)
                }
                DynamicIslandExpandedRegion(.bottom) {
                    Text(context.state.detail)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            } compactLeading: {
                Image(systemName: YaverActivityStyle.glyph(context.state.status))
                    .foregroundStyle(YaverActivityStyle.tint(context.state.status))
            } compactTrailing: {
                Text(context.attributes.machine.prefix(4))
                    .font(.caption2)
            } minimal: {
                Image(systemName: YaverActivityStyle.glyph(context.state.status))
                    .foregroundStyle(YaverActivityStyle.tint(context.state.status))
            }
        }
        // The one line that reaches the car. CarPlay Developer Guide (June
        // 2026): Live Activities appear on the CarPlay Dashboard, and "your app
        // does not need to be a CarPlay app" to support them. No entitlement.
        .supplementalActivityFamilies([.small])
    }
}
#endif
