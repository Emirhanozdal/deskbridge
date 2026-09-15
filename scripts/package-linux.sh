#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
app="$root/work/dist/DeskBridge-linux-x64"
stage="$root/work/linux-package"
# single source of truth for the package version
version="$(awk '/^Version:/{print $2; exit}' "$root/packaging/linux/control")"
output="$root/outputs/deskbridge_${version}_amd64.deb"
test -x "$app/DeskBridge"
test -x "$app/resources/deskbridge"
test -x "$app/resources/deskbridge-input"
rm -rf "$stage"
mkdir -p "$stage/DEBIAN" "$stage/opt/deskbridge" "$stage/usr/bin" \
  "$stage/usr/share/applications" "$stage/usr/share/icons/hicolor/256x256/apps" \
  "$stage/etc/xdg/autostart" "$root/outputs"
cp -a "$app/." "$stage/opt/deskbridge/"
cp "$root/packaging/linux/control" "$stage/DEBIAN/control"
cp "$root/packaging/linux/deskbridge.desktop" "$stage/usr/share/applications/deskbridge.desktop"
cp "$root/packaging/linux/deskbridge.desktop" "$stage/etc/xdg/autostart/deskbridge.desktop"
cp "$root/desktop/icon.png" "$stage/usr/share/icons/hicolor/256x256/apps/deskbridge.png"
ln -s /opt/deskbridge/DeskBridge "$stage/usr/bin/deskbridge"
ln -s /opt/deskbridge/DeskBridge "$stage/usr/bin/deskbridge-app"
dpkg-deb --build --root-owner-group "$stage" "$output"
sha256sum "$output" > "$output.sha256"
echo "$output"
