// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "NotificationsKit",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [.library(name: "NotificationsKit", targets: ["NotificationsKit"])],
    targets: [
        .target(name: "NotificationsKit")
    ]
)
