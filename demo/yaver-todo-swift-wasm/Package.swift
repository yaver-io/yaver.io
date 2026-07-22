// swift-tools-version:5.9
import PackageDescription

// Yaver todo — Tokamak / SwiftWasm fixture.
//
// The SAME todo UX as yaver-todo-rn, in Swift, so any difference you observe
// between them is the TRANSPORT and not the app. That is the whole point of the
// fixture family: the app is the control.
//
// Detected by desktop/agent/swift_project_detect.go as SwiftKindTokamak, which
// routes it to chrome-webrtc on a Linux Cloud Workspace — no Mac, no simulator.
let package = Package(
    name: "TodoApp",
    platforms: [.macOS(.v12)],
    dependencies: [
        .package(url: "https://github.com/TokamakUI/Tokamak", from: "0.11.0"),
    ],
    targets: [
        .executableTarget(
            name: "TodoApp",
            dependencies: [
                .product(name: "TokamakShim", package: "Tokamak"),
            ]
        ),
    ]
)
