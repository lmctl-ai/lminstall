package install

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func ExecuteOwned(cmd *exec.Cmd) error { return runOwned(cmd) }
func (m *Manager) LaunchCommand(provider string, args []string) ([]string, error) {
	p, e := m.Manifest.Provider(provider)
	if e != nil {
		return nil, errors.New("unknown_provider")
	}
	dir := m.dir(provider)
	if e = owned(dir, provider); e != nil {
		return nil, errors.New("provider_not_installed")
	}
	b, e := os.ReadFile(filepath.Join(dir, "receipt.json"))
	var rec receipt
	if e != nil || json.Unmarshal(b, &rec) != nil || rec.Provider != provider {
		return nil, errors.New("provider_not_installed")
	}
	if (p.Recipe(m.OS).Channel == "kimi" || p.Recipe(m.OS).Channel == "muse" || p.Recipe(m.OS).Channel == "hermes") && rec.Channel != p.Recipe(m.OS).Channel {
		return nil, errors.New("provider_method_migration_required")
	}
	binary, e := relative(dir, rec.Bin)
	if e != nil {
		return nil, errors.New("invalid_installed_binary")
	}
	if _, e = os.Stat(binary); e != nil {
		return nil, errors.New("installed_binary_missing")
	}
	if runtime.GOOS != "windows" || !strings.EqualFold(filepath.Ext(binary), ".cmd") {
		return append([]string{binary}, args...), nil
	}
	return ResolveNPMEntry(binary, p.Binary, p.LaunchConfig.NPMPackage, args)
}
func ResolveNPMEntry(shim, binary, packageName string, args []string) ([]string, error) {
	if packageName == "" || strings.Contains(packageName, "..") || filepath.IsAbs(packageName) {
		return nil, errors.New("unsupported_npm_launcher")
	}
	pkg := filepath.Join(filepath.Dir(shim), "node_modules", filepath.FromSlash(packageName))
	root, e := filepath.EvalSymlinks(pkg)
	if e != nil {
		return nil, errors.New("npm_package_missing")
	}
	b, e := os.ReadFile(filepath.Join(root, "package.json"))
	if e != nil {
		return nil, errors.New("npm_package_metadata_missing")
	}
	var data struct{ Bin json.RawMessage }
	if json.Unmarshal(b, &data) != nil {
		return nil, errors.New("invalid_npm_bin")
	}
	var entry string
	if json.Unmarshal(data.Bin, &entry) != nil {
		var entries map[string]string
		if json.Unmarshal(data.Bin, &entries) != nil {
			return nil, errors.New("invalid_npm_bin")
		}
		entry = entries[binary]
	}
	path, e := relative(root, entry)
	if e != nil {
		return nil, errors.New("npm_entry_escape")
	}
	path, e = filepath.EvalSymlinks(path)
	if e != nil {
		return nil, errors.New("npm_entry_missing")
	}
	rel, e := filepath.Rel(root, path)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("npm_entry_escape")
	}
	if strings.EqualFold(filepath.Ext(path), ".exe") {
		return append([]string{path}, args...), nil
	}
	node, e := exec.LookPath("node")
	if e != nil {
		return nil, errors.New("node_prerequisite_missing")
	}
	return append([]string{node, path}, args...), nil
}
