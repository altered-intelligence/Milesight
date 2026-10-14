// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "CommandsKit",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [.library(name: "CommandsKit", targets: ["CommandsKit"])],
    dependencies: [.package(path: "../CoreKit"), .package(path: "../LiveKit")],
    targets: [
        .target(name: "CommandsKit", dependencies: ["CoreKit", "LiveKit"]),
        .testTarget(name: "CommandsKitTests", dependencies: ["CommandsKit"])
    ]
)
