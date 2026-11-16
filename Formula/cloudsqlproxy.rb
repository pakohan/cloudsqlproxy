# Builds the menu bar app from source, from the tagged commit.
# `make release` updates tag and revision.
class Cloudsqlproxy < Formula
  desc "Menu bar app that proxies local ports to Cloud SQL instances"
  homepage "https://github.com/pakohan/cloudsqlproxy"
  url "https://github.com/pakohan/cloudsqlproxy.git",
      using:    :git,
      tag:      "v0.2.1",
      revision: "8b088ca54ea2821135eefcd016cf763f9d89c999"
  head "https://github.com/pakohan/cloudsqlproxy.git", branch: "main"

  # Swift comes with the Command Line Tools, which Homebrew requires anyway.
  depends_on "go" => :build
  depends_on macos: :sonoma

  def install
    system "make", "app", "SWIFT_FLAGS=--disable-sandbox"
    prefix.install "dist/Cloud SQL Proxy.app"
  end

  service do
    run [opt_prefix/"Cloud SQL Proxy.app/Contents/MacOS/cloudsql-tray"]
    process_type :interactive
  end

  def caveats
    <<~EOS
      Start the app now and at every login with:
        brew services start cloudsqlproxy

      Leave "Start at login" in the app unchecked, or it starts twice.
      To launch it once without the service:
        open "#{opt_prefix}/Cloud SQL Proxy.app"
    EOS
  end

  test do
    assert_path_exists prefix/"Cloud SQL Proxy.app/Contents/MacOS/cloudsql-tray"
    assert_match "com.pakohan.cloudsqlproxy",
                 shell_output("plutil -p '#{prefix}/Cloud SQL Proxy.app/Contents/Info.plist'")
  end
end
