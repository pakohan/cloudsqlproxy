import SwiftUI

/// State of the sign-in window, kept across openings of the window.
@MainActor
@Observable
final class SignInState {
    var error = ""
    var status = ""
    var running = false

    /// Stops a running login, e.g. when the window is closed.
    func cancel() {
        if running { Core.cancelSignIn() }
    }
}

/// Lets the user sign in again via gcloud without a terminal.
struct SignInView: View {
    let state: SignInState
    /// Called to close the window; true after a successful sign-in.
    let onDone: (Bool) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Your Google Cloud credentials have expired").bold()
            Text("Sign in again to keep the Cloud SQL proxies working.\nA browser window will open for the login.")
            Text(truncated(state.error, 300))
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
            if !state.status.isEmpty {
                Text(state.status)
            }
            Spacer(minLength: 0)
            HStack {
                Spacer()
                Button("Cancel") {
                    state.cancel()
                    onDone(false)
                }
                .keyboardShortcut(.cancelAction)
                Button("Sign in with Google", action: signIn)
                    .keyboardShortcut(.defaultAction)
                    .disabled(state.running)
            }
        }
        .padding(20)
        .frame(minWidth: 420, minHeight: 220, alignment: .topLeading)
    }

    private func signIn() {
        state.running = true
        state.status = "Waiting for the login in your browser…"
        Task {
            let result = await Task.detached { Result { try Core.signIn() } }.value
            state.running = false
            switch result {
            case .success(true):
                state.status = ""
                onDone(true)
            case .success(false):
                state.status = ""
            case .failure(let error):
                state.status = "\(error.localizedDescription) (see logs)"
            }
        }
    }
}

func truncated(_ s: String, _ n: Int) -> String {
    s.count <= n ? s : s.prefix(n) + "…"
}
