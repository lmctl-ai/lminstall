package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/lmctl-ai/lminstall/internal/install"
	"os"
	"path/filepath"
	"runtime"
)

func lmctlCLI(args []string) int {
	flags := flag.NewFlagSet("lmctl", flag.ContinueOnError)
	root := flags.String("root", "", "owned installation prefix (default: the root of the active lmctl when lminstall owns it, else LMINSTALL_ROOT, else the default prefix)")
	release := flags.String("release", "latest", "exact tag or latest")
	candidate := flags.String("candidate", "", "trusted candidate manifest for draft integration")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "lminstall lmctl [--root DIR] [--release TAG|latest] [--candidate FILE]")
		return 2
	}
	path := os.Getenv("PATH")
	active, activeRoot := install.ActiveLmctl(path)
	if *root == "" {
		switch {
		case activeRoot != "":
			// Upgrade in place: the copy the shell runs is the copy that changes.
			*root = activeRoot
		case os.Getenv("LMINSTALL_ROOT") != "":
			*root = os.Getenv("LMINSTALL_ROOT")
		default:
			base := os.Getenv("XDG_DATA_HOME")
			if runtime.GOOS == "windows" {
				base = os.Getenv("LOCALAPPDATA")
			}
			if base == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 2
				}
				base = filepath.Join(home, ".local", "share")
			}
			*root = filepath.Join(base, "lminstall")
		}
	}
	absolute, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	result, err := install.InstallLmctl(context.Background(), absolute, *release, *candidate)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	out := struct {
		install.LmctlInstallation
		Active     bool   `json:"active"`
		ShadowedBy string `json:"shadowedBy,omitempty"`
	}{LmctlInstallation: result}
	if shadow := install.LmctlShadow(result.BinDir, path); shadow == "" {
		out.Active = true
	} else {
		out.ShadowedBy = shadow
		// The install itself succeeded, but the shell will not run it: say so instead of leaving a silent no-op.
		if active != "" && shadow == active {
			fmt.Fprintf(os.Stderr, "lminstall: installed lmctl %s in %s, but `lmctl` still runs %s (it comes first on PATH).\n  Put %s first on PATH, or remove/replace that copy, or install over it with --root.\n", result.Version, result.BinDir, active, result.BinDir)
		} else {
			fmt.Fprintf(os.Stderr, "lminstall: installed lmctl %s in %s, but it is not what `lmctl` resolves to (%s). Put %s first on PATH.\n", result.Version, result.BinDir, shadow, result.BinDir)
		}
	}
	json.NewEncoder(os.Stdout).Encode(out)
	return 0
}
