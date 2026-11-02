package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func reviewTar(name string, kind byte) []byte {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	w := tar.NewWriter(gz)
	h := &tar.Header{Name: name, Mode: 0755, Typeflag: kind}
	if kind == tar.TypeReg {
		h.Size = 4
	}
	if kind == tar.TypeSymlink {
		h.Linkname = "../outside"
	}
	w.WriteHeader(h)
	if kind == tar.TypeReg {
		w.Write([]byte("data"))
	}
	w.Close()
	gz.Close()
	return b.Bytes()
}
func TestWP137BadArchives(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    []byte
		ext  string
	}{{"tar-traversal", reviewTar("../escape", tar.TypeReg), ".tar.gz"}, {"tar-link", reviewTar("link", tar.TypeSymlink), ".tar.gz"}, {"zip-truncated", []byte("PK\003\004truncated"), ".zip"}, {"tar-truncated", reviewTar("tool", tar.TypeReg)[:20], ".tar.gz"}} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "x"+tc.ext)
			os.WriteFile(p, tc.b, 0600)
			if e := extractArchive(p, t.TempDir()); e == nil {
				t.Fatal("accepted malformed archive")
			}
		})
	}
}
func TestWP137TruncatedGzipFooter(t *testing.T) {
	b := reviewTar("tool", tar.TypeReg)
	p := filepath.Join(t.TempDir(), "x.tar.gz")
	os.WriteFile(p, b[:len(b)-8], 0600)
	if e := extractArchive(p, t.TempDir()); e == nil {
		t.Fatal("TRUNCATED_GZIP_FOOTER_ACCEPTED")
	}
}
func TestWP137PreexistingSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	reviewSymlink(t, outside, filepath.Join(root, "link"))
	p := filepath.Join(t.TempDir(), "x.zip")
	os.WriteFile(p, zipBytes(t, "link/escaped", []byte("data")), 0600)
	e := extractArchive(p, root)
	_, st := os.Stat(filepath.Join(outside, "escaped"))
	if e == nil && st == nil {
		t.Fatal("PREEXISTING_SYMLINK_ESCAPED_EXTRACTION_ROOT")
	}
}
func TestWP137UninstallDoesNotFollowSymlink(t *testing.T) {
	m, _ := fixture(t)
	d := m.dir("tool")
	os.MkdirAll(d, 0700)
	os.WriteFile(filepath.Join(d, ".lminstall-owner"), []byte("tool\n"), 0600)
	outside := t.TempDir()
	keep := filepath.Join(outside, "keep")
	os.WriteFile(keep, []byte("keep"), 0600)
	reviewSymlink(t, outside, filepath.Join(d, "link"))
	if _, e := m.Apply(context.Background(), "uninstall", "tool"); e != nil {
		t.Fatal(e)
	}
	if b, e := os.ReadFile(keep); e != nil || string(b) != "keep" {
		t.Fatal("UNINSTALL_DELETED_EXTERNAL_DATA", e)
	}
}
func TestWP137AgyRejectedUpgradePreservesOld(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX version fixture; portable raw/tar state tests cover Windows")
	}
	payload := []byte("#!/bin/sh\nprintf 'tool 1.2.3\\n'\n")
	version := "1.2.3"
	var base string
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/binary" {
			w.Write(payload)
			return
		}
		h := sha512.Sum512(payload)
		fmt.Fprintf(w, `{"version":%q,"url":%q,"sha512":%q}`, version, base+"/binary", hex.EncodeToString(h[:]))
	}))
	defer s.Close()
	base = s.URL
	previous := client
	client = s.Client()
	defer func() { client = previous }()
	m, _ := fixture(t)
	m.Runner = ExecRunner{Log: io.Discard}
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "update-manifest", UpdateURL: base + "/manifest", Bin: "bin/tool", BinDir: "bin"}
	if _, e := m.Apply(context.Background(), "install", "tool"); e != nil {
		t.Fatal(e)
	}
	old, _ := os.ReadFile(filepath.Join(m.dir("tool"), "bin/tool"))
	version = "2.0.0"
	payload = []byte("#!/bin/sh\nprintf 'tool 99.99.99\\n'\n")
	if _, e := m.Apply(context.Background(), "upgrade", "tool"); e == nil {
		t.Fatal("wrong version accepted")
	}
	state, e := m.Status(context.Background(), "tool")
	t.Logf("after rejected upgrade: state=%s version=%s err=%v", state.State, state.Version, e)
	now, _ := os.ReadFile(filepath.Join(m.dir("tool"), "bin/tool"))
	if !bytes.Equal(old, now) {
		t.Fatal("REJECTED_AGY_UPGRADE_REPLACED_WORKING_BINARY")
	}
}

func TestWP137HermesPortableGitOfficialMirror(t *testing.T) {
	previous := client
	mirror := false
	client = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "hermes-assets.nousresearch.com" {
			mirror = true
		}
		return &http.Response{StatusCode: 403, Status: "403 Forbidden", Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil)), Request: r}, nil
	})}
	defer func() { client = previous }()
	m, _ := fixture(t)
	m.OS = "windows"
	_, _ = m.hermesGit(context.Background(), t.TempDir(), map[string]string{})
	if !mirror {
		t.Fatal("MISSING_OFFICIAL_PORTABLE_GIT_AVAILABILITY_MIRROR")
	}
}

func TestWP137ZipSymlinkRefused(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "link"}
	h.SetMode(os.ModeSymlink | 0755)
	w, _ := z.CreateHeader(h)
	w.Write([]byte("../outside"))
	z.Close()
	p := filepath.Join(t.TempDir(), "x.zip")
	os.WriteFile(p, b.Bytes(), 0600)
	if e := extractArchive(p, t.TempDir()); e == nil {
		t.Fatal("ZIP_SYMLINK_ACCEPTED")
	}
}
func TestWP137HTTPNeverRequested(t *testing.T) {
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer s.Close()
	if e := download(context.Background(), s.URL, filepath.Join(t.TempDir(), "payload"), ""); e == nil || requests != 0 {
		t.Fatalf("HTTP request allowed: requests=%d error=%v", requests, e)
	}
}
func TestWP137TopProviderSymlinkRefused(t *testing.T) {
	m, _ := fixture(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, ".lminstall-owner"), []byte("tool\n"), 0600)
	keep := filepath.Join(outside, "keep")
	os.WriteFile(keep, []byte("keep"), 0600)
	os.MkdirAll(filepath.Dir(m.dir("tool")), 0700)
	reviewSymlink(t, outside, m.dir("tool"))
	if _, e := m.Apply(context.Background(), "uninstall", "tool"); e == nil {
		t.Fatal("provider-directory symlink accepted")
	}
	if _, e := os.Stat(keep); e != nil {
		t.Fatal("external data changed", e)
	}
}

func reviewSymlink(t *testing.T, target, link string) {
	t.Helper()
	if e := os.Symlink(target, link); e != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink privilege unavailable: %v", e)
		}
		t.Fatal(e)
	}
}
