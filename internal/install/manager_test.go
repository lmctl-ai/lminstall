package install

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls  [][]string
	output string
	fail   bool
}

func (f *fakeRunner) Run(_ context.Context, args []string, _ string, _ map[string]string, capture bool) (string, error) {
	f.calls = append(f.calls, append([]string{}, args...))
	if f.fail {
		return "", errors.New("provider failed")
	}
	if capture {
		return f.output, nil
	}
	return "", nil
}
func fixture(t *testing.T) (*Manager, *fakeRunner) {
	t.Helper()
	f := &fakeRunner{output: "tool 1.2.3\n"}
	m := &Manager{Root: t.TempDir(), OS: "linux", Arch: "amd64", Runner: f, Log: &bytes.Buffer{}, Manifest: Manifest{Providers: []Provider{{ID: "tool", Binary: "tool", Source: "https://example.invalid/official", Recipes: map[string]Recipe{"*": {Channel: "steps", Bin: "bin/tool", BinDir: "bin", Steps: []Step{{Args: []string{"fake", "install", "{dir}"}}}}}}}}}
	return m, f
}
func TestInstallIdempotentUpgradeAndOwnedUninstall(t *testing.T) {
	m, f := fixture(t)
	ctx := context.Background()
	r, e := m.Apply(ctx, "install", "tool")
	if e != nil {
		t.Fatal(e)
	}
	if r.Version != "tool 1.2.3" {
		t.Fatal(r)
	}
	_, e = m.Apply(ctx, "install", "tool")
	if e != nil {
		t.Fatal(e)
	}
	if len(f.calls) != 3 {
		t.Fatalf("reinstalled: %v", f.calls)
	}
	_, e = m.Apply(ctx, "upgrade", "tool")
	if e != nil {
		t.Fatal(e)
	}
	if len(f.calls) != 5 {
		t.Fatal(f.calls)
	}
	untouched := filepath.Join(m.Root, "keep")
	os.WriteFile(untouched, []byte("keep"), 0600)
	_, e = m.Apply(ctx, "uninstall", "tool")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(untouched); e != nil {
		t.Fatal(e)
	}
	_, e = m.Apply(ctx, "uninstall", "tool")
	if e != nil {
		t.Fatal(e)
	}
}
func TestVersionFailureIsNotInstalled(t *testing.T) {
	m, f := fixture(t)
	f.fail = true
	if _, e := m.Apply(context.Background(), "install", "tool"); e == nil {
		t.Fatal("accepted failed install")
	}
	f.fail = false
	f.output = "login required"
	if _, e := m.Apply(context.Background(), "install", "tool"); e == nil {
		t.Fatal("accepted non-version output")
	}
}
func TestRefusesUnknownUnsafeAndUnownedPaths(t *testing.T) {
	m, _ := fixture(t)
	for _, id := range []string{"missing", "../tool", "/tmp/x"} {
		if _, e := m.Apply(context.Background(), "install", id); e == nil {
			t.Fatal(id)
		}
	}
	dir := filepath.Join(m.Root, "providers", "tool")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "user-data"), []byte("mine"), 0600)
	if _, e := m.Apply(context.Background(), "uninstall", "tool"); e == nil {
		t.Fatal("removed unowned directory")
	}
	if b, e := os.ReadFile(filepath.Join(dir, "user-data")); e != nil || string(b) != "mine" {
		t.Fatalf("unowned data changed: %q %v", b, e)
	}
}
func TestManifestDataDefinesCommandsAndPlatforms(t *testing.T) {
	m, f := fixture(t)
	m.OS = "windows"
	m.Manifest.Providers[0].Recipes["windows"] = Recipe{Unavailable: "vendor has no unattended Windows distribution"}
	r, e := m.Apply(context.Background(), "install", "tool")
	if e == nil || r.State != "unavailable" || len(f.calls) != 0 {
		t.Fatalf("%+v %v", r, e)
	}
	m.OS = "linux"
	m.Root = filepath.Join(m.Root, "space 雪 & value")
	_, e = m.Apply(context.Background(), "install", "tool")
	if e != nil {
		t.Fatal(e)
	}
	if len(f.calls[0]) != 3 || !strings.Contains(f.calls[0][2], "space 雪 & value") {
		t.Fatal(f.calls)
	}
}
func TestCatalogAndNoProductionCredentialFields(t *testing.T) {
	manifest, e := LoadManifest()
	if e != nil {
		t.Fatal(e)
	}
	if len(manifest.Providers) != 15 {
		t.Fatal(len(manifest.Providers))
	}
	for _, p := range manifest.Providers {
		if p.ID == "" || p.Binary == "" || !strings.HasPrefix(p.Source, "https://") {
			t.Fatal(p)
		}
	}
}

func TestProjectRecipeUsesRootLockAndUpdatesCheckout(t *testing.T) {
	m, f := fixture(t)
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "project", Repository: "https://example.invalid/official", Bin: "source/.venv/bin/tool", BinDir: "source/.venv/bin", Steps: []Step{{Args: []string{"uv", "sync", "--frozen", "--no-dev"}, Cwd: "{dir}/source"}}}
	if _, e := m.Apply(context.Background(), "install", "tool"); e != nil {
		t.Fatal(e)
	}
	// A successful clone leaves a checkout; the fake runner otherwise has no filesystem effects.
	os.MkdirAll(filepath.Join(m.dir("tool"), "source", ".git"), 0700)
	if _, e := m.Apply(context.Background(), "upgrade", "tool"); e != nil {
		t.Fatal(e)
	}
	if len(f.calls) != 7 || f.calls[0][1] != "clone" || f.calls[1][1] != "sync" || f.calls[3][3] != "fetch" || f.calls[4][3] != "checkout" {
		t.Fatal(f.calls)
	}
}

func TestFailedRemovalRetainsOwnership(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".lminstall-owner"), []byte("tool\n"), 0600)
	os.Mkdir(filepath.Join(dir, "bin"), 0700)
	err := removeOwned(dir, "tool", func(path string) error {
		if path == dir {
			os.Remove(filepath.Join(dir, ".lminstall-owner"))
			return errors.New("in use")
		}
		if filepath.Base(path) == "bin" {
			return errors.New("in use")
		}
		return os.RemoveAll(path)
	})
	if err == nil {
		t.Fatal("accepted failed cleanup")
	}
	if e := owned(dir, "tool"); e != nil {
		t.Fatalf("lost retry ownership: %v", e)
	}
}
