// Run with: go run scripts/mutate-launch.go
// Production-copy controls must fail assertions, not compilation.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type mutation struct{ name, file, before, after string }

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(source))
	tmp, e := os.MkdirTemp("", "lminstall-launch-controls-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	copy := filepath.Join(tmp, "source")
	e = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "build" || d.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(copy, rel), 0700)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source symlink refused: %s", rel)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		info, err := d.Info()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(filepath.Join(copy, rel), os.O_CREATE|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if e != nil {
		return e
	}
	cases := []mutation{
		{"release next page ignored", "internal/install/archive.go", "endpoint = u.ResolveReference(ref).String()", "endpoint = u.ResolveReference(ref).String()[:0]"},
		{"release foreign page trusted", "internal/install/archive.go", `u.Host != "api.github.com"`, `false`},
		{"Windows POSIX launcher unchecked", "internal/install/launcher.go", "if err != nil || errRead != nil || string(contents) != want {", "if false && (err != nil || errRead != nil || string(contents) != want) {"},
		{"padded credential accepted by run", "internal/launch/run.go", "if !validCredential(credential) {", "if !safeValue(credential) {"},
		{"padded credential accepted by bridge", "internal/launch/bridge.go", "if !validCredential(credential) {", "if !safeValue(credential) {"},
		{"unsupported base path accepted", "internal/launch/profile.go", `if u.Path != "" {`, `if false {`},
		{"provider exit gains launcher diagnostic", "internal/launch/run.go", "return code, nil", `return code, errors.New("provider_run_failed")`},
		{"credential persisted", "internal/launch/profile.go", "CredentialEnv: o.CredentialEnv", "CredentialEnv: os.Getenv(o.CredentialEnv)"},
		{"duplicated v1", "internal/launch/profile.go", `strings.TrimRight(base, "/") + "/v1"`, `strings.TrimRight(base, "/") + "/v1/v1"`},
		{"adapter files omitted", "internal/launch/profile.go", "for _, f := range p.Spec.Files {", "for _, f := range []install.LaunchFile{} {"},
		{"inherited secret", "internal/launch/run.go", "ok && keep[strings.ToUpper(k)]", `ok && (keep[strings.ToUpper(k)] || strings.Contains(k,"SECRET"))`},
		{"response credential reaches child", "internal/launch/body.go", "body.filter = redactor(&body.output, patterns)", "body.filter = redactor(&body.output, nil)"},
		{"redaction disabled", "internal/launch/redact.go", "patterns: patterns", "patterns: nil"},
		{"foreign profile adopted", "internal/launch/profile.go", "e != nil || len(entries) != 0", "e != nil || (false && len(entries) != 0)"},
		{"native npm through Node", "internal/install/launch_exec.go", `if strings.EqualFold(filepath.Ext(path), ".exe") {`, `if false && strings.EqualFold(filepath.Ext(path), ".exe") {`},
		{"wrong reply accepted", "cmd/lminstall-mock-e2e/main.go", "if observed != wanted {", "if false {"},
		{"unicode credential reflected", "internal/launch/redact.go", "set[upper.String()] = true", "delete(set, upper.String())"},
		{"routing environment omitted", "internal/launch/profile.go", "env[k] = expand(a, v)", "env[k] = expand(a, v)[:0]"},
		{"response headers unfiltered", "internal/launch/bridge.go", "sanitizeMetadata(resp.Header, patterns)", "sanitizeMetadata(resp.Header, nil)"},
		{"response trailers unfiltered", "internal/launch/body.go", "b.afterEOF()", "if false { b.afterEOF() }"},
		{"foreign gateway allowed", "internal/launch/cleanup.go", "got != expected", "false && got != expected"},
	}
	// Delete each argv element independently in every supported backend. This
	// includes isolation switches, their values and positional subcommands.
	catalog, e := os.ReadFile(filepath.Join(copy, "internal/install/providers.json"))
	if e != nil {
		return e
	}
	var document map[string]any
	if e = json.Unmarshal(catalog, &document); e != nil {
		return e
	}
	for pi, raw := range document["providers"].([]any) {
		provider := raw.(map[string]any)
		config, ok := provider["launch_config"].(map[string]any)
		if !ok {
			continue
		}
		backends, ok := config["backends"].(map[string]any)
		if !ok {
			continue
		}
		names := []string{}
		for name := range backends {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, backend := range names {
			spec := backends[backend].(map[string]any)
			if spec["supported"] != true {
				continue
			}
			argv, _ := spec["argv_prefix"].([]any)
			for ai, arg := range argv {
				var changed map[string]any
				if e = json.Unmarshal(catalog, &changed); e != nil {
					return e
				}
				cs := changed["providers"].([]any)[pi].(map[string]any)["launch_config"].(map[string]any)["backends"].(map[string]any)[backend].(map[string]any)
				args := append([]any{}, argv[:ai]...)
				args = append(args, argv[ai+1:]...)
				cs["argv_prefix"] = args
				data, err := json.MarshalIndent(changed, "", "  ")
				if err != nil {
					return err
				}
				cases = append(cases, mutation{fmt.Sprintf("required argv %s/%s[%d] %q", provider["id"], backend, ai, arg), "internal/install/providers.json", string(catalog), string(data)})
			}
		}
	}
	originals := map[string]string{}
	for _, c := range cases {
		b, e := os.ReadFile(filepath.Join(copy, c.file))
		if e != nil {
			return e
		}
		originals[c.file] = string(b)
	}
	test := func() ([]byte, error) {
		cmd := exec.Command("go", "test", "-count=1", "./...")
		cmd.Dir = copy
		b, err := cmd.CombinedOutput()
		if err != nil || runtime.GOOS == "windows" {
			return b, err
		}
		// Exercise the actual Windows validation branch on other test hosts too.
		// Only OS selection changes in this compile overlay, never validation.
		path := filepath.Join(copy, "internal/install/launcher.go")
		source, err := os.ReadFile(path)
		if err != nil {
			return b, err
		}
		branch := filepath.Join(tmp, "windows-branch.go")
		if err = os.WriteFile(branch, []byte(strings.ReplaceAll(string(source), `runtime.GOOS == "windows"`, `(m.OS == "windows" && runtime.GOOS != "")`)), 0600); err != nil {
			return b, err
		}
		overlay, _ := json.Marshal(map[string]any{"Replace": map[string]string{path: branch}})
		overlayFile := filepath.Join(tmp, "overlay.json")
		if err = os.WriteFile(overlayFile, overlay, 0600); err != nil {
			return b, err
		}
		windows := exec.Command("go", "test", "-overlay", overlayFile, "-count=1", "./internal/install", "-run", "TestWindowsRequiresBothOwnedLaunchers")
		windows.Dir = copy
		windows.Env = append(os.Environ(), "LMINSTALL_TEST_WINDOWS_BRANCH=1")
		extra, err := windows.CombinedOutput()
		return append(b, extra...), err
	}
	b, e := test()
	if e != nil {
		return fmt.Errorf("baseline failed: %s", b)
	}
	fmt.Println("BASELINE PASS")
	for _, c := range cases {
		for name, value := range originals {
			if e = os.WriteFile(filepath.Join(copy, name), []byte(value), 0600); e != nil {
				return e
			}
		}
		if strings.Count(originals[c.file], c.before) != 1 {
			return fmt.Errorf("mutation anchor mismatch: %s", c.name)
		}
		if e = os.WriteFile(filepath.Join(copy, c.file), []byte(strings.Replace(originals[c.file], c.before, c.after, 1)), 0600); e != nil {
			return e
		}
		b, e = test()
		if e == nil {
			return fmt.Errorf("SURVIVED %s", c.name)
		}
		if !strings.Contains(string(b), "--- FAIL: Test") || strings.Contains(string(b), "[build failed]") {
			return fmt.Errorf("control did not fail an assertion: %s\n%s", c.name, b)
		}
		fmt.Println("CAUGHT", c.name)
	}
	fmt.Printf("PASS %d launch controls\n", len(cases))
	return nil
}
