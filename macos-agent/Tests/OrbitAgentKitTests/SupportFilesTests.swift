import XCTest
@testable import OrbitAgentKit

/// Config and status file behaviour: round-trips, atomic writes, permissions, and
/// the `,omitempty` semantics that must match the Go agent's output.
final class SupportFilesTests: XCTestCase {

    private var home: String!

    override func setUp() {
        super.setUp()
        home = NSTemporaryDirectory() + "orbit-agent-test-\(UUID().uuidString)"
        try? FileManager.default.createDirectory(atPath: home, withIntermediateDirectories: true)
        setenv("ORBIT_AGENT_HOME", home, 1)
    }

    override func tearDown() {
        unsetenv("ORBIT_AGENT_HOME")
        try? FileManager.default.removeItem(atPath: home)
        super.tearDown()
    }

    func testConfigRoundTripAndPermissions() throws {
        let cfg = AgentConfig(server: "https://mdm.example.com", agentID: "agent-1", secret: "orba_x",
                              serverKey: "a2V5", checkinSeconds: 60, device: "dev-1")
        try cfg.save()

        let attrs = try FileManager.default.attributesOfItem(atPath: Paths.configPath)
        XCTAssertEqual((attrs[.posixPermissions] as? NSNumber)?.int16Value, 0o600)

        // pulse_addr / pulse_seed omitted when nil; support present as {}.
        let raw = String(data: try Data(contentsOf: URL(fileURLWithPath: Paths.configPath)), encoding: .utf8)!
        XCTAssertFalse(raw.contains("pulse_addr"))
        XCTAssertFalse(raw.contains("pulse_seed"))
        XCTAssertTrue(raw.contains("\"support\""))

        let loaded = try AgentConfig.load()
        XCTAssertEqual(loaded.server, "https://mdm.example.com")
        XCTAssertEqual(loaded.agentID, "agent-1")
        XCTAssertEqual(loaded.serverKey, "a2V5")
        XCTAssertEqual(loaded.checkinSeconds, 60)
    }

    func testLoadNotEnrolled() {
        XCTAssertThrowsError(try AgentConfig.load()) {
            XCTAssertEqual($0 as? AgentConfig.ConfigError, .notEnrolled)
        }
    }

    func testCheckinSecondsFloor() throws {
        let cfg = AgentConfig(server: "s", agentID: "a", secret: "x", serverKey: "k",
                              checkinSeconds: 0, device: "d")
        try cfg.save()
        XCTAssertEqual(try AgentConfig.load().checkinSeconds, 60)
    }

    func testPulseKeyStableAcrossCalls() throws {
        let cfg = AgentConfig(server: "s", agentID: "a", secret: "x", serverKey: "k",
                              checkinSeconds: 60, device: "d")
        try cfg.save()
        let k1 = try cfg.pulseKey()
        let k2 = try cfg.pulseKey()
        XCTAssertEqual(k1.rawRepresentation, k2.rawRepresentation)
        // Persisted and reloadable.
        XCTAssertEqual(try AgentConfig.load().pulseSeed, cfg.pulseSeed)
    }

    func testStatusOmitemptyAndRoundTrip() throws {
        var s = Status(version: "1.0.0", os: "darwin", enrolled: true)
        s.hostname = ""          // empty -> omitted
        s.server = "https://mdm.example.com"
        s.lastCheckinOK = true
        s.healthy = true
        s.permissions = ["full_disk_access": "granted"]
        s.updatedAt = "2026-10-12T10:55:30Z"
        try StatusFile.write(s, to: Paths.statusPath)

        let raw = String(data: try Data(contentsOf: URL(fileURLWithPath: Paths.statusPath)), encoding: .utf8)!
        XCTAssertFalse(raw.contains("hostname"))        // empty string omitted
        XCTAssertTrue(raw.contains("\"issues\""))       // always present
        XCTAssertTrue(raw.contains("\"enrolled\" : true") || raw.contains("\"enrolled\":true"))
        XCTAssertTrue(raw.contains("full_disk_access"))

        let back = try StatusFile.read(from: Paths.statusPath)
        XCTAssertEqual(back.version, "1.0.0")
        XCTAssertEqual(back.enrolled, true)
        XCTAssertEqual(back.permissions?["full_disk_access"], "granted")
        XCTAssertNil(back.hostname)
        XCTAssertEqual(back.issues.count, 0)
    }
}
