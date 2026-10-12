import Foundation
import CryptoKit

/// The agent's enrollment: who it is and whom it trusts. Mirrors
/// ../../internal/config/config.go. Holds the server address, the agent id and
/// bearer secret, the server's Ed25519 public key pinned at enrollment (every
/// task and the pulse handshake are verified against it), and the agent's own
/// pulse key seed. Written root-readable only (0600 in a 0700 directory).
public final class AgentConfig: Codable {
    public var server: String
    public var agentID: String
    public var secret: String
    public var serverKey: String
    public var checkinSeconds: Int
    public var device: String
    /// Where the agent keeps a pulse connection open (host:port); the server
    /// sends it, blank means the channel is off.
    public var pulseAddr: String?        // omitempty
    /// base64 of this agent's 32-byte Ed25519 seed for the pulse channel, made on
    /// first use; the public half is registered with the server.
    public var pulseSeed: String?        // omitempty
    /// The organization's help-desk contact, last sent by the server.
    public var support: Support

    enum CodingKeys: String, CodingKey {
        case server
        case agentID = "agent_id"
        case secret
        case serverKey = "server_key"
        case checkinSeconds = "checkin_seconds"
        case device
        case pulseAddr = "pulse_addr"
        case pulseSeed = "pulse_seed"
        case support
    }

    public init(server: String, agentID: String, secret: String, serverKey: String,
                checkinSeconds: Int, device: String,
                pulseAddr: String? = nil, pulseSeed: String? = nil, support: Support = Support()) {
        self.server = server
        self.agentID = agentID
        self.secret = secret
        self.serverKey = serverKey
        self.checkinSeconds = checkinSeconds
        self.device = device
        self.pulseAddr = pulseAddr
        self.pulseSeed = pulseSeed
        self.support = support
    }

    public required init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        server = try c.decodeIfPresent(String.self, forKey: .server) ?? ""
        agentID = try c.decodeIfPresent(String.self, forKey: .agentID) ?? ""
        secret = try c.decodeIfPresent(String.self, forKey: .secret) ?? ""
        serverKey = try c.decodeIfPresent(String.self, forKey: .serverKey) ?? ""
        checkinSeconds = try c.decodeIfPresent(Int.self, forKey: .checkinSeconds) ?? 0
        device = try c.decodeIfPresent(String.self, forKey: .device) ?? ""
        pulseAddr = try c.decodeIfPresent(String.self, forKey: .pulseAddr)
        pulseSeed = try c.decodeIfPresent(String.self, forKey: .pulseSeed)
        support = try c.decodeIfPresent(Support.self, forKey: .support) ?? Support()
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(server, forKey: .server)
        try c.encode(agentID, forKey: .agentID)
        try c.encode(secret, forKey: .secret)
        try c.encode(serverKey, forKey: .serverKey)
        try c.encode(checkinSeconds, forKey: .checkinSeconds)
        try c.encode(device, forKey: .device)
        if let pulseAddr, !pulseAddr.isEmpty { try c.encode(pulseAddr, forKey: .pulseAddr) }
        if let pulseSeed, !pulseSeed.isEmpty { try c.encode(pulseSeed, forKey: .pulseSeed) }
        try c.encode(support, forKey: .support)
    }

    public enum ConfigError: Error, Equatable {
        /// There is no configuration yet (or it is incomplete).
        case notEnrolled
    }

    private static let lock = NSLock()

    /// Reads the configuration, or throws `.notEnrolled` when absent/incomplete.
    public static func load() throws -> AgentConfig {
        let path = Paths.configPath
        guard let data = FileManager.default.contents(atPath: path) else {
            throw ConfigError.notEnrolled
        }
        let cfg = try JSONDecoder().decode(AgentConfig.self, from: data)
        if cfg.server.isEmpty || cfg.secret.isEmpty || cfg.serverKey.isEmpty {
            throw ConfigError.notEnrolled
        }
        if cfg.checkinSeconds <= 0 { cfg.checkinSeconds = 60 }
        return cfg
    }

    /// Writes the configuration, readable by its owner alone, via a temporary
    /// file renamed into place so two saves can't clobber each other.
    public func save() throws {
        AgentConfig.lock.lock()
        defer { AgentConfig.lock.unlock() }
        let fm = FileManager.default
        try fm.createDirectory(atPath: Paths.dir, withIntermediateDirectories: true,
                               attributes: [.posixPermissions: 0o700])
        let enc = JSONEncoder()
        enc.outputFormatting = [.prettyPrinted]
        let data = try enc.encode(self)
        let tmp = (Paths.dir as NSString).appendingPathComponent("config-\(UUID().uuidString).tmp")
        try data.write(to: URL(fileURLWithPath: tmp), options: .atomic)
        try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: tmp)
        _ = try? fm.removeItem(atPath: Paths.configPath)
        try fm.moveItem(atPath: tmp, toPath: Paths.configPath)
    }

    /// Returns this agent's Ed25519 key for the pulse channel, making and saving
    /// one on first use. The caller registers the public half with the server.
    /// The stored seed is the 32-byte Ed25519 private seed, base64 (standard).
    public func pulseKey() throws -> Curve25519.Signing.PrivateKey {
        AgentConfig.lock.lock()
        defer { AgentConfig.lock.unlock() }
        if let seed = pulseSeed, let raw = Data(base64Encoded: seed), raw.count == 32,
           let key = try? Curve25519.Signing.PrivateKey(rawRepresentation: raw) {
            return key
        }
        let key = Curve25519.Signing.PrivateKey()
        pulseSeed = key.rawRepresentation.base64EncodedString()
        // save() takes the lock itself; release ours around it.
        AgentConfig.lock.unlock()
        try save()
        AgentConfig.lock.lock()
        return key
    }
}
