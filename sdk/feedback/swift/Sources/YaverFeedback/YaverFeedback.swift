import Foundation
#if canImport(UIKit)
import UIKit
#endif

/// Yaver Feedback SDK for native iOS / Swift.
///
/// ─── What this closes ───────────────────────────────────────────────────────
///
/// Until this existed there was no native Swift feedback SDK — RN, Flutter, Web
/// and Unity shipped, and native iOS was viewer-triggered only (a
/// `launch-feedback` control message pushed down the WebRTC events channel from
/// the Yaver viewer). That still works and remains the fallback; this adds the
/// in-app path so a Swift-only app has the same loop as everyone else.
///
/// ─── Three ways to adopt it, and the SDK stands alone ───────────────────────
///
/// You do NOT need the Yaver app. "Drop it in, point it at an agent" is a
/// legitimate end state:
///
///   1. SDK only            — your app + your own agent. Shake-to-report.
///   2. SDK + remote runtime — plus reviewing the app from your phone over
///                             WebRTC while it runs in a simulator on a box.
///   3. Full Yaver          — plus the coding loop.
///
/// ─── Why there is no container-suppression check here ───────────────────────
///
/// The RN SDK must read `YaverInfo.isYaver` and disable its own shake handler
/// when loaded inside the Yaver container, because there the CONTAINER owns
/// shake (Reload / Back to Yaver) and two overlays would fight.
///
/// A Swift app is never in that position: the container loads a Hermes bytecode
/// bundle, which is React Native only. When a Swift app runs "inside Yaver" it
/// does so in an iOS SIMULATOR on a Cloud Workspace, streamed over WebRTC — the
/// phone sends a `shake` session command, the box injects a hardware shake with
/// `simctl`, and THIS SDK fires its overlay inside the simulator. The phone's
/// exit affordance lives in native viewer chrome outside the video, so the two
/// cannot collide.
///
/// Adding a suppression check "for symmetry" would disable feedback in exactly
/// the case it is needed.
public final class YaverFeedback {

    public static let shared = YaverFeedback()

    private var config: FeedbackConfig?
    private var timeline: [TimelineEvent] = []
    private var errors: [CapturedError] = []
    private let startedAt = Date()
    private let lock = NSLock()
    #if canImport(UIKit)
    private var shakeObserver: NSObjectProtocol?
    #endif

    private init() {}

    /// Initialise.
    ///
    /// Guard this behind a debug check in your app. The SDK does not guard
    /// itself: "is this a release build" is the host app's question to answer,
    /// and a wrong guess either way is worse than the caller being explicit.
    public static func initialize(_ config: FeedbackConfig) {
        precondition(!config.agentURL.isEmpty, "agentURL is required")
        precondition(
            !config.authToken.isEmpty,
            "authToken is required — use an SDK token, never a user session token"
        )
        shared.config = config
        if config.shakeEnabled { shared.startShakeObserver() }
        if config.captureErrors { shared.installErrorHandler() }
    }

    /// Stop listening.
    public static func stop() {
        #if canImport(UIKit)
        if let o = shared.shakeObserver {
            NotificationCenter.default.removeObserver(o)
            shared.shakeObserver = nil
        }
        #endif
    }

    /// Add a timestamped annotation to the next report.
    public static func mark(type: String, text: String? = nil) {
        let s = shared
        s.lock.lock(); defer { s.lock.unlock() }
        s.timeline.append(
            TimelineEvent(
                time: Date().timeIntervalSince(s.startedAt),
                type: type,
                text: text
            )
        )
    }

    /// Capture and submit a report now.
    ///
    /// Fire-and-forget by design: a feedback SDK must never block the UI or
    /// surface a network error to the user. A failure is logged and dropped —
    /// the user's app is not the place to debug our transport.
    public static func open(note: String? = nil) {
        guard let cfg = shared.config else {
            print("[YaverFeedback] open() before initialize() — ignoring")
            return
        }
        let screenshot = cfg.captureScreenshot ? shared.captureScreenshot() : nil
        let report = shared.buildReport(screenshot: screenshot, note: note)
        shared.submit(cfg: cfg, report: report)
    }

    // MARK: - Shake

    #if canImport(UIKit)
    private func startShakeObserver() {
        // UIKit already delivers a debounced, system-recognised shake, so
        // unlike Android there is no accelerometer thresholding to tune here.
        // Using the platform gesture also means a `simctl` hardware shake — how
        // a streamed session triggers feedback — arrives through the same path.
        shakeObserver = NotificationCenter.default.addObserver(
            forName: .yaverDeviceDidShake,
            object: nil,
            queue: .main
        ) { _ in YaverFeedback.open() }
    }
    #else
    private func startShakeObserver() {}
    #endif

    // MARK: - Capture

    private func buildReport(screenshot: String?, note: String?) -> FeedbackReport {
        lock.lock()
        let tl = timeline
        let errs = errors
        lock.unlock()

        #if canImport(UIKit)
        let bounds = UIScreen.main.bounds
        let device = DeviceInfo(
            platform: "ios",
            osVersion: UIDevice.current.systemVersion,
            model: UIDevice.current.model,
            screenWidth: Int(bounds.width),
            screenHeight: Int(bounds.height)
        )
        #else
        let device = DeviceInfo(
            platform: "ios", osVersion: "unknown", model: "unknown",
            screenWidth: 0, screenHeight: 0
        )
        #endif

        return FeedbackReport(
            id: UUID().uuidString,
            screenshots: screenshot.map { [$0] } ?? [],
            timeline: tl,
            errors: errs,
            deviceInfo: device,
            appVersion: Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String,
            buildId: Bundle.main.infoDictionary?["CFBundleVersion"] as? String,
            note: note,
            createdAt: ISO8601DateFormatter().string(from: Date())
        )
    }

    private func captureScreenshot() -> String? {
        #if canImport(UIKit)
        guard let window = UIApplication.shared.connectedScenes
            .compactMap({ $0 as? UIWindowScene })
            .flatMap({ $0.windows })
            .first(where: { $0.isKeyWindow })
        else { return nil }

        let renderer = UIGraphicsImageRenderer(bounds: window.bounds)
        let image = renderer.image { _ in
            // afterScreenUpdates: false — true forces a synchronous layout pass
            // and can deadlock if called from inside a layout callback, which
            // is precisely where a shake handler often lands.
            window.drawHierarchy(in: window.bounds, afterScreenUpdates: false)
        }
        // 60% JPEG: a bug report needs to be legible, not archival, and the
        // payload crosses a phone network.
        guard let data = image.jpegData(compressionQuality: 0.6) else { return nil }
        return "data:image/jpeg;base64," + data.base64EncodedString()
        #else
        return nil
        #endif
    }

    private func installErrorHandler() {
        let previous = NSGetUncaughtExceptionHandler()
        NSSetUncaughtExceptionHandler { exception in
            YaverFeedback.shared.lock.lock()
            YaverFeedback.shared.errors.append(
                CapturedError(
                    message: exception.reason ?? exception.name.rawValue,
                    stack: exception.callStackSymbols.prefix(40).joined(separator: "\n"),
                    timestamp: ISO8601DateFormatter().string(from: Date())
                )
            )
            YaverFeedback.shared.lock.unlock()
            // ALWAYS chain. Swallowing the host app's handler would break their
            // Crashlytics/Sentry reporting — a feedback SDK that costs someone
            // their crash telemetry is a net loss however good its own reports.
            previous?(exception)
        }
    }

    // MARK: - Submit

    private func submit(cfg: FeedbackConfig, report: FeedbackReport) {
        guard let url = URL(string: cfg.agentURL.trimmedTrailingSlash + "/feedback"),
              let body = try? JSONSerialization.data(withJSONObject: report.toDictionary())
        else {
            print("[YaverFeedback] could not build request — check agentURL")
            return
        }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(cfg.authToken)", forHTTPHeaderField: "Authorization")
        req.httpBody = body
        req.timeoutInterval = 15

        URLSession.shared.dataTask(with: req) { _, response, error in
            if let error = error {
                print("[YaverFeedback] submit failed: \(error.localizedDescription)")
                return
            }
            if let http = response as? HTTPURLResponse, !(200...299).contains(http.statusCode) {
                // Name the status. "Feedback failed" without one costs a
                // session; 401 vs 404 vs 500 are three different fixes.
                print("[YaverFeedback] rejected: HTTP \(http.statusCode) — check the SDK token and agentURL")
                return
            }
            let s = YaverFeedback.shared
            s.lock.lock(); s.timeline.removeAll(); s.errors.removeAll(); s.lock.unlock()
        }.resume()
    }
}

private extension String {
    var trimmedTrailingSlash: String {
        hasSuffix("/") ? String(dropLast()) : self
    }
}

#if canImport(UIKit)
public extension Notification.Name {
    /// Posted by `UIWindow.motionEnded` when the device is shaken.
    static let yaverDeviceDidShake = Notification.Name("io.yaver.feedback.deviceDidShake")
}

/// Bridges UIKit's shake gesture to a notification.
///
/// UIKit only delivers `motionEnded` to the first responder, so a plain
/// observer never fires without this. Host apps that already subclass UIWindow
/// can post `.yaverDeviceDidShake` themselves instead of using this class.
open class YaverShakeWindow: UIWindow {
    open override func motionEnded(_ motion: UIEvent.EventSubtype, with event: UIEvent?) {
        if motion == .motionShake {
            NotificationCenter.default.post(name: .yaverDeviceDidShake, object: nil)
        }
        super.motionEnded(motion, with: event)
    }
}
#endif
