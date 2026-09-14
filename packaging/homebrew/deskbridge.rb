class Deskbridge < Formula
  desc "Terminal-first keyboard, mouse, and file-transfer bridge for macOS and Linux"
  homepage "https://example.invalid/deskbridge"
  version "0.1.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://example.invalid/deskbridge-0.1.0-darwin-arm64.tar.gz"
      sha256 "cb08b443616f2991e929e9ca9a76a2bb13b9d3ab50f1dfee741cd1154e7d23bd"
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
