import Foundation

/// Where the agent keeps its files, mirroring ../../internal/config/config.go so
/// the Swift agent and the Go agent (and the installers that place these files)
/// agree. macOS only; the Go binary still serves Windows and Linux.
///
/// `ORBIT_AGENT_HOME` overrides every directory, for tests and for running a
/// second copy beside the service. The variable name is a contract shared with
/// the Go agent — it is the side-by-side differential-test mechanism.
public enum Paths {
    /// The override, or nil when the service's real locations are used.
    static var home: String? {
        let home = ProcessInfo.processInfo.environment["ORBIT_AGENT_HOME"] ?? ""
        return home.isEmpty ? nil : home
    }

    /// Where the configuration and state live. Root-readable only in production.
    public static var dir: String {
        home ?? "/Library/Application Support/Orbit Agent"
    }

    /// What changes as the agent runs (tasks already run). Same as `dir` on macOS.
    public static var stateDir: String { home ?? dir }

    /// A place anyone signed in can read, where the service writes the tray's
    /// status file. Beside the service binary on macOS.
    public static var publicDir: String { home ?? "/Library/Orbit" }

    /// The configuration file.
    public static var configPath: String { (dir as NSString).appendingPathComponent("config.json") }

    /// The world-readable status file the menu-bar app reads.
    public static var statusPath: String { (publicDir as NSString).appendingPathComponent("status.json") }

    /// The replay store.
    public static var seenPath: String { (stateDir as NSString).appendingPathComponent("seen.json") }

    /// Where `install` puts the agent itself: inside the signed .app bundle so
    /// Full Disk Access, Screen Recording and Accessibility list it as "Orbit
    /// Agent". Kept in step with ServiceInfo.plist and the .pkg payload.
    public static var binaryPath: String {
        "/Library/Orbit/Orbit Agent Service.app/Contents/MacOS/orbit-agent"
    }
}
