# DeskBridge Windows Packaging

Windows support is phase 2.

The Go CLI can already be cross-built:

```bash
make release-windows
```

Planned packaging:

- `deskbridge.exe` release archive
- winget manifest
- Chocolatey package
- optional Windows service for receive/startup mode
- Deskflow/Synergy binary discovery in common install paths
