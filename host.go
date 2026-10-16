package main

// webviewHost is the native window + JS bridge.
// Windows: go-webview2 (Edge WebView2). Linux: webview_go (WebKitGTK).
type webviewHost interface {
	Bind(name string, fn interface{}) error
	Init(js string)
	Eval(js string)
	Dispatch(fn func())
	Navigate(url string)
	LoadUI(html string)
	SetSize(width, height int)
	Run()
	Destroy()
}
