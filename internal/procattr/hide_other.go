//go:build !windows

package procattr

import "os/exec"

func HideConsole(cmd *exec.Cmd) {}
