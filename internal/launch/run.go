package launch

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lmctl-ai/lminstall/internal/install"
)

type IO struct {
	In       io.Reader
	Out, Err io.Writer
}

func cleanEnvironment(overrides map[string]string) []string {
	keep := map[string]bool{"PATH": true, "SYSTEMROOT": true, "WINDIR": true, "COMSPEC": true, "PATHEXT": true, "TEMP": true, "TMP": true, "LANG": true, "LC_ALL": true, "TERM": true, "PROCESSOR_ARCHITECTURE": true, "PROCESSOR_ARCHITEW6432": true}
	values := map[string]string{}
	for _, item := range os.Environ() {
		k, v, ok := strings.Cut(item, "=")
		if ok && keep[strings.ToUpper(k)] {
			if runtime.GOOS == "windows" {
				k = strings.ToUpper(k)
			}
			values[k] = v
		}
	}
	for k, v := range overrides {
		if runtime.GOOS == "windows" {
			k = strings.ToUpper(k)
		}
		values[k] = v
	}
	out := []string{}
	for k, v := range values {
		out = append(out, k+"="+v)
	}
	return out
}
func Run(ctx context.Context, manifest install.Manifest, provider, dir string, args []string, streams IO) (int, error) {
	p, canonical, e := Load(dir, provider)
	if e != nil {
		return 2, e
	}
	credential := "lminstall-mock-key"
	patterns := [][]byte{}
	if p.CredentialEnv != "" {
		credential = os.Getenv(p.CredentialEnv)
		if credential == "" {
			return 1, errors.New("credential_environment_missing")
		}
		if !safeValue(credential) {
			return 1, errors.New("credential_environment_invalid")
		}
		patterns = sensitiveEncodings(credential)
	} else if p.Backend != "mock" {
		return 1, errors.New("credential_environment_required")
	}
	for _, a := range args {
		if redactString(a, patterns) != a {
			return 2, errors.New("credential_in_argv_refused")
		}
	}
	manager := install.Manager{Root: p.InstallRoot, OS: runtime.GOOS, Arch: runtime.GOARCH, Manifest: manifest}
	// Resolve before creating a runtime or contacting any upstream server.
	if _, e = manager.LaunchCommand(provider, nil); e != nil {
		return 1, e
	}
	runs := filepath.Join(canonical, "runs")
	if st, err := os.Lstat(runs); err == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
		return 1, errors.New("runtime_directory_refused")
	}
	if e = os.MkdirAll(runs, 0700); e != nil {
		return 1, errors.New("runtime_create_failed")
	}
	root, e := os.MkdirTemp(runs, "run-")
	if e != nil {
		return 1, errors.New("runtime_create_failed")
	}
	defer os.RemoveAll(root)
	placeholder := nonce()
	bridge, e := startBridge(ctx, p, credential, placeholder, patterns)
	if e != nil {
		return 1, e
	}
	defer bridge.Close()
	env, files, prefix, e := Render(p, root, bridge.url, placeholder)
	if e != nil {
		return 2, errors.New("config_render_failed")
	}
	if e = writeFiles(root, files); e != nil {
		return 1, errors.New("runtime_config_write_failed")
	}
	for _, path := range env {
		if filepath.IsAbs(path) && strings.HasPrefix(path, root) {
			os.MkdirAll(path, 0700)
		}
	}
	work := filepath.Join(root, "work")
	if e = os.MkdirAll(work, 0700); e != nil {
		return 1, errors.New("runtime_work_create_failed")
	}
	command, e := manager.LaunchCommand(provider, append(prefix, args...))
	if e != nil {
		return 1, e
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env = cleanEnvironment(env)
	cmd.Dir = work
	cmd.Stdin = streams.In
	if streams.Out == nil {
		streams.Out = io.Discard
	}
	if streams.Err == nil {
		streams.Err = io.Discard
	}
	out := redactor(streams.Out, patterns)
	errout := redactor(streams.Err, patterns)
	cmd.Stdout = out
	cmd.Stderr = errout
	e = executeRuntime(cmd, provider == "hermes")
	out.Close()
	errout.Close()
	// The profile has a unique empty HOME for this invocation. Gateway cleanup is
	// provider-specific and must validate its recorded HOME/process identity.
	if provider == "hermes" {
		if cleanupErr := cleanupHermes(env["HERMES_HOME"]); cleanupErr != nil {
			return 1, cleanupErr
		}
	}
	if e != nil {
		var status *exec.ExitError
		if errors.As(e, &status) {
			code := status.ExitCode()
			if code < 1 {
				code = 1
			}
			return code, errors.New("provider_run_failed")
		}
		return 1, errors.New("provider_start_failed")
	}
	return 0, nil
}
