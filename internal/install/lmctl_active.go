package install

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// firstOnPath finds the first executable `name` in pathEnv, the way a shell does.
func firstOnPath(name, pathEnv string) string {
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = []string{".cmd", ".exe", ".bat", ""}
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		for _, ext := range exts {
			candidate := filepath.Join(dir, name+ext)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode()&0111 != 0) {
				return candidate
			}
		}
	}
	return ""
}

// ActiveLmctl reports the lmctl a shell with pathEnv would run and, when that is a launcher this
// installer wrote (<root>/lmctl/bin/lmctl next to its install.json receipt), the root that owns it.
func ActiveLmctl(pathEnv string) (active, root string) {
	active = firstOnPath("lmctl", pathEnv)
	if active == "" {
		return "", ""
	}
	installationDir := filepath.Dir(filepath.Dir(active))
	if filepath.Base(installationDir) != "lmctl" || filepath.Base(filepath.Dir(active)) != "bin" {
		return active, ""
	}
	if _, err := os.Stat(filepath.Join(installationDir, "install.json")); err != nil {
		return active, ""
	}
	return active, filepath.Dir(installationDir)
}

// LmctlShadow tells whether the installed launchers in binDir are what `lmctl` resolves to on
// pathEnv. An empty result means they are; otherwise it is the path that wins instead (or "not on PATH").
func LmctlShadow(binDir, pathEnv string) string {
	active := firstOnPath("lmctl", pathEnv)
	if active == "" {
		return "not on PATH"
	}
	want, err1 := filepath.EvalSymlinks(filepath.Join(binDir, filepath.Base(active)))
	got, err2 := filepath.EvalSymlinks(active)
	if err1 == nil && err2 == nil {
		if runtime.GOOS == "windows" && strings.EqualFold(want, got) || want == got {
			return ""
		}
	}
	return active
}
