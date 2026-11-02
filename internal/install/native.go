package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var sha256Hex = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)

func getBytes(ctx context.Context, location string) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", location, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "lminstall")
	resp, e := secureDo(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: %s (no authentication attempted)", location, resp.Status)
	}
	return io.ReadAll(resp.Body)
}
func getJSON(ctx context.Context, location string, value any) error {
	b, e := getBytes(ctx, location)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, value)
}
func muslHost() bool {
	for _, p := range []string{"/lib/libc.musl-x86_64.so.1", "/lib/libc.musl-aarch64.so.1", "/lib/ld-musl-x86_64.so.1", "/lib/ld-musl-aarch64.so.1"} {
		if _, e := os.Stat(p); e == nil {
			return true
		}
	}
	return false
}

// The official Kimi installer permits a bare-binary download as an alternative
// to its tarball. Both are verified against this same binary checksum.
func (m *Manager) kimi(ctx context.Context, p Provider, r Recipe, dir string) (string, string, string, error) {
	if m.OS == "linux" && muslHost() {
		return "", "", "", fmt.Errorf("Kimi native builds do not support musl; official alternative: npm install -g @moonshot-ai/kimi-code")
	}
	base := strings.TrimSuffix(r.UpdateURL, "/")
	b, e := getBytes(ctx, base+"/latest")
	if e != nil {
		return "", "", "", e
	}
	version := strings.TrimSpace(string(b))
	if !releaseVersion.MatchString(version) {
		return "", "", "", fmt.Errorf("invalid Kimi release version %q", version)
	}
	var manifest struct {
		Version   string
		Platforms map[string]struct{ Filename, Checksum string }
	}
	if e = getJSON(ctx, base+"/binaries/"+url.PathEscape(version)+"/manifest.json", &manifest); e != nil {
		return "", "", "", e
	}
	osName := m.OS
	if m.OS == "windows" {
		osName = "win32"
	}
	target := osName + "-" + m.expand("{assetarch}", dir)
	asset, ok := manifest.Platforms[target]
	if !ok || manifest.Version != version || !sha256Hex.MatchString(asset.Checksum) || asset.Filename == "" || strings.ContainsAny(asset.Filename, "/\\") {
		return "", "", "", fmt.Errorf("invalid Kimi manifest for %s", target)
	}
	bin := m.expand(r.Bin, dir)
	dest, e := relative(dir, bin)
	if e != nil {
		return "", "", "", e
	}
	stage, e := os.MkdirTemp(dir, "kimi-stage-")
	if e != nil {
		return "", "", "", e
	}
	defer os.RemoveAll(stage)
	candidate := filepath.Join(stage, filepath.Base(dest))
	candidate, e = fetchKimiPayload(ctx, base, version, target, asset.Filename, asset.Checksum, stage, candidate)
	if e != nil {
		return "", "", "", e
	}
	if e = os.Chmod(candidate, 0755); e != nil {
		return "", "", "", e
	}
	relativeCandidate, e := filepath.Rel(dir, candidate)
	if e != nil {
		return "", "", "", e
	}
	verified, e := m.verifiedVersion(ctx, p, r, relativeCandidate, version)
	if e != nil {
		return "", "", "", e
	}
	if _, err := os.Stat(dest); err == nil {
		if e = copyExecutable(dest, dest+".bak"); e != nil {
			return "", "", "", e
		}
	}
	if e = os.Rename(candidate, dest); e != nil {
		return "", "", "", e
	}
	region := filepath.Join(dir, "home", ".kimi-code", "region")
	if e = os.MkdirAll(filepath.Dir(region), 0700); e != nil {
		return "", "", "", e
	}
	f, e := os.OpenFile(region, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e == nil {
		_, e = f.WriteString("mainland-cn\n")
		f.Close()
	} else if os.IsExist(e) {
		e = nil
	}
	return bin, m.expand(r.BinDir, dir), verified, e
}

// Muse's shell/PowerShell launcher selects a channel, then verifies the native
// artifact's declared SHA-256 and exact size. No launcher code or login is needed
// for a public artifact; a private/restricted release is an explicit error.
func (m *Manager) muse(ctx context.Context, p Provider, r Recipe, dir string) (string, string, string, error) {
	var channel struct {
		Channel, Version, ManifestURL, Urgency string
		NotificationText                       *string
	}
	// Explicit tags mirror the publisher's underscore-separated keys.
	var raw struct {
		Channel          string  `json:"channel"`
		Version          string  `json:"version"`
		ManifestURL      string  `json:"manifest_url"`
		Urgency          string  `json:"urgency"`
		NotificationText *string `json:"notification_text"`
	}
	if e := getJSON(ctx, r.UpdateURL, &raw); e != nil {
		return "", "", "", e
	}
	channel.Channel, channel.Version, channel.ManifestURL, channel.Urgency, channel.NotificationText = raw.Channel, raw.Version, raw.ManifestURL, raw.Urgency, raw.NotificationText
	if channel.Channel != "muse-stable" || !releaseVersion.MatchString(channel.Version) || channel.ManifestURL == "" || channel.NotificationText == nil || (channel.Urgency != "none" && channel.Urgency != "encouraged" && channel.Urgency != "must_download") {
		return "", "", "", fmt.Errorf("invalid Muse channel manifest")
	}
	var release struct {
		Version   string
		Algorithm string `json:"checksum_algorithm"`
		Artifacts map[string]struct {
			URL, Checksum string
			Size          int64
		}
	}
	if e := getJSON(ctx, channel.ManifestURL, &release); e != nil {
		return "", "", "", e
	}
	arch := "x86"
	if m.Arch == "arm64" {
		arch = "aarch64"
	}
	osName := m.OS
	if osName == "darwin" {
		osName = "macos"
	}
	asset, ok := release.Artifacts[arch+"_"+osName]
	if !ok || release.Version != channel.Version || release.Algorithm != "sha256" || !sha256Hex.MatchString(asset.Checksum) || asset.Size <= 0 {
		return "", "", "", fmt.Errorf("invalid Muse release manifest for %s_%s", arch, osName)
	}
	bin := m.expand(r.Bin, dir)
	dest, e := relative(dir, bin)
	if e != nil {
		return "", "", "", e
	}
	// Stage outside PATH so both integrity checks finish before publishing bytes.
	stage, e := os.MkdirTemp(dir, "muse-stage-")
	if e != nil {
		return "", "", "", e
	}
	defer os.RemoveAll(stage)
	candidate := filepath.Join(stage, filepath.Base(dest))
	if e = download(ctx, asset.URL, candidate, "sha256:"+asset.Checksum); e != nil {
		return "", "", "", e
	}
	stat, e := os.Stat(candidate)
	if e != nil {
		return "", "", "", e
	}
	if stat.Size() != asset.Size {
		return "", "", "", fmt.Errorf("Muse artifact size mismatch: got %d expected %d", stat.Size(), asset.Size)
	}
	if e = os.Chmod(candidate, 0755); e != nil {
		return "", "", "", e
	}
	relativeCandidate, e := filepath.Rel(dir, candidate)
	if e != nil {
		return "", "", "", e
	}
	verified, e := m.verifiedVersion(ctx, p, r, relativeCandidate, channel.Version)
	if e != nil {
		return "", "", "", e
	}
	if e = os.Rename(candidate, dest); e != nil {
		return "", "", "", e
	}
	return bin, m.expand(r.BinDir, dir), verified, nil
}

// Match the official archive-first order. The script authenticates the extracted
// binary, not the compressed container. Only that verified binary is published.
func fetchKimiPayload(ctx context.Context, base, version, target, filename, checksum, stage, raw string) (string, error) {
	archive := filepath.Join(stage, "kimi.tar.gz")
	location := base + "/binaries/" + url.PathEscape(version) + "/kimi-code-" + target + ".tar.gz"
	if e := download(ctx, location, archive, ""); e == nil {
		extracted := filepath.Join(stage, "extracted")
		if e = extractArchive(archive, extracted); e == nil {
			name := "kimi"
			if strings.HasPrefix(target, "win32-") {
				name += ".exe"
			}
			candidate := filepath.Join(extracted, name)
			if stat, err := os.Stat(candidate); err == nil && stat.Mode().IsRegular() {
				f, e := os.Open(candidate)
				if e != nil {
					return "", e
				}
				h := sha256.New()
				_, e = io.Copy(h, f)
				f.Close()
				if e != nil {
					return "", e
				}
				if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), checksum) {
					return "", fmt.Errorf("Kimi extracted binary checksum mismatch")
				}
				return candidate, nil
			}
		}
	}
	// Download or extraction unavailable: the vendor's bare-binary fallback.
	if e := download(ctx, base+"/binaries/"+url.PathEscape(version)+"/"+url.PathEscape(filename), raw, "sha256:"+checksum); e != nil {
		return "", e
	}
	return raw, nil
}
