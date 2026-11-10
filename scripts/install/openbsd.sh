#!/bin/sh

set -eu

if [ "$(uname -s)" != OpenBSD ]; then
	echo "This installer is for OpenBSD." >&2
	exit 1
fi
if [ "$(id -u)" != 0 ]; then
	echo "Run this installer as root." >&2
	exit 1
fi

case "$(uname -m)" in
	amd64|x86_64) architecture=amd64 ;;
	arm64|aarch64) architecture=arm64 ;;
	*) echo "Unsupported OpenBSD architecture: $(uname -m)" >&2; exit 1 ;;
esac

download_root=https://sitebrush.com/download/latest/server-app
binary_name=sitebrush_openbsd_${architecture}
temporary_directory=$(mktemp -d /tmp/sitebrush-install.XXXXXXXX)
staged_binary=
trap 'rm -rf "$temporary_directory"; if [ -n "$staged_binary" ]; then rm -f "$staged_binary"; fi' EXIT HUP INT TERM

ftp -o "$temporary_directory/$binary_name" "$download_root/$binary_name"
ftp -o "$temporary_directory/SHA256SUMS" "$download_root/SHA256SUMS"
expected_checksum=$(awk -v binary="$binary_name" '$2 == binary { print $1 }' "$temporary_directory/SHA256SUMS")
case "$expected_checksum" in
	????????????????????????????????????????????????????????????????) ;;
	*) echo "Missing SHA-256 checksum for $binary_name" >&2; exit 1 ;;
esac
actual_checksum=$(sha256 -q "$temporary_directory/$binary_name")
if [ "$actual_checksum" != "$expected_checksum" ]; then
	echo "SHA-256 verification failed for $binary_name" >&2
	exit 1
fi

mkdir -p /usr/local/bin
staged_binary=$(mktemp /usr/local/bin/.sitebrush-install.XXXXXXXX)
cp "$temporary_directory/$binary_name" "$staged_binary"
chmod 0755 "$staged_binary"
mv -f "$staged_binary" /usr/local/bin/sitebrush
staged_binary=

/usr/local/bin/sitebrush -install
