package install

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
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

func zipBytes(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	w, e := z.Create(name)
	if e != nil {
		t.Fatal(e)
	}
	w.Write(data)
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestArchivePathTraversalAndUnboundedPayload(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "C:\\escape"} {
		p := filepath.Join(t.TempDir(), "x.zip")
		os.WriteFile(p, zipBytes(t, name, []byte("bad")), 0600)
		if e := extractArchive(p, t.TempDir()); e == nil {
			t.Fatal("accepted", name)
		}
	}
	root := t.TempDir()
	p := filepath.Join(root, "x.zip")
	data := bytes.Repeat([]byte("x"), 1100000)
	os.WriteFile(p, zipBytes(t, "folder/雪", data), 0600)
	if e := extractArchive(p, filepath.Join(root, "out")); e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(filepath.Join(root, "out/folder/雪"))
	if e != nil || !bytes.Equal(got, data) {
		t.Fatal(e, len(got))
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestReleaseUpgradeKeepsBinDirAndChecksDigest(t *testing.T) {
	payload := zipBytes(t, "bundle/bin/tool", []byte("binary"))
	hash := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(hash[:])
	tag := "v1.2.3"
	var url string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/asset" {
			w.Write(payload)
		} else {
			fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":"bundle.zip","browser_download_url":%q,"digest":%q}]}`, tag, url+"/asset", digest)
		}
	}))
	defer server.Close()
	url = server.URL
	old := client
	client = server.Client()
	originalTransport := client.Transport
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		if copy.URL.Host == "api.github.com" {
			u := *copy.URL
			u.Scheme = "https"
			u.Host = strings.TrimPrefix(url, "https://")
			u.Path = "/release"
			copy.URL = &u
		}
		return originalTransport.RoundTrip(copy)
	})
	defer func() { client = old }()
	m, f := fixture(t)
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "release", Repository: "test/repo", Asset: "bundle.zip"}
	first, e := m.Apply(context.Background(), "install", "tool")
	if e != nil {
		t.Fatal(e)
	}
	// Status must qualify the stable PATH entry on every supported OS.
	launcher := filepath.Join(first.BinDir, "tool")
	if runtime.GOOS == "windows" {
		launcher += ".cmd"
	}
	if e := os.Remove(launcher); e != nil {
		t.Fatal(e)
	}
	if status, e := m.Status(context.Background(), "tool"); e != nil || status.State != "broken" {
		t.Fatalf("missing launcher: %+v %v", status, e)
	}
	if _, e := m.Apply(context.Background(), "install", "tool"); e != nil {
		t.Fatal(e)
	}
	if status, e := m.Status(context.Background(), "tool"); e != nil || status.State != "installed" {
		t.Fatalf("repaired launcher: %+v %v", status, e)
	}
	f.output = "tool 2.0.0"
	tag = "v2.0.0"
	second, e := m.Apply(context.Background(), "upgrade", "tool")
	if e != nil {
		t.Fatal(e)
	}
	if first.BinDir != second.BinDir {
		t.Fatalf("upgrade invalidated PATH: %s -> %s", first.BinDir, second.BinDir)
	}
	digest = "sha256:" + strings.Repeat("0", 64)
	if _, e = m.Apply(context.Background(), "upgrade", "tool"); e == nil {
		t.Fatal("accepted bad checksum")
	}
}

func TestOfficialUpdateManifestChecksSHA512(t *testing.T) {
	data := []byte("native binary")
	h := sha512.Sum512(data)
	digest := hex.EncodeToString(h[:])
	var url string
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/binary" {
			w.Write(data)
		} else {
			fmt.Fprintf(w, `{"version":"1.2.3","url":%q,"sha512":%q}`, url+"/binary", digest)
		}
	}))
	defer s.Close()
	url = s.URL
	old := client
	client = s.Client()
	defer func() { client = old }()
	m, _ := fixture(t)
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "update-manifest", UpdateURL: url + "/manifest", Bin: "bin/tool", BinDir: "bin"}
	if _, e := m.Apply(context.Background(), "install", "tool"); e != nil {
		t.Fatal(e)
	}
	digest = strings.Repeat("0", 128)
	if _, e := m.Apply(context.Background(), "upgrade", "tool"); e == nil {
		t.Fatal("accepted bad SHA512")
	}
}

func TestPublicReleaseFallbackWithoutAPICredentials(t *testing.T) {
	payload := zipBytes(t, "bin/tool", []byte("native"))
	h := sha256.Sum256(payload)
	sum := hex.EncodeToString(h[:])
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api":
			w.WriteHeader(403)
		case strings.HasSuffix(r.URL.Path, "/latest"):
			http.Redirect(w, r, "/test/repo/releases/tag/v1.2.3", 302)
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			fmt.Fprintf(w, "%s  bundle.zip\n", sum)
		case strings.HasSuffix(r.URL.Path, "/bundle.zip"):
			w.Write(payload)
		case strings.Contains(r.URL.Path, "/releases/tag/"):
			w.Write([]byte("release"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	old := client
	client = server.Client()
	tr := client.Transport
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		u := *copy.URL
		originalHost := u.Host
		u.Scheme = "https"
		u.Host = strings.TrimPrefix(server.URL, "https://")
		if originalHost == "api.github.com" {
			u.Path = "/api"
		}
		copy.URL = &u
		return tr.RoundTrip(copy)
	})
	defer func() { client = old }()
	m, _ := fixture(t)
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "release", Repository: "test/repo", Asset: "bundle.zip", Checksums: "SHA256SUMS"}
	if _, e := m.Apply(context.Background(), "install", "tool"); e != nil {
		t.Fatal(e)
	}
}
