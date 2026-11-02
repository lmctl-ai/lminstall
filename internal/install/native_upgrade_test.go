package install

import (
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
	"strings"
	"testing"
)

// Read the actual candidate path. Returning a preselected fake version would
// miss validation accidentally performed against the old installed pathname.
type candidateVersionRunner struct{ calls int }

func (r *candidateVersionRunner) Run(_ context.Context, args []string, _ string, _ map[string]string, _ bool) (string, error) {
	r.calls++
	b, err := os.ReadFile(args[0])
	return string(b), err
}

func TestNativeRejectedUpgradeKeepsBinaryAndReceipt(t *testing.T) {
	for _, kind := range []string{"raw", "tar", "kimi", "muse", "release"} {
		t.Run(kind, func(t *testing.T) {
			version := "1.2.3"
			data := []byte("tool 1.2.3\n")
			var base string
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sha := sha256.Sum256(data)
				digest := hex.EncodeToString(sha[:])
				packed := kimiTarFixture(t, data)
				switch {
				case r.URL.Path == "/latest":
					fmt.Fprint(w, version)
				case strings.HasSuffix(r.URL.Path, "/manifest.json"):
					fmt.Fprintf(w, `{"version":%q,"platforms":{"linux-x64":{"filename":"native","checksum":%q}}}`, version, digest)
				case r.URL.Path == "/channel":
					fmt.Fprintf(w, `{"channel":"muse-stable","version":%q,"manifest_url":%q,"urgency":"none","notification_text":""}`, version, base+"/release")
				case r.URL.Path == "/release":
					fmt.Fprintf(w, `{"version":%q,"checksum_algorithm":"sha256","artifacts":{"x86_linux":{"url":%q,"checksum":%q,"size":%d}}}`, version, base+"/binary", digest, len(data))
				case r.URL.Path == "/update":
					url := base + "/binary"
					payload := data
					if kind == "tar" {
						url = base + "/payload.tar.gz"
						payload = packed
					}
					h := sha512.Sum512(payload)
					fmt.Fprintf(w, `{"version":%q,"url":%q,"sha512":%q}`, version, url, hex.EncodeToString(h[:]))
				case strings.Contains(r.URL.Path, "releases/latest"):
					payload := zipBytes(t, "bundle/tool", data)
					h := sha256.Sum256(payload)
					fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":"tool.zip","digest":%q,"browser_download_url":%q}]}`, "v"+version, "sha256:"+hex.EncodeToString(h[:]), base+"/payload.zip")
				case strings.HasSuffix(r.URL.Path, ".tar.gz"):
					w.Write(packed)
				case r.URL.Path == "/payload.zip":
					w.Write(zipBytes(t, "bundle/tool", data))
				default:
					w.Write(data)
				}
			}))
			defer server.Close()
			base = server.URL
			previous := client
			client = server.Client()
			transport := client.Transport
			client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				req := r.Clone(r.Context())
				u := *req.URL
				u.Host = strings.TrimPrefix(base, "https://")
				req.URL = &u
				return transport.RoundTrip(req)
			})
			defer func() { client = previous }()
			m, _ := fixture(t)
			runner := &candidateVersionRunner{}
			m.Runner = runner
			recipe := Recipe{Channel: "update-manifest", UpdateURL: base + "/update", Bin: "bin/tool", BinDir: "bin", ArchiveBinary: "kimi"}
			if kind == "kimi" {
				recipe.Channel = "kimi"
				recipe.UpdateURL = base
			}
			if kind == "muse" {
				recipe.Channel = "muse"
				recipe.UpdateURL = base + "/channel"
			}
			if kind == "release" {
				recipe.Channel = "release"
				recipe.Repository = "fixture/tool"
				recipe.Asset = "tool.zip"
				recipe.ArchiveBinary = ""
			}
			m.Manifest.Providers[0].Recipes["*"] = recipe
			first, err := m.Apply(context.Background(), "install", "tool")
			if err != nil {
				t.Fatal(err)
			}
			if runner.calls != 1 {
				t.Fatalf("verified candidate was executed %d times before installation completed", runner.calls)
			}
			before, err := os.ReadFile(first.Binary)
			if err != nil {
				t.Fatal(err)
			}
			receiptPath := filepath.Join(m.dir("tool"), "receipt.json")
			receiptBefore, _ := os.ReadFile(receiptPath)
			statBefore, _ := os.Stat(receiptPath)
			version = "2.0.0"
			data = []byte("tool 99.99.99\n")
			if _, err = m.Apply(context.Background(), "upgrade", "tool"); err == nil || !strings.Contains(err.Error(), "version mismatch") {
				t.Fatalf("wrong candidate not rejected by version check: %v", err)
			}
			after, _ := os.ReadFile(first.Binary)
			receiptAfter, _ := os.ReadFile(receiptPath)
			statAfter, _ := os.Stat(receiptPath)
			if !bytes.Equal(before, after) {
				t.Fatal("rejected candidate changed previous binary")
			}
			if !bytes.Equal(receiptBefore, receiptAfter) || !statBefore.ModTime().Equal(statAfter.ModTime()) {
				t.Fatal("rejected candidate changed previous receipt")
			}
			status, err := m.Status(context.Background(), "tool")
			if err != nil || status.State != "installed" || status.Version != "tool 1.2.3" {
				t.Fatalf("previous installation not usable: %+v %v", status, err)
			}
			data = []byte("tool 2.0.0\n")
			next, err := m.Apply(context.Background(), "upgrade", "tool")
			if err != nil || next.Version != "tool 2.0.0" {
				t.Fatalf("valid upgrade did not publish: %+v %v", next, err)
			}
		})
	}
}
