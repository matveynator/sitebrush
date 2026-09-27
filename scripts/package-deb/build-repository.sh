#!/usr/bin/env bash

set -euo pipefail

###############################################################################
# Arguments and dependencies
###############################################################################

if [ "$#" -lt 4 ]; then
    echo "usage: $0 <repository-root> <signing-key> <deb> <deb> [deb ...]" >&2
    exit 2
fi

REPOSITORY_ROOT="$1"
SIGNING_KEY="$2"
shift 2
PACKAGES=("$@")

for COMMAND in apt-ftparchive dpkg-scanpackages gpg gzip; do
    if ! command -v "$COMMAND" >/dev/null 2>&1; then
        echo "required command not found: $COMMAND" >&2
        exit 1
    fi
done

###############################################################################
# Archive package pool
###############################################################################

POOL="$REPOSITORY_ROOT/pool/main/s/sitebrush"
DIST="$REPOSITORY_ROOT/dists/stable"

mkdir -p \
    "$POOL" \
    "$DIST/main/binary-amd64" \
    "$DIST/main/binary-arm64"

for PACKAGE in "${PACKAGES[@]}"; do
    if [ ! -f "$PACKAGE" ]; then
        echo "package not found: $PACKAGE" >&2
        exit 1
    fi

    cp -f "$PACKAGE" "$POOL/"
done

###############################################################################
# Architecture indexes
###############################################################################

(
    cd "$REPOSITORY_ROOT"

    dpkg-scanpackages -a amd64 pool /dev/null \
        > dists/stable/main/binary-amd64/Packages
    gzip -9ck dists/stable/main/binary-amd64/Packages \
        > dists/stable/main/binary-amd64/Packages.gz

    dpkg-scanpackages -a arm64 pool /dev/null \
        > dists/stable/main/binary-arm64/Packages
    gzip -9ck dists/stable/main/binary-arm64/Packages \
        > dists/stable/main/binary-arm64/Packages.gz
)

###############################################################################
# Release metadata and signatures
###############################################################################

apt-ftparchive \
    -o APT::FTPArchive::Release::Origin="SiteBrush" \
    -o APT::FTPArchive::Release::Label="SiteBrush" \
    -o APT::FTPArchive::Release::Suite="stable" \
    -o APT::FTPArchive::Release::Codename="stable" \
    -o APT::FTPArchive::Release::Architectures="amd64 arm64" \
    -o APT::FTPArchive::Release::Components="main" \
    -o APT::FTPArchive::Release::Description="SiteBrush APT repository" \
    release "$DIST" > "$DIST/Release"

gpg --batch --yes \
    --local-user "$SIGNING_KEY" \
    --armor \
    --detach-sign \
    --output "$DIST/Release.gpg" \
    "$DIST/Release"

gpg --batch --yes \
    --local-user "$SIGNING_KEY" \
    --armor \
    --clearsign \
    --output "$DIST/InRelease" \
    "$DIST/Release"

gpg --batch --yes \
    --armor \
    --export "$SIGNING_KEY" \
    > "$REPOSITORY_ROOT/sitebrush-archive-keyring.asc"

###############################################################################
# Result
###############################################################################

echo "APT repository generated at: $REPOSITORY_ROOT"
echo "Publish this directory at: https://sitebrush.com/apt/"
