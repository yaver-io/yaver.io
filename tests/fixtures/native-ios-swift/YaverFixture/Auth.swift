// Yaver native-ios-swift fixture authentication helper.
// Hardcoded creds (admin/admin) — DO NOT use as production auth pattern.

import Foundation

public enum Auth {
    public static let validUsername = "admin"
    public static let validPassword = "admin"

    public static func authenticate(username: String, password: String) -> Bool {
        return username == validUsername && password == validPassword
    }
}
