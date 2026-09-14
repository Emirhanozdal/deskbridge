class Deskbridge < Formula
  desc "Terminal-first keyboard, mouse, and file-transfer bridge for macOS and Linux"
  homepage "https://example.invalid/deskbridge"
  version "0.1.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://example.invalid/deskbridge-0.1.0-darwin-arm64.tar.gz"
      sha256 "32887680964bc93e01bf969ab77be20c3bc50aaec201fbf05e9cdf8213f90934"
    else
      url "https://example.invalid/deskbridge-0.1.0-darwin-amd64.tar.gz"
      sha256 "CHANGE_ME"
    end
  end

  def install
    bin.install "deskbridge"
  end

  test do
    system "#{bin}/deskbridge", "help"
  end
end
