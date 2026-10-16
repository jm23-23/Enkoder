//go:build linux

package main

/*
#cgo CFLAGS: -D_GNU_SOURCE
#cgo LDFLAGS: -ldl

#include <dlfcn.h>
#include <stddef.h>
#include <stdlib.h>

typedef void (*enkoder_eval_fn)(void *, const char *, long, const char *, const char *, void *, void *, void *);
typedef void (*enkoder_run_fn)(void *, const char *, void *, void *, void *);
typedef void *(*enkoder_bin_child_fn)(void *);
typedef void *(*enkoder_loader_new_fn)(void);
typedef int (*enkoder_loader_write_fn)(void *, const unsigned char *, size_t, void *);
typedef int (*enkoder_loader_close_fn)(void *, void *);
typedef void *(*enkoder_loader_pixbuf_fn)(void *);
typedef void (*enkoder_set_icon_fn)(void *, void *);
typedef void (*enkoder_unref_fn)(void *);

// webview_go Eval uses dlopen("libwebkit2gtk-4.1.so", RTLD_NOLOAD). Ubuntu
// loads libwebkit2gtk-4.1.so.0, so that lookup fails and Eval is a no-op
// (Bind promises never resolve, DOM never updates). Call the already-loaded
// symbols instead.
void enkoder_eval(void *gtk_window, const char *js) {
	if (gtk_window == NULL || js == NULL) {
		return;
	}
	enkoder_bin_child_fn get_child = (enkoder_bin_child_fn)dlsym(RTLD_DEFAULT, "gtk_bin_get_child");
	if (get_child == NULL) {
		return;
	}
	void *view = get_child(gtk_window);
	if (view == NULL) {
		return;
	}
	enkoder_eval_fn eval = (enkoder_eval_fn)dlsym(RTLD_DEFAULT, "webkit_web_view_evaluate_javascript");
	if (eval != NULL) {
		eval(view, js, (long)-1, NULL, NULL, NULL, NULL, NULL);
		return;
	}
	enkoder_run_fn run = (enkoder_run_fn)dlsym(RTLD_DEFAULT, "webkit_web_view_run_javascript");
	if (run != NULL) {
		run(view, js, NULL, NULL, NULL);
	}
}

// Window/taskbar icon from the embedded PNG (GdkPixbufLoader). Same dlsym
// approach as above, so no extra pkg-config deps for this file.
void enkoder_set_icon(void *gtk_window, const unsigned char *png, size_t len) {
	if (gtk_window == NULL || png == NULL || len == 0) {
		return;
	}
	enkoder_loader_new_fn loader_new = (enkoder_loader_new_fn)dlsym(RTLD_DEFAULT, "gdk_pixbuf_loader_new");
	enkoder_loader_write_fn loader_write = (enkoder_loader_write_fn)dlsym(RTLD_DEFAULT, "gdk_pixbuf_loader_write");
	enkoder_loader_close_fn loader_close = (enkoder_loader_close_fn)dlsym(RTLD_DEFAULT, "gdk_pixbuf_loader_close");
	enkoder_loader_pixbuf_fn loader_pixbuf = (enkoder_loader_pixbuf_fn)dlsym(RTLD_DEFAULT, "gdk_pixbuf_loader_get_pixbuf");
	enkoder_set_icon_fn set_icon = (enkoder_set_icon_fn)dlsym(RTLD_DEFAULT, "gtk_window_set_icon");
	enkoder_unref_fn unref = (enkoder_unref_fn)dlsym(RTLD_DEFAULT, "g_object_unref");
	if (!loader_new || !loader_write || !loader_close || !loader_pixbuf || !set_icon || !unref) {
		return;
	}
	void *loader = loader_new();
	if (loader == NULL) {
		return;
	}
	if (loader_write(loader, png, len, NULL) && loader_close(loader, NULL)) {
		void *pixbuf = loader_pixbuf(loader);
		if (pixbuf != NULL) {
			set_icon(gtk_window, pixbuf);
		}
	} else {
		loader_close(loader, NULL);
	}
	unref(loader);
}
*/
import "C"
import (
	_ "embed"
	"errors"
	"unsafe"

	webview "github.com/webview/webview_go"
)

//go:embed assets/icon.png
var iconPNG []byte

type linuxHost struct {
	webview.WebView
}

func (h linuxHost) SetSize(width, height int) {
	h.WebView.SetSize(width, height, webview.HintNone)
}

func (h linuxHost) LoadUI(html string) {
	h.SetHtml(html)
}

func (h linuxHost) Eval(js string) {
	if h.WebView == nil || js == "" {
		return
	}
	cjs := C.CString(js)
	defer C.free(unsafe.Pointer(cjs))
	C.enkoder_eval(h.Window(), cjs)
}

func newHost() (webviewHost, error) {
	w := webview.New(false)
	if w == nil || w.Window() == nil {
		return nil, errors.New("Nie udało się uruchomić WebKitGTK.\nZainstaluj pakiety: libwebkit2gtk-4.1 (lub 4.0), libgtk-3-0.")
	}
	w.SetTitle(appTitle())
	setWindowIcon(w.Window())
	return linuxHost{w}, nil
}

func setWindowIcon(window unsafe.Pointer) {
	if len(iconPNG) == 0 {
		return
	}
	png := C.CBytes(iconPNG)
	defer C.free(png)
	C.enkoder_set_icon(window, (*C.uchar)(png), C.size_t(len(iconPNG)))
}

func wrapFilePickerBinds(v webviewHost) {
	// Bind's webview_return still uses stock Eval (no-op here). Don't let
	// await hang; the path is applied via our Eval in finishPick.
	v.Init(`(function () {
  function fireForget(name) {
    var orig = window[name];
    if (typeof orig !== "function") return;
    window[name] = function () {
      try { orig.apply(null, arguments); } catch (e) {}
      return Promise.resolve("");
    };
  }
  fireForget("openInput");
  fireForget("saveOutput");
})();`)
}
