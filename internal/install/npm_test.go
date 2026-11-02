package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestSemverRanges(t *testing.T) {
	cases := []struct {
		rng, ver string
		want     bool
	}{
		{"^1.2.3", "1.9.0", true}, {"^1.2.3", "2.0.0", false}, {"^0.2.3", "0.2.9", true}, {"^0.2.3", "0.3.0", false},
		{"~1.2.3", "1.2.9", true}, {"~1.2.3", "1.3.0", false}, {">=1.0.0 <2.0.0", "1.5.0", true}, {">=1.0.0 <2.0.0", "2.0.0", false},
		{"1.x", "1.4.0", true}, {"1.2", "1.3.0", false}, {"*", "9.9.9", true}, {"1.0.0 - 2.0.0", "1.5.0", true},
		{"^1.0.0 || ^3.0.0", "3.1.0", true}, {"^1.0.0 || ^3.0.0", "2.1.0", false}, {"^1.0.0", "1.1.0-beta.1", false},
		{"^1.1.0-beta.1", "1.1.0-beta.2", true}, {">= 1.0.0", "1.0.0", true}, {"<1.0.0", "0.9.9", true},
	}
	for _, c := range cases {
		r, e := parseSemverRange(c.rng)
		if e != nil {
			t.Fatalf("%s: %v", c.rng, e)
		}
		v, ok := parseSemver(c.ver)
		if !ok {
			t.Fatal(c.ver)
		}
		if got := r.satisfies(v); got != c.want {
			t.Errorf("%s satisfies %s: got %v want %v", c.ver, c.rng, got, c.want)
		}
	}
}

func npmTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: "package/" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

type fakePkg struct {
	meta  map[string]any
	files map[string]string
}

// fakeRegistry serves packuments and tarballs over TLS and routes the shared
// HTTP client to it. corrupt names a package whose tarball differs from its integrity.
func fakeRegistry(t *testing.T, pkgs map[string]map[string]fakePkg, tags map[string]string, corrupt string) {
	t.Helper()
	var base string
	tarballs := map[string][]byte{}
	var mu sync.Mutex // the client resolves and downloads concurrently
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if b, ok := tarballs[r.URL.Path]; ok {
			w.Write(b)
			return
		}
		name := strings.Replace(strings.TrimPrefix(r.URL.Path, "/"), "%2f", "/", 1)
		versions, ok := pkgs[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		doc := map[string]any{"name": name, "dist-tags": map[string]string{"latest": tags[name]}, "versions": map[string]any{}}
		for ver, p := range versions {
			body := npmTarball(t, p.files)
			sum := sha512.Sum512(body)
			path := "/tarballs/" + strings.ReplaceAll(name, "/", "_") + "-" + ver + ".tgz"
			if name == corrupt {
				body = append([]byte{}, body...)
				tarballs[path] = npmTarball(t, map[string]string{"evil.js": "x"})
			} else {
				tarballs[path] = body
			}
			meta := map[string]any{"name": name, "version": ver, "dist": map[string]string{"tarball": base + path, "integrity": "sha512-" + base64.StdEncoding.EncodeToString(sum[:])}}
			for k, v := range p.meta {
				meta[k] = v
			}
			doc["versions"].(map[string]any)[ver] = meta
		}
		json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(server.Close)
	base = server.URL
	old := client
	client = server.Client()
	t.Cleanup(func() { client = old })
	t.Setenv("LMINSTALL_NPM_REGISTRY", base)
}

func TestNativeNPMInstallResolvesPlatformDependenciesWithoutTools(t *testing.T) {
	fakeRegistry(t, map[string]map[string]fakePkg{
		"@scope/tool": {"1.2.3": {meta: map[string]any{
			"bin":                  map[string]string{"tool": "bin/tool.js"},
			"dependencies":         map[string]string{"dep": "^1.0.0", "alias": "npm:real@^2.0.0"},
			"optionalDependencies": map[string]string{"tool-linux-x64": "1.2.3", "tool-win32-x64": "1.2.3", "tool-missing": "^1"},
		}, files: map[string]string{"bin/tool.js": "#!/usr/bin/env node\n", "package.json": "{}"}}},
		"dep":            {"1.0.0": {files: map[string]string{"index.js": "old"}}, "1.4.0": {files: map[string]string{"index.js": "new"}}, "2.0.0": {files: map[string]string{"index.js": "major"}}},
		"real":           {"2.1.0": {files: map[string]string{"index.js": "real"}}},
		"tool-linux-x64": {"1.2.3": {meta: map[string]any{"os": []string{"linux"}, "cpu": []string{"x64"}}, files: map[string]string{"tool": "native"}}},
		"tool-win32-x64": {"1.2.3": {meta: map[string]any{"os": []string{"win32"}, "cpu": []string{"x64"}}, files: map[string]string{"tool.exe": "native"}}},
	}, map[string]string{"@scope/tool": "1.2.3", "dep": "2.0.0", "real": "2.1.0", "tool-linux-x64": "1.2.3", "tool-win32-x64": "1.2.3"}, "")
	m, f := fixture(t)
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "npm", Package: "@scope/tool@latest", Bin: "npm/bin/tool", BinDir: "npm/bin"}
	res, e := m.Apply(context.Background(), "install", "tool")
	if e != nil {
		t.Fatal(e)
	}
	for _, call := range f.calls {
		if len(call) != 2 || call[1] != "--version" {
			t.Fatalf("installer started a process other than the version check: %v", call)
		}
	}
	nm := filepath.Join(m.dir("tool"), "npm", "lib", "node_modules")
	read := func(rel string) string {
		b, e := os.ReadFile(filepath.Join(nm, filepath.FromSlash(rel)))
		if e != nil {
			t.Fatalf("%s: %v", rel, e)
		}
		return string(b)
	}
	if read("dep/index.js") != "new" {
		t.Error("^1.0.0 should pick 1.4.0, not the 2.0.0 latest tag")
	}
	if read("alias/index.js") != "real" {
		t.Error("npm: alias not installed under its alias name")
	}
	if read("tool-linux-x64/tool") != "native" {
		t.Error("matching optional platform package missing")
	}
	if _, e = os.Stat(filepath.Join(nm, "tool-win32-x64")); !os.IsNotExist(e) {
		t.Error("foreign platform package installed")
	}
	if runtime.GOOS != "windows" {
		target, e := os.Readlink(filepath.Join(res.BinDir, "tool"))
		if e != nil || !strings.HasSuffix(filepath.ToSlash(target), "lib/node_modules/@scope/tool/bin/tool.js") {
			t.Fatalf("bin link: %q %v", target, e)
		}
		st, _ := os.Stat(filepath.Join(res.BinDir, "tool"))
		if st.Mode()&0o111 == 0 {
			t.Error("bin target is not executable")
		}
	}
	if entries, _ := filepath.Glob(filepath.Join(m.dir("tool"), "npm-stage-*")); len(entries) != 0 {
		t.Errorf("staging directory left behind: %v", entries)
	}
}

func TestNativeNPMRejectsIntegrityMismatch(t *testing.T) {
	fakeRegistry(t, map[string]map[string]fakePkg{
		"tool": {"1.0.0": {meta: map[string]any{"bin": "cli.js"}, files: map[string]string{"cli.js": "ok"}}},
	}, map[string]string{"tool": "1.0.0"}, "tool")
	m, f := fixture(t)
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "npm", Package: "tool@latest", Bin: "npm/bin/tool", BinDir: "npm/bin"}
	if _, e := m.Apply(context.Background(), "install", "tool"); e == nil || !strings.Contains(e.Error(), "checksum mismatch") {
		t.Fatalf("tampered tarball accepted: %v", e)
	}
	if len(f.calls) != 0 {
		t.Fatalf("unverified package code ran: %v", f.calls)
	}
}

func TestNativeNPMRefusesTraversal(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "package/../../escape", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	file := filepath.Join(dir, "p.tgz")
	os.WriteFile(file, buf.Bytes(), 0o600)
	if e := extractNPMTarball(file, filepath.Join(dir, "out")); e == nil {
		t.Fatal("path traversal accepted")
	}
}

// The product must never start package managers or other host tools to install.
func TestInstallerSourceStartsNoHostTools(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, banned := range []string{`"npm"`, `"git"`, `"node"`, `"uv"`, `"sh"`, `"bash"`, `"curl"`, `"tar"`, `"unzip"`} {
			if strings.Contains(string(b), "exec.Command("+banned) || strings.Contains(string(b), "exec.CommandContext(ctx, "+banned) {
				t.Errorf("%s shells out to %s", f, banned)
			}
		}
	}
	_ = fmt.Sprint
}

func TestClaudeNativeChannelVerifiesManifestChecksum(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			binary := []byte("claude native binary")
			sum := sha512.Sum512(binary) // not sha256: replaced below
			_ = sum
			h := sha256Sum(binary)
			if bad {
				h = strings.Repeat("0", 64)
			}
			var base string
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/latest":
					fmt.Fprint(w, "9.8.7\n")
				case strings.HasSuffix(r.URL.Path, "/manifest.json"):
					fmt.Fprintf(w, `{"platforms":{"linux-x64":{"checksum":%q,"size":%d}}}`, h, len(binary))
				default:
					w.Write(binary)
				}
			}))
			defer s.Close()
			base = s.URL
			old := client
			client = s.Client()
			defer func() { client = old }()
			m, f := fixture(t)
			f.output = "9.8.7 (Claude Code)\n"
			m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "claude", UpdateURL: base, BinDir: "bin"}
			res, e := m.Apply(context.Background(), "install", "tool")
			if bad {
				if e == nil || len(f.calls) != 0 {
					t.Fatalf("bad checksum accepted: %v %v", e, f.calls)
				}
				return
			}
			if e != nil || res.Version != "9.8.7 (Claude Code)" {
				t.Fatalf("%+v %v", res, e)
			}
			if _, e = os.Stat(filepath.Join(m.dir("tool"), "versions", "9.8.7", "claude")); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func sha256Sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// A monorepo's "latest" release may belong to another component (qwen-code
// publishes an SDK). tag_pattern must select the newest release of the product.
func TestReleaseTagPatternSkipsUnrelatedLatest(t *testing.T) {
	data := []byte("tool 2.0.0\n")
	payload := zipBytes(t, "bundle/tool", data)
	sum := sha256Sum(payload)
	var base string
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			fmt.Fprint(w, `{"tag_name":"sdk-typescript-v0.1.27","assets":[]}`)
		case strings.HasSuffix(r.URL.Path, "/releases"):
			fmt.Fprintf(w, `[{"tag_name":"sdk-typescript-v0.1.27","assets":[]},{"tag_name":"v2.1.0-preview.1","prerelease":true,"assets":[]},{"tag_name":"v2.0.0","assets":[{"name":"tool-2.0.0.zip","digest":"sha256:%s","browser_download_url":%q}]}]`, sum, base+"/payload.zip")
		default:
			w.Write(payload)
		}
	}))
	defer s.Close()
	base = s.URL
	old := client
	client = s.Client()
	tr := client.Transport
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		c := r.Clone(r.Context())
		u := *c.URL
		u.Host = strings.TrimPrefix(base, "https://")
		c.URL = &u
		return tr.RoundTrip(c)
	})
	defer func() { client = old }()
	m, f := fixture(t)
	f.output = "tool 2.0.0\n"
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "release", Repository: "fixture/tool", Asset: "tool-{version}.zip", TagPattern: `^v[0-9]+\.[0-9]+\.[0-9]+$`}
	res, e := m.Apply(context.Background(), "install", "tool")
	if e != nil || res.State != "installed" {
		t.Fatalf("%+v %v", res, e)
	}
}
