#!/bin/bash
# Builds dist/gorget_<version>_macos.pkg (universal). Must run on a Mac: the tray app needs cgo and Xcode tools.
# Not signed or notarised (no Apple developer certificate yet); users must allow it in System Settings > Privacy & Security.
set -euo pipefail
VERSION=${1:?usage: build-pkg.sh VERSION}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
OUT="$ROOT/dist"; WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT
mkdir -p "$OUT"
LDFLAGS="-s -w -X github.com/anand34577/gorget/client.Version=$VERSION"

(cd "$ROOT/desktop/frontend" && npm ci --no-audit --no-fund && npm run build)
for arch in amd64 arm64; do
    GOOS=darwin GOARCH=$arch CGO_ENABLED=0 go build -C "$ROOT" -trimpath -ldflags "$LDFLAGS" -o "$WORK/gorget-$arch" ./cmd/gorget
    (cd "$ROOT/desktop" && GOOS=darwin GOARCH=$arch CGO_ENABLED=1 go build -trimpath -ldflags "$LDFLAGS" -o "$WORK/desktop-$arch" .)
done
lipo -create -output "$WORK/gorget" "$WORK/gorget-amd64" "$WORK/gorget-arm64"
lipo -create -output "$WORK/gorget-desktop" "$WORK/desktop-amd64" "$WORK/desktop-arm64"

# App bundle
APP="$WORK/root/Applications/Gorget.app/Contents"
mkdir -p "$APP/MacOS" "$APP/Resources" "$WORK/root/usr/local/bin" "$WORK/root/Library/LaunchAgents"
cp "$WORK/gorget-desktop" "$APP/MacOS/gorget-desktop"
sed "s/@VERSION@/$VERSION/g" "$ROOT/packaging/macos/Info.plist" > "$APP/Info.plist"
ICONSET="$WORK/gorget.iconset"; mkdir "$ICONSET"
for s in 16 32 64 128 256 512; do sips -z $s $s "$ROOT/desktop/icons/app.png" --out "$ICONSET/icon_${s}x${s}.png" >/dev/null; done
iconutil -c icns "$ICONSET" -o "$APP/Resources/gorget.icns"
cp "$WORK/gorget" "$WORK/root/usr/local/bin/gorget"
cp "$ROOT/packaging/macos/net.gorget.desktop.plist" "$WORK/root/Library/LaunchAgents/"

pkgbuild --root "$WORK/root" --identifier net.gorget.pkg --version "$VERSION" \
    --scripts "$ROOT/packaging/macos/scripts" --install-location / "$OUT/gorget_${VERSION}_macos.pkg"
echo "built $OUT/gorget_${VERSION}_macos.pkg"
