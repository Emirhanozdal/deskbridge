# DeskBridge

Personal Mac + Linux keyboard, mouse and file bridge, built in Go.

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

## Keyboard and Mouse

Install Deskflow on both computers. Grant required OS input permissions and
configure Deskflow certificates and trusted fingerprints. Then create the layout:

```bash
deskbridge init
deskbridge pair
deskbridge deskflow-config --write
deskbridge start-server --name YOUR_MAC_SCREEN_NAME
```

On Linux, while the relay is connected:

```bash
deskbridge start-client --name YOUR_LINUX_SCREEN_NAME --host 127.0.0.1:24801
```

The local port 24801 forwards to the peer's Deskflow port 24800. The file proxy
on 47890 forwards only to the peer's internal upload server. Both listeners bind
only to localhost. Modern Deskflow settings are generated separately from the
screen layout. Start commands run in the foreground.

## Installation

Requires Node.js 18+, curl and tar for npx; no Go compiler or sudo is needed.
The launcher verifies native release checksums and caches the binaries.
Supported packages: Apple Silicon macOS, x86_64 Linux. Windows is phase 2.
The bare npm registry name is not published; use the GitHub npx command.

Debian/Ubuntu package installation:

```bash
curl -fL --retry 5 https://emirhanozdal.github.io/deskbridge/i -o /tmp/db.sh && bash /tmp/db.sh
```

## Verification and Limits

The deployed 443 relay was tested from the school network using two clients on
one Mac: authenticated inner TLS and byte-identical binary file uploads passed
in both directions. Unauthenticated relay requests returned 401. Unit tests
reject mismatched pairing secrets and invalid relay origins.

Physical Mac-to-Linux keyboard/mouse sharing still requires testing on the
Linux machine. Automatic background startup and native cross-desktop file
dragging are not implemented. `doctor` reports local dependency detection,
not proof of a connected or operational KVM.

The legacy `tunnel` command uses cloudflared on 7844; it is no longer the default.
That route timed out on the tested school network. `receive-local` explicitly
starts the local HTTP API; `--ui` optionally enables its browser upload form.

## Development

```bash
make test build
make release VERSION=0.2.0
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
