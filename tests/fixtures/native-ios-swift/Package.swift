// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "NativeIOSSwiftFixture",
    platforms: [.iOS(.v17)],
    products: [
        .library(name: "NativeIOSSwiftFixture", targets: ["NativeIOSSwiftFixture"]),
    ],
    targets: [
        .target(name: "NativeIOSSwiftFixture"),
    ]
)
