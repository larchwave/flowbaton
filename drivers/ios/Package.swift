// swift-tools-version: 6.0

import PackageDescription

let package = Package(
    name: "FlowBatonIOSRunner",
    platforms: [
        .iOS(.v17),
        .macOS(.v14),
    ],
    products: [
        .library(
            name: "FlowBatonIOSRunner",
            targets: ["FlowBatonIOSRunner"]
        ),
    ],
    targets: [
        .target(name: "FlowBatonIOSRunner"),
        .testTarget(
            name: "FlowBatonIOSRunnerTests",
            dependencies: ["FlowBatonIOSRunner"]
        ),
    ]
)
