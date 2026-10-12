import Foundation

/// The JSON bodies of Orbit RMM's agent API. Mirrors ../../internal/api/client.go
/// and the server's apps/rmm/api.py. The wire is order-insensitive JSON; Go
/// `,omitempty` fields are modelled as `Optional` and omitted when nil.

/// What the agent tells the server about the computer at enrollment.
public struct EnrollRequest: Codable {
    public var token: String
    public var hostname: String
    public var os: String
    public var osVersion: String
    public var arch: String
    public var serial: String
    public var machineID: String
    public var manufacturer: String
    public var model: String
    public var version: String

    enum CodingKeys: String, CodingKey {
        case token, hostname, os
        case osVersion = "os_version"
        case arch, serial
        case machineID = "machine_id"
        case manufacturer, model, version
    }

    public init(token: String, hostname: String, os: String, osVersion: String, arch: String,
                serial: String, machineID: String, manufacturer: String, model: String, version: String) {
        self.token = token; self.hostname = hostname; self.os = os; self.osVersion = osVersion
        self.arch = arch; self.serial = serial; self.machineID = machineID
        self.manufacturer = manufacturer; self.model = model; self.version = version
    }
}

/// The agent's identity and the key it must trust, returned by enrollment.
public struct EnrollResponse: Codable {
    public var agentID: String
    public var secret: String
    public var serverKey: String
    public var checkinSeconds: Int
    public var pulseAddr: String?
    public var device: String

    enum CodingKeys: String, CodingKey {
        case agentID = "agent_id"
        case secret
        case serverKey = "server_key"
        case checkinSeconds = "checkin_seconds"
        case pulseAddr = "pulse_addr"
        case device
    }
}

/// The server's answer to a check-in.
public struct CheckinResponse: Codable {
    public var tasks: [Sealed]
    public var checkinSeconds: Int
    public var live: Bool
    public var watchServices: [String]
    public var pulseAddr: String?
    public var serverTime: Int64
    public var support: Support?

    enum CodingKeys: String, CodingKey {
        case tasks
        case checkinSeconds = "checkin_seconds"
        case live
        case watchServices = "watch_services"
        case pulseAddr = "pulse_addr"
        case serverTime = "server_time"
        case support
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        tasks = try c.decodeIfPresent([Sealed].self, forKey: .tasks) ?? []
        checkinSeconds = try c.decodeIfPresent(Int.self, forKey: .checkinSeconds) ?? 0
        live = try c.decodeIfPresent(Bool.self, forKey: .live) ?? false
        watchServices = try c.decodeIfPresent([String].self, forKey: .watchServices) ?? []
        pulseAddr = try c.decodeIfPresent(String.self, forKey: .pulseAddr)
        serverTime = try c.decodeIfPresent(Int64.self, forKey: .serverTime) ?? 0
        support = try c.decodeIfPresent(Support.self, forKey: .support)
    }
}

/// The answer to a long poll.
public struct WaitResponse: Codable {
    public var tasks: [Sealed]
    public var live: Bool

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        tasks = try c.decodeIfPresent([Sealed].self, forKey: .tasks) ?? []
        live = try c.decodeIfPresent(Bool.self, forKey: .live) ?? false
    }
    enum CodingKeys: String, CodingKey { case tasks, live }
}

/// What a task produced, reported back to the server.
public struct TaskResult: Codable {
    public var exitCode: Int?       // always present (null when nil)
    public var stdout: String
    public var stderr: String
    public var data: [String: AnyCodable]?   // omitempty
    public var error: String?                // omitempty

    enum CodingKeys: String, CodingKey {
        case exitCode = "exit_code"
        case stdout, stderr, data, error
    }

    public init(exitCode: Int?, stdout: String = "", stderr: String = "",
                data: [String: AnyCodable]? = nil, error: String? = nil) {
        self.exitCode = exitCode; self.stdout = stdout; self.stderr = stderr
        self.data = data; self.error = error
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(exitCode, forKey: .exitCode)   // null when nil, matching Go *int
        try c.encode(stdout, forKey: .stdout)
        try c.encode(stderr, forKey: .stderr)
        if let data, !data.isEmpty { try c.encode(data, forKey: .data) }
        if let error, !error.isEmpty { try c.encode(error, forKey: .error) }
    }
}
