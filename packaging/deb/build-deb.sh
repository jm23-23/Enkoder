#!/bin/sh
# Builds dist/enkoder_<version>_<arch>.deb (Debian / Ubuntu).
# Needs: go, dpkg-dev, the GTK/WebKit -dev packages from the Makefile.
# Install: sudo apt install ./dist/enkoder_<version>_<arch>.deb
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
cd "$root"

version=${VERSION:-$(sed -n 's/^var version = "\(.*\)"/\1/p' version.go)}
arch=$(dpkg --print-architecture)
maintainer=${MAINTAINER:-"$(git config user.name) <$(git config user.email)>"}
[ -n "$version" ] || { echo "No version found in version.go" >&2; exit 1; }

# Same webkit2gtk-4.0 shim as build.sh.
if ! pkg-config --exists webkit2gtk-4.0 2>/dev/null && pkg-config --exists webkit2gtk-4.1 2>/dev/null; then
	PKG_CONFIG_PATH="$root/build/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
	export PKG_CONFIG_PATH
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
pkg="$work/enkoder_${version}_${arch}"

go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$work/enkoder" .

install -Dm755 "$work/enkoder" "$pkg/usr/bin/enkoder"
install -Dm644 enkoder.desktop "$pkg/usr/share/applications/enkoder.desktop"
install -Dm644 assets/icon.png "$pkg/usr/share/icons/hicolor/256x256/apps/enkoder.png"
install -Dm644 assets/icon.svg "$pkg/usr/share/icons/hicolor/scalable/apps/enkoder.svg"
install -Dm644 README.md "$pkg/usr/share/doc/enkoder/README.md"
install -Dm644 LICENSE "$pkg/usr/share/doc/enkoder/copyright"

# Library deps of the binary (gtk, webkit2gtk, libc...) from dpkg-shlibdeps.
mkdir -p "$work/src/debian"
printf 'Source: enkoder\n\nPackage: enkoder\nArchitecture: any\n' >"$work/src/debian/control"
shlibs=$(cd "$work/src" && dpkg-shlibdeps -O "$pkg/usr/bin/enkoder" 2>/dev/null | sed -n 's/^shlibs:Depends=//p')

mkdir -p "$pkg/DEBIAN"
cat >"$pkg/DEBIAN/control" <<CONTROL
Package: enkoder
Version: $version
Architecture: $arch
Maintainer: $maintainer
Installed-Size: $(du -sk "$pkg/usr" | cut -f1)
Depends: ${shlibs:+$shlibs, }ffmpeg, zenity
Section: video
Priority: optional
Homepage: https://github.com/jm23-23/Enkoder
Description: desktop GUI for re-encoding videos with ffmpeg
 Pick a source file and the video, audio and subtitle tracks, and Enkoder
 builds the ffmpeg command (HEVC via NVENC or libx265, AV1 via SVT-AV1)
 and encodes in the background.
CONTROL

mkdir -p dist
dpkg-deb --root-owner-group -Zxz --build "$pkg" "dist/enkoder_${version}_${arch}.deb"
