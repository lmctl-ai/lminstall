package install

// Aggregate lmctl installation reuses the release channel's safe tree extraction.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const lmctlRepository = "lmctlhq/lmctl-mono"

var lmctlVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+-github\.[1-9][0-9]*\.[1-9][0-9]*$`)

type LmctlAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Digest string `json:"digest"`
}
type LmctlCandidate struct {
	ID      int64        `json:"id"`
	Tag     string       `json:"tag"`
	Version string       `json:"version"`
	SHA     string       `json:"sha"`
	Assets  []LmctlAsset `json:"assets"`
}
type LmctlInstallation struct {
	Version     string `json:"version"`
	PackageRoot string `json:"packageRoot"`
	BinDir      string `json:"binDir"`
	Digest      string `json:"digest"`
}
type lmctlReceipt struct {
	LmctlInstallation
	Files map[string]string `json:"files"`
}

func lmctlGet(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/"+lmctlRepository+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "lminstall")
	req.Header.Set("Accept", "application/vnd.github+json")
	if strings.HasPrefix(path, "/releases/assets/") {
		req.Header.Set("Accept", "application/octet-stream")
	}
	if token := firstEnv("LMCTL_RELEASE_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	safe := *client
	previousRedirect := safe.CheckRedirect
	safe.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if next.URL.Scheme != "https" {
			return fmt.Errorf("release redirect requires HTTPS")
		}
		if next.URL.Hostname() != "api.github.com" {
			next.Header.Del("Authorization")
		}
		if previousRedirect != nil {
			return previousRedirect(next, via)
		}
		return nil
	}
	resp, err := safe.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("private lmctl release request failed: HTTP %d (token needs Contents:read on %s)", resp.StatusCode, lmctlRepository)
	}
	return io.ReadAll(resp.Body)
}
func lmctlAsset(ctx context.Context, a LmctlAsset) ([]byte, error) {
	if a.ID <= 0 {
		return nil, fmt.Errorf("invalid release asset ID")
	}
	b, err := lmctlGet(ctx, "/releases/assets/"+strconv.FormatInt(a.ID, 10))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	if a.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return nil, fmt.Errorf("lmctl asset digest mismatch: %s", a.Name)
	}
	return b, nil
}
func validateLmctlTree(root, version string) error {
	if !lmctlVersion.MatchString(version) {
		return fmt.Errorf("invalid lmctl release version")
	}
	var p struct {
		Version string            `json:"version"`
		Bin     map[string]string `json:"bin"`
	}
	b, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &p); err != nil {
		return err
	}
	if p.Version != version || p.Bin["lmctl"] != "bin/lmctl" || p.Bin["lmscript"] != "bin/lmscript" {
		return fmt.Errorf("lmctl aggregate version or entrypoint mismatch")
	}
	for _, name := range []string{"bin/lmctl", "bin/lmscript", "dist/cli/index.js", "dist/cli/lmscript.js", "dist/cli/schema.sql", "dist/dsl.js", "dist/lmscript.js"} {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("lmctl aggregate requires regular file %s", name)
		}
	}
	return nil
}
func lmctlFiles(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular aggregate file")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	return out, err
}
func lmctlCheckVersions(ctx context.Context, node, root, version string) error {
	// Version probes must not inspect operator profiles or inherit the private download token.
	home, err := os.MkdirTemp("", "lminstall-lmctl-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	var environment []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		upper := strings.ToUpper(key)
		if upper == "HOME" || upper == "USERPROFILE" || strings.HasPrefix(upper, "LMCTL_") || strings.HasPrefix(upper, "LMSCRIPT_") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "API_KEY") {
			continue
		}
		environment = append(environment, item)
	}
	environment = append(environment, "HOME="+home, "USERPROFILE="+home, "LMCTL_NO_UPDATE_NOTIFIER=1", "LMCTL_LMBEE_BRIDGE=0")
	for _, name := range []string{"lmctl", "lmscript"} {
		command := exec.CommandContext(ctx, node, filepath.Join(root, "bin", name), "--version")
		command.Env = environment
		out, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s version probe failed: %w", name, err)
		}
		if strings.TrimSpace(string(out)) != name+" "+version {
			return fmt.Errorf("%s does not report exact release version", name)
		}
	}
	return nil
}
func lmctlLaunchers(bin, node, root string) error {
	if err := os.MkdirAll(bin, 0700); err != nil {
		return err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(filepath.ToSlash(s), "'", "'\"'\"'") + "'" }
	for _, name := range []string{"lmctl", "lmscript"} {
		entry := filepath.Join(root, "bin", name)
		text := "#!/bin/sh\nexec " + quote(node) + " " + quote(entry) + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(text), 0755); err != nil {
			return err
		}
		if runtime.GOOS == "windows" {
			if strings.ContainsAny(node+entry, "\r\n\"%") {
				return fmt.Errorf("unsafe Windows launcher path")
			}
			text = "@echo off\r\n\"" + node + "\" \"" + entry + "\" %*\r\nexit /b %errorlevel%\r\n"
			if err := os.WriteFile(filepath.Join(bin, name+".cmd"), []byte(text), 0600); err != nil {
				return err
			}
		}
	}
	return nil
}

// InstallLmctl accepts an exact tag/latest, or a trusted candidate manifest whose
// asset IDs allow read-only jobs to download private draft bytes without listing drafts.
// Tokens remain in the environment and are sent only to api.github.com.
func InstallLmctl(ctx context.Context, root, selector, candidateFile string) (LmctlInstallation, error) {
	var result LmctlInstallation
	if !filepath.IsAbs(root) || strings.ContainsAny(root, "\r\n") {
		return result, fmt.Errorf("lmctl root must be a single-line absolute owned prefix")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return result, fmt.Errorf("lmctl aggregate requires Node >=24.15.0; use the lminstall Action to provision it")
	}
	versionOut, err := exec.CommandContext(ctx, node, "--version").Output()
	if err != nil {
		return result, err
	}
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(string(versionOut)), "v"), ".")
	if len(parts) < 2 {
		return result, fmt.Errorf("invalid Node version")
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	if major < 24 || (major == 24 && minor < 15) {
		return result, fmt.Errorf("Node >=24.15.0 required")
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return result, err
	}
	lockPath := filepath.Join(root, ".lmctl-install.lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return result, fmt.Errorf("lmctl installer prefix busy: %w", err)
	}
	lockInfo, _ := lock.Stat()
	lock.Close()
	defer func() {
		if current, e := os.Stat(lockPath); e == nil && os.SameFile(lockInfo, current) {
			os.Remove(lockPath)
		}
	}()
	installationDir := filepath.Join(root, "lmctl")
	if err = os.Mkdir(installationDir, 0700); err == nil {
		if err = os.WriteFile(filepath.Join(installationDir, ".lminstall-owner"), []byte("lmctl\n"), 0600); err != nil {
			return result, err
		}
	} else if os.IsExist(err) {
		if err = owned(installationDir, "lmctl"); err != nil {
			return result, err
		}
	} else {
		return result, err
	}
	var c LmctlCandidate
	if candidateFile != "" {
		b, e := os.ReadFile(candidateFile)
		if e != nil {
			return result, e
		}
		if err = json.Unmarshal(b, &c); err != nil {
			return result, err
		}
	} else {
		path := "/releases/latest"
		if selector != "latest" {
			if !lmctlVersion.MatchString(strings.TrimPrefix(selector, "v")) {
				return result, fmt.Errorf("expected exact lmctl tag or latest")
			}
			path = "/releases/tags/" + selector
		}
		b, e := lmctlGet(ctx, path)
		if e != nil {
			return result, e
		}
		var release struct {
			ID     int64        `json:"id"`
			Tag    string       `json:"tag_name"`
			Assets []LmctlAsset `json:"assets"`
		}
		if err = json.Unmarshal(b, &release); err != nil {
			return result, err
		}
		c = LmctlCandidate{ID: release.ID, Tag: release.Tag, Version: strings.TrimPrefix(release.Tag, "v"), Assets: release.Assets}
	}
	if !lmctlVersion.MatchString(c.Version) || c.Tag != "v"+c.Version || c.ID <= 0 {
		return result, fmt.Errorf("invalid lmctl release candidate")
	}
	if selector != "" && selector != "latest" && selector != c.Tag {
		return result, fmt.Errorf("candidate does not match requested tag")
	}
	want := map[string]bool{"lmctl-" + c.Version + ".tgz": true, "release.json": true, "SHA256SUMS": true}
	assets := map[string]LmctlAsset{}
	for _, a := range c.Assets {
		if !want[a.Name] || assets[a.Name].ID != 0 {
			return result, fmt.Errorf("unexpected or duplicate release asset")
		}
		assets[a.Name] = a
	}
	if len(assets) != 3 {
		return result, fmt.Errorf("lmctl release requires three assets")
	}
	sums, err := lmctlAsset(ctx, assets["SHA256SUMS"])
	if err != nil {
		return result, err
	}
	metadata, err := lmctlAsset(ctx, assets["release.json"])
	if err != nil {
		return result, err
	}
	var meta struct {
		Version    string `json:"version"`
		SHA        string `json:"sha"`
		Asset      string `json:"asset"`
		Repository string `json:"repository"`
	}
	if err = json.Unmarshal(metadata, &meta); err != nil {
		return result, err
	}
	name := "lmctl-" + c.Version + ".tgz"
	if meta.Version != c.Version || meta.Asset != name || meta.Repository != lmctlRepository || (c.SHA != "" && meta.SHA != c.SHA) {
		return result, fmt.Errorf("lmctl release metadata mismatch")
	}
	for _, item := range []struct {
		name   string
		digest string
	}{{name, strings.TrimPrefix(assets[name].Digest, "sha256:")}, {"release.json", strings.TrimPrefix(assets["release.json"].Digest, "sha256:")}} {
		found, e := checksumFor(sums, item.name)
		if e != nil {
			return result, e
		}
		if found != item.digest {
			return result, fmt.Errorf("release checksum evidence mismatch")
		}
	}
	digest := strings.TrimPrefix(assets[name].Digest, "sha256:")
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(digest) {
		return result, fmt.Errorf("invalid archive digest")
	}
	receiptPath := filepath.Join(installationDir, "install.json")
	packageRoot := filepath.Join(installationDir, "releases", digest, "package")
	result = LmctlInstallation{Version: c.Version, PackageRoot: packageRoot, BinDir: filepath.Join(installationDir, "bin"), Digest: digest}
	var previous lmctlReceipt
	if b, e := os.ReadFile(receiptPath); e == nil && json.Unmarshal(b, &previous) == nil && previous.Digest == digest {
		files, e := lmctlFiles(packageRoot)
		if e != nil {
			return result, e
		}
		a, _ := json.Marshal(files)
		b, _ := json.Marshal(previous.Files)
		if string(a) != string(b) {
			return result, fmt.Errorf("installed lmctl tree changed; refusing cached payload")
		}
	} else {
		if info, e := os.Stat(filepath.Dir(packageRoot)); e == nil && info.IsDir() {
			return result, fmt.Errorf("unrecorded lmctl payload already exists; refusing overwrite")
		}
		archive, e := lmctlAsset(ctx, assets[name])
		if e != nil {
			return result, e
		}
		if e = os.MkdirAll(installationDir, 0700); e != nil {
			return result, e
		}
		stage, e := os.MkdirTemp(installationDir, "candidate-")
		if e != nil {
			return result, e
		}
		defer os.RemoveAll(stage)
		archivePath := filepath.Join(stage, "release.tgz")
		if e = os.WriteFile(archivePath, archive, 0600); e != nil {
			return result, e
		}
		unpack := filepath.Join(stage, "tree")
		if e = extractArchive(archivePath, unpack); e != nil {
			return result, e
		}
		if e = validateLmctlTree(filepath.Join(unpack, "package"), c.Version); e != nil {
			return result, e
		}
		if e = lmctlCheckVersions(ctx, node, filepath.Join(unpack, "package"), c.Version); e != nil {
			return result, e
		}
		if e = os.MkdirAll(filepath.Dir(filepath.Dir(packageRoot)), 0700); e != nil {
			return result, e
		}
		if e = os.Rename(unpack, filepath.Dir(packageRoot)); e != nil {
			return result, e
		}
	}
	if err = validateLmctlTree(packageRoot, c.Version); err != nil {
		return result, err
	}
	if err = lmctlCheckVersions(ctx, node, packageRoot, c.Version); err != nil {
		return result, err
	}
	if err = lmctlLaunchers(result.BinDir, node, packageRoot); err != nil {
		return result, err
	}
	files, err := lmctlFiles(packageRoot)
	if err != nil {
		return result, err
	}
	b, err := json.Marshal(lmctlReceipt{result, files})
	if err != nil {
		return result, err
	}
	temp, err := os.CreateTemp(installationDir, "receipt-")
	if err != nil {
		return result, err
	}
	path := temp.Name()
	defer os.Remove(path)
	if _, err = temp.Write(b); err != nil {
		temp.Close()
		return result, err
	}
	if err = temp.Close(); err != nil {
		return result, err
	}
	if err = os.Rename(path, receiptPath); err != nil {
		return result, err
	}
	return result, nil
}
