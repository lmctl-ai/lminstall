//go:build !windows

package install

import "os/exec"

func runOwned(cmd *exec.Cmd) error { return cmd.Run() }
