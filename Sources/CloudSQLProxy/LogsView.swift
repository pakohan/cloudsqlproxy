import AppKit
import SwiftUI

/// Shows the in-memory log and follows new lines.
struct LogsView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        VStack(spacing: 0) {
            LogTextView(text: model.logText)
            Divider()
            HStack {
                Text(Core.logPath())
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .foregroundStyle(.secondary)
                Spacer()
                Button("Copy All") {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(model.logText, forType: .string)
                }
                Button("Clear") { model.clearLogs() }
                Button("Open Log File") {
                    NSWorkspace.shared.open(URL(fileURLWithPath: Core.logPath()))
                }
            }
            .padding(8)
        }
    }
}

/// A read-only NSTextView: SwiftUI's Text is slow with thousands of lines.
private struct LogTextView: NSViewRepresentable {
    let text: String

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSTextView.scrollableTextView()
        let view = scroll.documentView as! NSTextView
        view.isEditable = false
        view.font = .monospacedSystemFont(ofSize: NSFont.smallSystemFontSize, weight: .regular)
        view.textContainerInset = NSSize(width: 4, height: 4)
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        let view = scroll.documentView as! NSTextView
        guard view.string != text else { return }
        view.string = text
        view.scrollToEndOfDocument(nil)
    }
}
