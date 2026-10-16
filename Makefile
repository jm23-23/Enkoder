# Linux build for Enkoder.
# Build deps: build-essential pkg-config libgtk-3-dev
#             libwebkit2gtk-4.1-dev  (or libwebkit2gtk-4.0-dev on older distros)
# Runtime:    ffmpeg zenity libwebkit2gtk-4.1-0 (or 4.0)
#
# github.com/webview/webview_go still pkg-config's webkit2gtk-4.0.
# If only 4.1 is installed, we prepend build/pkgconfig so the shim .pc is used.

.PHONY: all enkoder test clean install uninstall winres deb

PKGCONFIG_SHIM := $(CURDIR)/build/pkgconfig
HAS_WEBKIT40 := $(shell pkg-config --exists webkit2gtk-4.0 && echo yes)
HAS_WEBKIT41 := $(shell pkg-config --exists webkit2gtk-4.1 && echo yes)

ifneq ($(HAS_WEBKIT40),yes)
  ifeq ($(HAS_WEBKIT41),yes)
    export PKG_CONFIG_PATH := $(PKGCONFIG_SHIM)$(if $(PKG_CONFIG_PATH),:$(PKG_CONFIG_PATH))
  endif
endif

all: enkoder

enkoder:
	@pkg-config --exists gtk+-3.0 >/dev/null 2>&1 || { \
		echo "GTK/WebKit headers not found. Install them with:"; \
		echo "  sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev"; \
		exit 1; \
	}
	go build -o enkoder .

test:
	go test ./...

clean:
	rm -f enkoder
	rm -rf dist

# Per-user install: binary, icon and menu entry (no sudo).
PREFIX ?= $(HOME)/.local
ICONDIR := $(PREFIX)/share/icons/hicolor

install: enkoder
	install -Dm755 enkoder $(PREFIX)/bin/enkoder
	install -Dm644 assets/icon.png $(ICONDIR)/256x256/apps/enkoder.png
	install -Dm644 assets/icon.svg $(ICONDIR)/scalable/apps/enkoder.svg
	sed "s|^Exec=.*|Exec=$(PREFIX)/bin/enkoder|" enkoder.desktop > $(PREFIX)/share/applications/enkoder.desktop.tmp
	install -Dm644 $(PREFIX)/share/applications/enkoder.desktop.tmp $(PREFIX)/share/applications/enkoder.desktop
	rm -f $(PREFIX)/share/applications/enkoder.desktop.tmp
	-gtk-update-icon-cache -q $(ICONDIR) 2>/dev/null
	-update-desktop-database -q $(PREFIX)/share/applications 2>/dev/null

uninstall:
	rm -f $(PREFIX)/bin/enkoder $(ICONDIR)/256x256/apps/enkoder.png \
		$(ICONDIR)/scalable/apps/enkoder.svg $(PREFIX)/share/applications/enkoder.desktop

# Debian / Ubuntu package in dist/ (see packaging/deb/build-deb.sh).
deb:
	./packaging/deb/build-deb.sh

# Regenerate Windows icon resources after changing assets/icon.png.
# go install github.com/tc-hib/go-winres@latest
winres:
	go-winres make --in winres/winres.json --arch amd64,arm64
