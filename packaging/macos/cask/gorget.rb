# Homebrew cask. Publish in a tap (e.g. anand34577/homebrew-tap, Casks/gorget.rb) and update version and sha256 per release.
cask "gorget" do
  version "0.3.0"
  sha256 :no_check # replace with the sha256 of the .pkg from SHA256SUMS

  url "https://github.com/anand34577/gorget/releases/download/v#{version}/gorget_#{version}_macos.pkg"
  name "Gorget"
  desc "Self-hosted WireGuard mesh VPN client"
  homepage "https://github.com/anand34577/gorget"

  pkg "gorget_#{version}_macos.pkg"

  uninstall launchctl: "gorget",
            quit:      "net.gorget.desktop",
            pkgutil:   "net.gorget.pkg",
            delete:    [
              "/usr/local/bin/gorget",
              "/Applications/Gorget.app",
              "/Library/LaunchAgents/net.gorget.desktop.plist",
            ]

  zap trash: "/Library/Application Support/Gorget"
end
