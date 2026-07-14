import Foundation
import React

#if canImport(ActivityKit)
import ActivityKit
#endif

/// NativeModules.YaverLiveActivity — start / update / end the Live Activity
/// that renders on the CarPlay Dashboard, the Lock Screen, and the Dynamic
/// Island.
///
/// Deliberately dumb: the phone decides WHAT to say (one-line summaries already
/// produced by carVoiceCoding.ts / carSurfaceIntent.ts, which are the modules
/// that guarantee no code or diffs ever reach a driving surface). This module
/// only carries those strings to ActivityKit. It holds exactly one activity at
/// a time — a car dashboard with three competing Yaver cards would be worse
/// than none.
@objc(YaverLiveActivity)
class YaverLiveActivity: NSObject {

    @objc static func requiresMainQueueSetup() -> Bool { false }

#if canImport(ActivityKit)
    /// The single in-flight activity, if any.
    @available(iOS 16.2, *)
    private static var current: Activity<YaverActivityAttributes>? {
        get { _current as? Activity<YaverActivityAttributes> }
        set { _current = newValue }
    }
    private static var _current: Any?
#endif

    /// Start (or replace) the activity. Resolves the activity id, or rejects
    /// with a reason the JS layer can log — Live Activities are a nice-to-have,
    /// so callers treat failure as non-fatal.
    @objc(start:taskId:status:headline:detail:progress:resolver:rejecter:)
    func start(
        _ machine: String,
        taskId: String,
        status: String,
        headline: String,
        detail: String,
        progress: NSNumber?,
        resolver resolve: @escaping RCTPromiseResolveBlock,
        rejecter reject: @escaping RCTPromiseRejectBlock
    ) {
#if canImport(ActivityKit)
        guard #available(iOS 16.2, *) else {
            reject("unsupported", "Live Activities require iOS 16.2+", nil)
            return
        }
        guard ActivityAuthorizationInfo().areActivitiesEnabled else {
            // The user has switched Live Activities off for Yaver. Not an error
            // worth shouting about — report it and let the caller move on.
            reject("disabled", "Live Activities are disabled for Yaver in Settings", nil)
            return
        }

        // Only one at a time.
        Task { await Self.endAll() }

        let attributes = YaverActivityAttributes(machine: machine, taskId: taskId)
        let state = YaverActivityAttributes.ContentState(
            status: status,
            headline: headline,
            detail: detail,
            progress: progress?.doubleValue
        )

        do {
            let activity = try Activity.request(
                attributes: attributes,
                content: .init(state: state, staleDate: nil),
                pushType: nil
            )
            Self.current = activity
            resolve(activity.id)
        } catch {
            reject("start_failed", error.localizedDescription, error)
        }
#else
        reject("unsupported", "ActivityKit unavailable", nil)
#endif
    }

    /// Update the in-flight activity. No-op (resolves false) when none is live,
    /// so the JS caller never has to track whether start() succeeded.
    @objc(update:headline:detail:progress:resolver:rejecter:)
    func update(
        _ status: String,
        headline: String,
        detail: String,
        progress: NSNumber?,
        resolver resolve: @escaping RCTPromiseResolveBlock,
        rejecter reject: @escaping RCTPromiseRejectBlock
    ) {
#if canImport(ActivityKit)
        guard #available(iOS 16.2, *), let activity = Self.current else {
            resolve(false)
            return
        }
        let state = YaverActivityAttributes.ContentState(
            status: status,
            headline: headline,
            detail: detail,
            progress: progress?.doubleValue
        )
        Task {
            await activity.update(.init(state: state, staleDate: nil))
            resolve(true)
        }
#else
        resolve(false)
#endif
    }

    /// End the activity. `dismissAfter` seconds keeps a terminal result on the
    /// dashboard briefly — a driver who glances 4 seconds late should still see
    /// "Deploy failed" rather than an empty slot.
    @objc(end:headline:detail:dismissAfter:resolver:rejecter:)
    func end(
        _ status: String,
        headline: String,
        detail: String,
        dismissAfter: NSNumber?,
        resolver resolve: @escaping RCTPromiseResolveBlock,
        rejecter reject: @escaping RCTPromiseRejectBlock
    ) {
#if canImport(ActivityKit)
        guard #available(iOS 16.2, *), let activity = Self.current else {
            resolve(false)
            return
        }
        let state = YaverActivityAttributes.ContentState(
            status: status,
            headline: headline,
            detail: detail,
            progress: nil
        )
        let seconds = dismissAfter?.doubleValue ?? 8
        Task {
            await activity.end(
                .init(state: state, staleDate: nil),
                dismissalPolicy: .after(Date().addingTimeInterval(seconds))
            )
            Self.current = nil
            resolve(true)
        }
#else
        resolve(false)
#endif
    }

    /// True when the OS will actually show an activity — the JS layer checks
    /// this before bothering to build summaries.
    @objc(isAvailable:rejecter:)
    func isAvailable(
        _ resolve: @escaping RCTPromiseResolveBlock,
        rejecter reject: @escaping RCTPromiseRejectBlock
    ) {
#if canImport(ActivityKit)
        if #available(iOS 16.2, *) {
            resolve(ActivityAuthorizationInfo().areActivitiesEnabled)
            return
        }
#endif
        resolve(false)
    }

#if canImport(ActivityKit)
    @available(iOS 16.2, *)
    private static func endAll() async {
        for activity in Activity<YaverActivityAttributes>.activities {
            await activity.end(nil, dismissalPolicy: .immediate)
        }
        Self.current = nil
    }
#endif
}
