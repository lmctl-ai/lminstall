package install

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type pinnedTool struct {
	Version   string `json:"version"`
	Artifacts map[string]struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	} `json:"artifacts"`
}

func (m *Manager) hermes(ctx context.Context, p Provider, r Recipe, dir string) (string, string, error) {
	env := m.env(r, dir)
	git := "git"
	if _, e := prerequisite(git); e != nil {
		if m.OS != "windows" {
			return "", "", fmt.Errorf("Hermes requires Git; install Git with your system package manager (macOS: Xcode Command Line Tools); lminstall never uses sudo")
		}
		var err error
		git, err = m.hermesGit(ctx, dir, env)
		if err != nil {
			return "", "", err
		}
		env["PATH"] = filepath.Dir(git) + string(os.PathListSeparator) + os.Getenv("PATH")
	}
	if env["PATH"] == "" {
		env["PATH"] = os.Getenv("PATH")
	}
	// PM may offer to install missing system libraries. Exclude elevation and
	// distribution package managers: those prerequisites belong to an administrator.
	var e error
	env["PATH"], e = withoutSystemInstallers(dir, env["PATH"])
	if e != nil {
		return "", "", e
	}
	run := func(args []string, cwd string, capture bool) (string, error) {
		return m.Runner.Run(ctx, args, cwd, env, capture)
	}
	// The current official installer defaults to the latest stable release, not HEAD.
	tag, e := hermesStable(ctx)
	if e != nil {
		return "", "", e
	}
	source := filepath.Join(dir, "source")
	if _, e = os.Stat(filepath.Join(source, ".git")); os.IsNotExist(e) {
		_, e = run([]string{git, "clone", "--depth", "1", "--branch", tag, r.Repository, source}, "", false)
	} else if e == nil {
		_, e = run([]string{git, "-C", source, "fetch", "--depth", "1", "origin", "tag", tag}, "", false)
		if e == nil {
			_, e = run([]string{git, "-C", source, "checkout", "--detach", "FETCH_HEAD"}, "", false)
		}
	}
	if e != nil {
		return "", "", e
	}
	lockBytes, e := os.ReadFile(filepath.Join(source, "pm", "lock.json"))
	if e != nil {
		return "", "", e
	}
	var lock struct {
		Packages struct {
			UV     pinnedTool               `json:"uv"`
			Python struct{ Version string } `json:"python"`
		} `json:"packages"`
	}
	if e = json.Unmarshal(lockBytes, &lock); e != nil {
		return "", "", e
	}
	target := m.OS + "-" + m.expand("{assetarch}", dir)
	if m.OS == "windows" {
		target = "win32-" + m.expand("{assetarch}", dir)
	}
	if m.OS == "linux" && muslHost() {
		target += "-musl"
	}
	uv, e := m.hermesUV(ctx, dir, target, lock.Packages.UV)
	if e != nil {
		return "", "", e
	}
	if _, e = run([]string{uv, "--version"}, source, false); e != nil {
		return "", "", e
	}
	pyVersion := strings.SplitN(lock.Packages.Python.Version, "+", 2)[0]
	parts := strings.Split(pyVersion, ".")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("Hermes lock has no Python version")
	}
	minor := strings.Join(parts[:2], ".")
	if _, e = run([]string{uv, "python", "install", "--no-bin", "--no-registry", minor}, source, false); e != nil {
		return "", "", e
	}
	python, e := run([]string{uv, "python", "find", "--managed-python", minor}, source, true)
	if e != nil {
		return "", "", e
	}
	python = strings.TrimSpace(python)
	if !filepath.IsAbs(python) {
		return "", "", fmt.Errorf("uv returned no absolute Python path: %q", python)
	}
	// These are the official package-manager entry points, not installer scripts.
	// PM resolves its pinned tools and hash-verified uv.lock dependency graph.
	if _, e = run([]string{python, "-m", "pm.cli", "install"}, source, false); e != nil {
		return "", "", e
	}
	if _, e = run([]string{python, "-I", "-B", "-X", "utf8", filepath.Join(source, "hermes_cli", "source_completion.py"), "--source", source}, source, false); e != nil {
		return "", "", e
	}
	home := env["HERMES_HOME"]
	for _, sub := range []string{"cron", "sessions", "logs", "pairing", "hooks", "image_cache", "audio_cache", "memories", "skills"} {
		if e = os.MkdirAll(filepath.Join(home, sub), 0700); e != nil {
			return "", "", e
		}
	}
	for dest, template := range map[string]string{".env": ".env.example", "config.yaml": "cli-config.yaml.example"} {
		dst := filepath.Join(home, dest)
		if _, e = os.Stat(dst); os.IsNotExist(e) {
			b, err := os.ReadFile(filepath.Join(source, template))
			if err != nil {
				b = nil
			}
			if e = os.WriteFile(dst, b, 0600); e != nil {
				return "", "", e
			}
		}
	}
	// The upstream launcher is a provider command, not an installation script.
	// Its private HOME/PM store stays in the managed prefix.
	return m.expand(r.Bin, dir), m.expand(r.BinDir, dir), nil
}

func hermesStable(ctx context.Context) (string, error) {
	var release struct {
		Tag string `json:"tag_name"`
	}
	e := getJSON(ctx, "https://api.github.com/repos/NousResearch/hermes-agent/releases/latest", &release)
	if e != nil {
		req, err := http.NewRequestWithContext(ctx, "HEAD", "https://github.com/NousResearch/hermes-agent/releases/latest", nil)
		if err != nil {
			return "", err
		}
		resp, err := secureDo(req)
		if err != nil {
			return "", err
		}
		resp.Body.Close()
		const prefix = "/NousResearch/hermes-agent/releases/tag/"
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Request.URL.Path, prefix) {
			return "", fmt.Errorf("Hermes stable release lookup failed: %v", e)
		}
		release.Tag = strings.TrimPrefix(resp.Request.URL.Path, prefix)
	}
	if !releaseVersion.MatchString(strings.TrimPrefix(release.Tag, "v")) {
		return "", fmt.Errorf("invalid Hermes stable tag %q", release.Tag)
	}
	return release.Tag, nil
}

func (m *Manager) hermesUV(ctx context.Context, dir, target string, tool pinnedTool) (string, error) {
	asset, ok := tool.Artifacts[target]
	if !ok || !releaseVersion.MatchString(tool.Version) || !sha256Hex.MatchString(asset.SHA256) {
		return "", fmt.Errorf("Hermes lock has no verified uv for %s", target)
	}
	entry := filepath.Join(dir, "tools", "uv-"+tool.Version+"-"+target)
	name := "uv"
	if m.OS == "windows" {
		name += ".exe"
	}
	destination := filepath.Join(entry, name)
	// Always stage from the official artifact; never trust a global uv by name.
	u, e := url.Parse(asset.URL)
	if e != nil {
		return "", e
	}
	archive := filepath.Join(dir, filepath.Base(u.Path))
	if e = download(ctx, asset.URL, archive, "sha256:"+asset.SHA256); e != nil {
		if !downloadAvailabilityError(e) {
			return "", e
		}
		mirror := "https://hermes-assets.nousresearch.com/upstream/sha256/" + strings.ToLower(asset.SHA256)
		if e = download(ctx, mirror, archive, "sha256:"+asset.SHA256); e != nil {
			return "", e
		}
	}
	defer os.Remove(archive)
	stage, e := os.MkdirTemp(dir, "uv-stage-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(stage)
	if e = extractArchive(archive, stage); e != nil {
		return "", e
	}
	var binary string
	e = filepath.WalkDir(stage, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == name {
			if binary != "" {
				return fmt.Errorf("ambiguous uv binary")
			}
			binary = path
		}
		return nil
	})
	if e != nil {
		return "", e
	}
	if binary == "" {
		return "", fmt.Errorf("uv archive has no %s", name)
	}
	if e = os.MkdirAll(entry, 0700); e != nil {
		return "", e
	}
	if e = copyExecutable(binary, destination); e != nil {
		return "", e
	}
	return destination, nil
}

func copyExecutable(source, dest string) error {
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	f, e := os.CreateTemp(filepath.Dir(dest), ".native-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_, e = io.Copy(f, in)
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.Chmod(tmp, 0755); e != nil {
		return e
	}
	return os.Rename(tmp, dest)
}

func withoutSystemInstallers(dir, path string) (string, error) {
	if runtime.GOOS == "windows" {
		return path, nil
	}
	bin := filepath.Join(dir, "prerequisites")
	if e := os.RemoveAll(bin); e != nil {
		return "", e
	}
	if e := os.MkdirAll(bin, 0700); e != nil {
		return "", e
	}
	forbidden := map[string]bool{"sudo": true, "doas": true, "su": true, "apt": true, "apt-get": true, "dnf": true, "yum": true, "zypper": true, "pacman": true, "apk": true}
	seen := map[string]bool{}
	for _, entry := range filepath.SplitList(path) {
		if entry == "" || !filepath.IsAbs(entry) || strings.Contains(entry, "/.codex") {
			continue
		}
		files, e := os.ReadDir(entry)
		if e != nil {
			continue
		}
		for _, file := range files {
			if forbidden[file.Name()] || seen[file.Name()] || file.IsDir() {
				continue
			}
			seen[file.Name()] = true
			target := filepath.Join(bin, file.Name())
			if e = os.Symlink(filepath.Join(entry, file.Name()), target); e != nil && !os.IsExist(e) {
				return "", e
			}
		}
	}
	return bin, nil
}

// The official Windows installer bootstraps this verified portable Git when absent.
// This is a native self-extracting archive, not a PowerShell installer.
func (m *Manager) hermesGit(ctx context.Context, dir string, env map[string]string) (string, error) {
	arch, sha := "64-bit", "b365da794b1d2225eb24d5f5e09ef7792cfd5fa26c3a3586210280c80dff3a2a"
	if m.Arch == "arm64" {
		arch = "arm64"
		sha = "0db54010054c01f35501cf69e1e32d3710138ecb934d188bd77093afed24300e"
	}
	name := "PortableGit-2.53.0.3-" + arch + ".7z.exe"
	archive := filepath.Join(dir, name)
	if e := download(ctx, "https://github.com/git-for-windows/git/releases/download/v2.53.0.windows.3/"+name, archive, "sha256:"+sha); e != nil {
		return "", e
	}
	defer os.Remove(archive)
	target := filepath.Join(dir, "portable-git")
	if _, e := m.Runner.Run(ctx, []string{archive, "-y", "-o" + target}, "", env, false); e != nil {
		return "", e
	}
	return filepath.Join(target, "cmd", "git.exe"), nil
}
