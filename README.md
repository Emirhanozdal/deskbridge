# DeskBridge

One application for sharing a keyboard, mouse, clipboard files and ordinary
files between macOS and Linux over an authenticated relay on port 443.

DeskBridge is a monorepo. The native relay is in `cmd/deskbridge`, the desktop
application is in `desktop`, the Worker relay is in `relay`, and the integrated
Deskflow-derived input engine is in `engine/src`. Users install DeskBridge only;
the separate Deskflow interface is not part of the packaged application.

## Connect

```bash
npx --yes github:Emirhanozdal/deskbridge#v0.2.0 connect --side a
```

Use side `a` on the Mac and side `b` on Linux. Enter the same private pairing
code on both devices. Settings are saved in the OS user configuration directory
with mode 0600. Subsequent launches need only `deskbridge`, or the npx command.
Keep the process running on each machine.

Each installation uses a relay in the owner's Cloudflare account so traffic and
quotas are not shared with the project account. Use the Deploy to Cloudflare
button below, accept the prompted Worker and Durable Object setup, then enter the
resulting `https://...workers.dev` address and the same random pairing code on
both devices.

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/Emirhanozdal/deskbridge/tree/main/relay)

The application sends only a domain-separated SHA-256 hash of the 256-bit code
to the relay as its room identifier. The private code itself stays on the paired
devices.

Connections use WebSocket TLS over port 443, with a second mutually authenticated
TLS 1.3 connection inside it. The private code derives the inner certificate;
the relay cannot derive that key from its authentication hash. Do not publish
the pairing code. Each room supports one paired device on each side.

## Files

In another terminal while both peers are connected:

```bash
deskbridge send-peer ./file.zip
```

Received files go to Downloads, or the directory set with `connect --dir`.
The same command works in either direction. The desktop application accepts
files dropped onto its transfer area and exposes received files as native drag
sources. File copy/paste transport can be enabled in Settings.

## Desktop Application

The desktop interface pairs devices, places the peer around the main screen,
sends files, records transfer history, and can mirror native file-copy clipboard
events. Relay credentials stay in the main process and are never exposed to the
renderer. macOS builds embed the relay and `deskbridge-input` engine inside one
`DeskBridge.app` bundle.

The local port 24801 forwards to the peer's input port 24800. The file proxy on
47890 forwards only to the peer's internal upload server. Both listeners bind
only to localhost. The relay and input service restart automatically after a
process failure when their per-user background services are enabled.

## Installation

Requires Node.js 18+, curl and tar for npx; no Go compiler or sudo is needed.
The launcher verifies native release checksums and caches the binaries.
Supported packages: Apple Silicon macOS, x86_64 Linux. Windows is phase 2.
The bare npm registry name is not published; use the GitHub npx command for the
legacy terminal build. Packaged desktop releases do not require Node.js.

Linux Mint/Debian/Ubuntu desktop installation:

```bash
curl -fL --retry 5 https://emirhanozdal.github.io/deskbridge/i -o /tmp/db.sh && bash /tmp/db.sh
```

The desktop package contains the relay and input engine. Do not install or open
Deskflow separately. Existing pairing settings are reused from the DeskBridge
user configuration directory.

## Verification and Limits

The deployed 443 relay was tested from the school network using two clients on
one Mac: authenticated inner TLS and byte-identical binary file uploads passed
in both directions. Unauthenticated relay requests returned 401. Unit tests
reject mismatched pairing secrets and invalid relay origins.

The current source contains native file clipboard transport, duplicate-safe
atomic uploads, native outbound dragging, startup controls and graphical screen
placement. Unit tests cover clipboard formats, layout preservation, engine
discovery, upload safety and relay authentication. The integrated input engine
has been compiled and packaged on Apple Silicon macOS.

The integrated engine is active on the development Mac and has passed macOS
input-permission validation. The Linux desktop package is built on Ubuntu 24.04;
a physical Linux Mint interoperability run is still required. Apple notarization
is not complete. `doctor` reports local dependency detection, not proof of a
connected or operational KVM.

The legacy `tunnel` command uses cloudflared on 7844; it is no longer the default.
That route timed out on the tested school network. `receive-local` explicitly
starts the local HTTP API; `--ui` optionally enables its browser upload form.

## Development

```bash
make test build
make release VERSION=0.2.0
npm --prefix desktop install
node scripts/build-input.cjs
node scripts/package-macos.cjs
```

Worker source and deployment config are isolated in `relay/` for Cloudflare's
deploy flow. Deployment requires Cloudflare authorization but no API token is
stored by DeskBridge. A Workers/SQLite Durable Object hosts each WebSocket pair,
uses the Hibernation API while idle, and does not store file contents. The
project owner's live relay is allowlisted to the development device pair; public
users deploy the same source into their own account.

Live relay test (requires the owner's private code file):

```bash
DESKBRIDGE_TEST_RELAY=https://YOUR-WORKER.workers.dev \
DESKBRIDGE_TEST_CODE_FILE=/path/to/private-code \
go test ./cmd/deskbridge -run TestLiveRelayFileTransfer -v
```
