# This formula is auto-updated by goreleaser on release.
# Manual edits will be overwritten.
class CcPort < Formula
  desc "Claude Code project portability tool"
  homepage "https://github.com/it-bens/cc-port"
  license "MIT"

  # Placeholder — goreleaser fills in the real values on release.
  url "https://github.com/it-bens/cc-port/releases/download/v0.0.0/cc-port_Darwin_arm64.tar.gz"
  sha256 "placeholder"

  def install
    bin.install "cc-port"
  end

  test do
    system "#{bin}/cc-port", "version"
  end
end
