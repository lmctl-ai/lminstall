package launch

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/lmctl-ai/lminstall/internal/install"
)

// mockPlaceholder is the nonsecret key a mock backend sees. A mock profile binds
// no credential, so no bridge is needed and the value can live on disk.
const mockPlaceholder = "lminstall-mock-key"

// Runtime is a persistent rendering of a mock-backend profile for an orchestrator
// that starts the installed provider itself, many times, with shared state (for
// example lmctl seeding a session and then resuming it). Unlike `run`, nothing
// here is removed on exit and no credential is ever bound: only the mock backend
// is accepted, so the environment carries the literal loopback base URL and the
// placeholder key.
type Runtime struct {
	Profile          Profile           `json:"profile"`
	ProfileDir       string            `json:"profile_dir"`
	RuntimeDir       string            `json:"runtime_dir"`
	Home             string            `json:"home"`
	Work             string            `json:"work"`
	Executable       string            `json:"executable"`
	Environment      map[string]string `json:"environment"`
	Files            []string          `json:"files"`
	ArgvPrefix       []string          `json:"argv_prefix"`
	CredentialPolicy string            `json:"credential_policy"`
}

// Environment renders the profile's mock routing into runtimeDir (absolute; created
// with mode 0700; an existing file or symlink at that path is refused) and returns
// what an orchestrator must export before starting the provider executable.
func Environment(manifest install.Manifest, provider, dir, runtimeDir string) (Runtime, error) {
	var r Runtime
	p, canonical, e := Load(dir, provider)
	if e != nil {
		return r, e
	}
	if p.Backend != "mock" {
		return r, errors.New("env_requires_mock_backend")
	}
	if !filepath.IsAbs(runtimeDir) {
		return r, errors.New("runtime_dir_must_be_absolute")
	}
	runtimeDir = filepath.Clean(runtimeDir)
	if st, err := os.Lstat(runtimeDir); err == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
		return r, errors.New("runtime_directory_refused")
	}
	manager := install.Manager{Root: p.InstallRoot, OS: runtime.GOOS, Arch: runtime.GOARCH, Manifest: manifest}
	command, e := manager.LaunchCommand(provider, nil)
	if e != nil {
		return r, e
	}
	if e = os.MkdirAll(runtimeDir, 0700); e != nil {
		return r, errors.New("runtime_create_failed")
	}
	env, files, prefix, e := Render(p, runtimeDir, p.BaseURL, mockPlaceholder)
	if e != nil {
		return r, errors.New("config_render_failed")
	}
	if e = writeFiles(runtimeDir, files); e != nil {
		return r, errors.New("runtime_config_write_failed")
	}
	for _, path := range env {
		if filepath.IsAbs(path) && strings.HasPrefix(path, runtimeDir+string(filepath.Separator)) {
			os.MkdirAll(path, 0700)
		}
	}
	work := filepath.Join(runtimeDir, "work")
	if e = os.MkdirAll(work, 0700); e != nil {
		return r, errors.New("runtime_work_create_failed")
	}
	written := make([]string, 0, len(files))
	for name := range files {
		written = append(written, filepath.Join(runtimeDir, filepath.FromSlash(name)))
	}
	sort.Strings(written)
	return Runtime{Profile: p, ProfileDir: canonical, RuntimeDir: runtimeDir, Home: filepath.Join(runtimeDir, "home"), Work: work, Executable: command[0], Environment: env, Files: written, ArgvPrefix: prefix, CredentialPolicy: "mock backend only; the literal loopback base and a nonsecret placeholder key are written; no credential is read or bound"}, nil
}

func runtimeExports(r Runtime) (map[string]string, []string, error) {
	values := map[string]string{}
	for k, v := range r.Environment {
		values[k] = v
	}
	values["LMINSTALL_RUNTIME_DIR"] = r.RuntimeDir
	values["LMINSTALL_PROFILE_DIR"] = r.ProfileDir
	values["LMINSTALL_PROVIDER"] = r.Profile.Provider
	values["LMINSTALL_BACKEND"] = r.Profile.Backend
	values["LMINSTALL_MODEL"] = r.Profile.Model
	values["LMINSTALL_BASE_URL"] = r.Profile.BaseURL
	values["LMINSTALL_EXECUTABLE"] = r.Executable
	keys := make([]string, 0, len(values))
	for k, v := range values {
		if !envName.MatchString(k) || !safeValue(v) {
			return nil, nil, errors.New("env_value_unsafe")
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return values, keys, nil
}

// GitHubEnvRuntime renders KEY=VALUE lines for $GITHUB_ENV: the provider's
// environment plus LMINSTALL_* routing facts. Values with line breaks are refused
// rather than written as a multi-line record.
func GitHubEnvRuntime(r Runtime) (string, error) {
	values, keys, e := runtimeExports(r)
	if e != nil {
		return "", e
	}
	var out strings.Builder
	for _, k := range keys {
		out.WriteString(k + "=" + values[k] + "\n")
	}
	return out.String(), nil
}

// ShellEnvRuntime renders POSIX `export` lines with single-quoted values.
func ShellEnvRuntime(r Runtime) (string, error) {
	values, keys, e := runtimeExports(r)
	if e != nil {
		return "", e
	}
	var out strings.Builder
	for _, k := range keys {
		out.WriteString("export " + k + "='" + strings.ReplaceAll(values[k], "'", `'\''`) + "'\n")
	}
	return out.String(), nil
}
