// swift-tools-version:5.9
//
// The Orbit RMM agent for macOS, in Swift. Windows and Linux stay on the Go
// binary in this same repository (../cmd, ../internal); this package builds only
// the darwin agent and must keep byte-for-byte wire parity with the Go agent and
// the Python server (../../Orbit MDM). See ../PROTOCOL.md for the contract.
//
// Built per the existing macOS precedent: a universal binary (arm64 + x86_64),
// min macOS 12. CryptoKit, Network.framework and Foundation are system
// frameworks, so there are no external package dependencies; CZlib links the
// system libz, which the RFB/VNC server needs (Compression.framework can't
// reproduce the Go zlib stream framing).
import PackageDescription

let package = Package(
    name: "orbit-agent",
    platforms: [.macOS(.v12)],
    targets: [
        // System zlib, for the RFB/VNC server's per-session zlib stream.
        .systemLibrary(name: "CZlib", path: "Sources/CZlib"),

        // The agent's library: wire protocol, crypto, config/status, system and
        // service integration. Split into folders now; can become separate
        // targets later without moving the public API.
        .target(
            name: "OrbitAgentKit",
            dependencies: ["CZlib"],
            path: "Sources/OrbitAgentKit"
        ),

        // The command-line entry point the service, installers and postinstall run.
        .executableTarget(
            name: "orbit-agent",
            dependencies: ["OrbitAgentKit"],
            path: "Sources/orbit-agent"
        ),

        .testTarget(
            name: "OrbitAgentKitTests",
            dependencies: ["OrbitAgentKit"],
            path: "Tests/OrbitAgentKitTests",
            // Golden vectors generated from the Go agent, shared across Go, Swift
            // and the Python server. See ../scripts/gen_vectors.
            resources: [.copy("Vectors")]
        ),
    ]
)
