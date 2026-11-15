package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/lmctl-ai/lminstall/internal/install"
	"os"
	"path/filepath"
)

func lmctlCLI(args []string) int {
	flags := flag.NewFlagSet("lmctl", flag.ContinueOnError)
	root := flags.String("root", "", "owned installation prefix")
	release := flags.String("release", "latest", "exact tag or latest")
	candidate := flags.String("candidate", "", "trusted candidate manifest for draft integration")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *root == "" {
		fmt.Fprintln(os.Stderr, "lminstall lmctl --root DIR [--release TAG|latest] [--candidate FILE]")
		return 2
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
	json.NewEncoder(os.Stdout).Encode(result)
	return 0
}
