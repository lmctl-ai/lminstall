package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Prepare a checksummed released lmmock. The mock API server is built into the
// executable (--builtin llm-api), so no script compiler or Node is needed.
func prepare(root, version string) error {
	if !filepath.IsAbs(root) {
		return errors.New("absolute_prepare_dir_required")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return e
	}
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	platform := map[string]string{"linux": "linux", "darwin": "macos", "windows": "windows"}[runtime.GOOS]
	if arch == "" || platform == "" {
		return errors.New("unsupported_fixture_platform")
	}
	asset := "lmmock-" + platform + "-" + arch
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	if version == "" || version == "latest" {
		tag, e := latestTag("lmctl-ai/lmmock")
		if e != nil {
			return e
		}
		version = tag
	}
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(version) {
		return errors.New("invalid_lmmock_version")
	}
	origin := "https://github.com/lmctl-ai/lmmock/releases/download/" + version + "/"
	sums, e := download(origin + "SHA256SUMS")
	if e != nil {
		return e
	}
	hashes := map[string]string{}
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			hashes[strings.TrimPrefix(f[1], "*")] = f[0]
		}
	}
	b, e := download(origin + asset)
	if e != nil {
		return e
	}
	h := sha256.Sum256(b)
	if hashes[asset] == "" || !strings.EqualFold(hex.EncodeToString(h[:]), hashes[asset]) {
		return errors.New("fixture_checksum_mismatch")
	}
	if e = os.WriteFile(filepath.Join(root, asset), b, 0700); e != nil {
		return e
	}
	out, _ := json.Marshal(settings{Binary: filepath.Join(root, asset), Version: version})
	fmt.Println(string(out))
	return os.WriteFile(filepath.Join(root, "settings.json"), out, 0600)
}

func httpClient() *http.Client {
	return &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("https_required")
		}
		return nil
	}}
}

// latestTag follows the public /releases/latest redirect (no API quota or token).
func latestTag(repo string) (string, error) {
	r, e := httpClient().Head("https://github.com/" + repo + "/releases/latest")
	if e != nil {
		return "", e
	}
	r.Body.Close()
	prefix := "/" + repo + "/releases/tag/"
	if r.StatusCode != 200 || !strings.HasPrefix(r.Request.URL.Path, prefix) {
		return "", fmt.Errorf("latest_release_lookup_failed: %s", r.Status)
	}
	return strings.TrimPrefix(r.Request.URL.Path, prefix), nil
}

func download(raw string) ([]byte, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" {
		return nil, errors.New("https_required")
	}
	r, e := httpClient().Get(raw)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("fixture_http_%d", r.StatusCode)
	}
	return io.ReadAll(r.Body)
}
