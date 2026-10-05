// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "CommandsKit",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [.library(name: "CommandsKit", targets: ["CommandsKit"])],
    dependencies: [.package(path: "../CoreKit")],
    targets: [
        .target(name: "CommandsKit", dependencies: ["CoreKit"])
    ]
)
