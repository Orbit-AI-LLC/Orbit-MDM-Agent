import Foundation

/// Speaks Orbit RMM's agent API (/api/rmm/v1/), JSON over HTTPS. Mirrors
/// ../../internal/api/client.go.
///
/// Each call sets its own deadline on the request (checkin 60s, wait 90s long
/// poll, result 120s). There is deliberately NO session-wide timeout: a global
/// cap would cut the 90s long poll short. Every call but `enroll` sends
/// `Authorization: Bearer <secret>`.
public final class HTTPClient {
    public let server: String
    public var secret: String
    public let version: String
    private let session: URLSession

    public enum APIError: Error, Equatable {
        case retired          // 410: no longer managed
        case unauthorized     // 401: credentials refused
        case server(status: Int, message: String)
        case badURL
    }

    public init(server: String, secret: String, version: String) {
        self.server = server.hasSuffix("/") ? String(server.dropLast()) : server
        self.secret = secret
        self.version = version
        let cfg = URLSessionConfiguration.ephemeral
        // No global cap that could undercut the 90s long poll; per-request
        // deadlines govern instead.
        cfg.timeoutIntervalForRequest = 3600
        cfg.timeoutIntervalForResource = 3600
        cfg.waitsForConnectivity = false
        self.session = URLSession(configuration: cfg)
    }

    // MARK: - Calls

    public func enroll(_ req: EnrollRequest) async throws -> EnrollResponse {
        try await call(.post, "enroll", body: req, auth: false, deadline: 60, as: EnrollResponse.self)
    }

    public func checkin(metrics: [String: AnyCodable]) async throws -> CheckinResponse {
        let body: [String: AnyCodable] = ["version": AnyCodable(version), "metrics": AnyCodable(metrics.mapValues { $0.value })]
        return try await call(.post, "checkin", body: body, auth: true, deadline: 60, as: CheckinResponse.self)
    }

    public func wait() async throws -> WaitResponse {
        try await call(.get, "wait", body: Optional<Int>.none, auth: true, deadline: 90, as: WaitResponse.self)
    }

    public func registerPulseKey(_ publicKey: String) async throws {
        try await callVoid(.post, "pulse_key", body: ["pulse_key": publicKey], auth: true, deadline: 30)
    }

    public func start(taskID: String) async throws {
        try await callVoid(.post, "tasks/\(taskID)/start", body: [String: String](), auth: true, deadline: 30)
    }

    public func report(taskID: String, result: TaskResult) async throws {
        try await callVoid(.post, "tasks/\(taskID)/result", body: result, auth: true, deadline: 120)
    }

    // MARK: - Transport

    private enum Method: String { case get = "GET", post = "POST" }

    private func makeRequest<B: Encodable>(_ method: Method, _ path: String, body: B?,
                                           auth: Bool, deadline: TimeInterval) throws -> URLRequest {
        guard let url = URL(string: "\(server)/api/rmm/v1/\(path)") else { throw APIError.badURL }
        var req = URLRequest(url: url)
        req.httpMethod = method.rawValue
        req.timeoutInterval = deadline
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        req.setValue("orbit-agent/\(version)", forHTTPHeaderField: "User-Agent")
        if auth { req.setValue("Bearer \(secret)", forHTTPHeaderField: "Authorization") }
        if let body {
            let enc = JSONEncoder()
            req.httpBody = try enc.encode(body)
        }
        return req
    }

    private func send(_ req: URLRequest) async throws -> Data {
        let (data, resp) = try await session.data(for: req)
        guard let http = resp as? HTTPURLResponse else { return data }
        switch http.statusCode {
        case 410: throw APIError.retired
        case 401: throw APIError.unauthorized
        case 400...:
            var message = "the server answered \(http.statusCode)"
            if let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let m = obj["message"] as? String, !m.isEmpty {
                message = m
            }
            throw APIError.server(status: http.statusCode, message: message)
        default:
            return data
        }
    }

    private func call<B: Encodable, R: Decodable>(_ method: Method, _ path: String, body: B?,
                                                  auth: Bool, deadline: TimeInterval, as: R.Type) async throws -> R {
        let data = try await send(try makeRequest(method, path, body: body, auth: auth, deadline: deadline))
        return try JSONDecoder().decode(R.self, from: data)
    }

    private func callVoid<B: Encodable>(_ method: Method, _ path: String, body: B?,
                                        auth: Bool, deadline: TimeInterval) async throws {
        _ = try await send(try makeRequest(method, path, body: body, auth: auth, deadline: deadline))
    }
}
