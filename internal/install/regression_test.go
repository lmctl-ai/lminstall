package install

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReviewReleaseMissingDigestMustNotInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX script fixture; Windows launcher covered by native tests")
	}
	payload := zipBytes(t, "bin/tool", []byte("#!/bin/sh\nprintf 'tool 1.0.0\\n'\n"))
	checksums := 0
	var base string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			fmt.Fprintf(w, `{"tag_name":"v1.0.0","assets":[{"name":"bundle.zip","browser_download_url":%q,"digest":null},{"name":"SHA256SUMS","browser_download_url":%q}]}`, base+"/asset", base+"/SHA256SUMS")
		case "/asset":
			w.Write(payload)
		case "/SHA256SUMS":
			checksums++
			fmt.Fprintf(w, "%s  bundle.zip\n", strings.Repeat("0", 64))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base = server.URL
	old := client
	client = server.Client()
	tr := client.Transport
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		if copy.URL.Host == "api.github.com" {
			u := *copy.URL
			u.Host = strings.TrimPrefix(base, "https://")
			u.Path = "/release"
			copy.URL = &u
		}
		return tr.RoundTrip(copy)
	})
	defer func() { client = old }()
	m, _ := fixture(t)
	m.Runner = ExecRunner{Log: io.Discard}
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "release", Repository: "test/repo", Asset: "bundle.zip", Checksums: "SHA256SUMS"}
	result, err := m.Apply(context.Background(), "install", "tool")
	t.Logf("result=%+v error=%v checksum_requests=%d", result, err, checksums)
	if err == nil || checksums != 1 || !strings.Contains(err.Error(), "checksum") {
		t.Fatal("release must fetch and enforce its declared checksum", err, checksums)
	}
}
func TestReviewHTTPSRedirectMustNotDowngrade(t *testing.T) {
	httpHits := 0
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { httpHits++; w.Write([]byte("plaintext payload")) }))
	defer plain.Close()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/payload", http.StatusFound)
	}))
	defer tls.Close()
	old := client
	client = tls.Client()
	defer func() { client = old }()
	dest := filepath.Join(t.TempDir(), "payload")
	err := download(context.Background(), tls.URL+"/redirect", dest, "")
	data, _ := os.ReadFile(dest)
	t.Logf("error=%v http_hits=%d downloaded=%q", err, httpHits, data)
	if err == nil || httpHits != 0 || len(data) != 0 {
		t.Fatal("HTTPS downgrade must be refused before plaintext request or publication", err, httpHits)
	}
}
func TestReviewAdvertisedVersionMustMatchBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX script fixture; Windows launcher covered by native tests")
	}
	var base string
	payload := []byte("#!/bin/sh\nprintf 'tool 99.99.99\\n'\n")
	h := sha512.Sum512(payload)
	digest := hex.EncodeToString(h[:])
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/binary" {
			w.Write(payload)
			return
		}
		fmt.Fprintf(w, `{"version":"1.2.3","url":%q,"sha512":%q}`, base+"/binary", digest)
	}))
	defer s.Close()
	base = s.URL
	old := client
	client = s.Client()
	defer func() { client = old }()
	m, _ := fixture(t)
	m.Runner = ExecRunner{Log: io.Discard}
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "update-manifest", UpdateURL: base + "/manifest", Bin: "bin/tool", BinDir: "bin"}
	result, err := m.Apply(context.Background(), "install", "tool")
	t.Logf("advertised=1.2.3 result=%+v error=%v", result, err)
	if err == nil {
		t.Fatal("BUG: advertised version 1.2.3 accepted binary 99.99.99")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "version") {
		t.Fatalf("fixture error before version assertion: %v", err)
	}
}

func TestReviewMissingStableLauncherMustRepair(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX script fixture; Windows launcher covered by native tests")
	}
	payload := zipBytes(t, "bin/tool", []byte("#!/bin/sh\nprintf 'tool 1.0.0\\n'\n"))
	h := sha256.Sum256(payload)
	digest := "sha256:" + hex.EncodeToString(h[:])
	var base string
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/asset" {
			w.Write(payload)
			return
		}
		fmt.Fprintf(w, `{"tag_name":"v1.0.0","assets":[{"name":"bundle.zip","browser_download_url":%q,"digest":%q}]}`, base+"/asset", digest)
	}))
	defer s.Close()
	base = s.URL
	old := client
	client = s.Client()
	tr := client.Transport
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		if copy.URL.Host == "api.github.com" {
			u := *copy.URL
			u.Host = strings.TrimPrefix(base, "https://")
			u.Path = "/release"
			copy.URL = &u
		}
		return tr.RoundTrip(copy)
	})
	defer func() { client = old }()
	m, _ := fixture(t)
	m.Runner = ExecRunner{Log: io.Discard}
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "release", Repository: "test/repo", Asset: "bundle.zip"}
	first, err := m.Apply(context.Background(), "install", "tool")
	if err != nil {
		t.Fatal("fixture", err)
	}
	launcher := filepath.Join(first.BinDir, "tool")
	if err = os.Remove(launcher); err != nil {
		t.Fatal(err)
	}
	status, err := m.Status(context.Background(), "tool")
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := m.Apply(context.Background(), "install", "tool")
	if err != nil {
		t.Fatal(err)
	}
	_, missing := os.Stat(launcher)
	t.Logf("status=%s repeat=%s launcher_error=%v", status.State, repeated.State, missing)
	if missing != nil {
		t.Fatal("BUG: repeat install reports installed but fails to repair missing stable PATH launcher")
	}
}

func TestDownloadedStepsRequireChecksum(t *testing.T) {
	manifest, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range manifest.Providers {
		for osName, recipe := range p.Recipes {
			for _, step := range recipe.Steps {
				if step.URL != "" {
					// The JSON assertion also catches missing digest fields before Step gains one.
					b, _ := json.Marshal(step)
					var fields map[string]any
					json.Unmarshal(b, &fields)
					if fields["digest"] == nil || fields["digest"] == "" {
						t.Errorf("%s/%s: downloaded installer must have a reviewed digest", p.ID, osName)
					}
				}
			}
		}
	}
	// A custom manifest must not reintroduce an unchecked downloaded installer.
	m, _ := fixture(t)
	recipe := m.Manifest.Providers[0].Recipes["*"]
	recipe.Steps = []Step{{URL: "https://example.invalid/install.sh", File: "install.sh"}}
	m.Manifest.Providers[0].Recipes["*"] = recipe
	if Validate(m.Manifest) == nil {
		t.Error("accepted downloaded step without integrity")
	}
}

func TestReleaseDigestFallbackCases(t *testing.T) {
	for _, tc := range []struct {
		name, field, sums string
		declared, ok      bool
	}{
		{"missing-valid", "", "valid", true, true},
		{"version-mismatch", "", "valid", true, false},
		{"null-valid", `,"digest":null`, "valid", true, true},
		{"empty-valid", `,"digest":""`, "valid", true, true},
		{"null-wrong", `,"digest":null`, "wrong", true, false},
		{"missing-no-fallback", "", "valid", false, false},
		{"missing-entry", "", "absent", true, false},
		{"malformed-checksum", "", "malformed", true, false},
		{"conflicting-checksum", "", "conflict", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, f := fixture(t)
			if tc.name == "version-mismatch" {
				f.output = "tool 99.99.99"
			}
			payload := zipBytes(t, "bin/tool", []byte("fixture binary"))
			hash := sha256.Sum256(payload)
			digest := hex.EncodeToString(hash[:])
			var base string
			checksumHits, assetHits := 0, 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/release":
					fmt.Fprintf(w, `{"tag_name":"v1.2.3","assets":[{"name":"bundle.zip","browser_download_url":%q%s},{"name":"SHA256SUMS","browser_download_url":%q}]}`, base+"/asset", tc.field, base+"/sums")
				case "/asset":
					assetHits++
					w.Write(payload)
				case "/sums":
					checksumHits++
					switch tc.sums {
					case "valid":
						fmt.Fprintf(w, "%s  bundle.zip\n", digest)
					case "wrong":
						fmt.Fprintf(w, "%s  bundle.zip\n", strings.Repeat("0", 64))
					case "absent":
						fmt.Fprintf(w, "%s  another.zip\n", digest)
					case "malformed":
						fmt.Fprint(w, "not-a-hash  bundle.zip\n")
					case "conflict":
						fmt.Fprintf(w, "%s  bundle.zip\n%s  bundle.zip\n", digest, strings.Repeat("0", 64))
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			base = server.URL
			old := client
			client = server.Client()
			tr := client.Transport
			client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				copy := r.Clone(r.Context())
				if copy.URL.Host == "api.github.com" {
					u := *copy.URL
					u.Host = strings.TrimPrefix(base, "https://")
					u.Path = "/release"
					copy.URL = &u
				}
				return tr.RoundTrip(copy)
			})
			defer func() { client = old }()
			recipe := Recipe{Channel: "release", Repository: "test/repo", Asset: "bundle.zip"}
			if tc.declared {
				recipe.Checksums = "SHA256SUMS"
			}
			m.Manifest.Providers[0].Recipes["*"] = recipe
			result, err := m.Apply(context.Background(), "install", "tool")
			if tc.ok {
				if err != nil || result.State != "installed" || checksumHits != 1 || assetHits != 1 {
					t.Fatalf("%+v %v checksum=%d asset=%d", result, err, checksumHits, assetHits)
				}
			} else {
				if err == nil || result.State == "installed" || (tc.name != "version-mismatch" && len(f.calls) != 0) {
					t.Fatalf("unchecked code executed: %+v %v calls=%v", result, err, f.calls)
				}
			}
		})
	}
}

func TestSelectedVersionMatchesCompleteToken(t *testing.T) {
	m, f := fixture(t)
	for _, tc := range []struct {
		want, out string
		ok        bool
	}{
		{"v1.2.3", "tool 1.2.3", true}, {"1.2.3", "tool v1.2.3 (build abc)", true},
		{"v1.2.3-beta.1", "tool 1.2.3-beta.1", true},
		{"1.2.3", "tool 11.2.3", false}, {"1.2.3", "tool 1.2.30", false},
		{"1.2.3", "tool 1.2.3-beta", false}, {"1.2.3", "tool 99.99.99", false},
	} {
		f.output = tc.out
		err := m.verifySelectedVersion(context.Background(), m.Manifest.Providers[0], Recipe{}, "bin/tool", tc.want)
		if (err == nil) != tc.ok {
			t.Errorf("%q vs %q: %v", tc.want, tc.out, err)
		}
	}
}

func TestDownloadedStepChecksumBeforeExecution(t *testing.T) {
	data := []byte("private fixture installer")
	hash := sha256.Sum256(data)
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer s.Close()
	old := client
	client = s.Client()
	defer func() { client = old }()
	for _, valid := range []bool{false, true} {
		m, f := fixture(t)
		digest := "sha256:" + strings.Repeat("0", 64)
		if valid {
			digest = "sha256:" + hex.EncodeToString(hash[:])
		}
		recipe := m.Manifest.Providers[0].Recipes["*"]
		recipe.Steps = []Step{{URL: s.URL, File: "install.sh", Digest: digest}, {Args: []string{"fixture-installer"}}}
		m.Manifest.Providers[0].Recipes["*"] = recipe
		result, err := m.Apply(context.Background(), "install", "tool")
		if valid {
			if err != nil || result.State != "installed" {
				t.Fatalf("valid script: %+v %v", result, err)
			}
		} else if err == nil || len(f.calls) != 0 {
			t.Fatalf("unverified script executed: %+v %v calls=%v", result, err, f.calls)
		}
	}
}
