# Cloud SQL Proxy menu bar app: design

A macOS menu bar app that runs local TCP proxies to Google Cloud SQL instances
using `cloud.google.com/go/cloudsqlconn`. It replaces running `cloud-sql-proxy`
by hand for each instance.

The UI is SwiftUI. Everything else (config, credentials, API calls, proxies,
logs, start at login) is a Go core built as a static C library and linked
into the Swift binary. Go keeps the connector, which handles the ephemeral
client certificates and server verification; there is no Swift equivalent.

## Requirements

These came from the user and should be kept unless they say otherwise:

1. **Discovery, not typing.** The user picks the projects they care about,
   searching all projects the credentials can see. The app then lists the Cloud
   SQL instances of those projects, and the user enables or disables each one.
   Connection names are never typed by hand.
2. **Port is the only setting** per instance. New tunnels get the next free
   port: the highest used port plus one, starting at 10200.
3. **One menu bar toggle**: a single *Connect*/*Disconnect* item for all
   enabled instances. There are no per-instance menu items. The menu holds a
   status line, the toggle, *Tunnels…*, *Logs…* and *Quit*.
4. **Logs are viewable in the app** (the *Logs…* window) and also go to a file.
5. **Expired credentials open a window** that lets the user sign in again
   without a terminal.
6. **Settings persist** to `~/.config/cloudsqlproxy.json`, in the layout of
   the team's `cloud-sql-proxy/tunnels-all.json`.
7. **Enabled tunnels start automatically** when the app launches.
8. **Start at login** is an option in the tunnels window.

## Files

Go core (`core/`, package `main`, built with `-buildmode=c-archive`):

| File | Responsibility |
|---|---|
| `core/export.go` | The C API (`Core*` functions) the app calls; JSON for structured data |
| `core/events.go` | Calls the app's event callback (state changed, new logs, credentials invalid) |
| `core/app.go` | Connect, the periodic credential check, reconnect after sign-in |
| `core/config.go` | `ConfigStore`: load and save the JSON config, tunnel naming, port suggestion and conflict checks |
| `core/autostart.go` | Start at login through `~/Library/LaunchAgents/com.pakohan.cloudsqlproxy.plist` |
| `core/proxy.go` | `Manager`: one shared `cloudsqlconn.Dialer` and one listener per enabled instance |
| `core/auth.go` | `Auth`: ADC loading and expiry detection; `SignIn` runs `gcloud` |
| `core/gcp.go` | `ListProjects` (Resource Manager v1) and `ListInstances` (SQL Admin v1beta4) |
| `core/logs.go` | `LogBuffer`: `log` output goes to a file plus an in-memory ring |

SwiftUI app (`Sources/CloudSQLProxy/`; `Sources/CCore` is the module map for
the generated header):

| File | Responsibility |
|---|---|
| `App.swift` | `MenuBarExtra` menu, app delegate, tray icons, `Windows` (opens the windows) |
| `Core.swift` | Typed Swift wrappers around the C API |
| `AppModel.swift` | `@Observable` state: status, config, cached API results, logs |
| `TunnelsView.swift` | Tunnels window: project sidebar, *Add Project* sheet, instance rows with enable toggle and port, start at login |
| `LogsView.swift` | Log window (an `NSTextView`, since `Text` is slow with thousands of lines) |
| `SignInView.swift` | Re-login window |
| `ViewState.swift` | `ViewState`, an alias for SwiftUI's `State` property wrapper (see below) |

## Config format

This matches `tunnels-all.json` in the team's `cloud-sql-proxy` repo (its
Makefile generates and validates that file), plus a `projects` key:

```json
{
  "projects": ["my-project"],
  "tunnels": [
    {
      "name": "mydb",
      "instance": "my-project:europe-west1:my-db",
      "port": 10001,
      "label": "My DB (prod)",
      "enabled": true
    }
  ]
}
```

These rules follow the Makefile, with one deliberate exception:

- `name` is unique across all tunnels.
- `port` only has to be unique among **enabled** tunnels; the user asked for
  this. Disabled tunnels may share a port (the Makefile's `validate` target
  would reject that). New tunnels still get a port no tunnel uses yet.
  Conflicts show up in three places:
  - a red error under each affected row;
  - a red summary at the top of the detail pane (`PortConflicts`), which also
    catches hand-edited files;
  - an error dialog when enabling a tunnel whose port an enabled tunnel
    already uses. The app doesn't reassign the port silently.
  Every visible row re-checks its error when any port or enabled state
  changes (`rowChecks`), so fixing one side clears both.
- New tunnels get the highest port plus one (10200 if there are none), and
  `name` = `label` = instance name. On a name collision, the name gets a
  `project-` prefix.
- Tunnels are saved sorted by name.
- A missing `enabled` means enabled; the generated `tunnels-team-content.json`
  leaves it out. The app always writes it.
- Unknown top-level keys, such as `ssh_tunnels`, are kept when saving
  (`ConfigStore.other`).
- A tunnel entry is created once the user toggles it or edits its port.
  Removing a project from the list keeps its tunnels.

Writes are atomic: the file is written to a temp file and renamed.

## Key decisions

### Credentials and expiry detection

- Credentials are Application Default Credentials
  (`credentials.DetectDefault`, cloud-platform scope), usually created by
  `gcloud auth application-default login`.
- `Auth` wraps the token provider in `watchedTokenProvider`. A token-endpoint
  `auth.Error` with HTTP 400 or 401 (for example `invalid_grant` or
  `invalid_rapt`) counts as invalid credentials and calls `Auth.OnInvalid`,
  which opens `AuthWindow`. Network and 5xx errors don't count.
- Each failure is reported once per credential generation (`gen`,
  `reported`), so the window doesn't reopen over and over. `Reload()` resets
  both.
- The same wrapped credentials go to the dialer and to both API clients, so
  any of them can trigger the window.
- Listeners don't need a token. Expiry is therefore checked on purpose: right
  after *Connect*, and every 5 minutes while connected (`watchCredentials`).
- Re-login runs `gcloud auth application-default login` as a subprocess and
  logs its output. Apps launched from Finder don't inherit the shell's `PATH`,
  so `findGcloud` also checks the usual Homebrew and SDK install paths.
- After a successful login: `Auth.Reload()`, then `Manager.Reconnect()` (the
  dialer must be rebuilt to use the new credentials), then the instances list
  reloads. If an earlier *Connect* failed for lack of credentials,
  `connectAfterSignIn` connects now.

### Proxy manager

- `Manager.connected` is the user's intent. Individual listeners can fail
  (for example, port in use). Failures are logged, and the status line shows
  "N of M running".
- `Manager.Apply(connName)` syncs one instance with its config while
  connected. The instances window calls it on every toggle, and on port edits
  after a 1-second pause in typing, so the proxy isn't restarted on every
  keystroke.
- Stopping a proxy closes its listener **and** its open client and server
  connections (`proxy.conns`). Without that, `io.Copy` goroutines would leak.
- The dialer uses `WithLazyRefresh()`: certificates are refreshed when a
  connection needs them, not in the background. This suits a laptop that
  sleeps.
- Listeners bind to `127.0.0.1` only.

### Project and instance discovery

- The user's account sees about **9,300 projects**, so scanning all of them is
  not an option. The user adds projects explicitly with *Add Project…*. That
  dialog lists every accessible project (fetched once, cached until refresh,
  about 15 s) with a filter, and also accepts a typed project ID.
- Instances are loaded for the selected projects only, when the window opens,
  and cached until refresh.
- The first list entry, "Tunnels", shows every configured tunnel, including
  ones whose project isn't in the list.
- Projects where the SQL Admin API is disabled or access is denied show the
  error text in the detail pane.
- The detail pane has a filter field that matches name, label, connection
  name and engine/region/state. It resets when another list entry is
  selected.
- A row only creates a tunnel entry once the user edits its port or toggles
  it, not when it is shown with the suggested port.

### Start at login

A checkbox writes or removes a LaunchAgent plist with `RunAtLoad` that points
at the running binary (`os.Executable`). The plist is not loaded with
`launchctl`; it takes effect at the next login. On every start,
`UpdateAutostart` rewrites the plist if the binary has moved.

### Go core and C API

- The app calls `CoreInit` once at launch with a C callback. Go calls it from
  its own goroutines with an event number (`CORE_EVENT_*`); `AppModel` hops to
  the main thread and refreshes the status and config, or the logs, or opens
  the sign-in window.
- Strings returned by Go are `malloc`ed and freed by `Core.swift`. Errors come
  back as a message or `NULL`; lists come back as JSON
  (`{"items": [...], "error": "..."}`).
- Calls that do network or listener work (connect, apply, list, sign-in)
  block. The model runs them in detached tasks, never on the main thread.
- `Manager` and `ConfigStore` are mutex-protected and safe from any goroutine,
  so the Swift side doesn't need its own locking.
- Validation rules (port conflicts, suggested ports) stay in Go; the Swift
  rows ask the core (`CorePortConflict`, `CoreSuggestPort`) instead of
  duplicating them. Rows read `model.config` while rendering, and every config
  change sends a state event, so all visible rows re-check their errors.
- `CoreSetTunnel` only saves; `CoreApply` syncs the running proxy. Port edits
  are applied after a 1-second pause (`.task(id: portText)`).

### SwiftUI app

- Windows are `NSWindow`s hosting SwiftUI views (`Windows` in `App.swift`),
  not `Window` scenes. The sign-in window has to open from a core event
  rather than from a view, and the app has to call `NSApp.activate()` to bring
  windows to the front, because it has no Dock icon.
- Bars below a `NavigationSplitView` or `List` go in a `VStack` under it,
  not in `.safeAreaInset`. The split view ignores the inset and keeps its full
  height, so the *Start at login* bar covered the sidebar's *Add Project…*
  button (fixed in 0.2.1).
- No Dock icon: `LSUIElement` in `Info.plist`, plus
  `setActivationPolicy(.accessory)` so `make run` (no bundle) behaves the
  same.
- **Don't write `@State`.** Newer SDKs declare a `@State` macro, which takes
  precedence over the property wrapper. Its plugin (`SwiftUIMacros`) ships
  only with Xcode, so the build fails with just the Command Line Tools, which
  is what Homebrew builds with. Use `@ViewState`, an alias for the property
  wrapper.
- Tray icons are the SVGs in `assets/`, copied into the bundle's Resources
  and loaded as template images. A plain `swift build` binary has no bundle
  and falls back to the SF Symbols `cylinder` and `cylinder.fill`.
- `AppModel` drops stale API results using `loadGen`, like the Fyne version
  did, and only fetches while the tunnels window is visible.

### Logs

`log` output goes to `~/Library/Logs/cloudsqlproxy.log` (visible in Console.app)
and stderr, with the last 5,000 lines kept in memory. The log window redraws at
most every 200 ms and only while visible. `cloudsqlconn` debug output is logged
with a `[cloudsqlconn]` prefix. Proxy lines are prefixed with `[connection name]`.
The Swift side logs through `CoreLog`.

## Building and testing

```sh
make core     # build/libcore.a and build/libcore.h from core/
make build    # debug build with swift build (make run builds and starts it)
make app      # dist/Cloud SQL Proxy.app
make install  # copies the app to ~/Applications (quit the running app first)
```

- Needs Go and Swift. The Command Line Tools are enough; Xcode isn't needed.
  `Package.swift` links `build/libcore.a` through the `CCore` system library
  module, so build the core first (`make build` and `make app` do).
- The deployment target is macOS 14 (`@Observable`). It is set in
  `Package.swift`, in `Info.plist` (`LSMinimumSystemVersion`) and as
  `MACOSX_DEPLOYMENT_TARGET` in the Makefile for the cgo objects; keep them in
  sync.
- `make app` assembles the bundle by hand: the release binary (named
  `cloudsql-tray`, the old Fyne name, so existing Homebrew service definitions
  keep working), `Info.plist` with the version from `VERSION` and the build
  number = git commit count, `AppIcon.icns` made from `Icon.png` with `sips`
  and `iconutil`, and the tray SVGs. It then signs the bundle ad hoc.
- Icons: `Icon.png` is the app icon, generated from `assets/icon.svg` with
  `make icon` (needs `rsvg-convert`). The outlined tray icon means
  disconnected; the filled one means connected.
- CI: `.github/workflows/build.yml` builds the app on a macOS arm64 runner for
  every push to main and every PR, and uploads it as the artifact
  `cloud-sql-proxy-macos-arm64`. That artifact is a zip that contains a
  `ditto` zip of the `.app`.
- The ad-hoc signature isn't notarized. A downloaded copy is quarantined:
  run `xattr -dr com.apple.quarantine "Cloud SQL Proxy.app"`, or allow it
  under System Settings → Privacy & Security.
- To try a build while the installed app is running, start the bundle's
  binary with another `HOME` that holds a copy of
  `.config/gcloud/application_default_credentials.json` and a config with
  different ports. Otherwise both instances fight over the ports and the
  config file.

There are no automated tests yet. To check listing against real credentials,
write a throwaway `_test.go` in `core/` that calls `ListProjects`/`ListInstances`
with `(&Auth{}).Credentials()`. These calls are read-only.

## Distribution (Homebrew)

`Formula/cloudsqlproxy.rb` builds from source. A cask would download a
prebuilt app instead, which macOS quarantines, and the app is only signed ad
hoc (not notarized):

- **Install:** the repo itself is the tap (`brew tap pakohan/cloudsqlproxy
  https://github.com/pakohan/cloudsqlproxy`; the URL is needed because the
  repo isn't named `homebrew-cloudsqlproxy`). The formula clones the tagged
  commit over HTTPS and runs `make app`.
  Homebrew's `go` is a build dependency; Swift comes from the Command Line
  Tools. `go.mod`'s Go version must not be newer than Homebrew's `go`.
  `SWIFT_FLAGS=--disable-sandbox` is passed because SwiftPM's sandbox can't
  run inside Homebrew's.
- **Autostart:** the formula's `service` block runs the binary inside the
  bundle via `opt_prefix`, so `brew services start cloudsqlproxy` gives an
  autostart that survives upgrades. The in-app "Start at login" would point at
  a versioned Cellar path instead; the caveats say to leave it off.
- **Quarantine:** a locally built app isn't quarantined, so no `xattr` step is
  needed.
- **Releases:** `make release VERSION=x.y.z` writes the version to
  `VERSION`, tags `vx.y.z`, writes that tag and its commit into the
  formula, commits, and pushes `main` and the tag.
- **Testing the formula without pushing:**
  1. Create a local tap with `brew tap-new <user>/<name> --no-git`.
  2. Copy the formula into it, pointing the `url` at a `file://` clone that
     has the tag.
  3. Run `brew install --build-from-source`, then `brew test`.

  Don't set `HOMEBREW_NO_INSTALL_FROM_API`; it clones `homebrew/core`
  (1.5 GB).
- **Tap trust:** Homebrew 7 refuses to load formulae from untrusted
  third-party taps ("Refusing to load formula … from untrusted tap"). Users
  must run `brew trust pakohan/cloudsqlproxy` once after tapping.

## Not done yet

- Running via `go run` and enabling start at login points the LaunchAgent at
  a temporary binary; build the app first.
- Only arm64 builds. An Intel or universal build would need a second
  `GOARCH=amd64` core, `lipo`, and `swift build --arch x86_64 --arch arm64`.
- IAM database authentication, private IP and PSC are not exposed (the dialer
  uses defaults, which means public IP).
