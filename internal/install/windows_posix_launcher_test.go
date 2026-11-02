package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsRequiresBothOwnedLaunchers(t *testing.T) {
	if runtime.GOOS != "windows" && os.Getenv("LMINSTALL_TEST_WINDOWS_BRANCH") != "1" {
		t.Skip("Windows branch; portable control uses Go overlay")
	}
	m, _ := fixture(t)
	m.OS = "windows"
	p := m.Manifest.Providers[0]
	bin := "payload-1/bin/tool.exe"
	dir := filepath.Join(m.dir(p.ID), "bin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := "@echo off\r\n\"%~dp0..\\" + strings.ReplaceAll(bin, "/", "\\") + "\" %*\r\nexit /b %errorlevel%\r\n"
	if err := os.WriteFile(filepath.Join(dir, p.Binary+".cmd"), []byte(cmd), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.checkLauncher(p, bin); err == nil {
		t.Fatal("missing POSIX launcher accepted as installed")
	}
	if err := os.WriteFile(filepath.Join(dir, p.Binary), []byte("changed"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := m.checkLauncher(p, bin); err == nil {
		t.Fatal("changed POSIX launcher accepted as installed")
	}
	if err := m.publishLauncher(p, bin); err != nil {
		t.Fatal(err)
	}
	if err := m.checkLauncher(p, bin); err != nil {
		t.Fatal("launcher repair failed:", err)
	}
}
