import XCTest
@testable import OrbitAgentKit

/// Verifies the Swift task verifier against the shared golden vectors generated
/// from the Go agent (scripts/gen_vectors). Matching these == matching Go.
final class TaskVerifyTests: XCTestCase {

    struct Vectors: Decodable {
        let server_key: String
        let agent_id: String
        let now_unix: Int64
        let good: Good
        let not_for_us: Case
        let expired: Case
        let tampered: Case

        struct Good: Decodable { let sealed: Sealed; let expect: Expect }
        struct Expect: Decodable { let id: String; let agent: String; let kind: String; let issued: Int64; let expires: Int64 }
        struct Case: Decodable { let sealed: Sealed }
    }

    func loadVectors() throws -> Vectors {
        guard let url = Bundle.module.url(forResource: "task_verify", withExtension: "json", subdirectory: "Vectors") else {
            XCTFail("task_verify.json missing (run: go run ./scripts/gen_vectors)"); throw XCTSkip("no vectors")
        }
        return try JSONDecoder().decode(Vectors.self, from: Data(contentsOf: url))
    }

    func testGoodTaskVerifiesAndParses() throws {
        let v = try loadVectors()
        let now = Date(timeIntervalSince1970: TimeInterval(v.now_unix))
        let task = try Tasks.open(v.good.sealed, serverKey: v.server_key, agentID: v.agent_id, now: now, seen: nil)
        XCTAssertEqual(task.id, v.good.expect.id)
        XCTAssertEqual(task.agent, v.good.expect.agent)
        XCTAssertEqual(task.kind, v.good.expect.kind)
        XCTAssertEqual(task.issued, v.good.expect.issued)
        XCTAssertEqual(task.expires, v.good.expect.expires)
        // Payload parses as JSON (not byte-compared; it is re-parsed, never re-verified).
        let payload = try JSONSerialization.jsonObject(with: task.payload) as? [String: Any]
        XCTAssertEqual(payload?["shell"] as? String, "zsh")
    }

    func testTaskForAnotherAgentRejected() throws {
        let v = try loadVectors()
        let now = Date(timeIntervalSince1970: TimeInterval(v.now_unix))
        XCTAssertThrowsError(try Tasks.open(v.not_for_us.sealed, serverKey: v.server_key, agentID: v.agent_id, now: now, seen: nil)) {
            XCTAssertEqual($0 as? TaskError, .notForUs)
        }
    }

    func testExpiredTaskRejected() throws {
        let v = try loadVectors()
        let now = Date(timeIntervalSince1970: TimeInterval(v.now_unix))
        XCTAssertThrowsError(try Tasks.open(v.expired.sealed, serverKey: v.server_key, agentID: v.agent_id, now: now, seen: nil)) {
            XCTAssertEqual($0 as? TaskError, .expired)
        }
    }

    func testTamperedSignatureRejected() throws {
        let v = try loadVectors()
        let now = Date(timeIntervalSince1970: TimeInterval(v.now_unix))
        XCTAssertThrowsError(try Tasks.open(v.tampered.sealed, serverKey: v.server_key, agentID: v.agent_id, now: now, seen: nil)) {
            XCTAssertEqual($0 as? TaskError, .signature)
        }
    }

    func testReplayedTaskRejected() throws {
        let v = try loadVectors()
        let now = Date(timeIntervalSince1970: TimeInterval(v.now_unix))
        let seen = Seen(path: NSTemporaryDirectory() + "seen-\(UUID().uuidString).json")
        seen.add(v.good.expect.id, now: now)
        XCTAssertThrowsError(try Tasks.open(v.good.sealed, serverKey: v.server_key, agentID: v.agent_id, now: now, seen: seen)) {
            XCTAssertEqual($0 as? TaskError, .replayed)
        }
    }
}
