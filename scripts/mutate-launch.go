// Run with: go run scripts/mutate-launch.go
// Production-copy controls must fail assertions, not compilation.
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
		{"credential persisted", "internal/launch/profile.go", "CredentialEnv: o.CredentialEnv", "CredentialEnv: os.Getenv(o.CredentialEnv)"},
		{"duplicated v1", "internal/launch/profile.go", `strings.TrimRight(base, "/") + "/v1"`, `strings.TrimRight(base, "/") + "/v1/v1"`},
		{"adapter files omitted", "internal/launch/profile.go", "for _, f := range p.Spec.Files {", "for _, f := range []install.LaunchFile{} {"},
		{"inherited secret", "internal/launch/run.go", "ok && keep[strings.ToUpper(k)]", `ok && (keep[strings.ToUpper(k)] || strings.Contains(k,"SECRET"))`},
		{"response credential reaches child", "internal/launch/bridge.go", "filter := redactor(w, patterns)", "filter := redactor(w, nil)"},
		{"redaction disabled", "internal/launch/redact.go", "patterns: patterns", "patterns: nil"},
		{"foreign profile adopted", "internal/launch/profile.go", "e != nil || len(entries) != 0", "e != nil || (false && len(entries) != 0)"},
		{"native npm through Node", "internal/install/launch_exec.go", `if strings.EqualFold(filepath.Ext(path), ".exe") {`, `if false && strings.EqualFold(filepath.Ext(path), ".exe") {`},
		{"wrong reply accepted", "cmd/lminstall-mock-e2e/main.go", "if observed != wanted {", "if false {"},
		{"unicode credential reflected", "internal/launch/redact.go", "set[upper.String()] = true", "delete(set, upper.String())"},
		{"routing environment omitted", "internal/launch/profile.go", "env[k] = expand(a, v)", "env[k] = expand(a, v)[:0]"},
		{"foreign gateway allowed", "internal/launch/cleanup.go", "got != expected", "false && got != expected"},
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
		return cmd.CombinedOutput()
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
