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

The personal relay is authenticated; public repository access does not grant
access to it. The owner provisions a random 256-bit pairing code and stores only
its domain-separated SHA-256 authentication hash as the Worker AUTH_HASH secret.

Connections use WebSocket TLS over port 443, with a second mutually authenticated
TLS 1.3 connection inside it. The private code derives the inner certificate;
the relay cannot derive that key from its authentication hash. Do not publish
the pairing code. The service supports one pair at a time.

## Files

In another terminal while both peers are connected:

```bash
deskbridge send-peer ./file.zip
```

Received files go to Downloads, or the directory set with `connect --dir`.
The same command works in either direction. Native desktop-to-desktop dragging
is not implemented.

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

Debian/Ubuntu package installation:

```bash
curl -fL --retry 5 https://emirhanozdal.github.io/deskbridge/i -o /tmp/db.sh && bash /tmp/db.sh
```

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

The integrated engine still needs a physical macOS-to-Linux interoperability
run and macOS input-permission validation before it replaces the previously
verified compatibility engine on an existing installation. Linux desktop
packaging and Apple notarization are not complete. `doctor` reports local
dependency detection, not proof of a connected or operational KVM.

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

Worker source and deployment config are in `relay/`. Deployment requires
Cloudflare authorization. AUTH_HASH must be configured separately; no pairing
secret belongs in source control. A Workers/SQLite Durable Object hosts the
WebSocket pair and does not store file contents.

Live relay test (requires the owner's private code file):

```bash
DESKBRIDGE_TEST_RELAY=https://YOUR-WORKER.workers.dev \
DESKBRIDGE_TEST_CODE_FILE=/path/to/private-code \
go test ./cmd/deskbridge -run TestLiveRelayFileTransfer -v
```
