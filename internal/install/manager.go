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
	return strings.NewReplacer("{root}", m.Root, "{dir}", dir, "{os}", m.OS, "{arch}", m.Arch, "{assetarch}", assetarch, "{exe}", ext, "{archive}", archive).Replace(s)
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
	return e
}
func (m *Manager) verify(ctx context.Context, p Provider, r Recipe, bin string) (string, error) {
	path, e := relative(m.dir(p.ID), bin)
	if e != nil {
		return "", e
	}
	out, e := m.Runner.Run(ctx, []string{path, "--version"}, "", m.env(r, m.dir(p.ID)), true)
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
	out, e := m.verify(ctx, p, r, bin)
	if e != nil {
		return e
	}
	want := strings.TrimPrefix(expected, "v")
	for _, match := range versionToken.FindAllStringSubmatch(out, -1) {
		if match[1] == want {
			return nil
		}
	}
	return fmt.Errorf("%s version mismatch: selected %s, executable reported %s", p.ID, expected, out)
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
	v, e := m.verify(ctx, p, r, rec.Bin)
	if e != nil {
		res.State = "broken"
		res.Reason = e.Error()
		return res, nil
	}
	if r.Channel == "release" {
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
	bin, bindir, e := m.install(ctx, p, r, dir)
	if e != nil {
		res.State = "incomplete"
		res.Reason = e.Error()
		return res, e
	}
	v, e := m.verify(ctx, p, r, bin)
	if e != nil {
		res.State = "broken"
		res.Reason = e.Error()
		return res, e
	}
	if r.Channel == "release" {
		if e = m.publishLauncher(p, bin); e != nil {
			return res, e
		}
	}
	rec := receipt{id, bin, bindir, v}
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
func (m *Manager) install(ctx context.Context, p Provider, r Recipe, dir string) (string, string, error) {
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
				path, e := relative(dir, step.File)
				if e != nil {
					return e
				}
				if e = download(ctx, step.URL, path, step.Digest); e != nil {
					return e
				}
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
	case "npm":
		a := []string{"npm", "install", "--global", "--prefix", filepath.Join(dir, "npm"), "--no-audit", "--no-fund", r.Package}
		a = append(a, r.Args...)
		e = run(a, "")
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
	case "steps":
		e = steps()
	default:
		e = fmt.Errorf("unknown install channel %q", r.Channel)
	}
	return m.expand(r.Bin, dir), m.expand(r.BinDir, dir), e
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
