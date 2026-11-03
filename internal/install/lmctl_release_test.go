package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLmctlAggregateRequiresBothEntrypointsAndSafeVersion(t *testing.T) {
	root := t.TempDir()
	if err := validateLmctlTree(root, "0.2.38-github.1.1"); err == nil {
		t.Fatal("accepted absent aggregate")
	}
	for name, body := range map[string]string{"package.json": `{"version":"0.2.38-github.1.1","bin":{"lmctl":"bin/lmctl","lmscript":"bin/lmscript"}}`, "bin/lmctl": "one", "bin/lmscript": "two", "dist/dsl.js": "library", "dist/lmscript.js": "library", "dist/cli/index.js": "cli", "dist/cli/lmscript.js": "runner", "dist/cli/schema.sql": "schema"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(path), 0700)
		os.WriteFile(path, []byte(body), 0600)
	}
	if err := validateLmctlTree(root, "0.2.38-github.1.1"); err != nil {
		t.Fatal(err)
	}
	if err := validateLmctlTree(root, "0.2.38-github.2.1"); err == nil {
		t.Fatal("accepted wrong version")
	}
	os.Remove(filepath.Join(root, "bin/lmscript"))
	if err := validateLmctlTree(root, "0.2.38-github.1.1"); err == nil {
		t.Fatal("accepted missing second entrypoint")
	}
}

func TestLmctlPrivateReleaseInstallAndRerun(t *testing.T) {
	version := "0.2.38-github.123.1"
	files := map[string]string{"package/package.json": `{"version":"` + version + `","bin":{"lmctl":"bin/lmctl","lmscript":"bin/lmscript"}}`, "package/bin/lmctl": "console.log('lmctl " + version + "')", "package/bin/lmscript": "console.log('lmscript " + version + "')", "package/dist/dsl.js": "library", "package/dist/lmscript.js": "library", "package/dist/cli/index.js": "cli", "package/dist/cli/lmscript.js": "runner", "package/dist/cli/schema.sql": "schema"}
	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	meta := []byte(`{"version":"` + version + `","repository":"lmctlhq/lmctl-mono","sha":"` + strings.Repeat("a", 40) + `","asset":"lmctl-` + version + `.tgz"}`)
	digest := func(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
	sums := []byte(digest(packed.Bytes()) + "  lmctl-" + version + ".tgz\n" + digest(meta) + "  release.json\n")
	payloads := map[string][]byte{"1": packed.Bytes(), "2": meta, "3": sums}
	assets := []LmctlAsset{{1, "lmctl-" + version + ".tgz", "sha256:" + digest(packed.Bytes())}, {2, "release.json", "sha256:" + digest(meta)}, {3, "SHA256SUMS", "sha256:" + digest(sums)}}
	archiveGets := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic" {
			t.Error("private request omitted token")
		}
		if strings.HasPrefix(r.URL.Path, "/releases/assets/") {
			id := strings.TrimPrefix(r.URL.Path, "/releases/assets/")
			if id == "1" {
				archiveGets++
			}
			w.Write(payloads[id])
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 7, "tag_name": "v" + version, "assets": assets})
	}))
	defer server.Close()
	saved := client
	client = server.Client()
	transport := client.Transport
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = "https"
		u.Host = strings.TrimPrefix(server.URL, "https://")
		u.Path = strings.TrimPrefix(u.Path, "/repos/lmctlhq/lmctl-mono")
		copy.URL = &u
		return transport.RoundTrip(copy)
	})
	defer func() { client = saved }()
	t.Setenv("LMCTL_RELEASE_TOKEN", "synthetic")
	root := t.TempDir()
	first, err := InstallLmctl(context.Background(), root, "latest", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := InstallLmctl(context.Background(), root, "latest", "")
	if err != nil {
		t.Fatal(err)
	}
	if first != second || archiveGets != 1 {
		t.Fatalf("rerun duplicated payload: %+v %+v gets=%d", first, second, archiveGets)
	}
	unmanaged := t.TempDir()
	userFile := filepath.Join(unmanaged, "lmctl", "bin", "lmctl")
	os.MkdirAll(filepath.Dir(userFile), 0700)
	os.WriteFile(userFile, []byte("user-owned"), 0600)
	if _, err = InstallLmctl(context.Background(), unmanaged, "latest", ""); err == nil {
		t.Fatal("accepted unmanaged prefix")
	}
	if b, _ := os.ReadFile(userFile); string(b) != "user-owned" {
		t.Fatal("overwrote unmanaged launcher")
	}
	if _, err = InstallLmctl(context.Background(), t.TempDir()+"\noutput=injection", "latest", ""); err == nil || !strings.Contains(err.Error(), "single-line") {
		t.Fatal("accepted multi-line Action prefix")
	}
	os.WriteFile(filepath.Join(first.PackageRoot, "dist/cli/schema.sql"), []byte("corrupt"), 0600)
	if _, err = InstallLmctl(context.Background(), root, "latest", ""); err == nil {
		t.Fatal("accepted corrupt installed sibling")
	}
}

func TestLmctlVersionProbeDoesNotExposeReleaseToken(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "bin"), 0700)
	for _, name := range []string{"lmctl", "lmscript"} {
		source := "if(process.env.LMCTL_RELEASE_TOKEN)process.exit(9);console.log('" + name + " 0.2.38-github.1.1')"
		os.WriteFile(filepath.Join(root, "bin", name), []byte(source), 0600)
	}
	t.Setenv("LMCTL_RELEASE_TOKEN", "synthetic-token")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	if err = lmctlCheckVersions(context.Background(), node, root, "0.2.38-github.1.1"); err != nil {
		t.Fatal(err)
	}
}
