package install

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Zero deadlines: caller cancellation is the only application-imposed bound.
var client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true}}

// Copy the client so fixture transports and callers retain their configuration.
// Check each hop before making the redirected request, including metadata lookups.
func secureDo(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return nil, fmt.Errorf("download requires HTTPS: %s", req.URL)
	}
	c := *client
	previous := c.CheckRedirect
	c.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if next.URL.Scheme != "https" {
			return fmt.Errorf("redirect requires HTTPS: %s", next.URL)
		}
		if previous != nil {
			return previous(next, via)
		}
		return nil
	}
	return c.Do(req)
}

func download(ctx context.Context, url, dest, digest string) error {
	if !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("download requires HTTPS: %s", url)
	}
	req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		return e
	}
	req.Header.Set("User-Agent", "lminstall")
	resp, e := secureDo(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &downloadStatusError{URL: url, Status: resp.Status, Code: resp.StatusCode}
	}
	if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(dest), ".download-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	var hash hash.Hash = sha256.New()
	if strings.HasPrefix(digest, "sha512:") {
		hash = sha512.New()
	}
	_, copyErr := io.Copy(io.MultiWriter(f, hash), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if digest != "" {
		want := strings.TrimPrefix(strings.TrimPrefix(digest, "sha256:"), "sha512:")
		if len(want) != len(hash.Sum(nil))*2 || !strings.EqualFold(want, hex.EncodeToString(hash.Sum(nil))) {
			return fmt.Errorf("checksum mismatch for %s", url)
		}
	}
	return os.Rename(name, dest)
}
func archivePath(root, name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	for _, p := range strings.Split(name, "/") {
		if p == ".." {
			return "", fmt.Errorf("archive path escapes root: %s", name)
		}
	}
	if strings.HasPrefix(name, "/") || strings.Contains(strings.Split(name, "/")[0], ":") {
		return "", fmt.Errorf("absolute archive path: %s", name)
	}
	return relative(root, name)
}
func extractArchive(archive, dest string) error {
	if e := os.MkdirAll(dest, 0700); e != nil {
		return e
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	mkdir := func(name string) error {
		part := ""
		for _, segment := range strings.Split(filepath.Clean(name), string(filepath.Separator)) {
			part = filepath.Join(part, segment)
			if err := root.Mkdir(part, 0755); err != nil && !os.IsExist(err) {
				return err
			}
		}
		return nil
	}
	write := func(name string, mode os.FileMode, reader io.Reader) error {
		p, e := archivePath(dest, name)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(dest, p)
		if e != nil {
			return e
		}
		if e = mkdir(filepath.Dir(rel)); e != nil {
			return e
		}
		f, e := root.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm()&0755)
		if e != nil {
			return e
		}
		_, ce := io.Copy(f, reader)
		closeErr := f.Close()
		if ce != nil {
			return ce
		}
		return closeErr
	}
	if strings.HasSuffix(archive, ".zip") {
		z, e := zip.OpenReader(archive)
		if e != nil {
			return e
		}
		defer z.Close()
		for _, f := range z.File {
			p, e := archivePath(dest, f.Name)
			if e != nil {
				return e
			}
			if f.FileInfo().IsDir() {
				rel, err := filepath.Rel(dest, p)
				if err != nil {
					return err
				}
				if e = mkdir(rel); e != nil {
					return e
				}
				continue
			}
			if !f.Mode().IsRegular() {
				return fmt.Errorf("archive links/devices are refused: %s", f.Name)
			}
			r, e := f.Open()
			if e != nil {
				return e
			}
			e = write(f.Name, f.Mode(), r)
			r.Close()
			if e != nil {
				return e
			}
		}
		return nil
	}
	file, e := os.Open(archive)
	if e != nil {
		return e
	}
	defer file.Close()
	gz, e := gzip.NewReader(file)
	if e != nil {
		return e
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			// tar EOF precedes the gzip checksum/length trailer. Drain it before
			// accepting the container, including concatenated gzip members.
			_, e = io.Copy(io.Discard, gz)
			return e
		}
		if e != nil {
			return e
		}
		p, e := archivePath(dest, h.Name)
		if e != nil {
			return e
		}
		switch h.Typeflag {
		case tar.TypeDir:
			rel, err := filepath.Rel(dest, p)
			if err != nil {
				return err
			}
			if e = mkdir(rel); e != nil {
				return e
			}
		case tar.TypeReg, tar.TypeRegA:
			if e = write(h.Name, os.FileMode(h.Mode), tr); e != nil {
				return e
			}
		case tar.TypeXGlobalHeader:
			continue
		default:
			return fmt.Errorf("archive links/devices are refused: %s", h.Name)
		}
	}
}

type releaseAsset struct {
	Name     string `json:"name"`
	Digest   string `json:"digest"`
	Download string `json:"browser_download_url"`
}
type releaseInfo struct {
	Tag    string         `json:"tag_name"`
	Assets []releaseAsset `json:"assets"`
}

// GitHub's public release redirects/assets do not share the anonymous API quota.
// Fallback still verifies the publisher's checksum file; no credentials or retries.
func (m *Manager) publicRelease(ctx context.Context, r Recipe, dir string) (releaseInfo, error) {
	var info releaseInfo
	if r.Checksums == "" {
		return info, fmt.Errorf("API unavailable and no official checksum asset declared for %s", r.Repository)
	}
	req, e := http.NewRequestWithContext(ctx, "HEAD", "https://github.com/"+r.Repository+"/releases/latest", nil)
	if e != nil {
		return info, e
	}
	resp, e := secureDo(req)
	if e != nil {
		return info, e
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return info, fmt.Errorf("public release redirect: %s", resp.Status)
	}
	prefix := "/" + r.Repository + "/releases/tag/"
	if !strings.HasPrefix(resp.Request.URL.Path, prefix) {
		return info, fmt.Errorf("unexpected official release redirect %s", resp.Request.URL)
	}
	info.Tag = strings.TrimPrefix(resp.Request.URL.Path, prefix)
	if info.Tag == "" {
		return info, fmt.Errorf("empty release tag")
	}
	name := m.expand(strings.ReplaceAll(r.Asset, "{version}", strings.TrimPrefix(info.Tag, "v")), dir)
	base := "https://github.com/" + r.Repository + "/releases/download/" + url.PathEscape(info.Tag) + "/"
	req, e = http.NewRequestWithContext(ctx, "GET", base+url.PathEscape(r.Checksums), nil)
	if e != nil {
		return info, e
	}
	resp, e = secureDo(req)
	if e != nil {
		return info, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return info, fmt.Errorf("official checksum asset: %s", resp.Status)
	}
	body, e := io.ReadAll(resp.Body)
	if e != nil {
		return info, e
	}
	digest, e := checksumFor(body, name)
	if e != nil {
		return info, e
	}
	info.Assets = []releaseAsset{{Name: name, Digest: "sha256:" + digest, Download: base + url.PathEscape(name)}}
	return info, nil
}
func checksumFor(body []byte, name string) (string, error) {
	digest := ""
	for _, line := range strings.Split(string(body), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 && strings.TrimPrefix(strings.Join(parts[1:], " "), "*") == name {
			if digest != "" && digest != parts[0] {
				return "", fmt.Errorf("ambiguous checksum for %s", name)
			}
			digest = parts[0]
		}
	}
	if digest == "" {
		return "", fmt.Errorf("official checksum file has no %s", name)
	}
	return digest, nil
}

func releaseChecksum(ctx context.Context, info releaseInfo, filename, name string) (string, error) {
	for _, asset := range info.Assets {
		if asset.Name != filename {
			continue
		}
		req, e := http.NewRequestWithContext(ctx, "GET", asset.Download, nil)
		if e != nil {
			return "", e
		}
		resp, e := secureDo(req)
		if e != nil {
			return "", e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("official checksum asset: %s", resp.Status)
		}
		body, e := io.ReadAll(resp.Body)
		if e != nil {
			return "", e
		}
		digest, e := checksumFor(body, name)
		if e != nil {
			return "", e
		}
		return "sha256:" + digest, nil
	}
	return "", fmt.Errorf("release %s has no checksum asset %s", info.Tag, filename)
}

func (m *Manager) release(ctx context.Context, p Provider, r Recipe, dir string) (string, string, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/"+r.Repository+"/releases/latest", nil)
	if e != nil {
		return "", "", e
	}
	req.Header.Set("User-Agent", "lminstall")
	resp, e := secureDo(req)
	if e != nil {
		return "", "", e
	}
	defer resp.Body.Close()
	var release releaseInfo
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		release, e = m.publicRelease(ctx, r, dir)
		if e != nil {
			return "", "", e
		}
	} else {
		if resp.StatusCode != 200 {
			return "", "", fmt.Errorf("official release lookup for %s: %s", r.Repository, resp.Status)
		}
		if e = json.NewDecoder(resp.Body).Decode(&release); e != nil {
			return "", "", e
		}
	}
	name := m.expand(strings.ReplaceAll(r.Asset, "{version}", strings.TrimPrefix(release.Tag, "v")), dir)
	url, digest := "", ""
	for _, a := range release.Assets {
		if a.Name == name {
			url = a.Download
			digest = a.Digest
			break
		}
	}
	if url == "" {
		return "", "", fmt.Errorf("official release %s has no %s asset", release.Tag, name)
	}
	if digest == "" {
		if r.Checksums == "" {
			return "", "", fmt.Errorf("release %s has no digest or declared checksum asset", release.Tag)
		}
		digest, e = releaseChecksum(ctx, release, r.Checksums, name)
		if e != nil {
			return "", "", e
		}
	}
	archive := filepath.Join(dir, name)
	if e = download(ctx, url, archive, digest); e != nil {
		return "", "", e
	}
	stage, e := os.MkdirTemp(dir, "payload-stage-")
	if e != nil {
		return "", "", e
	}
	// Successful versioned payloads remain owned until uninstall.
	// Keep bundle-relative resources alongside the executable, under a stable PATH directory.
	if e = extractArchive(archive, stage); e != nil {
		os.RemoveAll(stage)
		return "", "", e
	}
	os.Remove(archive)
	binary := p.Binary
	if m.OS == "windows" {
		binary += ".exe"
	}
	var matches []string
	e = filepath.WalkDir(stage, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == binary {
			matches = append(matches, path)
		}
		return nil
	})
	if e != nil {
		return "", "", e
	}
	if len(matches) != 1 {
		return "", "", fmt.Errorf("release bundle must contain one %s, found %d", binary, len(matches))
	}
	if e = os.Chmod(matches[0], 0755); e != nil {
		return "", "", e
	}
	// A versioned payload never renames or overwrites a running executable.
	// Publishing the stable launcher happens only after successful --version.
	rel, e := filepath.Rel(dir, matches[0])
	if e != nil {
		return "", "", e
	}
	if e = m.verifySelectedVersion(ctx, p, r, rel, release.Tag); e != nil {
		return "", "", e
	}
	return rel, "bin", nil
}

// Mirrors may replace an unavailable transport, never failed integrity, local
// filesystem permissions, TLS verification or an HTTPS policy rejection.
type downloadStatusError struct {
	URL, Status string
	Code        int
}

func (e *downloadStatusError) Error() string { return fmt.Sprintf("GET %s: %s", e.URL, e.Status) }
func downloadAvailabilityError(err error) bool {
	var status *downloadStatusError
	if errors.As(err, &status) {
		return true
	}
	var certificate *tls.CertificateVerificationError
	if errors.As(err, &certificate) {
		return false
	}
	var dns *net.DNSError
	var op *net.OpError
	return errors.As(err, &dns) || errors.As(err, &op) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
