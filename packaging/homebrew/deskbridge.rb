class Deskbridge < Formula
  desc "Terminal-first keyboard, mouse, and file-transfer bridge for macOS and Linux"
  homepage "https://example.invalid/deskbridge"
  version "0.1.1"

  on_macos do
    if Hardware::CPU.arm?
      url "https://example.invalid/deskbridge-0.1.1-darwin-arm64.tar.gz"
      sha256 "9323a302dce9e9a1f3f82589d08ad37c6f05d66a4d26e2bb9f9017922885f30e"
    else
      url "https://example.invalid/deskbridge-0.1.1-darwin-amd64.tar.gz"
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
