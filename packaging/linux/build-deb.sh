#!/bin/bash
# Builds the .deb from a binary packaging/dist.sh already built.
#   build-deb.sh <vX.Y.Z> <trunkline binary> <out.deb> <amd64|arm64>
set -euo pipefail

if (( $# != 4 )); then
  echo "usage: build-deb.sh <vX.Y.Z> <trunkline binary> <out.deb> <amd64|arm64>" >&2
  exit 1
fi
tag=$1
bin=$2
out=$3
arch=$4
version=${tag#v}
here=$(cd "$(dirname "$0")" && pwd)
root=$(mktemp -d)
chmod 0755 "$root"
trap 'rm -rf "$root"' EXIT

install -D -m 0755 "$bin" "$root/usr/bin/trunkline"
install -D -m 0644 "$here/trunkline.desktop" "$root/usr/share/applications/trunkline.desktop"
install -D -m 0644 "$here/../trunkline.svg" "$root/usr/share/icons/hicolor/scalable/apps/trunkline.svg"
install -D -m 0644 "$here/../../LICENSE" "$root/usr/share/doc/trunkline/copyright"

size=$(du -sk "$root/usr" | cut -f1)
mkdir -p "$root/DEBIAN"
cat > "$root/DEBIAN/control" <<CONTROL
Package: trunkline
Version: $version
Architecture: $arch
Maintainer: Franco Coetzer <franco.coetzer@gmail.com>
Installed-Size: $size
Depends: libgtk-3-0t64 | libgtk-3-0, libwebkit2gtk-4.1-0, subversion
Section: vcs
Priority: optional
Homepage: https://github.com/FrancoCoetzer/TrunkLine
Description: Desktop client for Subversion (SVN)
 Browse repositories, check out, see changes with diffs, commit, update,
 resolve conflicts, read history, export and import. Runs the svn
 command-line client, so it works with the same working copies. Keeps
 itself up to date from the project's GitHub releases.
CONTROL

dpkg-deb --root-owner-group --build "$root" "$out" >/dev/null
echo "built $out"
