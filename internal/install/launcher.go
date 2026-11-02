package install

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Stable PATH entries select a versioned bundle. No provider-specific runtime code.
func (m *Manager) publishLauncher(p Provider, bin string) error {
	dir := m.dir(p.ID)
	target, e := relative(dir, bin)
	if e != nil {
		return e
	}
	stable := filepath.Join(dir, "bin")
	if e = os.MkdirAll(stable, 0700); e != nil {
		return e
	}
	name := p.Binary
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	f, e := os.CreateTemp(stable, ".launcher-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	f.Close()
	defer os.Remove(tmp)
	if runtime.GOOS == "windows" {
		// Only a relative bundle path is in the batch source; %~dp0 carries arbitrary Unicode roots.
		if strings.ContainsAny(bin, "\r\n\"%") {
			return fmt.Errorf("release path cannot be represented by a Windows launcher")
		}
		text := "@echo off\r\n\"%~dp0..\\" + strings.ReplaceAll(bin, "/", "\\") + "\" %*\r\nexit /b %errorlevel%\r\n"
		if e = os.WriteFile(tmp, []byte(text), 0600); e != nil {
			return e
		}
	} else {
		os.Remove(tmp)
		if p.Recipe(m.OS).Launcher == "exec" {
			text, e := execLauncher(bin)
			if e != nil {
				return e
			}
			if e = os.WriteFile(tmp, []byte(text), 0o755); e != nil {
				return e
			}
			return os.Rename(tmp, filepath.Join(stable, name))
		}
		rel, e := filepath.Rel(stable, target)
		if e != nil {
			return e
		}
		if e = os.Symlink(rel, tmp); e != nil {
			return e
		}
	}
	return os.Rename(tmp, filepath.Join(stable, name))
}

// Validate the PATH entry as well as the versioned payload before reusing it.
func (m *Manager) checkLauncher(p Provider, bin string) error {
	path := filepath.Join(m.dir(p.ID), "bin", p.Binary)
	if runtime.GOOS == "windows" {
		path += ".cmd"
		text, e := os.ReadFile(path)
		expected := "@echo off\r\n\"%~dp0..\\" + strings.ReplaceAll(bin, "/", "\\") + "\" %*\r\nexit /b %errorlevel%\r\n"
		if e != nil || string(text) != expected {
			return fmt.Errorf("missing or changed release launcher %s; run install to repair", path)
		}
	} else if p.Recipe(m.OS).Launcher == "exec" {
		want, err := execLauncher(bin)
		text, e := os.ReadFile(path)
		if e != nil || err != nil || string(text) != want {
			return fmt.Errorf("missing or changed release launcher %s; run install to repair", path)
		}
	} else {
		target, e := os.Readlink(path)
		want, err := relative(m.dir(p.ID), bin)
		if e != nil || err != nil || filepath.Clean(filepath.Join(filepath.Dir(path), target)) != want {
			return fmt.Errorf("missing or changed release launcher %s; run install to repair", path)
		}
	}
	return nil
}

// Some bundles locate their runtime relative to the path they were started as,
// so a symlink would resolve to the wrong root. This wrapper only forwards to the
// verified versioned payload; it installs nothing.
func execLauncher(bin string) (string, error) {
	if strings.ContainsAny(bin, "\r\n\"'$`\\") {
		return "", fmt.Errorf("release path cannot be represented by a launcher")
	}
	return "#!/bin/sh\nexec \"$(dirname \"$0\")/../" + bin + "\" \"$@\"\n", nil
}
