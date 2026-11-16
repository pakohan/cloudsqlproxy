APP_NAME := Cloud SQL Proxy
# The executable keeps the name of the Fyne build, which the Homebrew service
# definitions of existing installs point at.
EXE      := cloudsql-tray
DIST     := dist
APP      := $(DIST)/$(APP_NAME).app
VERSION  := $(shell cat VERSION)
# The bundle's build number is the commit count, so it increases with every commit.
BUILD    := $(shell git rev-list --count HEAD 2>/dev/null || echo 1)
# Keep in sync with Package.swift and LSMinimumSystemVersion in Info.plist.
export MACOSX_DEPLOYMENT_TARGET := 14.0

# Homebrew passes --disable-sandbox: SwiftPM's own sandbox can't nest inside
# the one Homebrew builds in.
SWIFT_FLAGS ?=

CORE     := build/libcore.a
GO_SRC   := $(wildcard core/*.go) go.mod go.sum

.PHONY: core build run app install icon clean release

# The Go core as a static C library plus header, which Package.swift links.
core: $(CORE)
$(CORE): $(GO_SRC)
	CGO_ENABLED=1 GOARCH=arm64 go build -buildmode=c-archive -trimpath -o $@ ./core

# Debug build, for development. Without the bundle the menu bar icon falls
# back to an SF Symbol.
build: $(CORE)
	swift build

run: build
	"$$(swift build --show-bin-path)/CloudSQLProxy"

# macOS app bundle in dist/, signed ad hoc.
app: $(CORE) build/AppIcon.icns
	swift build -c release --arch arm64 $(SWIFT_FLAGS)
	rm -rf "$(APP)"
	mkdir -p "$(APP)/Contents/MacOS" "$(APP)/Contents/Resources"
	cp "$$(swift build -c release --arch arm64 $(SWIFT_FLAGS) --show-bin-path)/CloudSQLProxy" "$(APP)/Contents/MacOS/$(EXE)"
	cp Info.plist "$(APP)/Contents/Info.plist"
	plutil -replace CFBundleShortVersionString -string "$(VERSION)" "$(APP)/Contents/Info.plist"
	plutil -replace CFBundleVersion -string "$(BUILD)" "$(APP)/Contents/Info.plist"
	cp build/AppIcon.icns assets/tray.svg assets/tray-connected.svg "$(APP)/Contents/Resources/"
	codesign --force --sign - "$(APP)"

build/AppIcon.icns: Icon.png
	rm -rf build/AppIcon.iconset
	mkdir -p build/AppIcon.iconset
	for s in 16 32 128 256 512; do \
		sips -z $$s $$s Icon.png --out build/AppIcon.iconset/icon_$${s}x$${s}.png >/dev/null; \
		sips -z $$((s*2)) $$((s*2)) Icon.png --out build/AppIcon.iconset/icon_$${s}x$${s}@2x.png >/dev/null; \
	done
	iconutil -c icns -o $@ build/AppIcon.iconset

# Quit the running app first; it is replaced in place.
install: app
	rm -rf "$(HOME)/Applications/$(APP_NAME).app"
	mkdir -p "$(HOME)/Applications"
	cp -R "$(APP)" "$(HOME)/Applications/"

# Regenerates Icon.png from assets/icon.svg; needs rsvg-convert (brew install librsvg).
icon:
	rsvg-convert -w 1024 -h 1024 assets/icon.svg -o Icon.png

clean:
	rm -rf build .build $(DIST)

# Usage: make release VERSION=0.2.0
# Sets the app version, tags the release, points the Homebrew formula at the
# tagged commit and pushes both. The formula commit comes after the tag, so the
# tag itself doesn't need to know its own revision.
release:
	@test "$(origin VERSION)" = "command line" || { echo "usage: make release VERSION=x.y.z"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "work tree is not clean"; exit 1; }
	echo "$(VERSION)" > VERSION
	git diff --quiet || git commit -qam "Release v$(VERSION)"
	git tag v$(VERSION)
	sed -i '' -e 's/tag: *"v[^"]*"/tag:      "v$(VERSION)"/' \
		-e "s/revision: *\"[0-9a-f]*\"/revision: \"$$(git rev-parse HEAD)\"/" Formula/cloudsqlproxy.rb
	git commit -qam "cloudsqlproxy $(VERSION)"
	git push origin main v$(VERSION)
