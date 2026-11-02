package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

type Runner interface {
	Run(context.Context, []string, string, map[string]string, bool) (string, error)
}
type Manager struct {
	Root, OS, Arch string
	Manifest       Manifest
	Runner         Runner
	Log            io.Writer
}
type Result struct {
	Provider string `json:"provider"`
	State    string `json:"state"`
	Version  string `json:"version,omitempty"`
	BinDir   string `json:"bin_dir,omitempty"`
	Binary   string `json:"binary,omitempty"`
	Source   string `json:"source"`
	Channel  string `json:"channel,omitempty"`
	Reason   string `json:"reason,omitempty"`
}
type receipt struct {
	Channel  string `json:"channel,omitempty"`
	Provider string `json:"provider"`
	Bin      string `json:"bin"`
	BinDir   string `json:"bin_dir"`
	Version  string `json:"version"`
}

var versionNumber = regexp.MustCompile(`[0-9]+\.[0-9]+`)

func (m *Manager) dir(id string) string { return filepath.Join(m.Root, "providers", id) }
func (m *Manager) expand(s, dir string) string {
	assetarch := m.Arch
	if assetarch == "amd64" {
		assetarch = "x64"
	}
	ext, archive := "", ".tar.gz"
	if m.OS == "windows" {
		ext = ".exe"
		archive = ".zip"
	}
	triple := map[string]string{"linux-amd64": "x86_64-unknown-linux-musl", "linux-arm64": "aarch64-unknown-linux-musl", "darwin-amd64": "x86_64-apple-darwin", "darwin-arm64": "aarch64-apple-darwin", "windows-amd64": "x86_64-pc-windows-msvc", "windows-arm64": "aarch64-pc-windows-msvc"}[m.OS+"-"+m.Arch]
	return strings.NewReplacer("{triple}", triple, "{root}", m.Root, "{dir}", dir, "{os}", m.OS, "{arch}", m.Arch, "{assetarch}", assetarch, "{exe}", ext, "{archive}", archive).Replace(s)
}
func relative(dir, name string) (string, error) {
	name = filepath.FromSlash(name)
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("invalid owned relative path %q", name)
	}
	p := filepath.Join(dir, name)
	r, e := filepath.Rel(dir, p)
	if e != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes managed directory: %q", name)
	}
	return p, nil
}
func (m *Manager) env(r Recipe, dir string) map[string]string {
	e := map[string]string{"GIT_TERMINAL_PROMPT": "0"}
	for k, v := range r.Env {
		e[k] = m.expand(v, dir)
	}
	if r.Channel == "hermes" {
		// Source builds may need an existing rustup toolchain. Isolating HOME must
		// not hide that prerequisite; downloads/build caches remain in our prefix.
		if _, ok := e["CARGO_HOME"]; !ok {
			e["CARGO_HOME"] = filepath.Join(dir, "cache", "cargo")
		}
		if _, ok := e["RUSTUP_HOME"]; !ok {
			root := os.Getenv("RUSTUP_HOME")
			if root == "" {
				if home, err := os.UserHomeDir(); err == nil {
					candidate := filepath.Join(home, ".rustup")
					if info, err := os.Stat(candidate); err == nil && info.IsDir() {
						root = candidate
					}
				}
			}
			if root != "" {
				e["RUSTUP_HOME"] = root
			}
		}
	}
	return e
}
func (m *Manager) verify(ctx context.Context, p Provider, r Recipe, bin string) (string, error) {
	path, e := relative(m.dir(p.ID), bin)
	if e != nil {
		return "", e
	}
	argv := []string{path, "--version"}
	if runtime.GOOS == "windows" && m.OS == "windows" && r.Channel == "npm" && strings.EqualFold(filepath.Ext(path), ".cmd") && p.LaunchConfig.NPMPackage != "" {
		// Run the package entry directly rather than through a batch interpreter.
		if direct, err := ResolveNPMEntry(path, p.Binary, p.LaunchConfig.NPMPackage, []string{"--version"}); err == nil {
			argv = direct
		}
	}
	out, e := m.Runner.Run(ctx, argv, "", m.env(r, m.dir(p.ID)), true)
	if e != nil {
		return "", fmt.Errorf("%s --version failed: %w", p.ID, e)
	}
	out = strings.TrimSpace(out)
	if !versionNumber.MatchString(out) {
		return "", fmt.Errorf("%s --version returned no version number: %s", p.ID, out)
	}
	return out, nil
}

// Match a complete version token, allowing a conventional v prefix and banner text.
var versionToken = regexp.MustCompile(`(?:^|[^A-Za-z0-9.])v?([0-9]+\.[0-9]+(?:\.[0-9]+)?(?:-[A-Za-z0-9.-]+)?(?:\+[A-Za-z0-9.-]+)?)(?:$|[^A-Za-z0-9.+-])`)

func (m *Manager) verifySelectedVersion(ctx context.Context, p Provider, r Recipe, bin, expected string) error {
	_, err := m.verifiedVersion(ctx, p, r, bin, expected)
	return err
}
func (m *Manager) verifiedVersion(ctx context.Context, p Provider, r Recipe, bin, expected string) (string, error) {
	out, err := m.verify(ctx, p, r, bin)
	if err != nil {
		return "", err
	}
	want := strings.TrimPrefix(expected, "v")
	for _, match := range versionToken.FindAllStringSubmatch(out, -1) {
		if match[1] == want {
			return out, nil
		}
	}
	return "", fmt.Errorf("%s version mismatch: selected %s, executable reported %s", p.ID, expected, out)
}

func owned(dir, id string) error {
	b, e := os.ReadFile(filepath.Join(dir, ".lminstall-owner"))
	if e != nil || string(b) != id+"\n" {
		return fmt.Errorf("refusing unmanaged directory %s; choose another --root (user files are untouched)", dir)
	}
	s, e := os.Lstat(dir)
	if e != nil || s.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("managed provider directory must not be a symlink: %s", dir)
	}
	return nil
}
func (m *Manager) Status(ctx context.Context, id string) (Result, error) {
	p, e := m.Manifest.Provider(id)
	if e != nil {
		return Result{}, e
	}
	r := p.Recipe(m.OS)
	res := Result{Provider: id, State: "absent", Source: p.Source, Channel: r.Channel}
	if r.Unavailable != "" {
		res.State = "unavailable"
		res.Reason = r.Unavailable
		return res, nil
	}
	dir := m.dir(id)
	if _, e = os.Stat(dir); errors.Is(e, os.ErrNotExist) {
		return res, nil
	}
	if e = owned(dir, id); e != nil {
		res.State = "unmanaged"
		res.Reason = e.Error()
		return res, nil
	}
	b, e := os.ReadFile(filepath.Join(dir, "receipt.json"))
	var rec receipt
	if e != nil || json.Unmarshal(b, &rec) != nil || rec.Provider != id {
		res.State = "incomplete"
		res.Reason = "installation has not passed --version; run lminstall install " + id
		return res, nil
	}
	if (r.Channel == "kimi" || r.Channel == "muse" || r.Channel == "hermes" || r.Channel == "claude") && rec.Channel != r.Channel {
		res.State = "incomplete"
		res.Reason = "installation method changed; run lminstall install " + id + " to use the official native method"
		return res, nil
	}
	v, e := m.verify(ctx, p, r, rec.Bin)
	if e != nil {
		res.State = "broken"
		res.Reason = e.Error()
		return res, nil
	}
	if usesLauncher(r.Channel) {
		if e = m.checkLauncher(p, rec.Bin); e != nil {
			res.State = "broken"
			res.Reason = e.Error()
			return res, nil
		}
	}
	res.State = "installed"
	res.Version = v
	res.Binary, _ = relative(dir, rec.Bin)
	res.BinDir, _ = relative(dir, rec.BinDir)
	return res, nil
}
func (m *Manager) Apply(ctx context.Context, op, id string) (Result, error) {
	p, e := m.Manifest.Provider(id)
	if e != nil {
		return Result{}, e
	}
	r := p.Recipe(m.OS)
	res := Result{Provider: id, State: "absent", Source: p.Source, Channel: r.Channel}
	if op != "install" && op != "upgrade" && op != "uninstall" {
		return res, fmt.Errorf("unknown operation %q", op)
	}
	if r.Unavailable != "" && op != "uninstall" {
		res.State = "unavailable"
		res.Reason = r.Unavailable
		return res, errors.New(r.Unavailable)
	}
	if m.Arch != "amd64" && m.Arch != "arm64" {
		return res, fmt.Errorf("unsupported architecture %s", m.Arch)
	}
	if e = os.MkdirAll(filepath.Join(m.Root, "locks"), 0700); e != nil {
		return res, e
	}
	lock := filepath.Join(m.Root, "locks", id+".lock")
	f, e := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return res, fmt.Errorf("%s is busy: %s (remove only after its installer process has exited)", id, lock)
	}
	info, _ := f.Stat()
	fmt.Fprintln(f, strconv.Itoa(os.Getpid()))
	f.Close()
	defer func() {
		if now, e := os.Stat(lock); e == nil && os.SameFile(info, now) {
			os.Remove(lock)
		}
	}()
	dir := m.dir(id)
	_, e = os.Lstat(dir)
	if errors.Is(e, os.ErrNotExist) {
		if op == "uninstall" {
			return res, nil
		}
		if e = os.MkdirAll(dir, 0700); e != nil {
			return res, e
		}
		if e = os.WriteFile(filepath.Join(dir, ".lminstall-owner"), []byte(id+"\n"), 0600); e != nil {
			return res, e
		}
	} else if e != nil {
		return res, e
	} else if e = owned(dir, id); e != nil {
		return res, e
	}
	if op == "uninstall" {
		e = removeOwned(dir, id, removeManaged)
		if e != nil {
			res.State = "incomplete"
			res.Reason = e.Error()
		}
		return res, e
	}
	if op == "install" {
		if current, e := m.Status(ctx, id); e == nil && current.State == "installed" {
			return current, nil
		}
	}
	for _, sub := range []string{"bin", "home"} {
		if e = os.MkdirAll(filepath.Join(dir, sub), 0700); e != nil {
			return res, e
		}
	}
	if op == "upgrade" {
		for _, name := range r.Replace {
			path, e := relative(dir, name)
			if e != nil {
				return res, e
			}
			if e = os.RemoveAll(path); e != nil {
				return res, e
			}
		}
	}
	bin, bindir, v, e := m.install(ctx, p, r, dir)
	if e != nil {
		res.State = "incomplete"
		res.Reason = e.Error()
		return res, e
	}
	// Native paths already checked the candidate at its private pathname. A
	// second invocation after replacement could fail after publication.
	if v == "" {
		v, e = m.verify(ctx, p, r, bin)
		if e != nil {
			res.State = "broken"
			res.Reason = e.Error()
			return res, e
		}
	}
	if usesLauncher(r.Channel) {
		if e = m.publishLauncher(p, bin); e != nil {
			return res, e
		}
	}
	rec := receipt{Provider: id, Bin: bin, BinDir: bindir, Version: v, Channel: r.Channel}
	b, _ := json.MarshalIndent(rec, "", "  ")
	if e = os.WriteFile(filepath.Join(dir, "receipt.json"), b, 0600); e != nil {
		return res, e
	}
	res.State = "installed"
	res.Version = v
	res.Binary, _ = relative(dir, bin)
	res.BinDir, _ = relative(dir, bindir)
	return res, nil
}
func usesLauncher(channel string) bool {
	return channel == "release" || channel == "hermes" || channel == "claude"
}
func (m *Manager) install(ctx context.Context, p Provider, r Recipe, dir string) (string, string, string, error) {
	run := func(args []string, cwd string) error {
		a := make([]string, len(args))
		for i, s := range args {
			a[i] = m.expand(s, dir)
		}
		_, e := m.Runner.Run(ctx, a, m.expand(cwd, dir), m.env(r, dir), false)
		return e
	}
	steps := func() error {
		for _, step := range r.Steps {
			if step.URL != "" {
				return fmt.Errorf("downloaded installer steps are unsupported; use native Go channels")
			}

			if len(step.Args) > 0 {
				if e := run(step.Args, step.Cwd); e != nil {
					return e
				}
			}
		}
		return nil
	}
	var e error
	switch r.Channel {
	case "kimi":
		return m.kimi(ctx, p, r, dir)
	case "muse":
		return m.muse(ctx, p, r, dir)
	case "hermes":
		return m.hermes(ctx, p, r, dir)
	case "npm":
		return m.npm(ctx, p, r, dir)
	case "uv":
		e = run([]string{"uv", "tool", "install", "--upgrade", "--force", "--python", r.Python, r.Package}, "")
	case "project":
		source := filepath.Join(dir, "source")
		if _, statErr := os.Stat(filepath.Join(source, ".git")); errors.Is(statErr, os.ErrNotExist) {
			e = run([]string{"git", "clone", "--depth", "1", r.Repository, source}, "")
		} else if statErr != nil {
			e = statErr
		} else {
			e = run([]string{"git", "-C", source, "fetch", "--depth", "1", "origin", "HEAD"}, "")
			if e == nil {
				e = run([]string{"git", "-C", source, "checkout", "--detach", "FETCH_HEAD"}, "")
			}
		}
		if e == nil {
			e = steps()
		}
	case "update-manifest":
		return m.updateManifest(ctx, p, r, dir)
	case "release":
		return m.release(ctx, p, r, dir)
	case "claude":
		return m.claude(ctx, p, r, dir)
	case "steps":
		e = steps()
	default:
		e = fmt.Errorf("unknown install channel %q", r.Channel)
	}
	return m.expand(r.Bin, dir), m.expand(r.BinDir, dir), "", e
}

// LookPath is only used for prerequisites; managed provider verification always uses its owned path.
func prerequisite(name string) (string, error) {
	p, e := exec.LookPath(name)
	if e != nil {
		return "", fmt.Errorf("required command %s is missing; see the provider's official installation link", name)
	}
	return p, nil
}

// Delete the ownership marker last. Failed Windows deletions remain safely retryable.
func removeOwned(dir, id string, remove func(string) error) error {
	if e := owned(dir, id); e != nil {
		return e
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.Name() == ".lminstall-owner" {
			continue
		}
		if e = remove(filepath.Join(dir, entry.Name())); e != nil {
			return e
		}
	}
	if e = os.Remove(filepath.Join(dir, ".lminstall-owner")); e != nil {
		return e
	}
	if e = os.Remove(dir); e != nil {
		os.WriteFile(filepath.Join(dir, ".lminstall-owner"), []byte(id+"\n"), 0600)
		return e
	}
	return nil
}
