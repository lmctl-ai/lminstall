package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/lmctl-ai/lminstall/internal/install"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLaunchHelperProcess(t *testing.T) {
	if os.Getenv("LMINSTALL_HELPER") != "launch" {
		return
	}
	req, _ := http.NewRequest("POST", os.Getenv("HELPER_BASE")+"/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+os.Getenv("HELPER_KEY"))
	r, e := http.DefaultClient.Do(req)
	if e != nil {
		os.Exit(5)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	os.WriteFile(filepath.Join(os.Getenv("HOME"), "captured"), b, 0600)
	environment, _ := json.Marshal(os.Environ())
	os.WriteFile(filepath.Join(os.Getenv("HOME"), "environment.json"), environment, 0600)
	fmt.Print(string(b))
	fmt.Fprint(os.Stderr, "stderr marker\n")
	if os.Getenv("WP138_RUNTIME_SECRET") != "" {
		fmt.Print("inherited credential: " + os.Getenv("WP138_RUNTIME_SECRET"))
	}
	code, _ := strconv.Atoi(os.Getenv("HELPER_EXIT"))
	os.Exit(code)
}

type fileObserver struct {
	t        *testing.T
	dir, key string
	out      strings.Builder
}

func (o *fileObserver) Write(b []byte) (int, error) {
	filepath.WalkDir(o.dir, func(path string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() {
			data, e := os.ReadFile(path)
			if e == nil {
				assertAbsent(o.t, data, o.key)
			}
		}
		return nil
	})
	o.out.Write(b)
	return len(b), nil
}
func TestRuntimeCredentialNeverReachesChildDiskOrOutput(t *testing.T) { testRuntimeStreams(t, 0) }
func TestProviderExitPreservesStreamsAndCleanup(t *testing.T)         { testRuntimeStreams(t, 17) }
func testRuntimeStreams(t *testing.T, exit int) {
	key := "SYNTHETIC-WP138-DO-NOT-PERSIST+/&<>"
	t.Setenv("WP138_RUNTIME_SECRET", key)
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("credential not bound upstream")
		}
		for _, p := range independentEncodings(key) {
			w.Write(p)
			w.Write([]byte("\n"))
		}
	}))
	defer server.Close()
	root := t.TempDir()
	dir := filepath.Join(root, "providers", "claude")
	os.MkdirAll(filepath.Join(dir, "bin"), 0700)
	exe, _ := os.Executable()
	in, _ := os.Open(exe)
	defer in.Close()
	name := "tool"
	if filepath.Ext(exe) == ".exe" {
		name += ".exe"
	}
	binary := filepath.Join(dir, "bin", name)
	out, e := os.OpenFile(binary, os.O_CREATE|os.O_WRONLY, 0755)
	if e != nil {
		t.Fatal(e)
	}
	io.Copy(out, in)
	out.Close()
	os.WriteFile(filepath.Join(dir, ".lminstall-owner"), []byte("claude\n"), 0600)
	receipt, _ := json.Marshal(map[string]string{"provider": "claude", "bin": "bin/" + name, "bin_dir": "bin", "version": "1.0.0"})
	os.WriteFile(filepath.Join(dir, "receipt.json"), receipt, 0600)
	spec := install.LaunchSpec{Supported: true, Protocol: "openai-chat", BaseShape: "v1", Adapter: "claude", AdapterVersion: 1, Environment: map[string]string{"LMINSTALL_HELPER": "launch", "HELPER_EXIT": strconv.Itoa(exit), "HELPER_BASE": "{base}", "HELPER_KEY": "{credential}"}, ArgvPrefix: []string{"-test.run=^TestLaunchHelperProcess$", "--"}, Headers: map[string]string{"Authorization": "Bearer {credential}"}}
	m := install.Manifest{Version: 1, Providers: []install.Provider{{ID: "claude", Binary: "tool", Source: "https://example.invalid", Recipes: map[string]install.Recipe{"*": {Channel: "steps"}}, LaunchConfig: install.LaunchConfig{Version: 1, Backends: map[string]install.LaunchSpec{"mock": spec}}}}}
	profile := filepath.Join(t.TempDir(), "profile")
	if _, e := Configure(m, Options{Provider: "claude", Backend: "mock", BaseURL: server.URL, Model: "m", CredentialEnv: "WP138_RUNTIME_SECRET", ProfileDir: profile, InstallRoot: root}); e != nil {
		t.Fatal(e)
	}
	observer := &fileObserver{t: t, dir: profile, key: key}
	var stderr strings.Builder
	code, e := Run(context.Background(), m, "claude", profile, []string{"", "literal quotes \" & $() 雪"}, IO{Out: observer, Err: &stderr})
	if e != nil || code != exit || !called {
		t.Fatalf("runtime failed: %d %v called=%v stderr=%s", code, e, called, stderr.String())
	}
	assertAbsent(t, []byte(observer.out.String()), key)
	assertAbsent(t, []byte(stderr.String()), key)
	if strings.Contains(observer.out.String(), "stderr marker") || !strings.Contains(stderr.String(), "stderr marker") {
		t.Fatal("stdout/stderr were merged")
	}
	entries, _ := os.ReadDir(filepath.Join(profile, "runs"))
	if len(entries) != 0 {
		t.Fatal("runtime HOME retained")
	}
}
func TestGatewayRejectsForeignHomeBeforeSignals(t *testing.T) {
	home := t.TempDir()
	other := t.TempDir()
	record := gatewayRecord{Kind: "hermes-gateway", Home: other, PID: os.Getpid(), Start: 1}
	if validateGatewayRecord(record, home) == nil {
		t.Fatal("foreign HOME accepted")
	}
}
