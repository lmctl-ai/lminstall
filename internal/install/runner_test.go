package install

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("LMINSTALL_HELPER") != "1" {
		return
	}
	os.Stdout.WriteString("tool 1.2.3\n" + strings.Repeat("x", 1200000))
	os.Exit(0)
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
