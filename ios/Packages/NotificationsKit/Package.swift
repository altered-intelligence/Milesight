// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "NotificationsKit",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [.library(name: "NotificationsKit", targets: ["NotificationsKit"])],
    dependencies: [.package(path: "../CoreKit")],
    targets: [
        .target(name: "NotificationsKit", dependencies: ["CoreKit"]),
        .testTarget(name: "NotificationsKitTests", dependencies: ["NotificationsKit"])
    ]
)
