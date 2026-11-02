package install

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// The same signed/checksummed artifact source exposed by an official installer,
// without executing its machine-wide PATH/service setup handoff.
func (m *Manager) updateManifest(ctx context.Context, p Provider, r Recipe, dir string) (string, string, string, error) {
	url := m.expand(r.UpdateURL, dir)
	req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		return "", "", "", e
	}
	resp, e := secureDo(req)
	if e != nil {
		return "", "", "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	var meta struct{ Version, URL, SHA512 string }
	if e = json.NewDecoder(resp.Body).Decode(&meta); e != nil {
		return "", "", "", e
	}
	if meta.Version == "" || meta.URL == "" || len(meta.SHA512) != 128 {
		return "", "", "", fmt.Errorf("invalid official update manifest %s", url)
	}
	bin := m.expand(r.Bin, dir)
	dest, e := relative(dir, bin)
	if e != nil {
		return "", "", "", e
	}
	// Keep the installed pathname untouched until the downloaded executable has
	// passed both integrity and selected-version validation.
	stage, e := os.MkdirTemp(dir, "native-stage-")
	if e != nil {
		return "", "", "", e
	}
	defer os.RemoveAll(stage)
	candidate := filepath.Join(stage, filepath.Base(dest))
	if strings.Contains(meta.URL, ".tar.gz") {
		archive := filepath.Join(stage, "native.tar.gz")
		if e = download(ctx, meta.URL, archive, "sha512:"+meta.SHA512); e != nil {
			return "", "", "", e
		}
		extracted := filepath.Join(stage, "extracted")
		if e = extractArchive(archive, extracted); e != nil {
			return "", "", "", e
		}
		source, err := relative(extracted, r.ArchiveBinary)
		if err != nil {
			return "", "", "", err
		}
		if e = copyExecutable(source, candidate); e != nil {
			return "", "", "", e
		}
	} else {
		if e = download(ctx, meta.URL, candidate, "sha512:"+meta.SHA512); e != nil {
			return "", "", "", e
		}
		if e = os.Chmod(candidate, 0755); e != nil {
			return "", "", "", e
		}
	}
	stagedBin, e := filepath.Rel(dir, candidate)
	if e != nil {
		return "", "", "", e
	}
	verified, e := m.verifiedVersion(ctx, p, r, stagedBin, meta.Version)
	if e != nil {
		return "", "", "", e
	}
	if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
		return "", "", "", e
	}
	if e = os.Rename(candidate, dest); e != nil {
		return "", "", "", e
	}
	return bin, m.expand(r.BinDir, dir), verified, nil
}
