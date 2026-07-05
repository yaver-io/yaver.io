import Foundation

enum YaverNativeCatalog {
    static let manifestSchemaVersion = "1"
    static let authProvider = "yaver-oauth"
    static let watchRole = "companion"
    static let companionApps = ["SFMG", "Carrotbet", "Personal Runtime", "Personal Health Agent"]
    static var companionSummary: String {
        companionApps.joined(separator: " · ")
    }
}
