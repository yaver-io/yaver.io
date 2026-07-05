import Foundation

enum YaverNativeCatalog {
    static let manifestSchemaVersion = "1"
    static let authProvider = "yaver-oauth"

    static let tvPrimaryApps = [
        "SFMG",
        "Carrotbet",
        "Personal Runtime",
    ]

    static var tvSummary: String {
        tvPrimaryApps.joined(separator: " · ")
    }
}
