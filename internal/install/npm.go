package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// A Go implementation of the part of `npm install --global` that provider
// packages need: registry metadata, semver resolution, optional/platform
// dependencies, integrity-checked tarballs and bin links. No npm, node or git
// process is started. Lifecycle scripts are never executed (equivalent to
// --ignore-scripts); packages that need them must ship a runnable payload.

const defaultNPMRegistry = "https://registry.npmjs.org"

type npmVersion struct {
	Name                 string                             `json:"name"`
	Version              string                             `json:"version"`
	Dependencies         map[string]string                  `json:"dependencies"`
	OptionalDependencies map[string]string                  `json:"optionalDependencies"`
	PeerDependencies     map[string]string                  `json:"peerDependencies"`
	PeerDependenciesMeta map[string]struct{ Optional bool } `json:"peerDependenciesMeta"`
	Bin                  json.RawMessage                    `json:"bin"`
	OS                   stringList                         `json:"os"`
	CPU                  stringList                         `json:"cpu"`
	Libc                 stringList                         `json:"libc"`
	HasInstallScript     bool                               `json:"hasInstallScript"`
	Scripts              map[string]string                  `json:"scripts"`
	Dist                 struct {
		Tarball   string `json:"tarball"`
		Integrity string `json:"integrity"`
		Shasum    string `json:"shasum"`
	} `json:"dist"`
}

// stringList decodes npm's "string or array of strings" fields.
type stringList []string

func (s *stringList) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*s = stringList{one}
		return nil
	}
	var many []string
	if e := json.Unmarshal(b, &many); e != nil {
		return nil // malformed platform metadata is treated as unrestricted
	}
	*s = many
	return nil
}

type npmPackument struct {
	Name     string                 `json:"name"`
	DistTags map[string]string      `json:"dist-tags"`
	Versions map[string]*npmVersion `json:"versions"`
}

type npmClient struct {
	registry string
	goos     string // node naming: linux, darwin, win32
	goarch   string // node naming: x64, arm64
	libc     string // glibc or musl on Linux
	log      io.Writer
	mu       sync.Mutex
	cache    map[string]*npmPackument
}

func newNPMClient(m *Manager, log io.Writer) *npmClient {
	osName := map[string]string{"linux": "linux", "darwin": "darwin", "windows": "win32"}[m.OS]
	cpu := map[string]string{"amd64": "x64", "arm64": "arm64"}[m.Arch]
	libc := ""
	if m.OS == "linux" {
		libc = "glibc"
		if m.OS == runtime.GOOS && muslHost() {
			libc = "musl"
		}
	}
	registry := strings.TrimSuffix(os.Getenv("LMINSTALL_NPM_REGISTRY"), "/")
	if registry == "" {
		registry = defaultNPMRegistry
	}
	if log == nil {
		log = io.Discard
	}
	return &npmClient{registry: registry, goos: osName, goarch: cpu, libc: libc, log: log, cache: map[string]*npmPackument{}}
}

func matchesList(list stringList, value string) bool {
	if len(list) == 0 {
		return true
	}
	allowed := false
	hasPositive := false
	for _, item := range list {
		if strings.HasPrefix(item, "!") {
			if item[1:] == value {
				return false
			}
			continue
		}
		hasPositive = true
		if item == value {
			allowed = true
		}
	}
	return allowed || !hasPositive
}

func (c *npmClient) platformOK(v *npmVersion) bool {
	if !matchesList(v.OS, c.goos) || !matchesList(v.CPU, c.goarch) {
		return false
	}
	return c.libc == "" || matchesList(v.Libc, c.libc)
}

func (c *npmClient) packument(ctx context.Context, name string) (*npmPackument, error) {
	c.mu.Lock()
	if p, ok := c.cache[name]; ok {
		c.mu.Unlock()
		return p, nil
	}
	c.mu.Unlock()
	req, e := http.NewRequestWithContext(ctx, "GET", c.registry+"/"+strings.Replace(name, "/", "%2f", 1), nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Accept", "application/vnd.npm.install-v1+json; q=1.0, application/json; q=0.8, */*")
	req.Header.Set("User-Agent", "lminstall")
	resp, e := secureDo(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("npm registry lookup for %s: %s", name, resp.Status)
	}
	var p npmPackument
	if e = json.NewDecoder(resp.Body).Decode(&p); e != nil {
		return nil, fmt.Errorf("npm registry metadata for %s: %w", name, e)
	}
	c.mu.Lock()
	c.cache[name] = &p
	c.mu.Unlock()
	return &p, nil
}

// resolve selects a version for an npm dependency specifier (range, exact
// version or dist-tag).
func (p *npmPackument) resolve(spec string) (*npmVersion, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		spec = "*"
	}
	if v, ok := p.DistTags[spec]; ok {
		if ver := p.Versions[v]; ver != nil {
			return ver, nil
		}
	}
	if ver := p.Versions[spec]; ver != nil {
		return ver, nil
	}
	rng, e := parseSemverRange(spec)
	if e != nil {
		return nil, fmt.Errorf("%s: %w", p.Name, e)
	}
	// npm prefers the latest dist-tag whenever it satisfies the range.
	if latest := p.DistTags["latest"]; latest != "" {
		if v, ok := parseSemver(latest); ok && rng.satisfies(v) && p.Versions[latest] != nil {
			return p.Versions[latest], nil
		}
	}
	names := make([]string, 0, len(p.Versions))
	for k := range p.Versions {
		names = append(names, k)
	}
	best, ok := maxSatisfying(names, rng)
	if !ok {
		return nil, fmt.Errorf("no version of %s satisfies %q", p.Name, spec)
	}
	return p.Versions[best], nil
}

// dependency specifiers: registry ranges and npm: aliases only.
func parseDependencySpec(spec string) (real, rng string, e error) {
	if strings.HasPrefix(spec, "npm:") {
		alias := strings.TrimPrefix(spec, "npm:")
		at := strings.LastIndex(alias, "@")
		if at <= 0 {
			return alias, "*", nil
		}
		return alias[:at], alias[at+1:], nil
	}
	for _, prefix := range []string{"file:", "git+", "git:", "github:", "http:", "https:", "link:", "workspace:"} {
		if strings.HasPrefix(spec, prefix) {
			return "", "", fmt.Errorf("unsupported dependency specifier %q", spec)
		}
	}
	if strings.Contains(spec, "/") && !strings.ContainsAny(spec, " <>=~^|") && !strings.HasPrefix(spec, ".") {
		return "", "", fmt.Errorf("unsupported dependency specifier %q", spec)
	}
	return "", spec, nil
}

type npmNode struct {
	name string // directory name under node_modules
	ver  *npmVersion
	dir  string // absolute package directory
}

type npmRequest struct {
	parent   *npmNode // nil for the root
	name     string   // dependency (alias) name
	spec     string
	optional bool
	peer     bool
}

type npmPlan struct {
	root     string // node_modules root
	nodes    map[string]*npmNode
	order    []*npmNode
	topLevel *npmNode
}

func (c *npmClient) satisfied(node *npmNode, real, rng string) bool {
	if real != "" && node.ver.Name != real {
		return false
	}
	if v, ok := parseSemver(node.ver.Version); ok {
		if r, e := parseSemverRange(rng); e == nil {
			return r.satisfies(v)
		}
	}
	return node.ver.Version == rng
}

// place decides where a dependency lives, following Node's upward lookup.
func (plan *npmPlan) place(c *npmClient, req npmRequest, real, rng string) (dir string, reuse *npmNode) {
	start := plan.root
	if req.parent != nil {
		start = filepath.Join(req.parent.dir, "node_modules")
	}
	for level := start; ; {
		if n, ok := plan.nodes[filepath.Join(level, filepath.FromSlash(req.name))]; ok {
			if c.satisfied(n, real, rng) {
				return "", n
			}
			return filepath.Join(start, filepath.FromSlash(req.name)), nil
		}
		if level == plan.root {
			break
		}
		// Step out of this package into the node_modules directory that holds it.
		up := filepath.Dir(filepath.Dir(level))
		if strings.HasPrefix(filepath.Base(up), "@") {
			up = filepath.Dir(up)
		}
		level = up
	}
	return filepath.Join(plan.root, filepath.FromSlash(req.name)), nil
}

// integrityDigest converts an npm integrity string (or legacy shasum) into the
// digest form understood by download().
func integrityDigest(v *npmVersion) (string, error) {
	var best string
	for _, token := range strings.Fields(v.Dist.Integrity) {
		algo, b64, ok := strings.Cut(token, "-")
		if !ok {
			continue
		}
		raw, e := base64.StdEncoding.DecodeString(b64)
		if e != nil {
			continue
		}
		switch algo {
		case "sha512":
			if len(raw) == 64 {
				return "sha512:" + hex.EncodeToString(raw), nil
			}
		case "sha256":
			if len(raw) == 32 {
				best = "sha256:" + hex.EncodeToString(raw)
			}
		}
	}
	if best != "" {
		return best, nil
	}
	if len(v.Dist.Shasum) == 40 {
		return "sha1:" + strings.ToLower(v.Dist.Shasum), nil
	}
	return "", fmt.Errorf("%s@%s: registry provides no usable integrity digest", v.Name, v.Version)
}

// extractNPMTarball unpacks an npm package tarball, removing its single
// top-level directory (conventionally "package/"). Links and devices are refused.
func extractNPMTarball(archive, dest string) error {
	if e := os.MkdirAll(dest, 0o755); e != nil {
		return e
	}
	root, e := os.OpenRoot(dest)
	if e != nil {
		return e
	}
	defer root.Close()
	f, e := os.Open(archive)
	if e != nil {
		return e
	}
	defer f.Close()
	gz, e := gzip.NewReader(f)
	if e != nil {
		return e
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	mkdir := func(rel string) error {
		part := ""
		for _, seg := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
			if seg == "." || seg == "" {
				continue
			}
			part = filepath.Join(part, seg)
			if err := root.Mkdir(part, 0o755); err != nil && !os.IsExist(err) {
				return err
			}
		}
		return nil
	}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			_, e = io.Copy(io.Discard, gz)
			return e
		}
		if e != nil {
			return e
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := strings.ReplaceAll(h.Name, "\\", "/")
		_, rest, found := strings.Cut(name, "/")
		if !found || rest == "" {
			continue // the top-level directory entry itself
		}
		if _, e = archivePath(dest, rest); e != nil {
			return e
		}
		rel := filepath.FromSlash(path.Clean(rest))
		switch h.Typeflag {
		case tar.TypeDir:
			if e = mkdir(rel); e != nil {
				return e
			}
		case tar.TypeReg, tar.TypeRegA:
			if e = mkdir(filepath.Dir(rel)); e != nil {
				return e
			}
			mode := os.FileMode(h.Mode).Perm() & 0o755
			mode |= 0o644
			out, e := root.OpenFile(rel, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if e != nil {
				return e
			}
			_, ce := io.Copy(out, tr)
			cl := out.Close()
			if ce != nil {
				return ce
			}
			if cl != nil {
				return cl
			}
		default:
			// npm drops links and special files from registry tarballs.
			continue
		}
	}
}

var packageName = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*$`)

func (c *npmClient) fetchPackages(ctx context.Context, names []string) error {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	errs := make([]error, len(names))
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			_, errs[i] = c.packument(ctx, name)
		}(i, name)
	}
	wg.Wait()
	return errors.Join(errs...)
}

// npmInstallInto resolves and installs the requested root package (a registry
// spec such as name@latest, or a GitHub project URL) into a fresh prefix.
func (c *npmClient) installInto(ctx context.Context, prefix, spec, goos string) (*npmNode, error) {
	nodeModules := filepath.Join(prefix, "lib", "node_modules")
	if goos == "windows" {
		nodeModules = filepath.Join(prefix, "node_modules")
	}
	if e := os.MkdirAll(nodeModules, 0o755); e != nil {
		return nil, e
	}
	plan := &npmPlan{root: nodeModules, nodes: map[string]*npmNode{}}
	tmp, e := os.MkdirTemp(prefix, "downloads-")
	if e != nil {
		return nil, e
	}
	defer os.RemoveAll(tmp)

	// Root package.
	var root *npmNode
	if strings.HasPrefix(spec, "git+https://") || strings.HasPrefix(spec, "github:") {
		var err error
		root, err = c.installGitHubRoot(ctx, plan, tmp, spec)
		if err != nil {
			return nil, err
		}
	} else {
		name, rng := spec, "latest"
		if at := strings.LastIndex(spec, "@"); at > 0 {
			name, rng = spec[:at], spec[at+1:]
		}
		if !packageName.MatchString(name) {
			return nil, fmt.Errorf("invalid npm package name %q", name)
		}
		pk, err := c.packument(ctx, name)
		if err != nil {
			return nil, err
		}
		ver, err := pk.resolve(rng)
		if err != nil {
			return nil, err
		}
		if !c.platformOK(ver) {
			return nil, fmt.Errorf("%s@%s does not support %s-%s", name, ver.Version, c.goos, c.goarch)
		}
		root = &npmNode{name: name, ver: ver, dir: filepath.Join(nodeModules, filepath.FromSlash(name))}
		if err = c.materialize(ctx, root, tmp); err != nil {
			return nil, err
		}
		plan.nodes[root.dir] = root
	}
	plan.order = append(plan.order, root)
	plan.topLevel = root

	frontier := []*npmNode{root}
	for len(frontier) > 0 {
		var requests []npmRequest
		var names []string
		for _, n := range frontier {
			for _, r := range collectRequests(n) {
				requests = append(requests, r)
				real, _, err := parseDependencySpec(r.spec)
				if err == nil {
					if real == "" {
						real = r.name
					}
					names = append(names, real)
				}
			}
		}
		sort.Strings(names)
		var unique []string
		for i, n := range names {
			if (i == 0 || n != names[i-1]) && packageName.MatchString(n) {
				unique = append(unique, n)
			}
		}
		// Optional/peer failures are tolerated individually below.
		_ = c.fetchPackages(ctx, unique)
		frontier = nil
		for _, req := range requests {
			child, err := c.resolveRequest(ctx, plan, req, tmp)
			if err != nil {
				if req.optional || req.peer {
					fmt.Fprintf(c.log, "lminstall: skipping optional %s: %v\n", req.name, err)
					continue
				}
				return nil, fmt.Errorf("%s: %w", req.name, err)
			}
			if child != nil {
				frontier = append(frontier, child)
			}
		}
	}
	return root, nil
}

func collectRequests(n *npmNode) []npmRequest {
	var out []npmRequest
	keys := func(m map[string]string) []string {
		var k []string
		for key := range m {
			k = append(k, key)
		}
		sort.Strings(k)
		return k
	}
	for _, k := range keys(n.ver.Dependencies) {
		if _, dup := n.ver.OptionalDependencies[k]; dup {
			continue
		}
		out = append(out, npmRequest{parent: n, name: k, spec: n.ver.Dependencies[k]})
	}
	for _, k := range keys(n.ver.OptionalDependencies) {
		out = append(out, npmRequest{parent: n, name: k, spec: n.ver.OptionalDependencies[k], optional: true})
	}
	for _, k := range keys(n.ver.PeerDependencies) {
		if _, ok := n.ver.Dependencies[k]; ok {
			continue
		}
		if meta, ok := n.ver.PeerDependenciesMeta[k]; ok && meta.Optional {
			continue
		}
		out = append(out, npmRequest{parent: n, name: k, spec: n.ver.PeerDependencies[k], peer: true})
	}
	return out
}

// resolveRequest installs one dependency. It returns the new node (whose own
// dependencies must be processed) or nil when an installed copy is reused.
func (c *npmClient) resolveRequest(ctx context.Context, plan *npmPlan, req npmRequest, tmp string) (*npmNode, error) {
	real, rng, e := parseDependencySpec(req.spec)
	if e != nil {
		return nil, e
	}
	if real == "" {
		real = req.name
	}
	if !packageName.MatchString(real) || !packageName.MatchString(req.name) {
		return nil, fmt.Errorf("invalid dependency name %q", req.name)
	}
	alias := ""
	if real != req.name {
		alias = real
	}
	dir, reuse := plan.place(c, req, alias, rng)
	if reuse != nil {
		return nil, nil
	}
	pk, e := c.packument(ctx, real)
	if e != nil {
		return nil, e
	}
	ver, e := pk.resolve(rng)
	if e != nil {
		return nil, e
	}
	if !c.platformOK(ver) {
		if req.optional {
			return nil, nil // normal npm behaviour: silently skipped
		}
		return nil, fmt.Errorf("%s@%s does not support %s-%s", real, ver.Version, c.goos, c.goarch)
	}
	node := &npmNode{name: req.name, ver: ver, dir: dir}
	if e = c.materialize(ctx, node, tmp); e != nil {
		if req.optional {
			return nil, e
		}
		return nil, e
	}
	plan.nodes[dir] = node
	plan.order = append(plan.order, node)
	return node, nil
}

func (c *npmClient) materialize(ctx context.Context, n *npmNode, tmp string) error {
	digest, e := integrityDigest(n.ver)
	if e != nil {
		return e
	}
	loc := n.ver.Dist.Tarball
	if u, err := url.Parse(loc); err != nil || u.Scheme != "https" {
		return fmt.Errorf("%s@%s: tarball URL must be HTTPS: %q", n.ver.Name, n.ver.Version, loc)
	}
	file := filepath.Join(tmp, strings.NewReplacer("/", "_", "@", "").Replace(n.ver.Name)+"-"+n.ver.Version+".tgz")
	if e = download(ctx, loc, file, digest); e != nil {
		return fmt.Errorf("%s@%s: %w", n.ver.Name, n.ver.Version, e)
	}
	defer os.Remove(file)
	if n.ver.HasInstallScript {
		fmt.Fprintf(c.log, "lminstall: %s@%s declares install scripts; lifecycle scripts are never executed\n", n.ver.Name, n.ver.Version)
	}
	return extractNPMTarball(file, n.dir)
}

var githubProject = regexp.MustCompile(`^(?:git\+https://github\.com/|github:)([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?(?:#([A-Za-z0-9_./-]+))?$`)

// installGitHubRoot installs a GitHub project the way npm does for a git spec,
// from GitHub's source tarball for the default branch (or #ref).
func (c *npmClient) installGitHubRoot(ctx context.Context, plan *npmPlan, tmp, spec string) (*npmNode, error) {
	m := githubProject.FindStringSubmatch(spec)
	if m == nil {
		return nil, fmt.Errorf("unsupported git npm specifier %q (only github.com projects are supported)", spec)
	}
	ref := m[3]
	if ref == "" {
		ref = "HEAD"
	}
	file := filepath.Join(tmp, "project.tgz")
	if e := download(ctx, "https://codeload.github.com/"+m[1]+"/"+m[2]+"/tar.gz/"+ref, file, ""); e != nil {
		return nil, e
	}
	stage := filepath.Join(tmp, "project")
	if e := extractNPMTarball(file, stage); e != nil {
		return nil, e
	}
	b, e := os.ReadFile(filepath.Join(stage, "package.json"))
	if e != nil {
		return nil, fmt.Errorf("GitHub project %s/%s has no package.json: %w", m[1], m[2], e)
	}
	var v npmVersion
	if e = json.Unmarshal(b, &v); e != nil {
		return nil, e
	}
	if !packageName.MatchString(v.Name) {
		return nil, fmt.Errorf("invalid package name %q in %s/%s", v.Name, m[1], m[2])
	}
	for _, hook := range []string{"preinstall", "install", "postinstall", "prepare"} {
		if v.Scripts[hook] != "" {
			fmt.Fprintf(c.log, "lminstall: %s declares a %s script; lifecycle scripts are never executed\n", v.Name, hook)
		}
	}
	dir := filepath.Join(plan.root, filepath.FromSlash(v.Name))
	if e = os.MkdirAll(filepath.Dir(dir), 0o755); e != nil {
		return nil, e
	}
	if e = os.Rename(stage, dir); e != nil {
		return nil, e
	}
	n := &npmNode{name: v.Name, ver: &v, dir: dir}
	plan.nodes[dir] = n
	return n, nil
}

// binEntries returns bin name -> relative script path for a package.
func binEntries(v *npmVersion) (map[string]string, error) {
	if len(v.Bin) == 0 || string(v.Bin) == "null" {
		return nil, nil
	}
	var one string
	if json.Unmarshal(v.Bin, &one) == nil {
		name := v.Name
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		return map[string]string{name: one}, nil
	}
	var many map[string]string
	if e := json.Unmarshal(v.Bin, &many); e != nil {
		return nil, fmt.Errorf("invalid bin field in %s: %w", v.Name, e)
	}
	return many, nil
}

// linkBins publishes the root package's executables the way a global npm
// install does: <prefix>/bin symlinks on POSIX, .cmd shims in the prefix on Windows.
func linkBins(prefix, goos string, root *npmNode) error {
	bins, e := binEntries(root.ver)
	if e != nil {
		return e
	}
	for name, entry := range bins {
		if name == "" || strings.ContainsAny(name, `/\:`) || name == ".." || name == "." {
			return fmt.Errorf("unsafe bin name %q", name)
		}
		target, e := relative(root.dir, entry)
		if e != nil {
			return e
		}
		if _, e = os.Stat(target); e != nil {
			return fmt.Errorf("%s: declared bin %s is missing", root.ver.Name, entry)
		}
		if goos == "windows" {
			rel, e := filepath.Rel(prefix, target)
			if e != nil {
				return e
			}
			if strings.ContainsAny(rel, "\r\n\"%") {
				return fmt.Errorf("package path cannot be represented by a Windows shim: %q", rel)
			}
			var text string
			if strings.EqualFold(filepath.Ext(target), ".exe") {
				text = "@ECHO off\r\n\"%~dp0\\" + rel + "\" %*\r\n"
			} else {
				text = "@ECHO off\r\nSETLOCAL\r\nSET \"_prog=node\"\r\nIF EXIST \"%~dp0\\node.exe\" SET \"_prog=%~dp0\\node.exe\"\r\n\"%_prog%\" \"%~dp0\\" + rel + "\" %*\r\n"
			}
			if e = os.WriteFile(filepath.Join(prefix, name+".cmd"), []byte(text), 0o644); e != nil {
				return e
			}
			continue
		}
		if e = os.Chmod(target, 0o755); e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); e != nil {
			return e
		}
		rel, e := filepath.Rel(filepath.Join(prefix, "bin"), target)
		if e != nil {
			return e
		}
		link := filepath.Join(prefix, "bin", name)
		os.Remove(link)
		if e = os.Symlink(rel, link); e != nil {
			return e
		}
	}
	return nil
}

// npm installs a registry or GitHub package into <provider>/npm with no external
// tools, verifies the published command, then atomically swaps it into place.
func (m *Manager) npm(ctx context.Context, p Provider, r Recipe, dir string) (string, string, string, error) {
	stage, e := os.MkdirTemp(dir, "npm-stage-")
	if e != nil {
		return "", "", "", e
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(stage)
		}
	}()
	client := newNPMClient(m, m.Log)
	root, e := client.installInto(ctx, stage, r.Package, m.OS)
	if e != nil {
		return "", "", "", e
	}
	if e = linkBins(stage, m.OS, root); e != nil {
		return "", "", "", e
	}
	bin := m.expand(r.Bin, dir)
	binRel := filepath.ToSlash(bin)
	if !strings.HasPrefix(binRel, "npm/") {
		return "", "", "", fmt.Errorf("npm recipe binary must be below npm/: %s", bin)
	}
	staged := filepath.ToSlash(filepath.Join(filepath.Base(stage), strings.TrimPrefix(binRel, "npm/")))
	version, e := m.verify(ctx, p, r, staged)
	if e != nil {
		return "", "", "", e
	}
	final := filepath.Join(dir, "npm")
	if e = os.RemoveAll(final); e != nil {
		return "", "", "", e
	}
	if e = os.Rename(stage, final); e != nil {
		return "", "", "", e
	}
	keep = true
	return bin, m.expand(r.BinDir, dir), version, nil
}
