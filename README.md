# DeskBridge

DeskBridge is a terminal-first device bridge for a personal Mac + Linux setup.
It is a single Go binary: no Python runtime, no venv, no app framework.

## Run With npx

```bash
npx --yes github:Emirhanozdal/deskbridge doctor
npx --yes github:Emirhanozdal/deskbridge
```

Requires Node.js 18+, curl, and tar. The launcher downloads and verifies the native
DeskBridge and cloudflared binaries once, with no sudo or Go compiler required.
Supported: Apple Silicon macOS and x86_64 Linux. Deskflow must still be installed
on both computers. The npm registry name is not published; use the GitHub command.

Current limitation: the tested school network times out connecting to Cloudflare
on port 7844. This release is not an end-to-end working school-network KVM.
Cross-desktop file dragging and automatic background startup remain unimplemented.

It pairs machines, generates Deskflow-compatible keyboard/mouse configuration,
starts Deskflow when installed, and transfers files between reachable devices.
For restricted school networks, it can keep the receiver bound to localhost and
expose it through Cloudflare Tunnel instead of opening a LAN port.

## Install From Source

Debian/Ubuntu x86_64 (public download, no GitHub login):

```bash
curl -fsSL --retry 5 https://emirhanozdal.github.io/deskbridge/i -o /tmp/deskbridge-install.sh && bash /tmp/deskbridge-install.sh
deskbridge
```

The installer installs DeskBridge and cloudflared. Running `deskbridge` or
`deskbridge receive` starts a Cloudflare file-transfer tunnel, bound locally to
127.0.0.1, and generates an upload token automatically. Keep the terminal running.
The temporary URL and token change on restart. No browser is needed; `--ui`
enables an optional browser drop zone. Desktop-to-desktop drag-and-drop is not
implemented. This HTTP tunnel does not carry Deskflow keyboard/mouse traffic.
Deskflow still requires its own reachable connection between the two devices.

Use `deskbridge receive-local` only for explicitly local HTTP reception.

```bash
go install ./cmd/deskbridge
```

Or build a local binary:

```bash
go build -o build/deskbridge ./cmd/deskbridge
./build/deskbridge help
```

## Future Package Shape

Homebrew:

```bash
brew install deskbridge
```

Debian/Ubuntu:

```bash
sudo apt install deskbridge
```

Manual public release download with GitHub CLI:

```bash
gh release download v0.1.3 -R Emirhanozdal/deskbridge -p 'deskbridge_*.deb'
sudo apt install ./deskbridge_*.deb
```

Windows is planned for phase 2. The Go CLI already has a cross-build target, but
the installer, winget/chocolatey manifests, Windows service mode, and
Deskflow/Synergy path detection still need proper Windows testing.

The repo includes starter packaging files under `packaging/`. Release URLs and
checksums need to be filled after publishing real build artifacts.

## Quick Start

```bash
deskbridge app
```

The interactive app lets you initialize a device, pair machines, generate the
Deskflow config, start server/client mode, and send or receive files.

Check readiness:

```bash
deskbridge doctor
```

## Direct Commands

On the iMac:

```bash
deskbridge init
deskbridge pair
deskbridge deskflow-config --write
deskbridge start-server
```

On the Linux PC:

```bash
deskbridge init
deskbridge start-client --host IMAC_REACHABLE_HOST_OR_OVERLAY_IP
deskbridge receive --dir ~/Downloads
```

This runs as a background-style HTTP API. It does not need a browser UI.
Use `--ui` only when you explicitly want a temporary browser drag-and-drop form.

Send a file from either machine:

```bash
deskbridge send ./file.zip --to https://YOUR-TUNNEL.trycloudflare.com --token YOUR_TOKEN
```

Token-protected receive:

```bash
DESKBRIDGE_TOKEN="$(openssl rand -hex 16)" deskbridge receive --dir ~/Downloads
```

Send with the same token:

```bash
deskbridge send ./file.zip --to http://127.0.0.1:47889 --token YOUR_TOKEN
```

## Cloudflare Tunnel

For school networks where LAN device-to-device traffic is blocked, do not bind
DeskBridge to `0.0.0.0`. Keep the receiver local and let Cloudflare carry the
HTTPS tunnel:

```bash
DESKBRIDGE_TOKEN="$(openssl rand -hex 16)"
deskbridge tunnel --dir ~/Downloads --token "$DESKBRIDGE_TOKEN"
```

`cloudflared` prints a temporary `https://*.trycloudflare.com` URL. On the other
machine:

```bash
deskbridge send ./file.zip --to https://YOUR-TUNNEL.trycloudflare.com --token "$DESKBRIDGE_TOKEN"
```

This avoids publishing a raw local port on the school network.

## Optional LAN Discovery

If the school network allows UDP broadcast:

```bash
# On one device
deskbridge advertise imac

# On the other
deskbridge scan --save
deskbridge pair
```

If discovery fails, enter the address manually. The host can be a LAN IP,
Ethernet IP, Tailscale/WireGuard IP, SSH tunnel endpoint, or Cloudflare Tunnel
URL for file transfer.

## Deskflow

DeskBridge does not vendor Deskflow. Install Deskflow on both machines, then let
DeskBridge generate the config and launch the installed binary.

Example config generation:

```bash
deskbridge --state examples/imac-linux.deskbridge.json deskflow-config --write --output deskflow.conf
deskbridge start-server --config deskflow.conf --dry-run
deskbridge --state examples/imac-linux.deskbridge.json start-client --controller imac --dry-run
```

For Deskflow 1.26, start commands generate separate INI settings with TLS and peer
fingerprint checks enabled. Pass `--name` matching the screen layout. The process
runs in the foreground so startup errors are visible. Grant the OS permissions
and configure trusted Deskflow peer fingerprints before connecting.

## Roadmap

- Phase 1: macOS + Linux terminal MVP, Deskflow config/launch, direct file transfer.
- Phase 1.5: Cloudflare Tunnel and overlay/relay fallback for blocked school networks.
- Phase 2: Windows support with `.exe`, installer, service startup, and package manager manifests.
