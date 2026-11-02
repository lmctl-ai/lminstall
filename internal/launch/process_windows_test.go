//go:build windows

package launch

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWindowsGatewayHelper(t *testing.T) {
	role := os.Getenv("WP138_GATEWAY_HELPER")
	if role == "" {
		return
	}
	if role == "gateway" {
		fmt.Println("READY")
		event := kernel.NewProc("CreateEventW")
		handle, _, _ := event.Call(0, 1, 0, 0)
		syscall.WaitForSingleObject(syscall.Handle(handle), syscall.INFINITE)
		os.Exit(0)
	}
	exe, _ := os.Executable()
	child := exec.Command(exe, "-test.run=^TestWindowsGatewayHelper$", "--", "gateway", "run")
	child.Env = append(os.Environ(), "WP138_GATEWAY_HELPER=gateway")
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x01000000 | 0x00000008 | 0x00000200}
	stdout, _ := child.StdoutPipe()
	if child.Start() != nil {
		fmt.Fprintln(os.Stderr, "windows_breakaway_unavailable")
		os.Exit(3)
	}
	if line, _ := bufio.NewReader(stdout).ReadString('\n'); line != "READY\n" {
		child.Process.Kill()
		child.Wait()
		os.Exit(4)
	}
	handle, e := syscall.OpenProcess(0x400, false, uint32(child.Process.Pid))
	if e != nil {
		child.Process.Kill()
		child.Wait()
		os.Exit(5)
	}
	birth, e := processBirth(handle)
	syscall.CloseHandle(handle)
	if e != nil {
		child.Process.Kill()
		child.Wait()
		os.Exit(6)
	}
	record := gatewayRecord{Kind: "hermes-gateway", Home: os.Getenv("HERMES_HOME"), PID: child.Process.Pid, Start: birth}
	b, _ := json.Marshal(record)
	if os.WriteFile(filepath.Join(record.Home, "gateway.pid"), b, 0600) != nil {
		child.Process.Kill()
		child.Wait()
		os.Exit(7)
	}
	os.Exit(0)
}
func TestWindowsHermesBreakawayAndOwnedCleanup(t *testing.T) {
	exe, _ := os.Executable()
	home := t.TempDir()
	command := func() *exec.Cmd {
		cmd := exec.Command(exe, "-test.run=^TestWindowsGatewayHelper$")
		cmd.Env = append(os.Environ(), "WP138_GATEWAY_HELPER=parent", "HERMES_HOME="+home)
		return cmd
	}
	// This is the pre-fix Job policy: the same detached request must fail.
	if e := executeRuntime(command(), false); e == nil {
		t.Fatal("restricted Job unexpectedly allowed breakaway")
	}
	if e := executeRuntime(command(), true); e != nil {
		t.Fatal("Hermes detached gateway refused", e)
	}
	b, e := os.ReadFile(filepath.Join(home, "gateway.pid"))
	if e != nil {
		t.Fatal(e)
	}
	var record gatewayRecord
	if json.Unmarshal(b, &record) != nil {
		t.Fatal("invalid helper record")
	}
	defer cleanupHermes(home)
	handle, e := syscall.OpenProcess(0x100000, false, uint32(record.PID))
	if e != nil {
		t.Fatal(e)
	}
	defer syscall.CloseHandle(handle)
	wrong := record
	wrong.Start++
	if cleanupGatewayProcess(wrong, home) == nil {
		t.Fatal("wrong birth accepted")
	}
	other := t.TempDir()
	if cleanupGatewayProcess(record, other) == nil {
		t.Fatal("foreign HOME accepted")
	}
	if e = cleanupHermes(home); e != nil {
		t.Fatal(e)
	}
	if status, _ := syscall.WaitForSingleObject(handle, 0); status != syscall.WAIT_OBJECT_0 {
		t.Fatal("owned detached process remains alive")
	}
}
