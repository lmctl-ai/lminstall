//go:build !windows

package install

import (
	"io"
	"os"
	"os/exec"
	"syscall"
)

// A private group belongs only to this command. Use an owned pipe so Cmd.Wait
// observes the parent exit rather than waiting forever for inherited worker FDs.
func runOwned(cmd *exec.Cmd) error {
	writer := cmd.Stdout
	if writer == nil {
		writer = io.Discard
	}
	reader, write, e := os.Pipe()
	if e != nil {
		return e
	}
	defer reader.Close()
	cmd.Stdout = write
	cmd.Stderr = write
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	if e = cmd.Start(); e != nil {
		write.Close()
		return e
	}
	write.Close()
	copied := make(chan error, 1)
	go func() { _, e := io.Copy(writer, reader); copied <- e }()
	err := cmd.Wait()
	// Only the process group created for this exact child PID, never a name/pattern.
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if e = <-copied; e != nil && err == nil {
		return e
	}
	return err
}
