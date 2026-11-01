package install

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("LMINSTALL_HELPER")
	if mode == "job-child" {
		f, e := os.OpenFile(os.Getenv("LMINSTALL_LOCK_FILE"), os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			panic(e)
		}
		defer f.Close()
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			panic(e)
		}
		defer listener.Close()
		fmt.Println("READY")
		for {
			c, e := listener.Accept()
			if e != nil {
				os.Exit(0)
			}
			c.Close()
		}
	}
	if mode == "job-parent" {
		exe, _ := os.Executable()
		child := exec.Command(exe, "-test.run=^TestHelperProcess$")
		child.Env = append(os.Environ(), "LMINSTALL_HELPER=job-child")
		child.Stderr = os.Stderr
		ready, e := child.StdoutPipe()
		if e != nil {
			panic(e)
		}
		if e = child.Start(); e != nil {
			panic(e)
		}
		text, e := bufio.NewReader(ready).ReadString('\n')
		if e != nil || strings.TrimSpace(text) != "READY" {
			panic("child readiness")
		}
		fmt.Printf("tool 1.2.3\n%d\n", child.Process.Pid)
		os.Exit(0)
	}
	if mode != "1" {
		return
	}
	os.Stdout.WriteString("tool 1.2.3\n" + strings.Repeat("x", 1200000))
	os.Exit(0)
}
func TestWindowsOwnedWorkersDrainBeforeFileOperations(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows job lifetime")
	}
	exe, _ := os.Executable()
	file := filepath.Join(t.TempDir(), "held.bin")
	out, e := (ExecRunner{Log: io.Discard}).Run(context.Background(), []string{exe, "-test.run=^TestHelperProcess$"}, "", map[string]string{"LMINSTALL_HELPER": "job-parent", "LMINSTALL_LOCK_FILE": file}, true)
	if e != nil {
		t.Fatal(e, out)
	}
	lines := strings.Fields(out)
	pid, e := strconv.Atoi(lines[len(lines)-1])
	if e != nil {
		t.Fatal(out)
	}
	if child, e := os.FindProcess(pid); e == nil {
		defer child.Kill()
	} // Only the helper PID this test started.
	if e = os.Remove(file); e != nil {
		t.Fatalf("owned worker still holds version-probe files: %v", e)
	}
}
func TestRunnerStreamsAndCapturesWithoutOutputCap(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	out, e := (ExecRunner{Log: io.Discard}).Run(context.Background(), []string{exe, "-test.run=^TestHelperProcess$"}, "", map[string]string{"LMINSTALL_HELPER": "1"}, true)
	if e != nil || len(out) != 1200011 {
		t.Fatal(e, len(out))
	}
}
func TestWindowsShimLiteralPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows shim")
	}
	dir := filepath.Join(t.TempDir(), "space 雪 & literal")
	os.MkdirAll(dir, 0700)
	file := filepath.Join(dir, "tool.cmd")
	os.WriteFile(file, []byte("@echo off\r\n@echo tool 1.2.3\r\n"), 0600)
	out, e := (ExecRunner{Log: io.Discard}).Run(context.Background(), []string{file, "--version"}, "", nil, true)
	if e != nil || strings.TrimSpace(out) != "tool 1.2.3" {
		t.Fatal(e, out)
	}
}
