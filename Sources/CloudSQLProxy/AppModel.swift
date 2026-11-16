import Foundation
import Observation

/// The instances of one project, as loaded from the SQL Admin API.
enum ProjectInstances {
    case loading
    case loaded([Instance])
    case failed(String)
}

/// App state shown by the menu and the windows. It is only touched on the main
/// thread; blocking core calls run in detached tasks.
@MainActor
@Observable
final class AppModel {
    static let shared = AppModel()

    private(set) var status = Status()
    private(set) var config = Config()
    private(set) var logText = ""
    private(set) var autostart = false

    // Cached API results for the tunnels window, kept until reload().
    private(set) var instances: [String: ProjectInstances] = [:]
    private(set) var allProjects: [Project]?
    private(set) var allProjectsError: String?

    /// Called when the credentials stop working, with the error message.
    @ObservationIgnored var onAuthInvalid: ((String) -> Void)?
    /// API results are only fetched while the tunnels window is visible.
    @ObservationIgnored var tunnelsVisible = false {
        didSet { if tunnelsVisible { loadMissingInstances() } }
    }
    /// Logs are only copied from the core while the log window is visible.
    @ObservationIgnored var logsVisible = false {
        didSet { if logsVisible { refreshLogs() } }
    }

    // loadGen increases on reload, so results of stale fetches are dropped.
    @ObservationIgnored private var loadGen = 0
    @ObservationIgnored private var logRefreshPending = false

    func start() throws {
        try Core.initialize { event, msg in
            let text = msg.map { String(cString: $0) } ?? ""
            DispatchQueue.main.async { AppModel.shared.handle(event: Int(event), msg: text) }
        }
        refreshState()
        autostart = Core.autostartEnabled()
    }

    private func handle(event: Int, msg: String) {
        switch CoreEvent(rawValue: event) {
        case .state: refreshState()
        case .logs: scheduleLogRefresh()
        case .authInvalid: onAuthInvalid?(msg)
        case nil: break
        }
    }

    private func refreshState() {
        status = Core.status()
        config = Core.config()
    }

    // MARK: - Menu

    var statusText: String {
        if status.connected { return "Connected: \(status.running) of \(status.enabled) tunnels running" }
        if status.enabled == 0 { return "No tunnels enabled" }
        return "Disconnected (\(status.enabled) tunnels enabled)"
    }

    func toggleConnection() {
        let connected = status.connected
        Task.detached { connected ? Core.disconnect() : Core.connect() }
    }

    // MARK: - Tunnels

    func tunnel(_ instance: String) -> Tunnel? {
        config.tunnels.first { $0.instance == instance }
    }

    func setTunnel(_ instance: String, port: Int, enabled: Bool) {
        do {
            try Core.setTunnel(instance, port: port, enabled: enabled)
        } catch {
            Core.log("Failed to save config: \(error.localizedDescription)")
        }
    }

    func apply(_ instance: String) {
        Task.detached { Core.apply(instance) }
    }

    func addProject(_ project: String) {
        do {
            try Core.addProject(project)
        } catch {
            Core.log("Failed to save config: \(error.localizedDescription)")
        }
        refreshState()
        if instances[project] == nil { loadInstances(project) }
    }

    func removeProject(_ project: String) {
        do {
            try Core.removeProject(project)
        } catch {
            Core.log("Failed to save config: \(error.localizedDescription)")
        }
        refreshState()
    }

    /// Loads the instances of every selected project that isn't cached.
    func loadMissingInstances() {
        refreshState()
        for p in config.projects where instances[p] == nil {
            loadInstances(p)
        }
    }

    private func loadInstances(_ project: String) {
        let gen = loadGen
        instances[project] = .loading
        Task {
            let result: ProjectInstances
            do {
                result = .loaded(try await Task.detached { try Core.listInstances(project) }.value)
            } catch {
                result = .failed(error.localizedDescription)
            }
            if gen == loadGen { instances[project] = result }
        }
    }

    /// Loads every project the credentials can see, once until reload().
    func loadAllProjects() {
        guard allProjects == nil, allProjectsError == nil else { return }
        let gen = loadGen
        Task {
            do {
                let projects = try await Task.detached { try Core.listProjects() }.value
                if gen == loadGen { allProjects = projects }
            } catch {
                if gen == loadGen { allProjectsError = error.localizedDescription }
            }
        }
    }

    /// Drops all cached API results and fetches them again.
    func reload() {
        loadGen += 1
        instances = [:]
        allProjects = nil
        allProjectsError = nil
        if tunnelsVisible { loadMissingInstances() }
    }

    func setAutostart(_ on: Bool) throws {
        defer { autostart = Core.autostartEnabled() }
        try Core.setAutostart(on)
    }

    // MARK: - Logs

    /// Coalesces bursts of log lines into one refresh every 200 ms.
    private func scheduleLogRefresh() {
        guard logsVisible, !logRefreshPending else { return }
        logRefreshPending = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) { [self] in
            logRefreshPending = false
            if logsVisible { refreshLogs() }
        }
    }

    private func refreshLogs() {
        logText = Core.logs()
    }

    func clearLogs() {
        Core.clearLogs()
        refreshLogs()
    }
}
