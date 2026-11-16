import SwiftUI

/// SwiftUI's property wrapper `State` under another name. Newer SDKs declare a
/// `@State` macro that takes precedence over the wrapper, and its plugin ships
/// only with Xcode, so `@State` doesn't compile with the Command Line Tools
/// alone (which Homebrew builds with).
typealias ViewState = SwiftUI.State
