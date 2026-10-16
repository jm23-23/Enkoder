//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"time"
)

func (a *app) pushInitialCaps() {
	go func() {
		time.Sleep(400 * time.Millisecond)
		caps, err := a.capabilities()
		if err != nil {
			return
		}
		payload, err := json.Marshal(caps)
		if err != nil {
			return
		}
		js := fmt.Sprintf(`(function(){ if (typeof applyCapabilities === "function") applyCapabilities(%s); })()`, payload)
		a.view.Dispatch(func() {
			a.view.Eval(js)
		})
	}()
}
