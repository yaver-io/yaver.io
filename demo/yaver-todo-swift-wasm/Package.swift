// swift-tools-version:5.9
import PackageDescription

// Yaver todo — Swift → WebAssembly fixture.
//
// Built on JavaScriptKit, NOT Tokamak. Tokamak was the obvious choice (a
// SwiftUI-compatible API) and it is ABANDONED: last release 0.11.1 in Nov 2022,
// last commit Feb 2023, and it fails to build against the only SwiftWasm SDK
// releases that exist (6.2/6.3) with "could not build C module CoreFoundation".
//
// JavaScriptKit is actively maintained — it ships the PackageToJS plugin the
// modern toolchain uses. The trade is that this is DOM-style Swift rather than
// SwiftUI-style. For proving the pipe (Swift → wasm → browser → WebRTC) that
// distinction does not matter; for selling "SwiftUI on Linux" it would, which is
// exactly why the audit no longer claims that.
let package = Package(
    name: "TodoApp",
    dependencies: [
        .package(url: "https://github.com/swiftwasm/JavaScriptKit", from: "0.19.0"),
    ],
    targets: [
        .executableTarget(
            name: "TodoApp",
            dependencies: [
                .product(name: "JavaScriptKit", package: "JavaScriptKit"),
            ]
        ),
    ]
)
