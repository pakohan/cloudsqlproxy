import CCore
import Foundation

// Swift wrappers around the C API of the Go core (core/export.go). Calls that
// do network or listener work block, so the model runs them off the main
// thread.

struct Tunnel: Decodable, Equatable {
    var name: String
    var instance: String
    var port: Int
    var label: String
    var enabled: Bool
}

struct Config: Decodable, Equatable {
    var projects: [String] = []
    var tunnels: [Tunnel] = []
}

struct Status: Decodable, Equatable {
    var connected = false
    var running = 0
    var enabled = 0
}

struct Project: Decodable, Identifiable, Hashable {
    var id: String
    var name: String
}

struct Instance: Decodable, Identifiable {
    var connectionName: String
    var name: String
    var region: String
    var databaseVersion: String
    var state: String

    var id: String { connectionName }
}

struct CoreError: LocalizedError {
    var message: String
    var errorDescription: String? { message }
}

private struct ListResult<Item: Decodable>: Decodable {
    var items: [Item]
    var error: String?
}

enum CoreEvent: Int {
    case state = 1
    case logs = 2
    case authInvalid = 3
}

enum Core {
    /// Starts the core. `handler` is called on Go threads.
    static func initialize(handler: core_event_fn) throws {
        try check(CoreInit(handler))
    }

    static func shutdown() { CoreShutdown() }
    static func log(_ msg: String) { withC(msg) { CoreLog($0) } }

    static func status() -> Status { decode(CoreStatus()) }
    static func connect() { CoreConnect() }
    static func disconnect() { CoreDisconnect() }
    static func apply(_ instance: String) { withC(instance) { CoreApply($0) } }

    static func config() -> Config { decode(CoreConfig()) }
    static func addProject(_ project: String) throws { try check(withC(project) { CoreAddProject($0) }) }
    static func removeProject(_ project: String) throws { try check(withC(project) { CoreRemoveProject($0) }) }

    static func setTunnel(_ instance: String, port: Int, enabled: Bool) throws {
        try check(withC(instance) { CoreSetTunnel($0, Int32(port), enabled) })
    }

    /// The name of another enabled tunnel using `port`, if any.
    static func portConflict(_ instance: String, port: Int) -> String? {
        let name = string(withC(instance) { CorePortConflict($0, Int32(port)) })
        return name.isEmpty ? nil : name
    }

    static func portConflicts() -> [String] { decode(CorePortConflicts()) }
    static func suggestPort(_ instance: String) -> Int { Int(withC(instance) { CoreSuggestPort($0) }) }

    static func listProjects() throws -> [Project] {
        let res: ListResult<Project> = decode(CoreListProjects())
        if let error = res.error { throw CoreError(message: error) }
        return res.items
    }

    static func listInstances(_ project: String) throws -> [Instance] {
        let res: ListResult<Instance> = decode(withC(project) { CoreListInstances($0) })
        if let error = res.error { throw CoreError(message: error) }
        return res.items
    }

    /// Runs the gcloud login and reconnects. Returns false if it was cancelled.
    static func signIn() throws -> Bool {
        guard let p = CoreSignIn() else { return true }
        let msg = string(p)
        if msg == "cancelled" { return false }
        throw CoreError(message: msg)
    }

    static func cancelSignIn() { CoreCancelSignIn() }

    static func logs() -> String { string(CoreLogs()) }
    static func clearLogs() { CoreClearLogs() }
    static func logPath() -> String { string(CoreLogPath()) }

    static func autostartEnabled() -> Bool { CoreAutostartEnabled() }
    static func setAutostart(_ on: Bool) throws { try check(CoreSetAutostart(on)) }

    // MARK: - Marshalling

    /// The Go functions take `char *`, which Swift strings don't convert to.
    private static func withC<R>(_ s: String, _ body: (UnsafeMutablePointer<CChar>) -> R) -> R {
        s.withCString { body(UnsafeMutablePointer(mutating: $0)) }
    }

    /// Copies and frees a string returned by Go.
    private static func string(_ p: UnsafeMutablePointer<CChar>?) -> String {
        guard let p else { return "" }
        defer { free(p) }
        return String(cString: p)
    }

    private static func check(_ p: UnsafeMutablePointer<CChar>?) throws {
        if p != nil { throw CoreError(message: string(p)) }
    }

    private static func decode<T: Decodable>(_ p: UnsafeMutablePointer<CChar>?) -> T {
        do {
            return try JSONDecoder().decode(T.self, from: Data(string(p).utf8))
        } catch {
            // The core only returns JSON it marshalled itself.
            fatalError("decode \(T.self): \(error)")
        }
    }
}
