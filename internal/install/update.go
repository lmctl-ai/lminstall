package install

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// The same signed/checksummed artifact source exposed by an official installer,
// without executing its machine-wide PATH/service setup handoff.
func (m *Manager) updateManifest(ctx context.Context, p Provider, r Recipe, dir string) (string, string, error) {
	url := m.expand(r.UpdateURL, dir)
	req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		return "", "", e
	}
	resp, e := secureDo(req)
	if e != nil {
		return "", "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	var meta struct{ Version, URL, SHA512 string }
	if e = json.NewDecoder(resp.Body).Decode(&meta); e != nil {
		return "", "", e
	}
	if meta.Version == "" || meta.URL == "" || len(meta.SHA512) != 128 {
		return "", "", fmt.Errorf("invalid official update manifest %s", url)
	}
	bin := m.expand(r.Bin, dir)
	dest, e := relative(dir, bin)
	if e != nil {
		return "", "", e
	}
	if strings.Contains(meta.URL, ".tar.gz") {
		archive := filepath.Join(dir, "native.tar.gz")
		if e = download(ctx, meta.URL, archive, "sha512:"+meta.SHA512); e != nil {
			return "", "", e
		}
		defer os.Remove(archive)
		stage, e := os.MkdirTemp(dir, "native-")
		if e != nil {
			return "", "", e
		}
		defer os.RemoveAll(stage)
		if e = extractArchive(archive, stage); e != nil {
			return "", "", e
		}
		source, e := relative(stage, r.ArchiveBinary)
		if e != nil {
			return "", "", e
		}
		in, e := os.Open(source)
		if e != nil {
			return "", "", e
		}
		defer in.Close()
		if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			return "", "", e
		}
		out, e := os.CreateTemp(filepath.Dir(dest), ".native-")
		if e != nil {
			return "", "", e
		}
		tmp := out.Name()
		defer os.Remove(tmp)
		_, e = io.Copy(out, in)
		ce := out.Close()
		if e != nil {
			return "", "", e
		}
		if ce != nil {
			return "", "", ce
		}
		if e = os.Chmod(tmp, 0755); e != nil {
			return "", "", e
		}
		if e = os.Rename(tmp, dest); e != nil {
			return "", "", e
		}
	} else {
		if e = download(ctx, meta.URL, dest, "sha512:"+meta.SHA512); e != nil {
			return "", "", e
		}
		if e = os.Chmod(dest, 0755); e != nil {
			return "", "", e
		}
	}
	if e = m.verifySelectedVersion(ctx, p, r, bin, meta.Version); e != nil {
		return "", "", e
	}
	return bin, m.expand(r.BinDir, dir), nil
}
