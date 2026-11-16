# Cloud SQL Proxy

A macOS menu bar app that proxies local ports to Google Cloud SQL instances.

- Pick projects, enable instances, choose a local port per instance.
- Connect or disconnect all enabled tunnels from the menu bar.
- Opens a sign-in window when your Google Cloud credentials expire.

Settings live in `~/.config/cloudsqlproxy.json`, and logs in
`~/Library/Logs/cloudsqlproxy.log`.

## Install with Homebrew

Homebrew builds the app from source on your Mac:

```sh
brew tap pakohan/cloudsqlproxy https://github.com/pakohan/cloudsqlproxy
brew trust pakohan/cloudsqlproxy         # Homebrew 7 refuses untrusted taps
brew install cloudsqlproxy
brew services start cloudsqlproxy        # start now and at every login
```

Upgrade with `brew update && brew upgrade cloudsqlproxy`, then
`brew services restart cloudsqlproxy`.

If the app runs as a Homebrew service, leave "Start at login" in the app
unchecked, or it starts twice. If you used a downloaded copy before, uncheck
"Start at login" in that copy, quit it, and delete it from `/Applications`.

## Build

Needs Go and Swift (the Command Line Tools are enough).

```sh
make app      # dist/Cloud SQL Proxy.app
make install  # copy it to ~/Applications
make release VERSION=0.2.0  # tag a release and update the Homebrew formula
```

See [DESIGN.md](DESIGN.md) for how it works.
