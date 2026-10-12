import Foundation

/// The small, world-readable file the service writes each check-in for the
/// menu-bar app to read. Mirrors ../../internal/status/status.go; the existing
/// Swift menu-bar app (menubar/macos) decodes this exact schema, so the field
/// names and types are frozen. Carries no secrets.
///
/// Go `,omitempty` string/map fields are modelled as `Optional` and encoded with
/// `encodeIfPresent`, so an empty value is omitted exactly as Go omits it. The
/// JSON is order-insensitive on the wire (the menu-bar app parses it), so key
/// order is not part of the contract.

public enum Severity {
    public static let error = "error"
    public static let warning = "warning"
    public static let info = "info"
}

public enum Permission {
    public static let granted = "granted"
    public static let denied = "denied"
    public static let unknown = "unknown"
}

/// One problem the tray shows, with an optional action the UI can carry out.
public struct Issue: Codable, Equatable {
    public var id: String
    public var severity: String
    public var title: String
    public var detail: String?   // omitempty
    public var fix: String?      // omitempty

    public init(id: String, severity: String, title: String, detail: String? = nil, fix: String? = nil) {
        self.id = id
        self.severity = severity
        self.title = title
        self.detail = detail?.isEmpty == true ? nil : detail
        self.fix = fix?.isEmpty == true ? nil : fix
    }

    enum CodingKeys: String, CodingKey { case id, severity, title, detail, fix }
}

/// The organization's help desk, shown by the tray.
public struct Support: Codable, Equatable {
    public var name: String?   // omitempty
    public var phone: String?  // omitempty
    public var email: String?  // omitempty
    public var url: String?    // omitempty

    public init(name: String? = nil, phone: String? = nil, email: String? = nil, url: String? = nil) {
        self.name = name?.isEmpty == true ? nil : name
        self.phone = phone?.isEmpty == true ? nil : phone
        self.email = email?.isEmpty == true ? nil : email
        self.url = url?.isEmpty == true ? nil : url
    }

    public var isEmpty: Bool { name == nil && phone == nil && email == nil && url == nil }

    enum CodingKeys: String, CodingKey { case name, phone, email, url }
}

/// The whole status file.
public struct Status: Codable, Equatable {
    public var version: String
    public var hostname: String?        // omitempty
    public var os: String
    public var enrolled: Bool
    public var server: String?          // omitempty
    public var device: String?          // omitempty
    public var lastCheckin: String?     // omitempty
    public var lastCheckinOK: Bool
    public var healthy: Bool
    public var issues: [Issue]          // always present ([] when none)
    public var permissions: [String: String]?  // omitempty
    public var support: Support         // always present (may serialize as {})
    public var updatedAt: String

    public init(version: String, os: String = "darwin", enrolled: Bool = false) {
        self.version = version
        self.os = os
        self.enrolled = enrolled
        self.lastCheckinOK = false
        self.healthy = false
        self.issues = []
        self.support = Support()
        self.updatedAt = ""
    }

    enum CodingKeys: String, CodingKey {
        case version, hostname, os, enrolled, server, device
        case lastCheckin = "last_checkin"
        case lastCheckinOK = "last_checkin_ok"
        case healthy, issues, permissions, support
        case updatedAt = "updated_at"
    }

    // Explicit encode so `,omitempty` fields are dropped when empty and the
    // always-present fields (bools, issues, support) are always written, matching
    // the Go encoder's output semantically.
    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(version, forKey: .version)
        try c.encodeIfPresent(nonEmpty(hostname), forKey: .hostname)
        try c.encode(os, forKey: .os)
        try c.encode(enrolled, forKey: .enrolled)
        try c.encodeIfPresent(nonEmpty(server), forKey: .server)
        try c.encodeIfPresent(nonEmpty(device), forKey: .device)
        try c.encodeIfPresent(nonEmpty(lastCheckin), forKey: .lastCheckin)
        try c.encode(lastCheckinOK, forKey: .lastCheckinOK)
        try c.encode(healthy, forKey: .healthy)
        try c.encode(issues, forKey: .issues)
        if let permissions, !permissions.isEmpty { try c.encode(permissions, forKey: .permissions) }
        try c.encode(support, forKey: .support)
        try c.encode(updatedAt, forKey: .updatedAt)
    }

    private func nonEmpty(_ s: String?) -> String? { (s?.isEmpty == false) ? s : nil }
}

public enum StatusFile {
    /// Saves the status for anyone on the computer to read (0644), replacing it
    /// in one step so a reader never sees a half-written file.
    public static func write(_ status: Status, to path: String) throws {
        let enc = JSONEncoder()
        enc.outputFormatting = [.prettyPrinted]
        let data = try enc.encode(status)
        let dir = (path as NSString).deletingLastPathComponent
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true,
                                                attributes: [.posixPermissions: 0o755])
        let tmp = path + ".tmp"
        try data.write(to: URL(fileURLWithPath: tmp), options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o644], ofItemAtPath: tmp)
        _ = try? FileManager.default.removeItem(atPath: path)
        try FileManager.default.moveItem(atPath: tmp, toPath: path)
    }

    /// Loads a status file (kept here so both writer and reader agree).
    public static func read(from path: String) throws -> Status {
        let data = try Data(contentsOf: URL(fileURLWithPath: path))
        return try JSONDecoder().decode(Status.self, from: data)
    }
}
