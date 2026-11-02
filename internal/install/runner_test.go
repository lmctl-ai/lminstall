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

func TestWindowsOwnedImageRefusesUntilProviderExits(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows mapped-image ownership")
	}
	m, _ := fixture(t)
	m.OS = "windows"
	dir := m.dir("tool")
	os.MkdirAll(filepath.Join(dir, "bin"), 0700)
	os.WriteFile(filepath.Join(dir, ".lminstall-owner"), []byte("tool\n"), 0600)
	exe, _ := os.Executable()
	in, e := os.Open(exe)
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	dest := filepath.Join(dir, "bin", "tool.exe")
	out, e := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY, 0755)
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(out, in)
	out.Close()
	held := filepath.Join(t.TempDir(), "held")
	child := exec.Command(dest, "-test.run=^TestHelperProcess$")
	child.Env = append(os.Environ(), "LMINSTALL_HELPER=job-child", "LMINSTALL_LOCK_FILE="+held)
	ready, e := child.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if child.ProcessState == nil {
			child.Process.Kill()
			child.Wait()
		}
	}()
	line, e := bufio.NewReader(ready).ReadString('\n')
	if e != nil {
		t.Fatal(e)
	}
	address := strings.TrimSpace(strings.TrimPrefix(line, "READY "))
	if e = os.Remove(dest); e == nil {
		t.Fatal("legacy-delete control unexpectedly removed a mapped image")
	}
	if _, e = m.Apply(context.Background(), "uninstall", "tool"); e == nil {
		t.Fatal("uninstall accepted a running mapped executable")
	}
	if e = owned(dir, "tool"); e != nil {
		t.Fatalf("failed uninstall lost ownership: %v", e)
	}
	c, e := net.Dial("tcp", address)
	if e != nil {
		t.Fatalf("uninstall killed a provider process: %v", e)
	}
	c.Close()
	// Only stop the exact child this test started, then wait for its exit.
	if e = child.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	child.Wait()
	if _, e = m.Apply(context.Background(), "uninstall", "tool"); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(dir); !os.IsNotExist(e) {
		t.Fatalf("owned namespace remains: %v", e)
	}
}

func TestExecLauncherForwardsToRelativePayload(t *testing.T) {
	text, e := execLauncher(filepath.Join("payload-1", "bin", "tool.exe"))
	if e != nil || text != "#!/bin/sh\nexec \"$(dirname \"$0\")/../payload-1/bin/tool.exe\" \"$@\"\n" {
		t.Fatalf("%q %v", text, e)
	}
	if _, e = execLauncher("bad\"; rm -rf x"); e == nil {
		t.Fatal("unsafe path accepted")
	}
}
