package install

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Zero deadlines: caller cancellation is the only application-imposed bound.
var client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true}}

func download(ctx context.Context, url, dest, digest string) error {
	if !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("download requires HTTPS: %s", url)
	}
	req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		return e
	}
	req.Header.Set("User-Agent", "lminstall")
	resp, e := client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
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
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, hash), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if digest != "" {
		want := strings.TrimPrefix(digest, "sha256:")
		if len(want) != 64 || !strings.EqualFold(want, hex.EncodeToString(hash.Sum(nil))) {
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
	write := func(name string, mode os.FileMode, reader io.Reader) error {
		p, e := archivePath(dest, name)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			return e
		}
		f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm()&0755)
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
				if e = os.MkdirAll(p, 0755); e != nil {
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
			return nil
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
			if e = os.MkdirAll(p, 0755); e != nil {
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

type releaseInfo struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name, URL, Digest string
		Download          string `json:"browser_download_url"`
	} `json:"assets"`
}

func (m *Manager) release(ctx context.Context, p Provider, r Recipe, dir string) (string, string, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/"+r.Repository+"/releases/latest", nil)
	if e != nil {
		return "", "", e
	}
	req.Header.Set("User-Agent", "lminstall")
	resp, e := client.Do(req)
	if e != nil {
		return "", "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("official release lookup for %s: %s", r.Repository, resp.Status)
	}
	var release releaseInfo
	if e = json.NewDecoder(resp.Body).Decode(&release); e != nil {
		return "", "", e
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
	archive := filepath.Join(dir, name)
	if e = download(ctx, url, archive, digest); e != nil {
		return "", "", e
	}
	stage, e := os.MkdirTemp(dir, "payload-stage-")
	if e != nil {
		return "", "", e
	}
	defer os.RemoveAll(stage)
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
	inside, e := filepath.Rel(stage, matches[0])
	if e != nil {
		return "", "", e
	}
	payload := filepath.Join(dir, "payload")
	backup, e := os.MkdirTemp(dir, "payload-old-")
	if e != nil {
		return "", "", e
	}
	os.Remove(backup)
	old := false
	if _, e = os.Stat(payload); e == nil {
		if e = os.Rename(payload, backup); e != nil {
			return "", "", fmt.Errorf("close the provider before upgrading: %w", e)
		}
		old = true
	}
	if e = os.Rename(stage, payload); e != nil {
		if old {
			os.Rename(backup, payload)
		}
		return "", "", e
	}
	if old {
		if e = os.RemoveAll(backup); e != nil {
			return "", "", fmt.Errorf("close the old provider and remove its owned backup %s: %w", backup, e)
		}
	}
	rel := filepath.Join("payload", inside)
	return rel, filepath.Dir(rel), nil
}
