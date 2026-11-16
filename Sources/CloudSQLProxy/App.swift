import AppKit
import SwiftUI

@main
struct CloudSQLProxyApp: App {
    @NSApplicationDelegateAdaptor private var delegate: AppDelegate
    private let model = AppModel.shared

    var body: some Scene {
        MenuBarExtra {
            Text(model.statusText)
            Button(model.status.connected ? "Disconnect" : "Connect") { model.toggleConnection() }
                .disabled(!model.status.connected && model.status.enabled == 0)
            Divider()
            Button("Tunnels…") { delegate.windows.showTunnels() }
            Button("Logs…") { delegate.windows.showLogs() }
            Divider()
            Button("Quit") { NSApp.terminate(nil) }
        } label: {
            Image(nsImage: model.status.connected ? TrayIcon.connected : TrayIcon.disconnected)
        }
    }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    let windows = Windows(model: AppModel.shared)

    func applicationDidFinishLaunching(_ notification: Notification) {
        // A menu bar app: no Dock icon, also when run outside the app bundle,
        // which has LSUIElement set.
        NSApp.setActivationPolicy(.accessory)
        let model = AppModel.shared
        model.onAuthInvalid = { [windows] msg in windows.showSignIn(error: msg) }
        do {
            try model.start()
        } catch {
            let alert = NSAlert()
            alert.messageText = "Cloud SQL Proxy can't start"
            alert.informativeText = error.localizedDescription
            alert.runModal()
            NSApp.terminate(nil)
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        Core.shutdown()
    }
}

/// Menu bar icons. The bundle has them in Resources; a plain `swift run`
/// binary falls back to SF Symbols. Template images adapt to light and dark
/// menu bars. The outlined icon means disconnected, the filled one connected.
enum TrayIcon {
    static let disconnected = load("tray", fallback: "cylinder")
    static let connected = load("tray-connected", fallback: "cylinder.fill")

    private static func load(_ name: String, fallback: String) -> NSImage {
        let image = Bundle.main.url(forResource: name, withExtension: "svg").flatMap(NSImage.init(contentsOf:))
            ?? NSImage(systemSymbolName: fallback, accessibilityDescription: nil)!
        image.size = NSSize(width: 18, height: 18)
        image.isTemplate = true
        return image
    }
}

/// Opens the app's windows. They are AppKit windows hosting SwiftUI views, so
/// they can be opened from model callbacks, not only from views, and brought to
/// the front although the app has no Dock icon.
@MainActor
final class Windows: NSObject, NSWindowDelegate {
    private let model: AppModel
    private var tunnels: NSWindow?
    private var logs: NSWindow?
    private var signIn: NSWindow?
    private let signInState = SignInState()

    init(model: AppModel) {
        self.model = model
    }

    func showTunnels() {
        model.tunnelsVisible = true
        show(&tunnels, title: "Cloud SQL Tunnels", size: NSSize(width: 1000, height: 600)) {
            TunnelsView().environment(model)
        }
    }

    func showLogs() {
        model.logsVisible = true
        show(&logs, title: "Cloud SQL Proxy Logs", size: NSSize(width: 900, height: 500)) {
            LogsView().environment(model)
        }
    }

    func showSignIn(error: String) {
        signInState.error = error
        show(&signIn, title: "Google Cloud Sign-in", size: NSSize(width: 520, height: 260)) {
            SignInView(state: signInState) { [weak self] signedIn in
                self?.signIn?.close()
                if signedIn { self?.model.reload() }
            }
        }
    }

    private func show<V: View>(_ window: inout NSWindow?, title: String, size: NSSize, content: () -> V) {
        if window == nil {
            let w = NSWindow(
                contentRect: NSRect(origin: .zero, size: size),
                styleMask: [.titled, .closable, .miniaturizable, .resizable],
                backing: .buffered, defer: false)
            w.title = title
            w.isReleasedWhenClosed = false
            w.contentViewController = NSHostingController(rootView: content())
            w.setContentSize(size)
            w.delegate = self
            w.center()
            window = w
        }
        NSApp.activate()
        window?.makeKeyAndOrderFront(nil)
    }

    func windowWillClose(_ notification: Notification) {
        switch notification.object as? NSWindow {
        case tunnels: model.tunnelsVisible = false
        case logs: model.logsVisible = false
        case signIn: signInState.cancel()
        default: break
        }
    }
}
