//go:build windows

package main

import (
	"errors"

	"github.com/jchv/go-webview2"
)

type windowsHost struct {
	webview2.WebView
}

func (h windowsHost) SetSize(width, height int) {
	h.WebView.SetSize(width, height, webview2.HintNone)
}

func (h windowsHost) LoadUI(html string) {
	h.Navigate("data:text/html," + encodeDataURL(html))
}

func newHost() (webviewHost, error) {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  appTitle(),
			Width:  920,
			Height: 780,
			IconId: 1, // RT_GROUP_ICON #1 from rsrc_windows_*.syso (winres/)
			Center: true,
		},
	})
	if w == nil {
		return nil, errors.New(tr("webview2_failed"))
	}
	return windowsHost{w}, nil
}
