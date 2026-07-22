// swift-tools-version:5.7
import PackageDescription

// Yaver Feedback SDK for Swift / iOS.
//
// ZERO DEPENDENCIES on purpose. This drops into third-party apps, and every
// transitive dependency is one their build might already have at a conflicting
// version — a feedback SDK is never worth a dependency conflict.
let package = Package(
    name: "YaverFeedback",
    platforms: [.iOS(.v13)],
    products: [
        .library(name: "YaverFeedback", targets: ["YaverFeedback"]),
    ],
    targets: [
        .target(name: "YaverFeedback", path: "Sources/YaverFeedback"),
    ]
)
