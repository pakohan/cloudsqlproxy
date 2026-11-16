// swift-tools-version: 5.9
import Foundation
import PackageDescription

// The Go core is built by `make core` into build/libcore.a and build/libcore.h.
let buildDir = URL(fileURLWithPath: #filePath).deletingLastPathComponent().appendingPathComponent("build").path

let package = Package(
    name: "CloudSQLProxy",
    platforms: [.macOS(.v14)],
    targets: [
        .systemLibrary(name: "CCore", path: "Sources/CCore"),
        .executableTarget(
            name: "CloudSQLProxy",
            dependencies: ["CCore"],
            linkerSettings: [
                .unsafeFlags(["-L", buildDir]),
                // Needed by the Go runtime and crypto/x509.
                .linkedFramework("CoreFoundation"),
                .linkedFramework("Security"),
                .linkedLibrary("resolv"),
            ]
        ),
    ]
)
