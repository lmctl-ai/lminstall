package install

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeExe(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

// managed lays out what InstallLmctl leaves behind: <root>/lmctl/bin/lmctl and <root>/lmctl/install.json.
func managed(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(root, "lmctl", "bin")
	writeExe(t, filepath.Join(bin, "lmctl"), "#!/bin/sh\n")
	if err := os.WriteFile(filepath.Join(root, "lmctl", "install.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestActiveLmctlFindsTheManagedRootOnlyForOurLaunchers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("launcher layout test uses unix modes")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "owned")
	bin := managed(t, root)
	active, got := ActiveLmctl(bin)
	if active != filepath.Join(bin, "lmctl") || got != root {
		t.Fatalf("managed launcher: active=%q root=%q", active, got)
	}
	// An npm-style copy is active, but not ours: no root, so lminstall never adopts it.
	npm := filepath.Join(dir, "local", "bin")
	writeExe(t, filepath.Join(npm, "lmctl"), "#!/bin/sh\n")
	active, got = ActiveLmctl(npm + string(os.PathListSeparator) + bin)
	if active != filepath.Join(npm, "lmctl") || got != "" {
		t.Fatalf("foreign copy first: active=%q root=%q", active, got)
	}
	// A bin directory named like ours but without a receipt is not ours either.
	fake := filepath.Join(dir, "x", "lmctl", "bin")
	writeExe(t, filepath.Join(fake, "lmctl"), "#!/bin/sh\n")
	if _, got = ActiveLmctl(fake); got != "" {
		t.Fatalf("no receipt, yet root=%q", got)
	}
	if active, got = ActiveLmctl(filepath.Join(dir, "empty")); active != "" || got != "" {
		t.Fatalf("nothing on PATH: %q %q", active, got)
	}
}

func TestLmctlShadowNamesWhatWinsOverTheInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("launcher layout test uses unix modes")
	}
	dir := t.TempDir()
	bin := managed(t, filepath.Join(dir, "owned"))
	npm := filepath.Join(dir, "local", "bin")
	writeExe(t, filepath.Join(npm, "lmctl"), "#!/bin/sh\n")
	sep := string(os.PathListSeparator)
	if s := LmctlShadow(bin, bin+sep+npm); s != "" {
		t.Fatalf("installed copy is first, yet shadowed by %q", s)
	}
	if s := LmctlShadow(bin, npm+sep+bin); s != filepath.Join(npm, "lmctl") {
		t.Fatalf("the copy that wins must be named, got %q", s)
	}
	if s := LmctlShadow(bin, filepath.Join(dir, "nowhere")); s != "not on PATH" {
		t.Fatalf("absent from PATH, got %q", s)
	}
	// A symlink to the installed launcher is still the installed launcher.
	link := filepath.Join(dir, "links", "lmctl")
	os.MkdirAll(filepath.Dir(link), 0700)
	if err := os.Symlink(filepath.Join(bin, "lmctl"), link); err != nil {
		t.Fatal(err)
	}
	if s := LmctlShadow(bin, filepath.Dir(link)); s != "" {
		t.Fatalf("symlink to the install counts as the install, got %q", s)
	}
}
