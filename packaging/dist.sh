#!/bin/bash
# Builds release files into build/dist. The Makefile's dist targets and the
# release workflow run it:
#
#   packaging/dist.sh linux <vX.Y.Z>     .deb and .rpm for this machine's
#                                        processor (amd64 or arm64; the app
#                                        links GTK and WebKit, so it can't be
#                                        cross-built)
#   packaging/dist.sh windows <vX.Y.Z>   setup .exe for x64 and ARM64, .msi for
#                                        x64 (cross-built: no cgo)
#   packaging/dist.sh sums               SHA256SUMS of everything in build/dist
#
# File names must match internal/update's AssetName. Each build is stamped
# with its version and install kind (deb, rpm, windows, msi) for the updater.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
dist=build/dist
stage=build/stage
mkdir -p "$dist" "$stage"

app_tags=desktop,production
linux_tags=$app_tags,webkit2_41

usage() {
  echo "usage: dist.sh linux|windows <vX.Y.Z> | dist.sh sums" >&2
  exit 1
}

need() {
  if ! command -v "$1" >/dev/null; then
    echo "$1 not found: sudo apt install $2" >&2
    exit 1
  fi
}

# build <out> <install kind> [go env...]: the app, stamped for the updater.
build() {
  local out=$1 kind=$2
  shift 2
  env "$@" go build -tags "$tags" \
    -ldflags "-s -w $extra -X trunkline/internal/update.Version=$tag -X trunkline/internal/update.Install=$kind" \
    -o "$out" .
}

linux() {
  need dpkg-deb dpkg
  need rpmbuild rpm
  local debarch rpmarch
  case $(go env GOARCH) in
    amd64)
      debarch=amd64
      rpmarch=x86_64
      ;;
    arm64)
      debarch=arm64
      rpmarch=aarch64
      ;;
    *)
      echo "no Linux packages for $(go env GOARCH)" >&2
      exit 1
      ;;
  esac
  tags=$linux_tags
  extra=""
  need gcc build-essential # cgo: without a C compiler Go leaves out Wails' Linux code
  echo "== $tag: Linux $debarch"
  build "$stage/trunkline-deb" deb CGO_ENABLED=1
  packaging/linux/build-deb.sh "$tag" "$stage/trunkline-deb" "$dist/trunkline_${v}_${debarch}.deb" "$debarch"
  build "$stage/trunkline-rpm" rpm CGO_ENABLED=1
  packaging/linux/build-rpm.sh "$tag" "$stage/trunkline-rpm" "$dist" "$rpmarch"
}

windows() {
  need makensis nsis
  need wixl wixl
  tags=$app_tags
  extra="-H windowsgui"
  local arch setup
  for arch in amd64 arm64; do
    echo "== $tag: Windows $arch"
    python3 packaging/windows/make_syso.py "$v" "$arch"
    if [[ $arch == "amd64" ]]; then
      setup="trunkline-$v-setup.exe" # the name releases have always had
    else
      setup="trunkline-$v-arm64-setup.exe"
    fi
    build "$stage/windows-$arch/trunkline.exe" windows GOOS=windows GOARCH="$arch"
    makensis -V2 -DVERSION="$v" -DAPPEXE="$root/$stage/windows-$arch/trunkline.exe" -DAPPARCH="$arch" -DLICENSEFILE="$root/LICENSE" \
      -DICONFILE="$root/packaging/windows/trunkline.ico" -DOUTFILE="$root/$dist/$setup" packaging/windows/installer.nsi
  done
  # The .msi: x64 only (wixl can't make an ARM64 one).
  echo "== $tag: Windows .msi"
  python3 packaging/windows/make_syso.py "$v" amd64
  build "$stage/msi/trunkline.exe" msi GOOS=windows GOARCH=amd64
  wixl -a x64 -D Version="$v" -D AppExe="$root/$stage/msi/trunkline.exe" -D License="$root/LICENSE" \
    -D Icon="$root/packaging/windows/trunkline.ico" -o "$dist/trunkline-$v-x64.msi" packaging/windows/trunkline.wxs \
    2> >(grep -v -e CRITICAL -e '^$' >&2)
}

sums() {
  (cd "$dist" && rm -f SHA256SUMS && sha256sum -- * > SHA256SUMS && ls -l)
}

what=${1:-}
case $what in
  sums)
    sums
    ;;
  linux | windows)
    tag=${2:-}
    if [[ -z $tag ]]; then
      echo "no tag: HEAD is not at a release tag (make release), or give one: make dist TAG=v9.9.9 for a test build" >&2
      exit 1
    fi
    if [[ ! $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
      echo "$tag is not a vX.Y.Z tag" >&2
      exit 1
    fi
    v=${tag#v}
    "$what"
    ;;
  *)
    usage
    ;;
esac
