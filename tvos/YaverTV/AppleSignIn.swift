// AppleSignIn.swift — native Sign in with Apple for the lean-back surfaces
// (tvOS + visionOS). Shared, like the rest of the client layer.
//
// This is the FAST PATH, and it is fast because it removes the second device
// entirely. The device-code flow (Backend.swift) is a workaround for a screen
// that cannot type: it asks the user to carry a short code to a phone or a
// browser and approve it there. But an Apple TV and a Vision Pro are already
// signed into an Apple ID — the identity is sitting right there on the device.
// Native Sign in with Apple hands it over directly:
//
//   Vision Pro : look at the button, pinch, Optic ID confirms  → signed in
//   Apple TV   : click, Touch ID on the paired iPhone or the TV's own confirm
//
// No code, no QR, no transcription, no second device. Seconds, not a minute.
//
// The backend contract already exists and is shared with the phone:
// `POST /auth/apple-native { identityToken, fullName }` (backend/convex/http.ts).
// It verifies the token's RS256 signature against Apple's JWKS with
// `audience: io.yaver.mobile` — which is exactly the bundle ID tvOS and visionOS
// already ship under (Universal Purchase), so the same endpoint accepts all
// three surfaces with no backend change.
//
// The device-code path stays as the fallback, because native Apple sign-in is
// not sufficient on its own:
//   * an account with 2FA gets `requires2fa` and no token — it MUST finish
//     somewhere that can show a TOTP prompt;
//   * an account that isn't an Apple account at all (Google, Microsoft, GitHub,
//     GitLab, passkey, email) has nothing to hand over here.
// Both are real users, so neither can be left at a dead end.

import AuthenticationServices
import Foundation

enum AppleNativeAuthError: Error, LocalizedError {
    /// The account has TOTP on. The native endpoint deliberately refuses to mint
    /// a session here (it returns a pendingToken instead), because a lean-back
    /// surface has nowhere good to prompt for a 6-digit code. Send them to the
    /// browser/phone path rather than pretending this failed.
    case requiresTwoFactor
    case noIdentityToken

    /// This email already signs into Yaver, but NOT with Apple.
    ///
    /// Yaver resolves an OAuth login strictly by linked provider identity and
    /// deliberately NOT by email (auth.ts::findUserForOAuth — the email argument
    /// is `_email`, unused). So signing in with Apple on an account that was
    /// created with Google does not find that account: it silently creates a
    /// SECOND one, with the same email, no linked machines, and nothing in it.
    /// On a headset that reads as "all my machines are gone".
    ///
    /// So the fast path checks first and refuses rather than stranding someone in
    /// an empty account they didn't ask for. Naming the providers they DO use is
    /// the whole value — "use Google" is actionable, "sign-in failed" is not.
    case accountUsesDifferentProvider(providers: [String])

    case failed(String)

    var errorDescription: String? {
        switch self {
        case .requiresTwoFactor:
            return "This account uses two-factor authentication. Finish signing in with Safari or approve from your phone."
        case .noIdentityToken:
            return "Apple didn't return an identity token. Try Safari or approve from your phone."
        case .accountUsesDifferentProvider(let providers):
            let names = providers.map(Self.displayName).joined(separator: " or ")
            return "This email already signs in to Yaver with \(names), not Apple. Signing in with Apple would start a separate, empty account. Use “Sign in with Safari” and choose \(names)."
        case .failed(let message):
            return message
        }
    }

    private static func displayName(_ provider: String) -> String {
        switch provider {
        case "google": return "Google"
        case "microsoft": return "Microsoft"
        case "github": return "GitHub"
        case "gitlab": return "GitLab"
        case "apple": return "Apple"
        case "email": return "email and password"
        default: return provider
        }
    }
}

/// Which sign-in methods an email already uses. Anonymous lookup — the backend
/// returns the same shape whether or not the address exists, so this cannot be
/// used to enumerate accounts.
private struct ExistingProviders: Decodable {
    let exists: Bool
    let providers: [String]
    let hasPasskey: Bool
}

enum AppleNativeAuth {
    /// Exchange Apple's identity token for a Yaver session token.
    static func exchange(identityToken: String, fullName: String?) async throws -> String {
        var req = URLRequest(url: Backend.convexSiteURL.appendingPathComponent("auth/apple-native"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")

        var body: [String: Any] = ["identityToken": identityToken]
        if let fullName, !fullName.isEmpty { body["fullName"] = fullName }
        req.httpBody = try JSONSerialization.data(withJSONObject: body)

        let (data, resp) = try await URLSession.shared.data(for: req)
        let obj = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]

        guard let http = resp as? HTTPURLResponse else {
            throw AppleNativeAuthError.failed("No response from Yaver.")
        }
        guard (200..<300).contains(http.statusCode) else {
            throw AppleNativeAuthError.failed(
                (obj?["error"] as? String) ?? "Apple sign-in failed (\(http.statusCode))."
            )
        }
        if obj?["requires2fa"] as? Bool == true {
            throw AppleNativeAuthError.requiresTwoFactor
        }
        guard let token = obj?["token"] as? String, !token.isEmpty else {
            throw AppleNativeAuthError.failed("Yaver didn't return a session token.")
        }
        return token
    }

    /// Would signing in with Apple land this person in the account they already
    /// have — or in a brand-new empty one?
    ///
    /// Called BEFORE `exchange`, because `exchange` mints. Once the session
    /// exists the fork has already happened and the user is looking at an empty
    /// machine list wondering where their box went.
    private static func assertAppleWontForkTheAccount(email: String) async throws {
        guard !email.isEmpty else { return }  // private-relay / no email: nothing to check

        var comps = URLComponents(
            url: Backend.convexSiteURL.appendingPathComponent("auth/email-providers"),
            resolvingAgainstBaseURL: false
        )!
        comps.queryItems = [URLQueryItem(name: "email", value: email)]
        guard let url = comps.url else { return }

        // Fail OPEN: this is a guard rail, not a gate. If the lookup is down we
        // let the sign-in proceed rather than block the one path that works.
        guard
            let (data, resp) = try? await URLSession.shared.data(from: url),
            let http = resp as? HTTPURLResponse, (200..<300).contains(http.statusCode),
            let found = try? JSONDecoder().decode(ExistingProviders.self, from: data)
        else { return }

        guard found.exists, !found.providers.isEmpty, !found.providers.contains("apple") else { return }
        throw AppleNativeAuthError.accountUsesDifferentProvider(providers: found.providers)
    }

    /// The email Apple asserted, read straight off the identity token's payload.
    ///
    /// Decoded WITHOUT verifying the signature, and that is fine precisely
    /// because nothing is trusted on the strength of it: it is used only to ask
    /// "which providers does this address already use", a question whose answer
    /// is public and unauthenticated anyway. The token that actually mints a
    /// session is verified server-side against Apple's JWKS (http.ts).
    private static func emailClaim(fromIdentityToken token: String) -> String {
        let parts = token.split(separator: ".")
        guard parts.count >= 2 else { return "" }

        var b64 = String(parts[1])
            .replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")
        while b64.count % 4 != 0 { b64 += "=" }

        guard
            let data = Data(base64Encoded: b64),
            let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
            let email = obj["email"] as? String
        else { return "" }
        return email.lowercased()
    }

    /// Pull the identity token + name out of an ASAuthorization result and trade
    /// it for a session token.
    static func completeSignIn(with authorization: ASAuthorization) async throws -> String {
        guard
            let credential = authorization.credential as? ASAuthorizationAppleIDCredential,
            let tokenData = credential.identityToken,
            let identityToken = String(data: tokenData, encoding: .utf8)
        else {
            throw AppleNativeAuthError.noIdentityToken
        }

        try await assertAppleWontForkTheAccount(email: emailClaim(fromIdentityToken: identityToken))

        // Apple only sends the name on the FIRST authorization for an app; every
        // later sign-in has nil here. That's fine — the backend keeps the name it
        // already stored and only writes a non-empty one.
        let name = [credential.fullName?.givenName, credential.fullName?.familyName]
            .compactMap { $0 }
            .joined(separator: " ")

        return try await exchange(identityToken: identityToken, fullName: name)
    }
}
