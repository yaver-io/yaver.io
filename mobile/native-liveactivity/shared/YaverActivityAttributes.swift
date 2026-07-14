import Foundation

#if canImport(ActivityKit)
import ActivityKit

/// The Live Activity contract, compiled into BOTH the app target (which starts
/// and updates the activity) and the widget extension (which draws it). One
/// file, two targets — the shape can never drift between them.
///
/// This is what puts Yaver on the CarPlay Dashboard. Per Apple's CarPlay
/// Developer Guide (June 2026): "Your app does not need to be a CarPlay app to
/// support widgets and Live Activities in CarPlay." No entitlement is involved.
/// The Dashboard renders the `.small` supplemental activity family — the same
/// one the Apple Watch Smart Stack uses — so the car view is built once and
/// reused, and it degrades to the Dynamic Island compact views on older iOS.
///
/// Driving-safety constraint, inherited from carVoiceCoding.ts: NOTHING here
/// may carry code, diffs, logs, or file contents. `headline` and `detail` are
/// pre-summarized one-liners produced on the phone. A glance, not a read.
@available(iOS 16.2, *)
public struct YaverActivityAttributes: ActivityAttributes {
    public struct ContentState: Codable, Hashable {
        /// Coarse state; drives the accent colour and the glyph.
        /// One of: "working", "done", "failed", "listening", "speaking".
        public var status: String
        /// One short line — "Building sfmg", "Deploy failed".
        public var headline: String
        /// Secondary line — "pokayoke · 2m". Keep it under ~24 chars.
        public var detail: String
        /// 0…1 when the task reports progress; nil for indeterminate work.
        public var progress: Double?

        public init(status: String, headline: String, detail: String, progress: Double? = nil) {
            self.status = status
            self.headline = headline
            self.detail = detail
            self.progress = progress
        }
    }

    /// The machine the work is running on — "pokayoke", "primary".
    public var machine: String
    /// Opaque task id, so a tap (where allowed) can deep-link to the task.
    public var taskId: String

    public init(machine: String, taskId: String) {
        self.machine = machine
        self.taskId = taskId
    }
}
#endif
