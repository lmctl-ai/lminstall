package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type hermesUpgradeRunner struct {
	lock      []byte
	version   string
	failBuild bool
}

func (f *hermesUpgradeRunner) Run(_ context.Context, args []string, cwd string, _ map[string]string, _ bool) (string, error) {
	if len(args) > 2 && args[1] == "python" && args[2] == "find" {
		return filepath.Join(cwd, "bootstrap-python"), nil
	}
	for _, arg := range args {
		if strings.HasSuffix(arg, "source_completion.py") {
			binary := filepath.Join(cwd, ".hermes/bin/hermes")
			if err := os.MkdirAll(filepath.Dir(binary), 0700); err != nil {
				return "", err
			}
			if err := os.WriteFile(binary, []byte("tool "+f.version), 0755); err != nil {
				return "", err
			}
			// Completion may publish its in-tree launcher before a later build fails.
			if f.failBuild {
				return "", fmt.Errorf("fixture product build failed after launcher publication")
			}
		}
	}
	if len(args) > 1 && args[1] == "--version" {
		if strings.Contains(args[0], ".hermes") {
			b, err := os.ReadFile(args[0])
			return string(b), err
		}
		return "uv 0.13.5", nil
	}
	return "", nil
}
func TestHermesRejectedBuildKeepsWorkingCheckoutAndReceipt(t *testing.T) {
	payload := zipBytes(t, "uv", []byte("private uv"))
	hash := sha256.Sum256(payload)
	var base string
	var lockBytes []byte
	version := "1.2.3"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "releases/latest") {
			fmt.Fprintf(w, `{"tag_name":%q}`, "v"+version)
		} else if strings.Contains(r.URL.Path, "/tar.gz/refs/tags/") {
			w.Write(sourceTarball(t, lockBytes))
		} else {
			w.Write(payload)
		}
	}))
	defer server.Close()
	base = server.URL
	old := client
	client = server.Client()
	transport := client.Transport
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		req := r.Clone(r.Context())
		u := *req.URL
		u.Host = strings.TrimPrefix(base, "https://")
		req.URL = &u
		return transport.RoundTrip(req)
	})
	defer func() { client = old }()
	lock, _ := json.Marshal(map[string]any{"packages": map[string]any{"uv": map[string]any{"version": "0.13.5", "artifacts": map[string]any{"linux-x64": map[string]string{"url": base + "/uv.zip", "sha256": hex.EncodeToString(hash[:])}}}, "python": map[string]string{"version": "3.14.3"}}})
	lockBytes = lock
	m, _ := fixture(t)
	runner := &hermesUpgradeRunner{lock: lock, version: version}
	m.Runner = runner
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "hermes", Repository: "https://github.com/fixture/hermes-agent.git", Bin: "source/.hermes/bin/hermes", BinDir: "source/.hermes/bin", Env: map[string]string{"HERMES_HOME": "{dir}/home/.hermes"}}
	first, err := m.Apply(context.Background(), "install", "tool")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(first.Binary)
	receiptPath := filepath.Join(m.dir("tool"), "receipt.json")
	receiptBefore, _ := os.ReadFile(receiptPath)
	version = "2.0.0"
	runner.version = version
	runner.failBuild = true
	if _, err = m.Apply(context.Background(), "upgrade", "tool"); err == nil || !strings.Contains(err.Error(), "fixture product build failed") {
		t.Fatalf("expected completion failure: %v", err)
	}
	after, _ := os.ReadFile(first.Binary)
	receiptAfter, _ := os.ReadFile(receiptPath)
	if !bytes.Equal(before, after) {
		t.Fatal("failed Hermes completion changed the previous working checkout")
	}
	if !bytes.Equal(receiptBefore, receiptAfter) {
		t.Fatal("failed Hermes completion changed receipt")
	}
	status, err := m.Status(context.Background(), "tool")
	if err != nil || status.State != "installed" || status.Version != "tool 1.2.3" {
		t.Fatalf("previous Hermes unavailable: %+v %v", status, err)
	}
	runner.failBuild = false
	next, err := m.Apply(context.Background(), "upgrade", "tool")
	if err != nil || next.Version != "tool 2.0.0" || next.BinDir != first.BinDir {
		t.Fatalf("valid Hermes build not published to stable PATH: %+v %v", next, err)
	}
}

// sourceTarball builds a GitHub-style source tarball (single top-level directory).
func sourceTarball(t *testing.T, lock []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		data []byte
	}{{"hermes-agent-1.2.3/pm/lock.json", lock}, {"hermes-agent-1.2.3/cli-config.yaml.example", []byte("x: 1\n")}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0644, Size: int64(len(f.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(f.data)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}
