#!/usr/bin/env bash
set -euo pipefail

archive_path=$1
staging_directory=$2
tarball_directory=$3

mkdir -p "$staging_directory" "$tarball_directory"
tar -xf "$archive_path" -C "$staging_directory"
sdk_directories=("$staging_directory"/MacOSX*.sdk)
test "${#sdk_directories[@]}" -eq 1
test -d "${sdk_directories[0]}"

# OSXCross requires the SDK version in both the archive and its root directory.
# Xcode may expose an unversioned MacOSX.sdk directory on its runner image.
sdk_version=$(python3 - "${sdk_directories[0]}/SDKSettings.plist" <<'PY'
import plistlib
import sys

with open(sys.argv[1], "rb") as settings_file:
    print(plistlib.load(settings_file)["Version"])
PY
)
[[ "$sdk_version" =~ ^[0-9]+(\.[0-9]+)*$ ]]
sdk_name="MacOSX${sdk_version}.sdk"
if [ "${sdk_directories[0]}" != "$staging_directory/$sdk_name" ]; then
  mv "${sdk_directories[0]}" "$staging_directory/$sdk_name"
fi
tar -czf "$tarball_directory/$sdk_name.tar.gz" -C "$staging_directory" "$sdk_name"
