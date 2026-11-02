package launch

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lmctl-ai/lminstall/internal/install"
)

type Options struct{ Provider, Backend, BaseURL, Model, CredentialEnv, ProfileDir, InstallRoot string }
type Profile struct {
	Version       int                `json:"version"`
	Provider      string             `json:"provider"`
	Backend       string             `json:"backend"`
	BaseURL       string             `json:"base_url"`
	Model         string             `json:"model"`
	CredentialEnv string             `json:"credential_env,omitempty"`
	InstallRoot   string             `json:"install_root"`
	Spec          install.LaunchSpec `json:"launch_config"`
}
type Plan struct {
	Profile          Profile           `json:"profile"`
	ProfileDir       string            `json:"profile_dir"`
	Environment      map[string]string `json:"environment"`
	Files            map[string]string `json:"files"`
	ArgvPrefix       []string          `json:"argv_prefix"`
	CredentialPolicy string            `json:"credential_policy"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var allowedAdapters = map[string]bool{"claude": true, "codex": true, "gemini": true, "copilot": true, "opencode": true, "kimi": true, "qwen": true, "pi": true, "dsh": true, "miniplayer": true, "lmplayer": true, "hermes": true, "agy": true}

const ownerFile = ".lminstall-launch-owner"
const profileFile = "profile.json"

func safeValue(s string) bool { return !strings.ContainsAny(s, "\r\n\x00") }
func canonicalBase(raw, backend, shape string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !safeValue(raw) {
		return "", errors.New("invalid_base_url")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", errors.New("invalid_base_url_scheme")
	}
	if backend == "mock" {
		host := u.Hostname()
		ip := net.ParseIP(host)
		if u.Scheme != "http" || ip == nil || !ip.IsLoopback() {
			return "", errors.New("mock_requires_http_loopback")
		}
	} else if backend == "ollama-cloud" {
		if u.Scheme != "https" || u.Hostname() != "ollama.com" || (u.Port() != "" && u.Port() != "443") || strings.Trim(u.Path, "/") != "" {
			return "", errors.New("ollama_cloud_requires_official_origin")
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if shape == "v1" {
		u.Path = strings.TrimSuffix(u.Path, "/v1")
	} else if shape != "origin" {
		return "", errors.New("unsupported_base_shape")
	}
	if shape == "origin" && strings.HasSuffix(u.Path, "/v1") {
		return "", errors.New("origin_base_must_not_end_in_v1")
	}
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/"), nil
}
func Configure(manifest install.Manifest, o Options) (Plan, error) {
	var empty Plan
	provider, e := manifest.Provider(o.Provider)
	if e != nil {
		return empty, errors.New("unknown_provider")
	}
	if provider.LaunchConfig.Version != 1 {
		return empty, errors.New("unsupported_launch_config_version")
	}
	spec, ok := provider.LaunchConfig.Backends[o.Backend]
	if !ok || !spec.Supported {
		return empty, fmt.Errorf("unsupported_backend: %s/%s", o.Provider, o.Backend)
	}
	if spec.AdapterVersion != 1 || !allowedAdapters[spec.Adapter] {
		return empty, errors.New("unsupported_config_adapter")
	}
	if !filepath.IsAbs(o.ProfileDir) || !safeValue(o.ProfileDir) || o.ProfileDir == "" {
		return empty, errors.New("profile_dir_must_be_absolute")
	}
	if o.Model == "" || !safeValue(o.Model) {
		return empty, errors.New("model_required")
	}
	if o.CredentialEnv != "" && (!envName.MatchString(o.CredentialEnv) || reservedCredentialName(o.CredentialEnv)) {
		return empty, errors.New("invalid_credential_env_name")
	}
	if o.Backend == "ollama-cloud" && o.CredentialEnv == "" {
		return empty, errors.New("credential_env_required")
	}
	base, e := canonicalBase(o.BaseURL, o.Backend, spec.BaseShape)
	if e != nil {
		return empty, e
	}
	root, e := filepath.Abs(o.InstallRoot)
	if e != nil || !safeValue(root) {
		return empty, errors.New("invalid_install_root")
	}
	dir, e := ownProfile(o.ProfileDir, o.Provider, true)
	if e != nil {
		return empty, e
	}
	lock, e := os.OpenFile(filepath.Join(dir, ".configure.lock"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return empty, errors.New("profile_busy")
	}
	lock.Close()
	defer os.Remove(filepath.Join(dir, ".configure.lock"))
	p := Profile{Version: 1, Provider: o.Provider, Backend: o.Backend, BaseURL: base, Model: o.Model, CredentialEnv: o.CredentialEnv, InstallRoot: root, Spec: spec}
	preview := filepath.Join(dir, "preview")
	env, files, args, e := Render(p, preview, base, "lminstall-placeholder")
	if e != nil {
		return empty, e
	}
	if e = writeFiles(preview, files); e != nil {
		return empty, e
	}
	b, e := json.MarshalIndent(p, "", "  ")
	if e != nil {
		return empty, e
	}
	if e = atomicFile(filepath.Join(dir, profileFile), append(b, '\n')); e != nil {
		return empty, errors.New("profile_write_failed")
	}
	return Plan{Profile: p, ProfileDir: dir, Environment: env, Files: files, ArgvPrefix: args, CredentialPolicy: "runtime environment reference; upstream authorization bound in memory; child uses a nonsecret placeholder"}, nil
}
func reservedCredentialName(s string) bool {
	switch strings.ToUpper(s) {
	case "PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "PATHEXT", "COMSPEC":
		return true
	}
	return strings.HasPrefix(strings.ToUpper(s), "LMINSTALL_")
}
func ownProfile(dir, provider string, create bool) (string, error) {
	if create {
		if e := os.MkdirAll(dir, 0700); e != nil {
			return "", errors.New("profile_create_failed")
		}
	}
	info, e := os.Lstat(dir)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("profile_directory_refused")
	}
	canonical, e := filepath.EvalSymlinks(dir)
	if e != nil {
		return "", errors.New("profile_directory_refused")
	}
	owner := filepath.Join(canonical, ownerFile)
	b, e := os.ReadFile(owner)
	if e == nil {
		if string(b) != "launch-v1:"+provider+"\n" {
			return "", errors.New("foreign_profile_refused")
		}
		return canonical, nil
	}
	if !create || !os.IsNotExist(e) {
		return "", errors.New("unowned_profile_refused")
	}
	entries, e := os.ReadDir(canonical)
	if e != nil || len(entries) != 0 {
		return "", errors.New("nonempty_unowned_profile_refused")
	}
	f, e := os.OpenFile(owner, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", errors.New("profile_owner_write_failed")
	}
	_, e = f.WriteString("launch-v1:" + provider + "\n")
	ce := f.Close()
	if e != nil || ce != nil {
		return "", errors.New("profile_owner_write_failed")
	}
	return canonical, nil
}
func Load(dir, provider string) (Profile, string, error) {
	var p Profile
	if !filepath.IsAbs(dir) {
		return p, "", errors.New("profile_dir_must_be_absolute")
	}
	canonical, e := ownProfile(dir, provider, false)
	if e != nil {
		return p, "", e
	}
	b, e := os.ReadFile(filepath.Join(canonical, profileFile))
	if e != nil || json.Unmarshal(b, &p) != nil {
		return p, "", errors.New("invalid_profile")
	}
	if (p.Backend != "mock" && p.Backend != "ollama-cloud") || p.Version != 1 || p.Provider != provider || !p.Spec.Supported || p.Spec.AdapterVersion != 1 || !allowedAdapters[p.Spec.Adapter] {
		return p, "", errors.New("invalid_profile_version_or_provider")
	}
	if _, e = canonicalBase(p.BaseURL, p.Backend, p.Spec.BaseShape); e != nil {
		return p, "", e
	}
	return p, canonical, nil
}
func atomicFile(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".profile-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(name, path)
}
func privatePath(root, rel string) (string, error) {
	if filepath.IsAbs(rel) || rel == "" {
		return "", errors.New("invalid_config_path")
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	r, e := filepath.Rel(root, path)
	if e != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", errors.New("config_path_escape")
	}
	// Refuse existing symlinks at any adapter path rather than writing through them.
	current := root
	for _, part := range strings.Split(r, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if st, e := os.Lstat(current); e == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("config_symlink_refused")
		}
	}
	return path, nil
}
func writeFiles(root string, files map[string]string) error {
	if st, e := os.Lstat(root); e == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
		return errors.New("runtime_directory_refused")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return e
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, rel := range keys {
		path, e := privatePath(root, rel)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		if e = atomicFile(path, []byte(files[rel])); e != nil {
			return e
		}
	}
	return nil
}
func quote(s string) string { b, _ := json.Marshal(s); return string(b) }
func expand(s string, v map[string]string) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := []string{}
	for _, k := range keys {
		pairs = append(pairs, "{"+k+"}", v[k])
		pairs = append(pairs, "{"+k+"_json}", quote(v[k]))
	}
	return strings.NewReplacer(pairs...).Replace(s)
}
func expandJSON(value any, v map[string]string) any {
	switch x := value.(type) {
	case string:
		return expand(x, v)
	case map[string]any:
		out := map[string]any{}
		for k, a := range x {
			out[expand(k, v)] = expandJSON(a, v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, a := range x {
			out[i] = expandJSON(a, v)
		}
		return out
	default:
		return value
	}
}
func Render(p Profile, root, base, credential string) (map[string]string, map[string]string, []string, error) {
	home := filepath.Join(root, "home")
	work := filepath.Join(root, "work")
	v := map[string]string{"home": filepath.ToSlash(home), "profile": filepath.ToSlash(root), "install": filepath.ToSlash(filepath.Join(p.InstallRoot, "providers", p.Provider)), "base": base, "v1": strings.TrimRight(base, "/") + "/v1", "model": p.Model, "credential": credential}
	env := map[string]string{"HOME": home, "USERPROFILE": home, "XDG_CONFIG_HOME": filepath.Join(home, "config"), "XDG_DATA_HOME": filepath.Join(home, "data"), "XDG_CACHE_HOME": filepath.Join(home, "cache"), "XDG_STATE_HOME": filepath.Join(home, "state"), "LOCALAPPDATA": filepath.Join(home, "local"), "APPDATA": filepath.Join(home, "roaming"), "CI": "true", "NO_COLOR": "1", "DO_NOT_TRACK": "1"}
	for k, a := range p.Spec.Environment {
		env[k] = expand(a, v)
	}
	files := map[string]string{}
	for _, f := range p.Spec.Files {
		rel := filepath.Join("home", filepath.FromSlash(f.Path))
		if _, e := privatePath(root, rel); e != nil {
			return nil, nil, nil, e
		}
		var b []byte
		var e error
		switch f.Format {
		case "json":
			b, e = json.Marshal(expandJSON(f.Data, v))
		case "text":
			b = []byte(expand(f.Text, v))
		default:
			return nil, nil, nil, errors.New("unsupported_config_format")
		}
		if e != nil {
			return nil, nil, nil, e
		}
		files[filepath.ToSlash(rel)] = string(b)
	}
	args := make([]string, len(p.Spec.ArgvPrefix))
	for i, a := range p.Spec.ArgvPrefix {
		args[i] = expand(a, v)
	}
	_ = work
	return env, files, args, nil
}
func GitHubEnv(plan Plan) string {
	values := map[string]string{"LMINSTALL_PROFILE_DIR": plan.ProfileDir, "LMINSTALL_PROVIDER": plan.Profile.Provider, "LMINSTALL_BACKEND": plan.Profile.Backend, "LMINSTALL_MODEL": plan.Profile.Model}
	keys := []string{}
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out strings.Builder
	for _, k := range keys {
		out.WriteString(k + "=" + values[k] + "\n")
	}
	return out.String()
}
func nonce() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic("randomness_unavailable")
	}
	return "lminstall-" + hex.EncodeToString(b[:])
}
