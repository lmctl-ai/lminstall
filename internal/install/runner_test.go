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
		fmt.Printf("READY %s\n", listener.Addr().String())
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
		if e != nil || !strings.HasPrefix(text, "READY ") {
			panic("child readiness")
		}
		fmt.Printf("tool 1.2.3\n%d\n%s\n", child.Process.Pid, strings.TrimSpace(strings.TrimPrefix(text, "READY ")))
		os.Exit(0)
	}
	if mode != "1" {
		return
	}
	os.Stdout.WriteString("tool 1.2.3\n" + strings.Repeat("x", 1200000))
	os.Exit(0)
}
func TestOwnedWorkersDrainBeforeFileOperations(t *testing.T) {

	exe, _ := os.Executable()
	file := filepath.Join(t.TempDir(), "held.bin")
	out, e := (ExecRunner{Log: io.Discard}).Run(context.Background(), []string{exe, "-test.run=^TestHelperProcess$"}, "", map[string]string{"LMINSTALL_HELPER": "job-parent", "LMINSTALL_LOCK_FILE": file}, true)
	if e != nil {
		t.Fatal(e, out)
	}
	lines := strings.Fields(out)
	pid, e := strconv.Atoi(lines[2])
	if e != nil {
		t.Fatal(out)
	}

	if c, e := net.Dial("tcp", lines[3]); e == nil {
		c.Close()
		if child, e := os.FindProcess(pid); e == nil {
			child.Kill()
		} // The live helper PID this test started.
		t.Fatal("owned worker still accepts connections after command exit")
	}
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

func TestReleaseLauncherUsesLiteralStablePath(t *testing.T) {
	m, _ := fixture(t)
	m.OS = runtime.GOOS
	m.Root = filepath.Join(m.Root, "space 雪 & literal")
	dir := m.dir("tool")
	exe, _ := os.Executable()
	name := "tool"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	rel := filepath.Join("payload-version", "bin", name)
	dest, _ := relative(dir, rel)
	os.MkdirAll(filepath.Dir(dest), 0700)
	in, e := os.Open(exe)
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	out, e := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY, 0755)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = io.Copy(out, in); e != nil {
		t.Fatal(e)
	}
	out.Close()
	p, _ := m.Manifest.Provider("tool")
	if e = m.publishLauncher(p, rel); e != nil {
		t.Fatal(e)
	}
	launcher := filepath.Join(dir, "bin", "tool")
	if runtime.GOOS == "windows" {
		launcher += ".cmd"
	}
	text, e := (ExecRunner{Log: io.Discard}).Run(context.Background(), []string{launcher, "-test.run=^TestHelperProcess$"}, "", map[string]string{"LMINSTALL_HELPER": "1"}, true)
	if e != nil || len(text) != 1200011 {
		t.Fatal(e, len(text))
	}
}
