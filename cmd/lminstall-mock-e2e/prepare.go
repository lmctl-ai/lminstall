package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Prepare a checksummed released API server and compile its published fixture.
// The private compiler is a CI prerequisite, never installed globally.
func prepare(root, compiler string) error {
	if !filepath.IsAbs(root) || compiler == "" {
		return errors.New("absolute_prepare_dir_and_compiler_required")
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
	const origin = "https://github.com/lmctl-ai/lmmock/releases/download/v0.1.94/"
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
	for _, name := range []string{asset, "examples.tar.gz"} {
		b, e := download(origin + name)
		if e != nil {
			return e
		}
		h := sha256.Sum256(b)
		if hashes[name] == "" || !strings.EqualFold(hex.EncodeToString(h[:]), hashes[name]) {
			return errors.New("fixture_checksum_mismatch")
		}
		if e = os.WriteFile(filepath.Join(root, name), b, 0700); e != nil {
			return e
		}
	}
	archive, e := os.Open(filepath.Join(root, "examples.tar.gz"))
	if e != nil {
		return e
	}
	defer archive.Close()
	zip, e := gzip.NewReader(archive)
	if e != nil {
		return e
	}
	defer zip.Close()
	tarball := tar.NewReader(zip)
	source := ""
	var schema []byte
	for {
		h, e := tarball.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if strings.TrimPrefix(h.Name, "./") == "examples/schema.json" && h.Typeflag == tar.TypeReg {
			schema, e = io.ReadAll(tarball)
			if e != nil {
				return e
			}
		}
		if strings.TrimPrefix(h.Name, "./") == "examples/llm-api.lms" && h.Typeflag == tar.TypeReg {
			b, e := io.ReadAll(tarball)
			if e != nil {
				return e
			}
			source = string(b)
		}
	}
	if len(schema) == 0 {
		return errors.New("published_schema_missing")
	}
	if e = os.WriteFile(filepath.Join(root, "schema.json"), schema, 0600); e != nil {
		return e
	}
	if source == "" {
		return errors.New("published_fixture_missing")
	}
	for _, anchor := range []string{`if (request.has("tools") && !tool_result)`, `if (b.has("tools") && !tool_result)`} {
		if !strings.Contains(source, anchor) {
			return errors.New("fixture_policy_anchor_missing")
		}
		source = strings.ReplaceAll(source, anchor, "if (false)")
	}
	input := filepath.Join(root, "no-tools.lms")
	program := filepath.Join(root, "no-tools.dslb")
	if e = os.WriteFile(input, []byte(source), 0600); e != nil {
		return e
	}
	cmd := exec.Command("node", compiler, "--no-preflight", input, program)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if e = cmd.Run(); e != nil {
		return fmt.Errorf("fixture_compile_failed: %w", e)
	}
	b, _ := json.Marshal(settings{Binary: filepath.Join(root, asset), Program: program})
	return os.WriteFile(filepath.Join(root, "settings.json"), b, 0600)
}
func download(raw string) ([]byte, error) {
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("https_required")
		}
		return nil
	}}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" {
		return nil, errors.New("https_required")
	}
	r, e := client.Get(raw)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("fixture_http_%d", r.StatusCode)
	}
	return io.ReadAll(r.Body)
}
