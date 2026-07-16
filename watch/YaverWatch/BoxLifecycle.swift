// BoxLifecycle.swift — the watch-side model of a managed cloud box waking up
// (or being found asleep). A managed box self-parks after idle to save cost:
// it snapshots + deletes its server, so a direct POST to /runner/session/turn
// just times out. Instead of a bare error, the watch surfaces "Box asleep —
// Wake" and, once a wake is requested, drives the SAME phase ladder every other
// Yaver surface shows (mobile `wakeMachine.ts`, web, car-voice, TV, CLI):
//
//   Asleep → Waking → Restoring → Booting → Connecting → Online → Ready
//
// The short labels + percents here are pinned to `wakeMachine.ts`'s PHASE_META
// (requested→"Waking", resuming→"Restoring", registering→"Connecting") so the
// wrist reads identically to the phone. Update both together if the ladder ever
// changes.
//
// What the watch can observe by itself: only whether the box answers
// GET http://<box.host>:18080/health (200 + {ok:true}). It does NOT hold the
// control-plane token and cannot see the machine's provision phase, so the
// ladder is driven optimistically on elapsed time (Restoring→Booting→Connecting)
// and capped below Online until /health actually answers — then it jumps to
// Online → Ready. The wake request itself is routed through the paired iPhone
// (PhoneSession.requestWakeBox), which holds the token.

import Foundation
import SwiftUI

/// The canonical box-wake phase ladder. Labels + percents are pinned to the
/// mobile `wakeMachine.ts` PHASE_META so every surface reads the same.
enum WakePhase: String, CaseIterable, Identifiable {
    case asleep
    case waking      // mobile: requested
    case restoring   // mobile: resuming
    case booting
    case connecting  // mobile: registering
    case online
    case ready
    case needsAuth   // mobile: needs-auth

    var id: String { rawValue }

    /// One/two-word chip label (matches PHASE_META.short exactly).
    var label: String {
        switch self {
        case .asleep:     return "Asleep"
        case .waking:     return "Waking"
        case .restoring:  return "Restoring"
        case .booting:    return "Booting"
        case .connecting: return "Connecting"
        case .online:     return "Online"
        case .ready:      return "Ready"
        case .needsAuth:  return "Sign-in needed"
        }
    }

    /// 0-100 for the progress bar (matches PHASE_META.percent exactly).
    var percent: Int {
        switch self {
        case .asleep:     return 0
        case .waking:     return 8
        case .restoring:  return 22
        case .booting:    return 52
        case .connecting: return 80
        case .online:     return 94
        case .ready:      return 100
        case .needsAuth:  return 80
        }
    }

    /// Full sentence for the primary status line (mirrors PHASE_META.label).
    var detail: String {
        switch self {
        case .asleep:     return "Asleep — parked to save cost"
        case .waking:     return "Waking your box…"
        case .restoring:  return "Restoring from the latest snapshot…"
        case .booting:    return "Booting the machine…"
        case .connecting: return "Connecting over the relay…"
        case .online:     return "Network connected — finishing up…"
        case .ready:      return "Ready"
        case .needsAuth:  return "Sign this box in on your phone to finish"
        }
    }

    var symbol: String {
        switch self {
        case .asleep:     return "moon.zzz.fill"
        case .waking:     return "sunrise.fill"
        case .restoring:  return "clock.arrow.circlepath"
        case .booting:    return "power"
        case .connecting: return "antenna.radiowaves.left.and.right"
        case .online:     return "wifi"
        case .ready:      return "checkmark.circle.fill"
        case .needsAuth:  return "person.badge.key.fill"
        }
    }

    /// Ordered steps for the little stepper dots (excludes the resting `asleep`
    /// and the `waking` request tick — mirrors wakeMachine.ts WAKE_STEPS).
    static let wakeSteps: [WakePhase] = [.restoring, .booting, .connecting, .online, .ready]
}

/// Drives a box-wake: asks the phone to wake the box, then polls /health and
/// advances the phase ladder to Ready. Also models "the box we tried is asleep"
/// so the UI can offer a Wake button instead of a bare error.
@MainActor
final class BoxLifecycle: ObservableObject {
    /// Current phase. Monotonic within a single wake run (the bar never jumps
    /// backwards) — see `setPhase`.
    @Published private(set) var phase: WakePhase = .ready

    /// True while a wake is in flight (drives the progress view, blocks re-tap).
    @Published private(set) var isWaking = false

    /// A user-facing hint when the wake can't proceed on its own — e.g. the
    /// phone (which holds the control-plane token) isn't reachable.
    @Published private(set) var message: String?

    /// The box a Wake would target (the last box a turn failed against).
    private(set) var box: BoxTarget?

    private var driveTask: Task<Void, Never>?

    /// Short-timeout session so a parked box's refused/dead connection fails
    /// fast on each health poll rather than hanging the ladder.
    private let health: URLSession = {
        let cfg = URLSessionConfiguration.ephemeral
        cfg.timeoutIntervalForRequest = 3
        cfg.waitsForConnectivity = false
        return URLSession(configuration: cfg)
    }()

    /// True when the box we last tried looks asleep and we're not already waking
    /// it — the cue to show "Box asleep — Wake".
    var needsWake: Bool { phase == .asleep && !isWaking }

    // MARK: - State transitions

    /// Record that a turn failed against `box` in a way that looks like the box
    /// is parked (connection refused / timeout, or /health unreachable). Flips
    /// the model into the "asleep, offer Wake" state.
    func markAsleep(box: BoxTarget) {
        self.box = box
        driveTask?.cancel()
        driveTask = nil
        isWaking = false
        message = nil
        phase = .asleep
    }

    /// A turn just succeeded against the box — it's clearly reachable, so drop
    /// any stale asleep/waking state.
    func markReachable() {
        guard !isWaking else { return }
        if phase != .ready { phase = .ready }
        message = nil
    }

    // MARK: - Wake

    /// Kick off a wake: route the request through the paired iPhone (which holds
    /// the control-plane token), then drive the health-poll ladder to Ready.
    /// No-op if a wake is already running.
    func wake(box: BoxTarget, machineId: String?, using phone: PhoneSession) {
        guard !isWaking else { return }
        self.box = box
        isWaking = true
        message = nil
        setPhase(.waking)
        driveTask?.cancel()
        driveTask = Task { [weak self] in
            await self?.driveWake(box: box, machineId: machineId, phone: phone)
        }
    }

    /// Cancel any in-flight wake and return to a neutral resting state.
    func reset() {
        driveTask?.cancel()
        driveTask = nil
        isWaking = false
        phase = .ready
        message = nil
    }

    // MARK: - Internals

    /// Max time to chase the ladder before we tell the user to check the phone.
    /// A snapshot recreate is ~1-2 min; 3 min is generous headroom.
    private let maxWaitSeconds: TimeInterval = 180

    private func driveWake(box: BoxTarget, machineId: String?, phone: PhoneSession) async {
        // 1. The watch can't wake the box itself — it doesn't hold the
        //    control-plane token. Ask the phone (PhoneSession.requestWakeBox).
        let dispatch = await phone.requestWakeBox(machineId: machineId, deviceId: box.id)
        if Task.isCancelled { return }
        switch dispatch {
        case .phoneUnreachable:
            // No phone in reach → we can't start the box. Fall back to the hint.
            isWaking = false
            phase = .asleep
            message = "Open Yaver on your iPhone to wake the box."
            return
        case .failed(let err):
            isWaking = false
            phase = .asleep
            message = err
            return
        case .sent:
            break
        }

        // 2. The box is (re)creating from its snapshot. We only get the /health
        //    signal, so advance an optimistic ceiling by elapsed time and cap it
        //    below Online until /health answers — then jump Online → Ready.
        setPhase(.restoring)
        let start = Date()
        while !Task.isCancelled {
            let elapsed = Date().timeIntervalSince(start)
            advanceOptimistic(elapsed)

            let probe = await healthProbe(box: box)

            // The box is up but its Yaver session expired: it cannot register
            // and will never reach Ready on its own, then parks itself again
            // for "not authorized in time". Say so and stop — marching on to
            // Ready would promise a box that can't run a single turn, and the
            // watch has no way to sign it in (that needs the phone).
            if probe.authExpired {
                if Task.isCancelled { return }
                setPhase(.needsAuth)
                message = "Your box is awake but signed out. Open Yaver on your phone to sign it in."
                isWaking = false
                return
            }

            if probe.answered {
                if Task.isCancelled { return }
                setPhase(.online)
                try? await Task.sleep(nanoseconds: 700_000_000)
                if Task.isCancelled { return }
                setPhase(.ready)
                message = "Your box is awake. Speak your command."
                isWaking = false
                return
            }

            if elapsed > maxWaitSeconds {
                isWaking = false
                phase = .asleep
                message = "Still waking — this is taking longer than usual. Check Yaver on your phone."
                return
            }

            try? await Task.sleep(nanoseconds: 4_000_000_000) // poll /health ~every 4s
        }
    }

    /// Move Restoring → Booting → Connecting on a time budget while /health is
    /// still dark. Never reaches Online — only a real /health 200 does that.
    private func advanceOptimistic(_ elapsed: TimeInterval) {
        let target: WakePhase
        switch elapsed {
        case ..<8:  target = .restoring
        case ..<28: target = .booting
        default:    target = .connecting
        }
        setPhase(target)
    }

    /// Monotonic within a wake run: the bar only ever fills. `asleep` is always
    /// allowed (it ends a run).
    private func setPhase(_ next: WakePhase) {
        if next == .asleep { phase = next; return }
        // needsAuth is a verdict, not a rung. The monotonic guard exists so the
        // bar never walks backwards mid-wake, but this state means the wake has
        // STOPPED — and since it sits at 80, an optimistic ladder that had
        // already crept past it would silently swallow the one message the user
        // needs.
        if next == .needsAuth { phase = next; return }
        if next.percent < phase.percent { return }
        phase = next
    }

    /// What a /health poll actually told us.
    ///
    /// `ok` is NOT "the box is usable" — it only means the agent process
    /// answered. A box whose Yaver session expired replies 200 with
    /// `{"ok":true,"authExpired":true,"lifecycle":{"state":"yaver-auth-expired",
    /// "usable":false,"recoveryMode":"reauth"}}`. Reading only `ok` marched the
    /// ladder to Ready for a box that could not run a single turn, and the
    /// user's next tap simply failed.
    struct HealthProbe {
        var answered: Bool
        var authExpired: Bool
    }

    private func healthProbe(box: BoxTarget) async -> HealthProbe {
        guard let url = URL(string: "http://\(box.host):\(Backend.agentPort)/health") else {
            return HealthProbe(answered: false, authExpired: false)
        }
        do {
            let (data, resp) = try await health.data(from: url)
            guard let http = resp as? HTTPURLResponse, http.statusCode == 200 else {
                return HealthProbe(answered: false, authExpired: false)
            }
            guard let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
                // 200 with no/other body still means the agent answered.
                return HealthProbe(answered: true, authExpired: false)
            }
            let ok = (obj["ok"] as? Bool) ?? true
            // Either spelling counts: the flat flag, or the lifecycle block the
            // newer agents publish.
            var expired = (obj["authExpired"] as? Bool) ?? false
            if let lifecycle = obj["lifecycle"] as? [String: Any] {
                if let state = lifecycle["state"] as? String, state == "yaver-auth-expired" {
                    expired = true
                }
                if let usable = lifecycle["usable"] as? Bool, usable == false {
                    expired = true
                }
            }
            if let state = obj["lifecycleState"] as? String, state == "yaver-auth-expired" {
                expired = true
            }
            return HealthProbe(answered: ok, authExpired: expired)
        } catch {
            return HealthProbe(answered: false, authExpired: false)
        }
    }
}
