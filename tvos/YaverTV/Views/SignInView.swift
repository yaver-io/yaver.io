// SignInView.swift — TV sign-in.
//
// Exactly two account-login paths:
//
//   1. Email + password directly on the TV.
//   2. Device-code QR approved by an already-authenticated Yaver phone. The
//      phone may itself have signed in with Apple, Google, Microsoft, GitHub,
//      GitLab, passkey, or email; the TV never implements those providers.
//
// Mirrors mobile/app/tv-signin.tsx.

import Combine
import SwiftUI
import UIKit
import CoreImage.CIFilterBuiltins

struct SignInView: View {
    @EnvironmentObject var store: YaverStore
    @State private var start: DeviceCodeStart?
    @State private var error: String?
    @State private var expired = false
    @State private var approving = false      // approval seen; token arriving
    @State private var rotatingCode = false   // coalesce expiry from both poll lanes
    // Email/password (2026-08-13): an account that isn't Apple-linked, or an
    // Apple account with 2FA, can still sign in on the couch. Typed with the
    // remote's on-screen keyboard; hits POST /auth/login (Backend.EmailAuth).
    @State private var email = ""
    @State private var password = ""
    @State private var emailBusy = false
    @State private var emailError: String?
    @FocusState private var emailFocused: Bool
    @FocusState private var passwordFocused: Bool
    // LAN approval (2026-08-13): an authenticated surface on the same network
    // saw this TV's UDP beacon and requested approval. Non-nil renders the
    // Allow/Deny prompt; confirm goes through DeviceCodeAuth.lanConfirm.
    @State private var lanApprover: LanPendingInfo?
    @State private var lanConfirming = false
    /// Broadcasts the waiting code on UDP 19837 so same-network surfaces can
    /// approve without a QR scan. Started when a code is minted, stopped on
    /// sign-in / disappear / code rotation.
    @State private var beacon = LanApprovalBeacon()
    @State private var pollTask: Task<Void, Never>?
    @State private var fallbackPollTask: Task<Void, Never>?
    /// Non-nil while polls are failing to reach the backend — surfaced verbatim
    /// so "nothing is happening" is never indistinguishable from "the network is
    /// down". Cleared by the first successful poll.
    @State private var unreachable: String?
    /// Drives the elapsed / expires-in line. A wait with no clock on it reads as
    /// a hang.
    @State private var now = Date()
    @State private var waitingSince = Date()

    var body: some View {
        HStack(alignment: .top, spacing: 56) {
            ScrollView(.vertical, showsIndicators: false) {
              VStack(alignment: .leading, spacing: 14) {
                Image("yaver-login-wordmark-light")
                    .resizable()
                    .aspectRatio(contentMode: .fit)
                    .frame(width: 260, height: 104, alignment: .leading)
                    .accessibilityLabel("Yaver")
                    .accessibilityIdentifier("signin.yaver-logo")

                Text("Sign in to Yaver")
                    .font(.system(size: 44, weight: .heavy))
                    .padding(.bottom, 12)

                Text("1 · Email and password")
                    .font(.system(size: 20, weight: .bold))

                TextField("Email", text: $email)
                    .textFieldStyle(.plain)
                    .font(.system(size: 20))
                    .keyboardType(.emailAddress)
                    .textContentType(.emailAddress)
                    .autocapitalization(.none)
                    .disableAutocorrection(true)
                    .padding(.horizontal, 16).padding(.vertical, 12)
                    .background(.gray.opacity(0.18), in: RoundedRectangle(cornerRadius: 12))
                    .focused($emailFocused)
                    .accessibilityIdentifier("signin.email")
                SecureField("Password", text: $password)
                    .textFieldStyle(.plain)
                    .font(.system(size: 20))
                    .textContentType(.password)
                    .padding(.horizontal, 16).padding(.vertical, 12)
                    .background(.gray.opacity(0.18), in: RoundedRectangle(cornerRadius: 12))
                    .focused($passwordFocused)
                    .submitLabel(.go)
                    .onSubmit { handleEmailSignIn() }
                    .accessibilityIdentifier("signin.password")
                TVEmailSubmitButton(
                    title: emailBusy ? "Signing in…" : "Sign in with email",
                    isEnabled: !emailBusy && !email.trimmingCharacters(in: .whitespaces).isEmpty && !password.isEmpty,
                    action: handleEmailSignIn
                )
                .frame(width: 310, height: 72, alignment: .leading)

                if emailBusy {
                    Label("Contacting Yaver…", systemImage: "lock.shield")
                        .font(.system(size: 16, weight: .medium))
                        .foregroundStyle(.secondary)
                        .accessibilityIdentifier("signin.email-progress")
                } else if let emailError {
                    Text(emailError)
                        .font(.system(size: 16, weight: .medium))
                        .foregroundStyle(.orange)
                        .accessibilityIdentifier("signin.email-error")
                }

                Divider().padding(.vertical, 10)

                Text("2 · Scan with the Yaver app")
                    .font(.system(size: 18, weight: .semibold))
                    .foregroundStyle(.secondary)

                stepText("1. Scan the code with your phone, or visit yaver.io/auth/device in any browser")
                stepText("2. Sign in if asked, then tap Approve")
                stepText("3. This Apple TV signs in automatically")

                if DeviceCodeAuth.lastOwnerUserId != nil {
                    // Proactive approve event ("mobil onay"): this TV remembers
                    // its owner, so their signed-in Yaver app is being offered a
                    // one-tap approve — say so, and say how to verify (number
                    // match), instead of leaving the shortcut undiscoverable.
                    Label {
                        Text("We've also asked the Yaver app on your phone — approve there and check the code matches this screen.")
                            .font(.system(size: 17))
                            .foregroundStyle(.secondary)
                    } icon: {
                        Image(systemName: "iphone.gen3.radiowaves.left.and.right")
                            .foregroundStyle(.blue)
                    }
                    .padding(.top, 6)
                }

                if let code = start?.userCode {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("OR ENTER THIS CODE")
                            .font(.system(size: 15, weight: .bold)).tracking(2)
                            .foregroundStyle(.secondary)
                        Text(code)
                            .font(.system(size: 46, weight: .heavy, design: .monospaced))
                            .tracking(4)
                        // LAN approval number match (2026-08-13): approving
                        // surfaces on the same network show this same 3-digit
                        // number. Compare on both screens before approving.
                        if let mc = start?.matchCode {
                            HStack(spacing: 8) {
                                Text("Match code")
                                    .font(.system(size: 15)).foregroundStyle(.secondary)
                                Text(mc)
                                    .font(.system(size: 26, weight: .heavy, design: .monospaced))
                                    .foregroundStyle(.green)
                            }
                            .padding(.top, 2)
                        }
                    }
                    .padding(.top, 24)
                }

                if approving {
                    Label("Approved — signing in…", systemImage: "checkmark.circle.fill")
                        .font(.system(size: 22, weight: .semibold))
                        .foregroundStyle(.green).padding(.top, 20)
                } else if let approver = lanApprover, let s = start {
                    // LAN approval prompt (2026-08-13): an authenticated
                    // surface on the same network saw this TV's beacon and
                    // asked to sign it in. The user physically at the TV
                    // confirms Allow/Deny — the number-match guard against a
                    // rogue LAN request. The 60s window expires server-side.
                    VStack(alignment: .leading, spacing: 12) {
                        Text("Approve sign-in from \(approver.approverEmail ?? "your phone")?")
                            .font(.system(size: 24, weight: .semibold))
                        HStack(spacing: 8) {
                            Text("Match code")
                                .font(.system(size: 15)).foregroundStyle(.secondary)
                            Text(approver.matchCode ?? s.matchCode ?? "---")
                                .font(.system(size: 34, weight: .heavy, design: .monospaced))
                                .foregroundStyle(.green)
                        }
                        Text("The same number must show on the requesting device. Approving signs this TV into that account.")
                            .font(.system(size: 15))
                            .foregroundStyle(.secondary)
                        HStack(spacing: 14) {
                            Button(lanConfirming ? "Confirming…" : "Allow") {
                                confirmLan(allow: true)
                            }
                            .buttonStyle(.borderedProminent)
                            .disabled(lanConfirming)
                            Button("Deny") {
                                confirmLan(allow: false)
                            }
                            .buttonStyle(.bordered)
                            .disabled(lanConfirming)
                        }
                        .padding(.top, 4)
                    }
                    .padding(.top, 20)
                } else if let s = start {
                    // A quiet live indicator so the screen never looks frozen while
                    // it waits — the Netflix "waiting for you to enter the code" feel.
                    //
                    // It used to be JUST a spinner and "Waiting for approval…", with
                    // no elapsed time, no expiry, and no distinction between "you
                    // haven't approved yet" and "this TV can't reach Yaver". A user
                    // whose approval silently went nowhere had nothing on screen to
                    // tell them so — for the full 15 minutes until the code rotated.
                    VStack(alignment: .leading, spacing: 6) {
                        HStack(spacing: 10) {
                            ProgressView()
                            Text(unreachable == nil
                                 ? "Waiting for approval…"
                                 : "Can't reach Yaver — retrying every 5s")
                                .foregroundStyle(unreachable == nil ? Color.secondary : Color.orange)
                        }
                        Text(waitDetail(s))
                            .font(.system(size: 15))
                            .foregroundStyle(.secondary)
                        if let unreachable {
                            Text(unreachable)
                                .font(.system(size: 15))
                                .foregroundStyle(.orange)
                        }
                    }
                    .font(.system(size: 18)).padding(.top, 20)
                }

                if let error {
                    VStack(alignment: .leading, spacing: 10) {
                        Text(error).foregroundStyle(.orange)
                        Button("Try again") { Task { await begin() } }   // was: hang forever with no way out
                    }
                    .padding(.top, 16)
                }
                if expired { Text("Code expired — generating a new one…").foregroundStyle(.secondary).padding(.top, 8) }

              }
              .frame(maxWidth: .infinity, alignment: .leading)
              .padding(.bottom, 36)
            }
            .frame(maxWidth: 620, maxHeight: .infinity, alignment: .topLeading)

            ZStack {
                RoundedRectangle(cornerRadius: 24).fill(.white)
                if let url = start?.verifyURL, let img = qrImage(url.absoluteString) {
                    Image(uiImage: img)
                        .interpolation(.none)
                        .resizable()
                        .frame(width: 300, height: 300)
                } else {
                    ProgressView()
                }
            }
            .frame(width: 360, height: 360)
        }
        .padding(64)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .task { await begin() }
        .onReceive(Timer.publish(every: 1, on: .main, in: .common).autoconnect()) { now = $0 }
        .onDisappear {
            beacon.stop()
            pollTask?.cancel()
            fallbackPollTask?.cancel()
        }
    }

    private func stepText(_ s: String) -> some View {
        Text(s).font(.system(size: 22)).foregroundStyle(.secondary)
    }

    /// "1:42 elapsed · code expires in 13:18" — the two facts a waiting user
    /// actually needs: that time is passing, and how long this code is good for.
    private func waitDetail(_ s: DeviceCodeStart) -> String {
        let elapsed = max(0, now.timeIntervalSince(waitingSince))
        var line = "\(clock(elapsed)) elapsed"
        let remaining = s.expiresAt / 1000 - now.timeIntervalSince1970
        if remaining > 0 {
            line += " · code expires in \(clock(remaining))"
        } else {
            line += " · code expired — generating a new one"
        }
        return line
    }

    private func clock(_ seconds: TimeInterval) -> String {
        let total = Int(seconds.rounded())
        return String(format: "%d:%02d", total / 60, total % 60)
    }

    /// Email/password sign-in — the couch path for an account that isn't
    /// Apple-linked (or has 2FA). Same POST /auth/login the web + CLI use;
    /// the server's own rate limits and allowlist apply unchanged.
    private func handleEmailSignIn() {
        let cleanEmail = email.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cleanEmail.isEmpty, !password.isEmpty, !emailBusy else { return }
        emailBusy = true
        emailError = nil
        emailFocused = false
        passwordFocused = false
        Task {
            defer { emailBusy = false }
            do {
                let token = try await EmailAuth.signIn(email: cleanEmail, password: password)
                pollTask?.cancel()      // the device code is moot now
                fallbackPollTask?.cancel()
                store.signIn(token: token)
            } catch {
                // Includes 2FA ("use the QR"), invalid creds, lockout, and the
                // server's 403 allowlist message verbatim.
                self.emailError = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            }
        }
    }

    /// LAN approval, phase 2 (2026-08-13): the user at the TV presses
    /// Allow/Deny. Allow binds the requesting surface's account; Deny clears
    /// the pending window (the QR path stays usable). On Allow the code is
    /// authorized server-side and the normal claim flow picks up the token.
    private func confirmLan(allow: Bool) {
        guard let s = start, !lanConfirming else { return }
        lanConfirming = true
        error = nil
        Task {
            defer { lanConfirming = false }
            let r = await DeviceCodeAuth.lanConfirm(deviceCode: s.deviceCode, allow: allow)
            if allow, r.ok == true, let handle = r.claimHandle {
                // Authorized by the LAN approver — claim exactly like a QR
                // approval. Stop the beacon; the code is spent.
                beacon.stop()
                let claimed = await DeviceCodeAuth.claim(deviceCode: s.deviceCode, claimHandle: handle)
                if let token = claimed.token {
                    pollTask?.cancel()
                    fallbackPollTask?.cancel()
                    store.signIn(token: token)
                    return
                }
                error = "Approved — but the session couldn't be picked up yet. Retrying…"
                lanApprover = nil
            } else if !allow {
                lanApprover = nil // denied — back to the normal waiting screen
            } else if let reason = r.reason, reason == "no_pending_approver" {
                lanApprover = nil
                error = "The approval request expired. Ask the other device to try again."
            } else {
                lanApprover = nil
                error = r.reason ?? "Approval failed — try the QR code instead."
            }
        }
    }

    private func begin() async {
        error = nil
        expired = false
        unreachable = nil
        lanApprover = nil
        beacon.stop()
        do {
            let s = try await DeviceCodeAuth.start()
            start = s
            waitingSince = Date()
            now = Date()
            // Same-network discovery: broadcast the waiting code so an
            // authenticated surface on the LAN can approve without scanning.
            if let nonce = s.approveNonce, !nonce.isEmpty {
                beacon.start(machineName: "Apple TV",
                             approveNonce: nonce,
                             matchCode: s.matchCode ?? "000",
                             expiresAtMs: s.expiresAt)
            }
            startPolling(s)
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func startPolling(_ s: DeviceCodeStart) {
        pollTask?.cancel()
        fallbackPollTask?.cancel()
        pollTask = Task {
            while !Task.isCancelled {
                if Task.isCancelled { return }
                let r = await DeviceCodeAuth.waitEvent(deviceCode: s.deviceCode)
                if await handlePollResult(r, for: s) { return }
            }
        }
        fallbackPollTask = Task {
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 5_000_000_000)
                if Task.isCancelled { return }
                let r = await DeviceCodeAuth.poll(deviceCode: s.deviceCode)
                if await handlePollResult(r, for: s) { return }
            }
        }
    }

    private func handlePollResult(_ r: DevicePollResult, for s: DeviceCodeStart) async -> Bool {
        if store.isAuthenticated { return true }
        unreachable = r.unreachableReason
        switch DeviceCodeDeliveryDecision.decide(status: r.status, token: r.token, claimInFlight: approving) {
        case .signIn(let token):
            beacon.stop()
            pollTask?.cancel()
            fallbackPollTask?.cancel()
            store.signIn(token: token)
            return true
        case .claim:
            beacon.stop()
            approving = true
            let claimed = await DeviceCodeAuth.claim(deviceCode: s.deviceCode, claimHandle: r.claimHandle)
            if store.isAuthenticated { return true }
            if let token = claimed.token {
                pollTask?.cancel()
                fallbackPollTask?.cancel()
                store.signIn(token: token)
                return true
            }
            approving = false
            unreachable = claimed.unreachableReason ?? "Approved, but this Apple TV could not pick up the session yet. Retrying..."
            return false
        case .rotate:
            if rotatingCode { return true }
            rotatingCode = true
            defer { rotatingCode = false }
            expired = true
            await begin()
            return true
        case .wait:
            // LAN approval surfaced — render Allow/Deny while the 60s window
            // is open; clear the prompt once the window lapses server-side.
            if r.status == .pending, let pending = r.lanPending {
                if pending.expiresAt == nil || (pending.expiresAt ?? 0) > Date().timeIntervalSince1970 * 1000 {
                    lanApprover = pending
                } else {
                    lanApprover = nil
                }
            } else if r.status == .pending {
                lanApprover = nil
            }
            return false
        }
    }

    private func qrImage(_ string: String) -> UIImage? {
        let context = CIContext()
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(string.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 12, y: 12)),
              let cg = context.createCGImage(output, from: output.extent) else { return nil }
        return UIImage(cgImage: cg)
    }
}

/// UIKit owns the Select-press delivery for the primary login action.
///
/// App Review reproduced a focused SwiftUI Button that did not invoke its
/// action on tvOS 27. The text fields and surrounding layout remain SwiftUI,
/// but this one release-critical action uses tvOS's native focusable UIButton
/// so a painted focus ring and a delivered press cannot diverge.
private struct TVEmailSubmitButton: UIViewRepresentable {
    let title: String
    let isEnabled: Bool
    let action: () -> Void

    final class Coordinator: NSObject {
        var action: () -> Void

        init(action: @escaping () -> Void) {
            self.action = action
        }

        @objc func activate() {
            action()
        }
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(action: action)
    }

    func makeUIView(context: Context) -> UIButton {
        let button = UIButton(type: .system)
        button.accessibilityIdentifier = "signin.email-submit"
        button.addTarget(context.coordinator, action: #selector(Coordinator.activate), for: .primaryActionTriggered)
        return button
    }

    func updateUIView(_ button: UIButton, context: Context) {
        context.coordinator.action = action
        var configuration = UIButton.Configuration.filled()
        configuration.title = title
        configuration.cornerStyle = .capsule
        configuration.baseForegroundColor = .black
        configuration.baseBackgroundColor = UIColor(red: 0.78, green: 0.84, blue: 0.96, alpha: 1)
        configuration.contentInsets = NSDirectionalEdgeInsets(top: 14, leading: 24, bottom: 14, trailing: 24)
        configuration.titleTextAttributesTransformer = UIConfigurationTextAttributesTransformer { incoming in
            var outgoing = incoming
            outgoing.font = .systemFont(ofSize: 27, weight: .medium)
            return outgoing
        }
        button.configuration = configuration
        button.isEnabled = isEnabled
    }
}
