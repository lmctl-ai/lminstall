package install

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Selecting the PATH executable must not discard its bundle-relative resources.
func TestReleaseInstallRetainsBundleSiblings(t *testing.T) {
	binary := "tool"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	files := map[string]string{
		"package/bin/" + binary:     "binary",
		"package/dist/config.json":  `{"source":"bundle"}`,
		"package/data/relative.txt": "owned sibling data",
	}
	var payload bytes.Buffer
	archive := zip.NewWriter(&payload)
	for name, content := range files {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload.Bytes())
	digest := "sha256:" + hex.EncodeToString(hash[:])
	var base string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/asset" {
			_, _ = w.Write(payload.Bytes())
			return
		}
		fmt.Fprintf(w, `{"tag_name":"v1.2.3","assets":[{"name":"bundle.zip","browser_download_url":%q,"digest":%q}]}`, base+"/asset", digest)
	}))
	t.Cleanup(server.Close)
	base = server.URL
	previous := client
	client = server.Client()
	transport := client.Transport
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		local := r.Clone(r.Context())
		if local.URL.Host == "api.github.com" {
			u := *local.URL
			u.Scheme, u.Host, u.Path = "https", strings.TrimPrefix(base, "https://"), "/release"
			local.URL = &u
		}
		return transport.RoundTrip(local)
	})
	t.Cleanup(func() { client = previous })
	manager, _ := fixture(t)
	manager.OS, manager.Arch = runtime.GOOS, runtime.GOARCH
	manager.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "release", Repository: "test/repo", Asset: "bundle.zip"}
	installed, err := manager.Apply(context.Background(), "install", "tool")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(installed.Binary))
	for name, want := range files {
		relative := strings.TrimPrefix(name, "package/")
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil || string(got) != want {
			t.Fatalf("release bundle sibling %s lost: got %q, error %v", relative, got, err)
		}
	}
	status, err := manager.Status(context.Background(), "tool")
	if err != nil || status.State != "installed" || status.Binary != installed.Binary {
		t.Fatalf("stable launcher must retain the selected bundle: %+v, error %v", status, err)
	}
}
