// OAuthSignIn.swift — in-app OAuth for the surfaces that have a browser.
//
// This is the SAME flow the phone runs (mobile/app/login.tsx::handleOAuth), on
// the same endpoints, so every provider Yaver already supports — and all of the
// account-linking machinery behind them — works here with no backend change:
//
//   open   https://yaver.io/api/auth/oauth/<provider>?client=mobile
//   return yaver://oauth-callback?token=<session token>
//
// `client=mobile` is what makes the web callback redirect to the `yaver://`
// scheme instead of back into the web dashboard (web/app/api/auth/oauth/
// [provider]/route.ts). ASWebAuthenticationSession intercepts that redirect
// itself, so the app does not need to register the URL type or field a deep
// link — the token comes back on the awaited call and cannot be lost to a
// route-mount race. That is the same reason the phone moved to
// openAuthSessionAsync.
//
// NOT AVAILABLE ON tvOS, and that is a platform fact rather than an omission:
// tvOS ships no browser and no ASWebAuthenticationSession, which is the whole
// reason the device-code + QR flow exists there. A TV genuinely cannot run an
// OAuth consent screen, so it borrows a phone that can. A Vision Pro can — it
// has Safari, a keyboard, and Optic ID autofill — so it should not be borrowing
// anything.
#if !os(tvOS)

import AuthenticationServices
import Foundation

enum OAuthProvider: String, CaseIterable, Identifiable {
    case google, microsoft, github, gitlab

    var id: String { rawValue }

    var label: String {
        switch self {
        case .google: return "Google"
        case .microsoft: return "Microsoft"
        case .github: return "GitHub"
        case .gitlab: return "GitLab"
        }
    }

    /// SF Symbol. Apple ships no brand marks, and shipping our own copies of
    /// third-party logos is a trademark question we don't need to open for a
    /// sign-in row — a neutral glyph plus the provider's name is unambiguous.
    var symbol: String {
        switch self {
        case .google: return "globe"
        case .microsoft: return "square.grid.2x2.fill"
        case .github: return "chevron.left.forwardslash.chevron.right"
        case .gitlab: return "arrow.triangle.branch"
        }
    }
}

enum OAuthSignInError: Error, LocalizedError {
    case cancelled
    case noToken
    case failed(String)

    var errorDescription: String? {
        switch self {
        case .cancelled: return nil
        case .noToken: return "Sign-in finished but Yaver didn't return a session token."
        case .failed(let message): return message
        }
    }
}

@MainActor
final class OAuthSignIn: NSObject, ASWebAuthenticationPresentationContextProviding {

    private var session: ASWebAuthenticationSession?

    /// Run the provider's consent screen and come back with a Yaver session token.
    func signIn(with provider: OAuthProvider) async throws -> String {
        var comps = URLComponents(
            url: Backend.webBaseURL
                .appendingPathComponent("api/auth/oauth")
                .appendingPathComponent(provider.rawValue),
            resolvingAgainstBaseURL: false
        )!
        // `client=mobile` selects the yaver:// redirect. Without it the callback
        // bounces into the web dashboard and the token never reaches the app.
        comps.queryItems = [URLQueryItem(name: "client", value: "mobile")]
        guard let url = comps.url else {
            throw OAuthSignInError.failed("Couldn't build the sign-in URL.")
        }

        return try await withCheckedThrowingContinuation { continuation in
            let session = ASWebAuthenticationSession(
                url: url,
                callbackURLScheme: "yaver"
            ) { callbackURL, error in
                if let error {
                    let code = (error as? ASWebAuthenticationSessionError)?.code
                    continuation.resume(
                        throwing: code == .canceledLogin
                            ? OAuthSignInError.cancelled
                            : OAuthSignInError.failed(error.localizedDescription)
                    )
                    return
                }
                guard
                    let callbackURL,
                    let token = URLComponents(url: callbackURL, resolvingAgainstBaseURL: false)?
                        .queryItems?
                        .first(where: { $0.name == "token" })?
                        .value,
                    !token.isEmpty
                else {
                    continuation.resume(throwing: OAuthSignInError.noToken)
                    return
                }
                continuation.resume(returning: token)
            }

            session.presentationContextProvider = self
            // Do NOT share the browser's cookie jar. A headset is a device people
            // hand to each other; an ephemeral session means "sign in" always
            // asks who you are rather than silently reusing whoever was last in
            // Safari.
            session.prefersEphemeralWebBrowserSession = true

            self.session = session
            if !session.start() {
                continuation.resume(throwing: OAuthSignInError.failed("Couldn't open the sign-in window."))
            }
        }
    }

    func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor {
        ASPresentationAnchor()
    }
}

#endif
