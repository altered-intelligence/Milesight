// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "AnalyticsKit",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [.library(name: "AnalyticsKit", targets: ["AnalyticsKit"])],
    dependencies: [.package(path: "../CoreKit")],
    targets: [
        .target(name: "AnalyticsKit", dependencies: ["CoreKit"])
    ]
)
