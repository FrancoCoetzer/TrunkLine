#!/bin/bash
# Builds the .rpm from a binary packaging/dist.sh already built.
#   build-rpm.sh <vX.Y.Z> <trunkline binary> <out dir> <x86_64|aarch64>
# writes <out dir>/trunkline-X.Y.Z-1.<arch>.rpm. Needs rpmbuild (Debian and
# Ubuntu: sudo apt install rpm). Its own package database is a throwaway one:
# a Debian system has none.
set -euo pipefail

if (( $# != 4 )); then
  echo "usage: build-rpm.sh <vX.Y.Z> <trunkline binary> <out dir> <x86_64|aarch64>" >&2
  exit 1
fi
tag=$1
bin=$(realpath "$2")
out=$(realpath "$3")
arch=$4
here=$(cd "$(dirname "$0")" && pwd)
top=$(mktemp -d)
trap 'rm -rf "$top"' EXIT
mkdir "$top/rpmdb"

rpmbuild -bb --quiet \
  --target "$arch" \
  --define "_topdir $top" \
  --define "_dbpath $top/rpmdb" \
  --define "tl_version ${tag#v}" \
  --define "tl_bin $bin" \
  --define "tl_here $here" \
  --define "tl_license $here/../../LICENSE" \
  "$here/trunkline.spec"

rpm=$(find "$top/RPMS" -name '*.rpm' -print -quit)
if [[ -z $rpm ]]; then
  echo "rpmbuild made no package" >&2
  exit 1
fi
cp "$rpm" "$out/"
echo "built $out/$(basename "$rpm")"
