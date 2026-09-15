#!/usr/bin/env bash
set -euo pipefail
cache="${XDG_CACHE_HOME:-$HOME/.cache}/deskbridge"
binary="$cache/0.2.0/linux-x64/deskbridge"
if [ ! -x "$binary" ]; then
  binary="$(command -v deskbridge || true)"
fi
if [ -z "$binary" ] || [ ! -x "$binary" ]; then
  echo 'Once DeskBridge kurulumunu tamamla.' >&2
  exit 1
fi
command -v systemctl >/dev/null || { echo 'systemd gerekli.' >&2; exit 1; }
config="${XDG_CONFIG_HOME:-$HOME/.config}"
[ -f "$config/deskbridge/relay.json" ] || { echo 'Once cihaz eslestirmesini tamamla.' >&2; exit 1; }
if [[ "$binary" == *'"'* || "$binary" == *'%'* || "$binary" == *$'\n'* ]]; then
  echo 'Desteklenmeyen kurulum yolu.' >&2; exit 1
fi
mkdir -p "$config/systemd/user"
printf '[Unit]\nDescription=DeskBridge encrypted relay\nStartLimitIntervalSec=0\n\n[Service]\nExecStart="%s" connect\nRestart=always\nRestartSec=5\n\n[Install]\nWantedBy=default.target\n' "$binary" > "$config/systemd/user/deskbridge.service"
systemctl --user daemon-reload
systemctl --user enable --now deskbridge.service
systemctl --user --no-pager status deskbridge.service
