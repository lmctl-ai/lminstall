package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/lmctl-ai/lminstall/internal/install"
	"github.com/lmctl-ai/lminstall/internal/launch"
)

func launchCLI(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "provider_required")
		return 2
	}
	operation, provider := args[0], args[1]
	flags := flag.NewFlagSet(operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	backend := flags.String("backend", "", "backend")
	base := flags.String("base-url", "", "base URL")
	model := flags.String("model", "", "model")
	credential := flags.String("credential-env", "", "environment variable name")
	dir := flags.String("profile-dir", "", "absolute profile directory")
	runtimeDir := flags.String("runtime-dir", "", "absolute persistent runtime directory (env)")
	format := flags.String("format", "plan", "plan, github-env or sh")
	root := flags.String("root", "", "owned installation root")
	manifestPath := flags.String("manifest", "", "trusted manifest")
	if flags.Parse(args[2:]) != nil {
		fmt.Fprintln(os.Stderr, "invalid_launch_arguments")
		return 2
	}
	m, e := install.LoadManifest()
	if *manifestPath != "" {
		b, err := os.ReadFile(*manifestPath)
		if err != nil {
			e = err
		} else {
			m = install.Manifest{}
			e = json.Unmarshal(b, &m)
			if e == nil {
				e = install.Validate(m)
			}
		}
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, "invalid_manifest")
		return 2
	}
	if operation == "configure" {
		if len(flags.Args()) != 0 || (*format != "plan" && *format != "github-env") {
			fmt.Fprintln(os.Stderr, "invalid_configure_arguments")
			return 2
		}
		if *root == "" {
			*root = os.Getenv("LMINSTALL_ROOT")
		}
		if *root == "" {
			var home string
			home, e = os.UserHomeDir()
			if e == nil {
				parent := os.Getenv("XDG_DATA_HOME")
				if runtime.GOOS == "windows" {
					parent = os.Getenv("LOCALAPPDATA")
				}
				if parent == "" {
					parent = filepath.Join(home, ".local", "share")
				}
				*root = filepath.Join(parent, "lminstall")
			}
		}
		if e != nil {
			fmt.Fprintln(os.Stderr, "install_root_unavailable")
			return 2
		}
		plan, err := launch.Configure(m, launch.Options{Provider: provider, Backend: *backend, BaseURL: *base, Model: *model, CredentialEnv: *credential, ProfileDir: *dir, InstallRoot: *root})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if *format == "github-env" {
			fmt.Print(launch.GitHubEnv(plan))
		} else {
			if json.NewEncoder(os.Stdout).Encode(plan) != nil {
				return 1
			}
		}
		return 0
	}
	if operation == "env" {
		if len(flags.Args()) != 0 || *runtimeDir == "" || (*format != "plan" && *format != "github-env" && *format != "sh") {
			fmt.Fprintln(os.Stderr, "invalid_env_arguments")
			return 2
		}
		if *backend != "" || *base != "" || *model != "" || *credential != "" || *root != "" {
			fmt.Fprintln(os.Stderr, "env_uses_profile_configuration")
			return 2
		}
		r, err := launch.Environment(m, provider, *dir, *runtimeDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		var text string
		switch *format {
		case "github-env":
			text, err = launch.GitHubEnvRuntime(r)
		case "sh":
			text, err = launch.ShellEnvRuntime(r)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if *format == "plan" {
			if json.NewEncoder(os.Stdout).Encode(r) != nil {
				return 1
			}
			return 0
		}
		fmt.Print(text)
		return 0
	}
	if operation != "run" {
		fmt.Fprintln(os.Stderr, "invalid_launch_operation")
		return 2
	}
	if *backend != "" || *base != "" || *model != "" || *credential != "" || *root != "" || *format != "plan" || *runtimeDir != "" {
		fmt.Fprintln(os.Stderr, "run_uses_profile_configuration")
		return 2
	}
	// flag.Parse consumes the separator; flags.Args are literal provider argv.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code, err := launch.Run(ctx, m, provider, *dir, flags.Args(), launch.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr})
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, err)
	}
	return code
}
