#!/usr/bin/env bash

set -euo pipefail

###############################################################################
# Arguments
###############################################################################

if [ "$#" -ne 4 ]; then
    echo "usage: $0 <binary> <architecture> <version> <output-dir>" >&2
    exit 2
fi

BINARY="$1"
ARCHITECTURE="$2"
VERSION="$3"
OUTPUT_DIR="$4"

case "$ARCHITECTURE" in
    amd64|arm64) ;;
    *)
        echo "unsupported Debian architecture: $ARCHITECTURE" >&2
        exit 2
        ;;
esac

if [ ! -f "$BINARY" ]; then
    echo "SiteBrush binary not found: $BINARY" >&2
    exit 1
fi

###############################################################################
# Package tree
###############################################################################

PACKAGE_ROOT="$(mktemp -d)"
trap 'rm -rf "$PACKAGE_ROOT"' EXIT

mkdir -p \
    "$PACKAGE_ROOT/DEBIAN" \
    "$PACKAGE_ROOT/usr/bin"

install -m 0755 "$BINARY" "$PACKAGE_ROOT/usr/bin/sitebrush"

INSTALLED_SIZE="$(du -sk "$PACKAGE_ROOT/usr" | awk '{print $1}')"

cat > "$PACKAGE_ROOT/DEBIAN/control" <<EOF
Package: sitebrush
Version: $VERSION
Section: web
Priority: optional
Architecture: $ARCHITECTURE
Maintainer: SiteBrush Project <matveynator@users.noreply.github.com>
Homepage: https://sitebrush.com/
Installed-Size: $INSTALLED_SIZE
Description: editable static website server
 SiteBrush imports existing websites, keeps their design and referenced assets,
 provides browser-based editing, and serves published pages as static HTML.
EOF

###############################################################################
# Service lifecycle
###############################################################################

cat > "$PACKAGE_ROOT/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e

if [ "$1" = "configure" ]; then
    /usr/bin/sitebrush -install
fi
EOF

cat > "$PACKAGE_ROOT/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e

case "$1" in
    remove|deconfigure)
        /usr/bin/sitebrush -uninstall || true
        ;;
esac
EOF

chmod 0755 \
    "$PACKAGE_ROOT/DEBIAN/postinst" \
    "$PACKAGE_ROOT/DEBIAN/prerm"

###############################################################################
# Build and verify
###############################################################################

mkdir -p "$OUTPUT_DIR"
PACKAGE_PATH="$OUTPUT_DIR/sitebrush_${VERSION}_${ARCHITECTURE}.deb"

dpkg-deb --build --root-owner-group "$PACKAGE_ROOT" "$PACKAGE_PATH"
dpkg-deb --info "$PACKAGE_PATH" >/dev/null
dpkg-deb --contents "$PACKAGE_PATH" | grep -q './usr/bin/sitebrush$'

echo "$PACKAGE_PATH"
