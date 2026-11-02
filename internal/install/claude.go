package install

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const claudeReleaseBase = "https://downloads.claude.ai/claude-code-releases"

// claude reproduces the official install.sh / install.ps1 download steps:
// read the latest version, read that version's manifest, download the platform
// binary and require its manifest SHA-256 (and size). The vendor's final
// `claude install` step only writes a launcher and shell integration under HOME,
// which lminstall replaces with an owned, versioned payload plus PATH launcher.
func (m *Manager) claude(ctx context.Context, p Provider, r Recipe, dir string) (string, string, string, error) {
	base := strings.TrimSuffix(r.UpdateURL, "/")
	if base == "" {
		base = claudeReleaseBase
	}
	b, e := getBytes(ctx, base+"/latest")
	if e != nil {
		return "", "", "", e
	}
	version := strings.TrimSpace(string(b))
	if !releaseVersion.MatchString(version) {
		return "", "", "", fmt.Errorf("invalid Claude Code release version %q (service unreachable or unavailable in your region?)", version)
	}
	arch := m.expand("{assetarch}", dir)
	platform := m.OS + "-" + arch
	exe := "claude"
	switch m.OS {
	case "windows":
		platform, exe = "win32-"+arch, "claude.exe"
	case "linux":
		if muslHost() && m.OS == hostOS() {
			platform += "-musl"
		}
	}
	var manifest struct {
		Platforms map[string]struct {
			Checksum string `json:"checksum"`
			Size     int64  `json:"size"`
		} `json:"platforms"`
	}
	if e = getJSON(ctx, base+"/"+url.PathEscape(version)+"/manifest.json", &manifest); e != nil {
		return "", "", "", e
	}
	asset, ok := manifest.Platforms[platform]
	if !ok || !sha256Hex.MatchString(asset.Checksum) {
		return "", "", "", fmt.Errorf("platform %s not found in Claude Code manifest %s", platform, version)
	}
	rel := filepath.Join("versions", version, exe)
	dest, e := relative(dir, rel)
	if e != nil {
		return "", "", "", e
	}
	if e = os.MkdirAll(filepath.Dir(dest), 0o700); e != nil {
		return "", "", "", e
	}
	if e = download(ctx, base+"/"+url.PathEscape(version)+"/"+platform+"/"+exe, dest, "sha256:"+asset.Checksum); e != nil {
		os.Remove(dest)
		return "", "", "", e
	}
	if st, err := os.Stat(dest); err != nil || (asset.Size > 0 && st.Size() != asset.Size) {
		os.Remove(dest)
		return "", "", "", fmt.Errorf("Claude Code binary size does not match its manifest")
	}
	if e = os.Chmod(dest, 0o755); e != nil {
		return "", "", "", e
	}
	verified, e := m.verifiedVersion(ctx, p, r, filepath.ToSlash(rel), version)
	if e != nil {
		return "", "", "", e
	}
	return filepath.ToSlash(rel), "bin", verified, nil
}
