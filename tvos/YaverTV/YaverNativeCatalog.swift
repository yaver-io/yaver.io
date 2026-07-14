import Foundation

enum YaverNativeCatalog {
    static let manifestSchemaVersion = "1"
    static let authProvider = "yaver-oauth"

    // `tvPrimaryApps` / `tvSummary` lived here: a hardcoded list of the author's
    // own projects ("SFMG", "Carrotbet", "Personal Runtime") rendered as a tile
    // subtitle on every user's Apple TV. Yaver is for everyone — a stranger's
    // project names are not a feature, and a betting brand on a dashboard headed
    // to App Review is a liability. Removed with the tile that showed it.
    // If a real catalog lands, it must come from the user's own box.
}
