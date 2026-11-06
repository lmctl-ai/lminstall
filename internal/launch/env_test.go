package launch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lmctl-ai/lminstall/internal/install"
)

func fakeInstalledTool(t *testing.T, provider string) (string, install.Manifest) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "providers", provider)
	os.MkdirAll(filepath.Join(dir, "bin"), 0700)
	binary := filepath.Join(dir, "bin", "tool")
	os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0755)
	os.WriteFile(filepath.Join(dir, ".lminstall-owner"), []byte(provider+"\n"), 0600)
	receipt, _ := json.Marshal(map[string]string{"provider": provider, "bin": "bin/tool", "bin_dir": "bin", "version": "1.0.0"})
	os.WriteFile(filepath.Join(dir, "receipt.json"), receipt, 0600)
	real := manifest(t)
	p, e := real.Provider(provider)
	if e != nil {
		t.Fatal(e)
	}
	p.Recipes = map[string]install.Recipe{"*": {Channel: "steps"}}
	m := install.Manifest{Version: 1, Providers: []install.Provider{p}}
	return root, m
}

func TestEnvironmentRendersPersistentMockRuntime(t *testing.T) {
	root, m := fakeInstalledTool(t, "claude")
	profile := filepath.Join(t.TempDir(), "profile")
	if _, e := Configure(m, Options{Provider: "claude", Backend: "mock", BaseURL: "http://127.0.0.1:4321", Model: "mock-agent", ProfileDir: profile, InstallRoot: root}); e != nil {
		t.Fatal(e)
	}
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	r, e := Environment(m, "claude", profile, runtimeDir)
	if e != nil {
		t.Fatal(e)
	}
	if r.Executable != filepath.Join(root, "providers", "claude", "bin", "tool") {
		t.Fatalf("executable: %s", r.Executable)
	}
	if r.Environment["HOME"] != filepath.Join(runtimeDir, "home") || r.Environment["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:4321" || r.Environment["ANTHROPIC_AUTH_TOKEN"] != mockPlaceholder {
		t.Fatalf("environment: %v", r.Environment)
	}
	for _, dir := range []string{r.Home, r.Work, r.Environment["XDG_CONFIG_HOME"], r.Environment["CLAUDE_CONFIG_DIR"]} {
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			t.Fatalf("runtime directory missing: %s", dir)
		}
	}
	if len(r.ArgvPrefix) == 0 || r.ArgvPrefix[1] != "mock-agent" {
		t.Fatalf("argv prefix: %v", r.ArgvPrefix)
	}
	// A second rendering into the same directory is idempotent: shared state survives.
	marker := filepath.Join(r.Home, "projects-marker")
	os.WriteFile(marker, []byte("x"), 0600)
	if _, e = Environment(m, "claude", profile, runtimeDir); e != nil {
		t.Fatal(e)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("re-rendering removed orchestrator state")
	}
	gh, e := GitHubEnvRuntime(r)
	if e != nil {
		t.Fatal(e)
	}
	for _, want := range []string{"ANTHROPIC_BASE_URL=http://127.0.0.1:4321\n", "LMINSTALL_RUNTIME_DIR=" + runtimeDir + "\n", "LMINSTALL_PROVIDER=claude\n", "LMINSTALL_MODEL=mock-agent\n", "LMINSTALL_EXECUTABLE=" + r.Executable + "\n", "HOME=" + r.Home + "\n"} {
		if !strings.Contains(gh, want) {
			t.Fatalf("github-env missing %q in\n%s", want, gh)
		}
	}
	sh, e := ShellEnvRuntime(r)
	if e != nil || !strings.Contains(sh, "export ANTHROPIC_AUTH_TOKEN='"+mockPlaceholder+"'\n") {
		t.Fatalf("shell env: %v\n%s", e, sh)
	}
}

func TestEnvironmentWritesCodexConfigFiles(t *testing.T) {
	root, m := fakeInstalledTool(t, "codex")
	profile := filepath.Join(t.TempDir(), "profile")
	if _, e := Configure(m, Options{Provider: "codex", Backend: "mock", BaseURL: "http://127.0.0.1:4321", Model: "mock-agent", ProfileDir: profile, InstallRoot: root}); e != nil {
		t.Fatal(e)
	}
	runtimeDir := filepath.Join(t.TempDir(), "runtime")
	r, e := Environment(m, "codex", profile, runtimeDir)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Files) != 1 || !strings.HasSuffix(r.Files[0], filepath.Join("home", "codex", "config.toml")) {
		t.Fatalf("files: %v", r.Files)
	}
	b, err := os.ReadFile(r.Files[0])
	if err != nil || !strings.Contains(string(b), "http://127.0.0.1:4321") {
		t.Fatalf("config.toml not rendered against the mock base: %v %s", err, b)
	}
	if r.Environment["CODEX_HOME"] != filepath.Join(runtimeDir, "home", "codex") {
		t.Fatalf("CODEX_HOME: %s", r.Environment["CODEX_HOME"])
	}
}

func TestEnvironmentRefusesLiveBackendsRelativeAndUnsafeRuntimeDirs(t *testing.T) {
	root, m := fakeInstalledTool(t, "claude")
	profile := filepath.Join(t.TempDir(), "profile")
	if _, e := Configure(m, Options{Provider: "claude", Backend: "ollama-cloud", BaseURL: "https://ollama.com", Model: "m", CredentialEnv: "OLLAMA_API_KEY", ProfileDir: profile, InstallRoot: root}); e != nil {
		t.Fatal(e)
	}
	if _, e := Environment(m, "claude", profile, filepath.Join(t.TempDir(), "runtime")); e == nil || e.Error() != "env_requires_mock_backend" {
		t.Fatalf("live backend accepted: %v", e)
	}
	mockProfile := filepath.Join(t.TempDir(), "mock-profile")
	if _, e := Configure(m, Options{Provider: "claude", Backend: "mock", BaseURL: "http://127.0.0.1:1", Model: "m", ProfileDir: mockProfile, InstallRoot: root}); e != nil {
		t.Fatal(e)
	}
	if _, e := Environment(m, "claude", mockProfile, "relative/runtime"); e == nil || e.Error() != "runtime_dir_must_be_absolute" {
		t.Fatalf("relative runtime accepted: %v", e)
	}
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, []byte("x"), 0600)
	if _, e := Environment(m, "claude", mockProfile, file); e == nil || e.Error() != "runtime_directory_refused" {
		t.Fatalf("file runtime accepted: %v", e)
	}
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(t.TempDir(), link)
	if _, e := Environment(m, "claude", mockProfile, link); e == nil || e.Error() != "runtime_directory_refused" {
		t.Fatalf("symlink runtime accepted: %v", e)
	}
	uninstalled := filepath.Join(t.TempDir(), "uninstalled")
	if _, e := Configure(m, Options{Provider: "claude", Backend: "mock", BaseURL: "http://127.0.0.1:1", Model: "m", ProfileDir: uninstalled, InstallRoot: t.TempDir()}); e != nil {
		t.Fatal(e)
	}
	if _, e := Environment(m, "claude", uninstalled, filepath.Join(t.TempDir(), "runtime")); e == nil || e.Error() != "provider_not_installed" {
		t.Fatalf("uninstalled provider accepted: %v", e)
	}
}
