import Foundation

/// The replay store: the ids of tasks already claimed, so none runs twice even if
/// the server re-delivers it. Mirrors ../../internal/tasks/seen.go — a JSON map
/// {task_id: unix_seconds_recorded} at `seen.json` in the state dir, pruned to the
/// last 30 days, persisted with a temporary file renamed into place (0600/0700).
public final class Seen {
    private let path: String
    private let lock = NSLock()
    private var ids: [String: Int64]
    /// How long a claimed id is remembered.
    private static let keep: Int64 = 30 * 24 * 60 * 60

    public init(path: String = Paths.seenPath) {
        self.path = path
        if let data = FileManager.default.contents(atPath: path),
           let m = try? JSONDecoder().decode([String: Int64].self, from: data) {
            ids = m
        } else {
            ids = [:]
        }
    }

    /// Whether this id has already been claimed.
    public func has(_ id: String) -> Bool {
        lock.lock(); defer { lock.unlock() }
        return ids[id] != nil
    }

    /// Claims an id atomically: records it and returns true if it was new, or
    /// returns false (do not run) if it was already present. Prunes and persists.
    @discardableResult
    public func add(_ id: String, now: Date = Date()) -> Bool {
        lock.lock(); defer { lock.unlock() }
        if ids[id] != nil { return false }
        let nowSecs = Int64(now.timeIntervalSince1970)
        ids[id] = nowSecs
        let cutoff = nowSecs - Seen.keep
        ids = ids.filter { $0.value >= cutoff }
        try? persist()
        return true
    }

    private func persist() throws {
        let fm = FileManager.default
        let dir = (path as NSString).deletingLastPathComponent
        try fm.createDirectory(atPath: dir, withIntermediateDirectories: true,
                               attributes: [.posixPermissions: 0o700])
        let enc = JSONEncoder()
        let data = try enc.encode(ids)
        let tmp = path + ".tmp-\(UUID().uuidString)"
        try data.write(to: URL(fileURLWithPath: tmp), options: .atomic)
        try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: tmp)
        _ = try? fm.removeItem(atPath: path)
        try fm.moveItem(atPath: tmp, toPath: path)
    }
}
