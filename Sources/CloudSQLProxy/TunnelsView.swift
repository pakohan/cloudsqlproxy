import SwiftUI

/// A sidebar entry: all configured tunnels, or one selected project.
private enum Entry: Hashable {
    case tunnels
    case project(String)
}

/// Shows the selected projects and their Cloud SQL instances, and lets the
/// user enable tunnels and pick their local ports.
struct TunnelsView: View {
    @Environment(AppModel.self) private var model
    @ViewState private var selection: Entry? = .tunnels
    @ViewState private var filter = ""
    @ViewState private var addingProject = false
    @ViewState private var removingProject: String?
    @ViewState private var autostartError: String?

    var body: some View {
        // The login toggle sits below the split view, not in its
        // safeAreaInset: the split view ignores that inset, so the bar covered
        // the sidebar's Add Project button.
        VStack(alignment: .leading, spacing: 0) {
            NavigationSplitView {
                sidebar
                    .navigationSplitViewColumnWidth(min: 220, ideal: 300)
            } detail: {
                detail
            }
            Divider()
            Toggle("Start at login", isOn: Binding(
                get: { model.autostart },
                set: { on in
                    do { try model.setAutostart(on) } catch { autostartError = error.localizedDescription }
                }))
                .padding(8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(.bar)
        }
        .sheet(isPresented: $addingProject) {
            AddProjectView { project in
                model.addProject(project)
                selection = .project(project)
            }
        }
        .confirmationDialog(
            "Remove \(removingProject ?? "") from the list?",
            isPresented: Binding(get: { removingProject != nil }, set: { if !$0 { removingProject = nil } })
        ) {
            Button("Remove", role: .destructive) {
                if let p = removingProject { model.removeProject(p) }
                selection = .tunnels
            }
        } message: {
            Text("Its tunnels stay configured.")
        }
        .alert("Start at login", isPresented: Binding(get: { autostartError != nil }, set: { if !$0 { autostartError = nil } })) {
        } message: {
            Text(autostartError ?? "")
        }
        .onChange(of: selection) { filter = "" }
        .onChange(of: model.config.projects) {
            if case .project(let p) = selection, !model.config.projects.contains(p) { selection = .tunnels }
        }
    }

    private var sidebar: some View {
        VStack(spacing: 0) {
            projectList
            Divider()
            sidebarButtons
        }
    }

    private var projectList: some View {
        List(selection: $selection) {
            HStack {
                Text("Tunnels").bold()
                Spacer()
                Text("\(model.config.tunnels.filter(\.enabled).count) / \(model.config.tunnels.count)")
                    .foregroundStyle(.secondary)
            }
            .tag(Entry.tunnels)
            ForEach(model.config.projects, id: \.self) { project in
                HStack {
                    Text(project).lineLimit(1).truncationMode(.middle)
                    Spacer()
                    Text(countText(project)).foregroundStyle(.secondary)
                }
                .tag(Entry.project(project))
            }
        }
    }

    private var sidebarButtons: some View {
        HStack {
            Button { addingProject = true } label: { Label("Add Project…", systemImage: "plus") }
            Button {
                if case .project(let p) = selection { removingProject = p }
            } label: { Label("Remove", systemImage: "minus") }
                .disabled(selection == .tunnels || selection == nil)
            Spacer()
            Button { model.reload() } label: { Image(systemName: "arrow.clockwise") }
                .help("Reload projects and instances")
        }
        .buttonStyle(.borderless)
        .padding(8)
    }

    private func countText(_ project: String) -> String {
        switch model.instances[project] {
        case .loaded(let instances): "\(instances.count)"
        case .failed: "!"
        case .loading, nil: "…"
        }
    }

    @ViewBuilder
    private var detail: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title).font(.headline)
            TextField("Filter instances", text: $filter)
                .textFieldStyle(.roundedBorder)
            let conflicts = model.config.tunnels.isEmpty ? [] : Core.portConflicts()
            if !conflicts.isEmpty {
                Text(conflicts.joined(separator: "\n") + "\nOnly one of them can be connected; change a port or disable one.")
                    .foregroundStyle(.red)
            }
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    rows
                }
            }
        }
        .padding()
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }

    private var title: String {
        switch selection {
        case .project(let p): p
        case .tunnels, nil: "Tunnels"
        }
    }

    @ViewBuilder
    private var rows: some View {
        switch selection {
        case .tunnels, nil:
            let tunnels = model.config.tunnels.filter { matches($0.name, $0.label, $0.instance) }
            if model.config.tunnels.isEmpty {
                Text("No tunnels yet. Add a project on the left and enable its instances.")
            } else if tunnels.isEmpty {
                Text("No instances match the filter.")
            }
            ForEach(tunnels, id: \.instance) { t in
                TunnelRow(instance: t.instance, title: t.label, subtitle: t.instance)
            }
        case .project(let project):
            switch model.instances[project] {
            case .loading, nil:
                Text("Loading instances…")
            case .failed(let error):
                Text(truncated(error, 500)).textSelection(.enabled)
            case .loaded(let instances) where instances.isEmpty:
                Text("No Cloud SQL instances in this project.")
            case .loaded(let instances):
                let shown = instances.map { inst in
                    (inst, model.tunnel(inst.connectionName)?.label.nonEmpty ?? inst.name,
                     [inst.databaseVersion, inst.region, inst.state].joined(separator: " · "))
                }.filter { inst, title, sub in matches(title, inst.name, inst.connectionName, sub) }
                if shown.isEmpty {
                    Text("No instances match the filter.")
                }
                ForEach(shown, id: \.0.id) { inst, title, sub in
                    TunnelRow(instance: inst.connectionName, title: title, subtitle: sub)
                }
            }
        }
    }

    private func matches(_ fields: String...) -> Bool {
        let q = filter.trimmingCharacters(in: .whitespaces).lowercased()
        return q.isEmpty || fields.joined(separator: " ").lowercased().contains(q)
    }
}

/// One instance with its enable toggle and local port.
private struct TunnelRow: View {
    @Environment(AppModel.self) private var model
    let instance: String
    let title: String
    let subtitle: String

    @ViewState private var portText = ""
    @ViewState private var initialPortText: String?
    // edited is set once the user changed the port, so a tunnel entry is only
    // created by the user's edit, not by showing the row.
    @ViewState private var edited = false
    @ViewState private var alert: String?

    var body: some View {
        // model.tunnel reads model.config, so every row re-checks its port
        // whenever any port or enabled state changes, and fixing a conflict on
        // one row clears the error on the other.
        let enabled = model.tunnel(instance)?.enabled ?? false
        let error = validate(portText, enabled: enabled).error

        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .center) {
                Toggle("", isOn: Binding(get: { enabled }, set: setEnabled))
                    .labelsHidden()
                VStack(alignment: .leading) {
                    Text(title).bold()
                    if !subtitle.isEmpty {
                        Text(subtitle).foregroundStyle(.secondary)
                    }
                }
                Spacer()
                Text("Port")
                TextField("", text: $portText)
                    .frame(width: 70)
                    .textFieldStyle(.roundedBorder)
                    .onChange(of: portText) { portChanged() }
            }
            if let error {
                Text(error).foregroundStyle(.red)
            }
            Divider().padding(.top, 4)
        }
        .padding(.vertical, 4)
        .onAppear {
            let text = String(Core.suggestPort(instance))
            initialPortText = text
            portText = text
        }
        // Restarting the proxy on every keystroke would be disruptive, so port
        // changes are applied once typing pauses.
        .task(id: portText) {
            guard edited else { return }
            try? await Task.sleep(for: .seconds(1))
            if !Task.isCancelled { model.apply(instance) }
        }
        .alert("Can't enable \(title)", isPresented: Binding(get: { alert != nil }, set: { if !$0 { alert = nil } })) {
        } message: {
            Text(alert ?? "")
        }
    }

    private func portChanged() {
        if !edited && portText == initialPortText { return }
        edited = true
        let enabled = model.tunnel(instance)?.enabled ?? false
        guard let port = validate(portText, enabled: enabled).port, port != model.tunnel(instance)?.port else { return }
        model.setTunnel(instance, port: port, enabled: enabled)
    }

    private func setEnabled(_ on: Bool) {
        guard let port = Int(portText.trimmingCharacters(in: .whitespaces)), (1...65535).contains(port) else {
            if on { alert = "Enter a valid port first." }
            return
        }
        if on, let other = Core.portConflict(instance, port: port) {
            alert = "Port \(port) is already used by the active tunnel \(other); pick another port first."
            return
        }
        model.setTunnel(instance, port: port, enabled: on)
        model.apply(instance)
    }

    /// Only an enabled tunnel can conflict with other enabled tunnels.
    private func validate(_ s: String, enabled: Bool) -> (port: Int?, error: String?) {
        guard let port = Int(s.trimmingCharacters(in: .whitespaces)), (1...65535).contains(port) else {
            return (nil, "Port must be between 1 and 65535.")
        }
        if enabled, let other = Core.portConflict(instance, port: port) {
            return (nil, "Port \(port) is already used by the active tunnel \(other).")
        }
        return (port, nil)
    }
}

/// Picks a project from every project the credentials can see, or takes a
/// typed project ID.
private struct AddProjectView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let onAdd: (String) -> Void

    @ViewState private var filter = ""
    @ViewState private var chosen: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Add Project").font(.headline)
            TextField("Filter, or type a project ID", text: $filter)
                .textFieldStyle(.roundedBorder)
                .onChange(of: filter) { chosen = nil }
            Text(statusText).foregroundStyle(.secondary)
            List(visible, selection: $chosen) { p in
                Text(p.name.isEmpty || p.name == p.id ? p.id : "\(p.id)  (\(p.name))")
                    .lineLimit(1)
                    .tag(p.id)
            }
            .listStyle(.bordered)
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Add", action: add)
                    .keyboardShortcut(.defaultAction)
                    .disabled(project.isEmpty)
            }
        }
        .padding()
        .frame(width: 550, height: 480)
        .onAppear { model.loadAllProjects() }
    }

    private var project: String {
        chosen ?? filter.trimmingCharacters(in: .whitespaces)
    }

    private var visible: [Project] {
        let all = model.allProjects ?? []
        let q = filter.trimmingCharacters(in: .whitespaces).lowercased()
        if q.isEmpty { return all }
        return all.filter { $0.id.lowercased().contains(q) || $0.name.lowercased().contains(q) }
    }

    private var statusText: String {
        if model.allProjectsError != nil {
            return "Failed to list projects (see logs). You can still type a project ID."
        }
        guard let all = model.allProjects else { return "Loading accessible projects…" }
        return "\(all.count) accessible projects"
    }

    private func add() {
        let p = project
        dismiss()
        onAdd(p)
    }
}

private extension String {
    var nonEmpty: String? { isEmpty ? nil : self }
}
