class Deskbridge < Formula
  desc "Terminal-first keyboard, mouse, and file-transfer bridge for macOS and Linux"
  homepage "https://example.invalid/deskbridge"
  version "0.1.2"

  on_macos do
    if Hardware::CPU.arm?
      url "https://example.invalid/deskbridge-0.1.2-darwin-arm64.tar.gz"
      sha256 "29418ca133cbfc2017ef65195d955720fbea7cda375683bf0cb00f1919c00937"
    else
      url "https://example.invalid/deskbridge-0.1.2-darwin-amd64.tar.gz"
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
