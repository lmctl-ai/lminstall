//go:build !windows

package launch

import (
	"io"
	"os"
	"os/exec"
	"syscall"
)

func executeRuntime(cmd *exec.Cmd) error {
	out, errout := cmd.Stdout, cmd.Stderr
	r1, w1, e := os.Pipe()
	if e != nil {
		return e
	}
	defer r1.Close()
	r2, w2, e := os.Pipe()
	if e != nil {
		w1.Close()
		return e
	}
	defer r2.Close()
	cmd.Stdout, cmd.Stderr = w1, w2
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if e = cmd.Start(); e != nil {
		w1.Close()
		w2.Close()
		return e
	}
	w1.Close()
	w2.Close()
	copied := make(chan error, 2)
	go func() { _, e := io.Copy(out, r1); copied <- e }()
	go func() { _, e := io.Copy(errout, r2); copied <- e }()
	result := cmd.Wait()
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	for range 2 {
		if e = <-copied; e != nil && result == nil {
			result = e
		}
	}
	return result
}
