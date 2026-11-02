package install

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type ExecRunner struct{ Log io.Writer }

func (r ExecRunner) Run(ctx context.Context, args []string, cwd string, env map[string]string, capture bool) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("empty command")
	}
	for _, arg := range args {
		ext := strings.ToLower(filepath.Ext(arg))
		if ext == ".sh" || ext == ".ps1" {
			return "", fmt.Errorf("installer scripts are unsupported: %s", arg)
		}
	}
	program, e := prerequisite(args[0])
	if e != nil {
		return "", e
	}
	argv := args[1:]
	merged := map[string]string{}
	for _, item := range os.Environ() {
		k, v, ok := strings.Cut(item, "=")
		if ok {
			if runtime.GOOS == "windows" {
				k = strings.ToUpper(k)
			}
			merged[k] = v
		}
	}
	for k, v := range env {
		if runtime.GOOS == "windows" {
			k = strings.ToUpper(k)
		}
		merged[k] = v
	}
	// PowerShell splatting passes literal array elements to Windows npm/provider .cmd shims.
	// Root paths and package arguments are data in environment JSON, never script text.
	if runtime.GOOS == "windows" && (strings.EqualFold(filepath.Ext(program), ".cmd") || strings.EqualFold(filepath.Ext(program), ".bat")) {
		b, _ := json.Marshal(argv)
		merged["LMINSTALL_COMMAND"] = program
		merged["LMINSTALL_ARGS"] = string(b)
		program = "powershell.exe"
		argv = []string{"-NoProfile", "-NonInteractive", "-Command", `$a=ConvertFrom-Json -InputObject $env:LMINSTALL_ARGS; & $env:LMINSTALL_COMMAND @a; exit $LASTEXITCODE`}
	}
	cmd := exec.CommandContext(ctx, program, argv...)
	cmd.Dir = cwd
	for k, v := range merged {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	log := r.Log
	if log == nil {
		log = io.Discard
	}
	var writer io.Writer = log
	if capture {
		writer = io.MultiWriter(log, &out)
	}
	// One shared writer/pipe also preserves order between stdout and stderr; no bounded capture.
	cmd.Stdout = writer
	cmd.Stderr = writer
	cmd.Stdin = nil
	e = runOwned(cmd)
	if e != nil {
		return out.String(), fmt.Errorf("%s: %w", args[0], e)
	}
	return out.String(), nil
}
