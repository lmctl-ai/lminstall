//go:build windows

package install

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

type heldCandidateRunner struct{ t *testing.T }

func (r heldCandidateRunner) Run(_ context.Context, args []string, _ string, _ map[string]string, _ bool) (string, error) {
	b, err := os.ReadFile(args[0])
	if err != nil {
		return "", err
	}
	name, err := syscall.UTF16PtrFromString(args[0])
	if err != nil {
		return "", err
	}
	// Model an image/scanner handle which outlives --version. Reads are allowed,
	// but renaming that candidate must fail. Only this test owns the handle.
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return "", err
	}
	r.t.Cleanup(func() { syscall.CloseHandle(handle) })
	if err := os.Rename(args[0], args[0]+".legacy-move"); err == nil {
		r.t.Fatal("legacy rename control did not encounter the held candidate")
	}
	return string(b), nil
}
func TestVerifiedWindowsCandidateCanPublishWhileItsPathIsHeld(t *testing.T) {
	data := []byte("tool 1.2.3")
	hash := sha512.Sum512(data)
	var base string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/binary" {
			w.Write(data)
		} else {
			fmt.Fprintf(w, `{"version":"1.2.3","url":%q,"sha512":%q}`, base+"/binary", hex.EncodeToString(hash[:]))
		}
	}))
	defer server.Close()
	base = server.URL
	old := client
	client = server.Client()
	defer func() { client = old }()
	m, _ := fixture(t)
	m.Runner = heldCandidateRunner{t: t}
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "update-manifest", UpdateURL: base + "/manifest", Bin: "bin/tool", BinDir: "bin"}
	result, err := m.Apply(context.Background(), "install", "tool")
	if err != nil {
		t.Fatal("verified candidate could not publish without renaming its held image", err)
	}
	b, err := os.ReadFile(filepath.Join(m.dir("tool"), "bin/tool"))
	if err != nil || string(b) != string(data) || result.State != "installed" {
		t.Fatalf("published bytes/state differ: %+v %q %v", result, b, err)
	}
}
