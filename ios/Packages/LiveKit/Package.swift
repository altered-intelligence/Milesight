// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "LiveKit",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [.library(name: "LiveKit", targets: ["LiveKit"])],
    dependencies: [.package(path: "../CoreKit")],
    targets: [
        .target(name: "LiveKit", dependencies: ["CoreKit"])
    ]
)
