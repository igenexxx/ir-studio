#!/usr/bin/env bash
set -euo pipefail

PKG="ir-studio"
VER="0.3.10"
REV="1"
ARCH="arm64"
DEB_NAME="${PKG}_${VER}-${REV}_${ARCH}.deb"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "[1/5] Running tests..."
go test -race ./...

echo "[2/5] Ensuring screenshots and 256x256 icon exist..."
if [[ ! -f "assets/images/ir-studio-icon.png" ]]; then
  go run ./cmd/shots -out assets/screenshots -scale 1 -icon assets/images/ir-studio-icon.png
fi

echo "[3/5] Cross-compiling ${ARCH} binary..."
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o "dist/${PKG}" ./cmd/ir-studio

echo "[4/5] Staging Debian package..."
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

mkdir -p "$STAGE/DEBIAN"
mkdir -p "$STAGE/usr/share/APPLaunch/bin"
mkdir -p "$STAGE/usr/share/APPLaunch/applications"
mkdir -p "$STAGE/usr/share/APPLaunch/share/images"

install -m 0755 "dist/${PKG}" "$STAGE/usr/share/APPLaunch/bin/${PKG}"
install -m 0644 "assets/images/ir-studio-icon.png" "$STAGE/usr/share/APPLaunch/share/images/ir-studio-icon.png"

cat > "$STAGE/usr/share/APPLaunch/applications/${PKG}.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=IR Studio
Comment=38 kHz Infrared studio, ML denoiser, universal probe & .ir library
Exec=/usr/share/APPLaunch/bin/${PKG}
Terminal=false
Sysplause=true
Icon=share/images/ir-studio-icon.png
Categories=Utility;
StartupNotify=false
EOF
chmod 0644 "$STAGE/usr/share/APPLaunch/applications/${PKG}.desktop"

SIZE_KB="$(du -sk "$STAGE/usr" | awk '{print $1}')"
cat > "$STAGE/DEBIAN/control" <<EOF
Package: ${PKG}
Version: ${VER}-${REV}
Section: utils
Priority: optional
Architecture: ${ARCH}
Installed-Size: ${SIZE_KB}
Maintainer: Zhenya <61760187+igenexxx@users.noreply.github.com>
Description: 38 kHz Infrared studio, ML denoiser and universal probe
 IR Studio unlocks Linux /dev/lirc* infrared TX/RX on CardputerZero with
 multi-protocol decoding, on-device 1D K-Means denoising, AC state-frame
 diffing, Universal Power-Off probing, Flipper .ir management, and opt-in MCP.
EOF

cat > "$STAGE/DEBIAN/postinst" <<'EOF'
#!/usr/bin/env bash
set -e
if [ -d /etc/udev/rules.d ]; then
  printf 'SUBSYSTEM=="lirc", KERNEL=="lirc[0-9]*", MODE="0660", GROUP="plugdev"\n' > /etc/udev/rules.d/99-ir-studio-lirc.rules
  chmod 0644 /etc/udev/rules.d/99-ir-studio-lirc.rules
fi
if command -v udevadm >/dev/null 2>&1; then
  udevadm control --reload-rules >/dev/null 2>&1 || true
  udevadm trigger --subsystem-match=lirc >/dev/null 2>&1 || true
fi
echo "ir-studio installed. /dev/lirc* granted to group plugdev."
EOF
chmod 0755 "$STAGE/DEBIAN/postinst"

echo "[5/5] Building dist/${DEB_NAME}..."
dpkg-deb --root-owner-group --build "$STAGE" "dist/${DEB_NAME}"
ls -lh "dist/${DEB_NAME}"
