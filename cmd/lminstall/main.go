package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lmctl-ai/lminstall/internal/install"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

var version = "dev"

const usage = "lminstall configure PROVIDER --backend mock|ollama-cloud|ollama-local --base-url URL --model ID --credential-env NAME --profile-dir ABS [--format plan|github-env] [--root DIR]\nlminstall run PROVIDER --profile-dir ABS -- <argv>\nlminstall env PROVIDER --profile-dir ABS --runtime-dir ABS [--format plan|github-env|sh]\nlminstall install|uninstall|upgrade PROVIDER... [--root DIR] [--json]\nlminstall list|status [PROVIDER...] [--root DIR] [--json]\n--manifest FILE replaces the trusted embedded provider data; --version prints lminstall's version.\n"

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	if len(args) > 0 && args[0] == "lmctl" {
		return lmctlCLI(args[1:])
	}
	if len(args) > 0 && (args[0] == "configure" || args[0] == "run" || args[0] == "env") {
		return launchCLI(args)
	}
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("lminstall " + version)
		return 0
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Print(usage)
		return 0
	}
	root := ""
	asJSON := false
	manifestPath := ""
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "--root", "--manifest":
			flag := args[i]
			i++
			if i == len(args) {
				fmt.Fprintln(os.Stderr, flag+" needs a value")
				return 2
			}
			if flag == "--root" {
				root = args[i]
			} else {
				manifestPath = args[i]
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintln(os.Stderr, "unknown flag", args[i])
				return 2
			}
			rest = append(rest, args[i])
		}
	}
	if len(rest) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	op := rest[0]
	if op != "list" && op != "status" && op != "install" && op != "uninstall" && op != "upgrade" {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	manifest, e := install.LoadManifest()
	if manifestPath != "" {
		var b []byte
		b, e = os.ReadFile(manifestPath)
		if e == nil {
			manifest = install.Manifest{}
			e = json.Unmarshal(b, &manifest)
		}
		if e == nil {
			e = install.Validate(manifest)
		}
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 2
	}
	if root == "" {
		root = os.Getenv("LMINSTALL_ROOT")
	}
	if root == "" {
		var base string
		if runtime.GOOS == "windows" {
			base = os.Getenv("LOCALAPPDATA")
		} else {
			base = os.Getenv("XDG_DATA_HOME")
			if base == "" {
				home, e := os.UserHomeDir()
				if e != nil {
					fmt.Fprintln(os.Stderr, e)
					return 2
				}
				base = filepath.Join(home, ".local", "share")
			}
		}
		root = filepath.Join(base, "lminstall")
	}
	root, e = filepath.Abs(root)
	if e != nil || strings.ContainsAny(root, "\r\n") {
		fmt.Fprintln(os.Stderr, "root must be a single-line absolute path")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m := install.Manager{Root: root, OS: runtime.GOOS, Arch: runtime.GOARCH, Manifest: manifest, Runner: install.ExecRunner{Log: os.Stderr}, Log: os.Stderr}
	ids := rest[1:]
	if len(ids) == 0 {
		if op != "list" && op != "status" {
			fmt.Fprintln(os.Stderr, "provide at least one provider; run lminstall list")
			return 2
		}
		for _, p := range manifest.Providers {
			ids = append(ids, p.ID)
		}
	}
	var results []install.Result
	code := 0
	for _, id := range ids {
		id = strings.ToLower(id)
		var r install.Result
		if op == "list" {
			p, err := manifest.Provider(id)
			e = err
			if e == nil {
				recipe := p.Recipe(runtime.GOOS)
				r = install.Result{Provider: id, State: "available", Source: p.Source, Channel: recipe.Channel}
				if recipe.Unavailable != "" {
					r.State = "unavailable"
					r.Reason = recipe.Unavailable
				}
			}
		} else if op == "status" {
			r, e = m.Status(ctx, id)
		} else {
			r, e = m.Apply(ctx, op, id)
		}
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			if r.State == "unavailable" {
				if code == 0 {
					code = 3
				}
			} else {
				code = 1
			}
		}
		if e == nil && r.State == "installed" && (op == "install" || op == "upgrade") {
			if path := os.Getenv("GITHUB_PATH"); path != "" {
				f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if err == nil {
					_, err = fmt.Fprintln(f, r.BinDir)
					f.Close()
				}
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
					code = 1
				}
			}
		}
		if e != nil && r.Reason == "" {
			r.Reason = e.Error()
		}
		if r.Provider == "" {
			r.Provider = id
		}
		if r.State == "" {
			r.State = "error"
		}
		results = append(results, r)
	}
	if asJSON {
		e = json.NewEncoder(os.Stdout).Encode(results)
		if e != nil && !errors.Is(e, os.ErrClosed) {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
	} else {
		for _, r := range results {
			fmt.Printf("%s\t%s\t%s\t%s\n", r.Provider, r.State, r.Version, r.BinDir)
			if r.Reason != "" {
				fmt.Println(r.Reason)
			}
		}
	}
	return code
}
