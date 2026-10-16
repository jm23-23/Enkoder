#!/bin/sh
# Linux build for Enkoder (see Makefile for package names).
set -e
root=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
cd "$root"

if ! pkg-config --exists gtk+-3.0 2>/dev/null; then
	echo "GTK/WebKit headers not found. Install them with:"
	echo "  sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev"
	exit 1
fi

if ! pkg-config --exists webkit2gtk-4.0 2>/dev/null && pkg-config --exists webkit2gtk-4.1 2>/dev/null; then
	PKG_CONFIG_PATH="$root/build/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
	export PKG_CONFIG_PATH
fi

go build -o enkoder .
echo "Built enkoder"
