// BoxLifecycle.swift — the "waking up" model for the tvOS surface.
//
// A managed cloud box auto-parks (self-park) after it's idle to save cost:
// it snapshots + deletes its server, so it has no live endpoint and a turn
// to it just fails. Resuming recreates the server from the latest snapshot
// (~1-2 min) and it re-registers over the free relay with its persisted
// token — no re-auth.
//
// This file mirrors mobile/src/lib/wakeMachine.ts (the single source of
// truth for the wake vocabulary) in Swift. The TV can't see the control
// plane's per-step provision phase like mobile polls off /subscription, so
// it drives the ladder off the one signal it CAN observe itself: the box
// answering GET /health. Same short labels, same order, same percents as
// every other surface:
//
//   Asleep → Waking → Restoring → Booting → Connecting → Online → Ready
//   (percents 0, 8, 22, 52, 80, 94, 100)

import Foundation
import SwiftUI

/// The canonical wake ladder. `short`/`percent` MUST match PHASE_META in
/// mobile/src/lib/wakeMachine.ts so the TV reads the same as phone/web/watch.
enum BoxPhase: String, CaseIterable, Equatable {
    case asleep       // parked, at rest — where a Wake starts
    case waking       // user asked; resume request in flight
    case restoring    // control plane accepted; recreating from snapshot
    case booting      // server exists; OS booting, agent not up yet
    case connecting   // agent starting + registering over the free relay
    case online       // reachable, finishing up
    case ready        // fully reachable and usable

    /// One/two-word chip label — identical to PHASE_META `short`.
    var short: String {
        switch self {
        case .asleep: return "Asleep"
        case .waking: return "Waking"
        case .restoring: return "Restoring"
        case .booting: return "Booting"
        case .connecting: return "Connecting"
        case .online: return "Online"
        case .ready: return "Ready"
        }
    }

    /// Full sentence for the primary status line.
    var label: String {
        switch self {
        case .asleep: return "Asleep — parked to save cost"
        case .waking: return "Waking your box…"
        case .restoring: return "Recreating from the latest snapshot…"
        case .booting: return "Booting the machine…"
        case .connecting: return "Connecting over the free relay…"
        case .online: return "Network connected — finishing up…"
        case .ready: return "Ready"
        }
    }

    /// 0-100 for the progress bar — identical to PHASE_META `percent`.
    var percent: Int {
        switch self {
        case .asleep: return 0
        case .waking: return 8
        case .restoring: return 22
        case .booting: return 52
        case .connecting: return 80
        case .online: return 94
        case .ready: return 100
        }
    }

    /// True once the relay leg is up — tints the bar/step green like mobile.
    var isNetwork: Bool { self == .connecting || self == .online || self == .ready }

    /// Ordered wake steps for the stepper (drops the resting `asleep`/`waking`
    /// ends). Mirrors WAKE_STEPS = [resuming, booting, registering, online, ready].
    static let wakeSteps: [BoxPhase] = [.restoring, .booting, .connecting, .online, .ready]
}

/// Drives a box back to life from the TV: fires the control-plane resume,
/// then polls the box's /health every ~4s and advances the phase ladder to
/// Online → Ready. One instance per screen; `@StateObject` it.
@MainActor
final class BoxLifecycle: ObservableObject {
    @Published private(set) var phase: BoxPhase = .asleep
    @Published private(set) var percent: Int = 0
    /// True while a wake run is in flight (drive spinners, disable re-tap).
    @Published private(set) var isRunning = false
    /// Last observed reachability of the tracked box (nil = not probed yet).
    @Published private(set) var reachable: Bool?
    @Published var error: String?

    /// The box this lifecycle is tracking (set on probe/wake).
    private(set) var box: BoxTarget?

    private var floor: Int = 0            // monotonic percent floor within a run
    private var pollTask: Task<Void, Never>?
    private let net: URLSession = {
        let cfg = URLSessionConfiguration.ephemeral
        cfg.timeoutIntervalForRequest = 5
        cfg.waitsForConnectivity = false
        return URLSession(configuration: cfg)
    }()

    // MARK: - Derived state

    /// A managed box we've observed to be unreachable — i.e. auto-parked. A
    /// self-hosted box that's merely offline is NOT "asleep" (we can't wake
    /// it), so this is gated on `managed`.
    var isBoxAsleep: Bool { (box?.managed ?? false) && reachable == false }

    /// The box is unreachable and there's no wake already running. The UI
    /// still checks `box.wakeable` to decide between a Wake button and the
    /// "start it from your computer or phone" message.
    var needsWake: Bool { reachable == false && !isRunning }

    // MARK: - Reachability probe (for the picker / pre-flight)

    /// One-shot /health probe used by the box picker to decide whether to
    /// surface a Wake affordance. Doesn't start a wake.
    func refreshReachability(_ box: BoxTarget) {
        self.box = box
        Task {
            let ok = await healthOK(box: box)
            // Don't clobber a live run's phase with a stale probe.
            if !isRunning { reachable = ok }
        }
    }

    /// Mark the box unreachable from an observed failure (e.g. a turn threw a
    /// connection error). Lets the picker/session show Wake without a probe.
    func markUnreachable(_ box: BoxTarget) {
        self.box = box
        if !isRunning { reachable = false }
    }

    // MARK: - Wake

    /// Resume the box, then poll /health and drive the ladder to Ready.
    /// No-op if a run is already in flight.
    func wake(_ box: BoxTarget, token: String) {
        guard !isRunning else { return }
        self.box = box
        error = nil

        guard box.wakeable, let machineId = box.machineId, !machineId.isEmpty else {
            error = "This box can't be woken from the TV — start it from your computer or phone."
            return
        }

        isRunning = true
        floor = 0
        setPhase(.waking)
        pollTask?.cancel()
        pollTask = Task { await run(box: box, token: token, machineId: machineId) }
    }

    func cancel() {
        pollTask?.cancel()
        pollTask = nil
        isRunning = false
    }

    private func run(box: BoxTarget, token: String, machineId: String) async {
        // 1. Ask the control plane to resume. Resolves when ACCEPTED — the box
        //    then boots + re-registers asynchronously, which the /health poll
        //    below observes.
        do {
            try await requestResume(token: token, machineId: machineId)
        } catch {
            finish(error: (error as? LocalizedError)?.errorDescription ?? error.localizedDescription)
            return
        }
        if Task.isCancelled { finish(error: nil); return }
        setPhase(.restoring)

        // 2. Poll /health until the box answers, walking Booting → Connecting
        //    while it's still cold so the bar keeps moving truthfully.
        var ticks = 0
        while !Task.isCancelled {
            if await healthOK(box: box) {
                reachable = true
                setPhase(.online)
                try? await Task.sleep(nanoseconds: 900_000_000)
                if Task.isCancelled { break }
                setPhase(.ready)
                finish(error: nil)
                return
            }
            ticks += 1
            if ticks == 1 {
                setPhase(.booting)
            } else if ticks >= 4 {          // ~16s in — relay leg is the slow part
                setPhase(.connecting)
            }
            try? await Task.sleep(nanoseconds: 4_000_000_000)
        }
        finish(error: nil)
    }

    // MARK: - Network

    /// POST {convexSiteURL}/billing/yaver-cloud/start { machineId } with a
    /// Bearer token — the SAME endpoint mobile's startManagedCloudMachine
    /// hits. Returns when the resume is accepted.
    private func requestResume(token: String, machineId: String) async throws {
        let url = Backend.convexSiteURL.appendingPathComponent("billing/yaver-cloud/start")
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.httpBody = try JSONSerialization.data(withJSONObject: ["machineId": machineId])

        let (data, resp) = try await URLSession.shared.data(for: req)
        guard let http = resp as? HTTPURLResponse else { throw AgentError(message: "No response from control plane.") }
        guard (200..<300).contains(http.statusCode) else {
            if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let err = obj["error"] as? String, !err.isEmpty {
                throw AgentError(message: err)
            }
            throw AgentError(message: "Wake request failed (\(http.statusCode)).")
        }
    }

    /// GET http://<host>:<port>/health — 200 + {ok:true} means the box is back.
    private func healthOK(box: BoxTarget) async -> Bool {
        guard let url = URL(string: "http://\(box.host):\(box.port)/health") else { return false }
        do {
            let (data, resp) = try await net.data(for: URLRequest(url: url))
            guard let http = resp as? HTTPURLResponse, http.statusCode == 200 else { return false }
            if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let ok = obj["ok"] as? Bool {
                return ok
            }
            return true   // 200 with no/other body still means reachable
        } catch {
            return false
        }
    }

    // MARK: - Phase bookkeeping (monotonic — the bar only ever fills)

    private func setPhase(_ p: BoxPhase) {
        phase = p
        floor = max(floor, p.percent)
        percent = floor
    }

    private func finish(error: String?) {
        isRunning = false
        if let error { self.error = error }
    }
}
