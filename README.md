# DeskBridge

DeskBridge is a terminal-first device bridge for a personal Mac + Linux setup.
It is a single Go binary: no Python runtime, no venv, no app framework.

It pairs machines, generates Deskflow-compatible keyboard/mouse configuration,
starts Deskflow when installed, and transfers files between reachable devices.

## Install From Source

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

Then open the printed receiver URL in a browser to upload files through the web
form, or send from another terminal with `deskbridge send`.

Send a file from either machine:

```bash
deskbridge send ./file.zip --to http://OTHER_REACHABLE_HOST_OR_OVERLAY_IP:47889
```

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
Ethernet IP, Tailscale/WireGuard IP, SSH tunnel endpoint, or a future relay
address.

## Deskflow

DeskBridge does not vendor Deskflow. Install Deskflow on both machines, then let
DeskBridge generate the config and launch the installed binary.

Example config generation:

```bash
deskbridge --state examples/imac-linux.deskbridge.json deskflow-config --write --output deskflow.conf
deskbridge start-server --config deskflow.conf --dry-run
deskbridge --state examples/imac-linux.deskbridge.json start-client --controller imac --dry-run
```

## Roadmap

- Phase 1: macOS + Linux terminal MVP, Deskflow config/launch, direct file transfer.
- Phase 1.5: overlay/relay fallback for blocked school networks.
- Phase 2: Windows support with `.exe`, installer, service startup, and package manager manifests.
