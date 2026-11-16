# CLAUDE.md

macOS menu bar app (SwiftUI app + Go core as a C library) that proxies local
ports to Cloud SQL instances.

Read `DESIGN.md` before making changes. It lists the user's requirements, what
each file does, the config format and the threading rules. Update it when you
change behavior.

- Build: `make build` (binary) or `make app` (dist/Cloud SQL Proxy.app).
- Config: `~/.config/cloudsqlproxy.json`. Logs: `~/Library/Logs/cloudsqlproxy.log`.
- Go calls into Swift only through the event callback; `AppModel` hops to the
  main thread. Blocking `Core.*` calls run in detached tasks.
- Use `@ViewState`, not `@State` (the `@State` macro needs Xcode).
