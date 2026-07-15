// VisionSignInView.swift — device-code sign-in for Vision Pro.
//
// Same Convex contract as tvOS (DeviceCodeAuth.start → poll → store.signIn).
// The PRESENTATION is deliberately not the same, and that is the whole point of
// this file existing separately.
//
// The TV screen says "scan this QR with your phone". On a television that works:
// the code is on a real screen, and the phone's camera can see it. In a headset
// it is a physical impossibility — the QR lives on a virtual plane inside the
// display, and the phone's camera is pointed at your living room. You are WEARING
// the thing you are being asked to photograph. That instruction could never be
// followed by anyone, and it was the first thing a new Vision Pro user hit.
//
// What is actually true while wearing a Vision Pro:
//
//   * The headset has Safari, a keyboard, and Optic ID autofill. It can simply
//     sign in BY ITSELF — no second device in the loop at all. That is the
//     primary path here, and it is strictly better than the TV's flow rather
//     than a workaround for it.
//   * You can still see your phone, through passthrough. So reading a short code
//     off the panel and typing it into the phone is possible — that is the
//     fallback, and it is why the code is set large and legible.
//   * You cannot photograph a virtual object. So there is no QR.

import AuthenticationServices
import SwiftUI

struct VisionSignInView: View {
    @EnvironmentObject var store: YaverStore
    @Environment(\.openURL) private var openURL

    @State private var start: DeviceCodeStart?
    @State private var error: String?
    @State private var expired = false
    @State private var opened = false
    @State private var appleBusy = false
    @State private var oauthBusy: OAuthProvider?
    @State private var pollTask: Task<Void, Never>?

    // Held for the lifetime of the view: ASWebAuthenticationSession is cancelled
    // the moment its owner is deallocated, so a locally-scoped one closes the
    // consent window in the user's face.
    @State private var oauth = OAuthSignIn()

    var body: some View {
        // Three panels do not fit the window at a comfortable reading distance —
        // unscrolled, the title clipped off the top and the fallback code clipped
        // off the bottom, which is a poor way to present the one thing a stuck
        // user needs to read.
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                VStack(alignment: .leading, spacing: 8) {
                    Text("Sign in to Yaver")
                        .font(.extraLargeTitle2)
                    Text("You can do this without taking the headset off.")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                }

                // Errors go ABOVE the panels, not below them. Underneath three
                // stacked panels the message renders off the bottom of the
                // scroll view — so a failed sign-in looked like a button that
                // did nothing at all, which is the exact bug this surface keeps
                // having. A failure the user has to scroll to find is a failure
                // they will never see.
                if let error {
                    Label(error, systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.orange)
                        .fixedSize(horizontal: false, vertical: true)
                        .padding(16)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16))
                }
                if expired {
                    Label("Code expired — generating a new one…", systemImage: "clock.arrow.circlepath")
                        .foregroundStyle(.secondary)
                }

                applePanel
                safariPanel
                phonePanel
            }
            .frame(maxWidth: 760, alignment: .leading)
            .padding(44)
        }
        .glassBackgroundEffect()
        .task { await begin() }
        .onDisappear { pollTask?.cancel() }
    }

    // MARK: - Fast path: the Apple ID already on this headset
    //
    // No code, no QR, no second device: look, pinch, Optic ID. This is the only
    // path that finishes in seconds, so it leads.

    private var applePanel: some View {
        VStack(alignment: .leading, spacing: 12) {
            Label("Fastest — you're already signed in to Apple", systemImage: "bolt.fill")
                .font(.title2.bold())

            Text("Uses the Apple ID on this headset. Optic ID confirms it — nothing to type.")
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            SignInWithAppleButton(.signIn) { request in
                request.requestedScopes = [.fullName, .email]
            } onCompletion: { result in
                Task { await handleApple(result) }
            }
            .signInWithAppleButtonStyle(.white)
            .frame(height: 56)
            .disabled(appleBusy)

            if appleBusy {
                ProgressView().controlSize(.small)
            }
        }
        .padding(24)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 22))
    }

    // MARK: - Everything that isn't Apple: Google, Microsoft, GitHub, passkey, email

    private var safariPanel: some View {
        VStack(alignment: .leading, spacing: 12) {
            Label("Or use another account", systemImage: "person.badge.key.fill")
                .font(.title3.bold())

            Text("The same providers as the phone, signed in right here. Use one of these if your Yaver account doesn't use Apple — or if it has two-factor turned on.")
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            // Real provider buttons, running the SAME OAuth endpoints the phone
            // uses — so every provider Yaver supports, and the account-linking
            // behind them, works here with no backend change. A headset has a
            // browser; it does not need to borrow a phone to run a consent screen.
            ForEach(OAuthProvider.allCases) { provider in
                Button {
                    Task { await handleOAuth(provider) }
                } label: {
                    Label("Continue with \(provider.label)", systemImage: provider.symbol)
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.bordered)
                .controlSize(.large)
                .disabled(oauthBusy != nil)
            }

            if let oauthBusy {
                HStack(spacing: 8) {
                    ProgressView().controlSize(.small)
                    Text("Waiting for \(oauthBusy.label)…").foregroundStyle(.secondary)
                }
            }

            // Passkey and email/password live on the web page rather than as
            // native buttons — they need a full form, which the OAuth session
            // gives us for free.
            Button {
                guard let url = start?.verifyURL else { return }
                opened = true
                openURL(url)
            } label: {
                Label(opened ? "Waiting for approval…" : "Passkey or email — open yaver.io",
                      systemImage: opened ? "hourglass" : "safari")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderless)
            .controlSize(.small)
            .disabled(start == nil)
            .padding(.top, 4)
        }
        .padding(24)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 22))
    }

    // MARK: - Fallback: type the code on a phone you can see through passthrough

    private var phonePanel: some View {
        VStack(alignment: .leading, spacing: 12) {
            Label("Or approve from your phone", systemImage: "iphone")
                .font(.title3.bold())

            Text("Open Yaver on your phone (you can see it through passthrough), go to Approve Device, and enter this code.")
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            if let code = start?.userCode {
                // Set big and monospaced on purpose: this is read off a floating
                // panel and typed into a device held below it.
                Text(code)
                    .font(.system(size: 44, weight: .heavy, design: .monospaced))
                    .tracking(6)
                    .textSelection(.enabled)
                    .accessibilityLabel("Sign-in code \(code.map(String.init).joined(separator: " "))")
            } else {
                ProgressView().controlSize(.small)
            }
        }
        .padding(24)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 22))
    }

    // MARK: - Apple

    private func handleApple(_ result: Result<ASAuthorization, Error>) async {
        error = nil
        switch result {
        case .failure(let err):
            // A user who backs out of the Apple sheet has not hit an error; do
            // not shout at them for changing their mind.
            let code = (err as? ASAuthorizationError)?.code
            if code == .canceled { return }

            // Apple's own message for the common failures here is "The operation
            // couldn't be completed. (com.apple.AuthenticationServices error
            // 1000.)", which tells a user precisely nothing and sends a developer
            // hunting. The two things that actually cause it are worth naming,
            // because both are fixable in about ten seconds:
            //
            //   * the device (very often a Simulator) is not signed in to an
            //     Apple ID at all — Settings ▸ Sign in;
            //   * the build carries no Sign-in-with-Apple entitlement, which is
            //     what happens to every CODE_SIGNING_ALLOWED=NO simulator build,
            //     entitlements included. The other two paths below still work,
            //     so the user is never stuck on this.
            if code == .unknown || code == .failed || code == .notHandled {
                error = "Apple sign-in isn't available on this device — it needs an Apple ID signed in (Settings ▸ Sign in), and a build signed with the Sign in with Apple entitlement. Use one of the options below instead."
            } else {
                error = err.localizedDescription
            }

        case .success(let authorization):
            appleBusy = true
            defer { appleBusy = false }
            do {
                let token = try await AppleNativeAuth.completeSignIn(with: authorization)
                pollTask?.cancel()      // the device code is moot now
                store.signIn(token: token)
            } catch {
                // Includes the two cases Apple sign-in genuinely cannot serve:
                // an account with 2FA, and an account that signs in with a
                // different provider (where Apple would fork a second, empty
                // account). Both messages name the way out — the Safari panel
                // directly below is it.
                self.error = error.localizedDescription
            }
        }
    }

    // MARK: - OAuth (Google / Microsoft / GitHub / GitLab)

    private func handleOAuth(_ provider: OAuthProvider) async {
        error = nil
        oauthBusy = provider
        defer { oauthBusy = nil }
        do {
            let token = try await oauth.signIn(with: provider)
            pollTask?.cancel()      // the device code is moot now
            store.signIn(token: token)
        } catch OAuthSignInError.cancelled {
            return                  // backing out is a choice, not an error
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - Device-code flow (unchanged contract)

    private func begin() async {
        error = nil
        expired = false
        opened = false
        do {
            // Register as what we actually are. The shared default is "tvos",
            // which would file this headset under Apple TV in the device list.
            let s = try await DeviceCodeAuth.start(
                machineName: "Apple Vision Pro",
                platform: "visionos",
                environment: "vision"
            )
            start = s
            startPolling(s)
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func startPolling(_ s: DeviceCodeStart) {
        pollTask?.cancel()
        pollTask = Task {
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 5_000_000_000)
                if Task.isCancelled { return }
                let r = await DeviceCodeAuth.poll(deviceCode: s.deviceCode)
                switch r.status {
                case .authorized:
                    if let token = r.token { store.signIn(token: token) }
                    return
                case .expired:
                    expired = true
                    await begin()
                    return
                case .pending:
                    continue
                }
            }
        }
    }
}
