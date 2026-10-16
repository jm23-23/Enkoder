//go:build linux

package main

import "os/exec"

func openBrowser(u string) error {
	cmd := exec.Command("xdg-open", u)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
