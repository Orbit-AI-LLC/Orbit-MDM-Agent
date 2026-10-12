import Foundation
import CryptoKit

/// Checks that a task really came from the server before anything runs it.
/// Mirrors ../../internal/tasks/open.go and the server's apps/rmm/signing.py.
///
/// The server signs the exact bytes of each task with its Ed25519 key — the key
/// this agent pinned at enrollment. A task runs only if the signature is good, it
/// names this agent, it hasn't expired, and it hasn't run before. The verifier
/// must verify the *received* envelope bytes and then parse *those same bytes*; it
/// must never re-encode the JSON before verifying (key order/whitespace would
/// change and the signature would no longer cover what was parsed).

/// A task as the server sends it: signed bytes and their signature (base64 std).
public struct Sealed: Codable, Equatable {
    public var id: String
    public var envelope: String
    public var signature: String
    public init(id: String, envelope: String, signature: String) {
        self.id = id; self.envelope = envelope; self.signature = signature
    }
}

/// A verified task.
public struct VerifiedTask: Equatable {
    public var id: String
    public var agent: String
    public var kind: String
    /// The task payload, re-serialized for the handler that runs it. Not
    /// byte-exact with the wire (it is parsed again, never re-verified).
    public var payload: Data
    public var issued: Int64
    public var expires: Int64
    /// The exact verified envelope bytes.
    public var envelope: Data
}

public enum TaskError: Error, Equatable {
    case signature   // the task's signature is not the server's
    case notForUs    // the task is for another agent
    case expired     // the task has expired
    case replayed    // the task has already run
}

public enum Tasks {
    /// Verifies a sealed task and returns it. `serverKey` is the pinned server
    /// public key (base64 std); `now` is the current time; `seen` is the replay
    /// store (checked, not yet claimed — the caller claims before running).
    public static func open(_ sealed: Sealed, serverKey: String, agentID: String,
                            now: Date, seen: Seen?) throws -> VerifiedTask {
        guard let keyData = Data(base64Encoded: serverKey), keyData.count == 32,
              let pub = try? Curve25519.Signing.PublicKey(rawRepresentation: keyData) else {
            throw TaskError.signature
        }
        guard let envelope = Data(base64Encoded: sealed.envelope) else { throw TaskError.signature }
        guard let signature = Data(base64Encoded: sealed.signature) else { throw TaskError.signature }
        guard pub.isValidSignature(signature, for: envelope) else { throw TaskError.signature }

        // Parse the SAME bytes we verified; never re-encode before verifying.
        guard let obj = try? JSONSerialization.jsonObject(with: envelope) as? [String: Any] else {
            throw TaskError.signature
        }
        let id = obj["id"] as? String ?? ""
        let agent = obj["agent"] as? String ?? ""
        let kind = obj["kind"] as? String ?? ""
        let issued = int64(obj["issued"])
        let expires = int64(obj["expires"])
        let payload = payloadBytes(obj["payload"])

        if agent != agentID { throw TaskError.notForUs }
        if expires != 0 && Int64(now.timeIntervalSince1970) > expires { throw TaskError.expired }
        if let seen, seen.has(id) { throw TaskError.replayed }

        return VerifiedTask(id: id, agent: agent, kind: kind, payload: payload,
                            issued: issued, expires: expires, envelope: envelope)
    }

    /// Re-serializes a task payload for the handler that runs it. Objects and
    /// arrays serialize directly; JSON `null` (and absent) become `null`; a scalar
    /// is wrapped in an array and unwrapped, since JSONSerialization cannot write a
    /// bare scalar. Not byte-exact with the wire — the payload is parsed again,
    /// never re-verified. Guards against the NSException JSONSerialization raises on
    /// a non-container top-level value.
    private static func payloadBytes(_ p: Any?) -> Data {
        let null = Data("null".utf8)
        guard let p, !(p is NSNull) else { return null }
        if JSONSerialization.isValidJSONObject(p) {
            return (try? JSONSerialization.data(withJSONObject: p)) ?? null
        }
        // Scalar (string / number / bool): wrap so it has a container top level.
        if let wrapped = try? JSONSerialization.data(withJSONObject: [p]), wrapped.count >= 2 {
            return wrapped.subdata(in: 1 ..< (wrapped.count - 1))
        }
        return null
    }

    private static func int64(_ v: Any?) -> Int64 {
        if let n = v as? NSNumber { return n.int64Value }
        if let i = v as? Int64 { return i }
        if let i = v as? Int { return Int64(i) }
        if let d = v as? Double { return Int64(d) }
        return 0
    }
}
