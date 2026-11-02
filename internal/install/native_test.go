package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNativeKimiAndMuseIntegrity(t *testing.T) {
	for _, provider := range []string{"kimi", "muse"} {
		for _, bad := range []string{"none", "checksum", "missing", "version", "size"} {
			if provider == "kimi" && bad == "size" {
				continue
			}
			t.Run(provider+"/"+bad, func(t *testing.T) {
				data := []byte("native fixture")
				hash := sha256.Sum256(data)
				digest := hex.EncodeToString(hash[:])
				if bad == "checksum" {
					digest = strings.Repeat("0", 64)
				}
				if bad == "missing" {
					digest = ""
				}
				version := "1.2.3"
				if provider == "muse" {
					version += "-R1"
				}
				reported := version
				if bad == "version" {
					reported = "99.99.99"
				}
				var base string
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/latest":
						fmt.Fprintln(w, version)
					case "/binaries/1.2.3/manifest.json":
						fmt.Fprintf(w, `{"version":"1.2.3","platforms":{"linux-x64":{"filename":"kimi-native","checksum":%q}}}`, digest)
					case "/channel":
						fmt.Fprintf(w, `{"channel":"muse-stable","version":%q,"manifest_url":%q,"urgency":"none","notification_text":""}`, version, base+"/release")
					case "/release":
						size := len(data)
						if bad == "size" {
							size++
						}
						fmt.Fprintf(w, `{"version":%q,"checksum_algorithm":"sha256","artifacts":{"x86_linux":{"url":%q,"checksum":%q,"size":%d}}}`, version, base+"/artifact", digest, size)
					default:
						w.Write(data)
					}
				}))
				defer server.Close()
				base = server.URL
				old := client
				client = server.Client()
				defer func() { client = old }()
				m, f := fixture(t)
				f.output = "tool " + reported
				r := Recipe{Channel: provider, Bin: "bin/tool", BinDir: "bin", UpdateURL: base}
				if provider == "muse" {
					r.UpdateURL += "/channel"
				}
				m.Manifest.Providers[0].Recipes["*"] = r
				result, err := m.Apply(context.Background(), "install", "tool")
				if bad == "none" {
					if err != nil || result.State != "installed" {
						t.Fatalf("%+v %v", result, err)
					}
					dest := filepath.Join(m.dir("tool"), "bin", "tool")
					os.WriteFile(dest, []byte("last good binary"), 0755)
					f.output = "tool 99.99.99"
					if _, err = m.Apply(context.Background(), "upgrade", "tool"); err == nil {
						t.Fatal("accepted wrong upgrade version")
					}
					kept, _ := os.ReadFile(dest)
					if string(kept) != "last good binary" {
						t.Fatal("failed version validation replaced working binary")
					}

				} else {
					if err == nil || result.State == "installed" {
						t.Fatalf("accepted %s: %+v", bad, result)
					}
					if bad != "version" && len(f.calls) != 0 {
						t.Fatalf("unverified code ran: %v", f.calls)
					}
					if bad == "size" {
						if _, e := os.Stat(filepath.Join(m.dir("tool"), "bin", "tool")); !os.IsNotExist(e) {
							t.Fatal("wrong size artifact published")
						}
					}
				}
			})
		}
	}
}

type hermesRunner struct {
	t      *testing.T
	calls  [][]string
	lock   []byte
	python string
}

func (f *hermesRunner) Run(ctx context.Context, args []string, cwd string, env map[string]string, capture bool) (string, error) {
	f.calls = append(f.calls, append([]string{}, args...))
	if len(args) > 1 && args[1] == "clone" {
		source := args[len(args)-1]
		os.MkdirAll(filepath.Join(source, "pm"), 0700)
		os.WriteFile(filepath.Join(source, "pm", "lock.json"), f.lock, 0600)
	}
	if len(args) > 2 && args[1] == "python" && args[2] == "find" {
		return f.python, nil
	}
	if len(args) > 1 && args[1] == "--version" {
		return "tool 1.2.3", nil
	}
	if args[0] == "uv" {
		f.t.Fatal("global uv must not be used")
	}
	return "", nil
}
func TestHermesBootstrapsVerifiedUVAndOfficialPM(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			data := zipBytes(t, "uv", []byte("private uv"))
			hash := sha256.Sum256(data)
			sum := hex.EncodeToString(hash[:])
			if bad {
				sum = strings.Repeat("0", 64)
			}
			var base string
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "releases/latest") {
					fmt.Fprint(w, `{"tag_name":"v1.2.3"}`)
				} else {
					w.Write(data)
				}
			}))
			defer server.Close()
			base = server.URL
			old := client
			client = server.Client()
			tr := client.Transport
			client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				c := r.Clone(r.Context())
				u := *c.URL
				u.Host = strings.TrimPrefix(base, "https://")
				c.URL = &u
				return tr.RoundTrip(c)
			})
			defer func() { client = old }()
			lock, _ := json.Marshal(map[string]any{"packages": map[string]any{"other": map[string]any{"artifacts": map[string]any{"linux-x64": []string{"multiple artifacts"}}}, "uv": map[string]any{"version": "0.13.5", "artifacts": map[string]any{"linux-x64": map[string]string{"url": base + "/uv.zip", "sha256": sum}}}, "python": map[string]any{"version": "3.14.3+build"}}})
			m, _ := fixture(t)
			f := &hermesRunner{t: t, lock: lock, python: filepath.Join(m.Root, "python", "python")}
			m.Runner = f
			r := Recipe{Channel: "hermes", Repository: "https://github.com/NousResearch/hermes-agent.git", Bin: "source/.hermes/bin/hermes", BinDir: "source/.hermes/bin", Env: map[string]string{"HERMES_HOME": "{dir}/home/.hermes"}}
			m.Manifest.Providers[0].Recipes["*"] = r
			result, e := m.Apply(context.Background(), "install", "tool")
			if bad {
				if e == nil {
					t.Fatal("accepted unverified uv")
				}
				if len(f.calls) != 1 {
					t.Fatalf("bootstrap executed before hash: %v", f.calls)
				}
				return
			}
			if e != nil || result.State != "installed" {
				t.Fatalf("%+v %v", result, e)
			}
			joined := ""
			for _, a := range f.calls {
				joined += strings.Join(a, " ") + "\n"
			}
			for _, want := range []string{"--branch v1.2.3", "uv --version", "python install --no-bin --no-registry 3.14", "python find --managed-python 3.14", "-m pm.cli install", "source_completion.py --source"} {
				if !strings.Contains(joined, want) {
					t.Errorf("missing %s in %s", want, joined)
				}
			}
		})
	}
}

func TestRunnerRefusesInstallerScripts(t *testing.T) {
	for _, args := range [][]string{{"bash", "install.sh"}, {"powershell.exe", "-File", "install.ps1"}, {"/tmp/install.sh"}} {
		if _, e := (ExecRunner{}).Run(context.Background(), args, "", nil, false); e == nil || !strings.Contains(e.Error(), "scripts are unsupported") {
			t.Errorf("%v: %v", args, e)
		}
	}
}

func TestHermesPrivatePathReflectsCurrentPrerequisites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX PATH isolation")
	}
	root := t.TempDir()
	first := t.TempDir()
	second := t.TempDir()
	for _, name := range []string{"node", "git", "sudo", "apt-get"} {
		os.WriteFile(filepath.Join(first, name), []byte("fixture"), 0700)
	}
	os.WriteFile(filepath.Join(second, "git"), []byte("fixture"), 0700)
	bin, e := withoutSystemInstallers(root, first)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"sudo", "apt-get"} {
		if _, e = os.Lstat(filepath.Join(bin, name)); !os.IsNotExist(e) {
			t.Error("system installer reachable", name)
		}
	}
	bin, e = withoutSystemInstallers(root, second)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Lstat(filepath.Join(bin, "node")); !os.IsNotExist(e) {
		t.Error("previous PATH leaked Node into a later no-node invocation")
	}
}

type cwdRunner struct{ dir string }

func (r *cwdRunner) Run(ctx context.Context, args []string, cwd string, env map[string]string, capture bool) (string, error) {
	if !capture {
		r.dir = cwd
	}
	return "tool 1.2.3", nil
}
func TestNPMPackageInstallUsesOwnedWorkingDirectory(t *testing.T) {
	m, _ := fixture(t)
	f := &cwdRunner{}
	m.Runner = f
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "npm", Package: "fixture@latest", Bin: "npm/bin/tool", BinDir: "npm/bin"}
	if _, e := m.Apply(context.Background(), "install", "tool"); e != nil {
		t.Fatal(e)
	}
	if f.dir != m.dir("tool") {
		t.Fatalf("installer inherited caller directory: %q", f.dir)
	}
}

func TestMuseWindowsVerifiesExecutableSuffix(t *testing.T) {
	data := []byte("Windows fixture")
	h := sha256.Sum256(data)
	sum := hex.EncodeToString(h[:])
	var base string
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/channel":
			fmt.Fprintf(w, `{"channel":"muse-stable","version":"1.2.3","manifest_url":%q,"urgency":"none","notification_text":""}`, base+"/release")
		case "/release":
			fmt.Fprintf(w, `{"version":"1.2.3","checksum_algorithm":"sha256","artifacts":{"x86_windows":{"url":%q,"checksum":%q,"size":%d}}}`, base+"/binary", sum, len(data))
		default:
			w.Write(data)
		}
	}))
	defer s.Close()
	base = s.URL
	old := client
	client = s.Client()
	defer func() { client = old }()
	m, f := fixture(t)
	m.OS = "windows"
	m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "muse", UpdateURL: base + "/channel", Bin: "bin/tool.exe", BinDir: "bin"}
	if _, e := m.Apply(context.Background(), "install", "tool"); e != nil {
		t.Fatal(e)
	}
	for _, args := range f.calls {
		if len(args) > 1 && args[1] == "--version" && !strings.HasSuffix(args[0], ".exe") {
			t.Fatalf("Windows verification needs an executable suffix: %v", args)
		}
	}
}

func TestHermesBootstrapMirrorKeepsIntegrityPolicy(t *testing.T) {
	for _, mode := range []string{"unavailable", "primary-corrupt", "mirror-corrupt"} {
		t.Run(mode, func(t *testing.T) {
			data := zipBytes(t, "uv", []byte("verified uv"))
			hash := sha256.Sum256(data)
			sum := hex.EncodeToString(hash[:])
			hits := 0
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/primary" {
					if mode == "primary-corrupt" {
						w.Write([]byte("corrupt"))
					} else {
						w.WriteHeader(403)
					}
					return
				}
				hits++
				if mode == "mirror-corrupt" {
					w.Write([]byte("corrupt mirror"))
				} else {
					w.Write(data)
				}
			}))
			defer s.Close()
			old := client
			client = s.Client()
			tr := client.Transport
			client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				c := r.Clone(r.Context())
				u := *c.URL
				u.Host = strings.TrimPrefix(s.URL, "https://")
				c.URL = &u
				return tr.RoundTrip(c)
			})
			defer func() { client = old }()
			var tool pinnedTool
			json.Unmarshal([]byte(fmt.Sprintf(`{"version":"0.13.5","artifacts":{"linux-x64":{"url":%q,"sha256":%q}}}`, s.URL+"/primary.tar.gz", sum)), &tool)
			asset := tool.Artifacts["linux-x64"]
			asset.URL = s.URL + "/primary.zip"
			tool.Artifacts["linux-x64"] = asset
			prev := client.Transport
			client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				c := r.Clone(r.Context())
				u := *c.URL
				if u.Path == "/primary.zip" {
					u.Path = "/primary"
				}
				c.URL = &u
				return prev.RoundTrip(c)
			})
			m, _ := fixture(t)
			dir := m.dir("tool")
			os.MkdirAll(dir, 0700)
			_, err := m.hermesUV(context.Background(), dir, "linux-x64", tool)
			if mode == "unavailable" {
				if err != nil || hits != 1 {
					t.Fatalf("mirror not used: %v hits=%d", err, hits)
				}
			} else {
				if err == nil {
					t.Fatal("accepted corrupt bootstrap")
				}
				want := 1
				if mode == "primary-corrupt" {
					want = 0
				}
				if hits != want {
					t.Fatalf("integrity fallback policy: %v hits=%d want=%d", err, hits, want)
				}
			}
		})
	}
}

func TestKimiOfficialPlatformKeys(t *testing.T) {
	for _, tc := range []struct{ os, arch, key string }{{"linux", "amd64", "linux-x64"}, {"linux", "arm64", "linux-arm64"}, {"darwin", "amd64", "darwin-x64"}, {"darwin", "arm64", "darwin-arm64"}, {"windows", "amd64", "win32-x64"}, {"windows", "arm64", "win32-arm64"}} {
		t.Run(tc.key, func(t *testing.T) {
			data := []byte("native fixture")
			h := sha256.Sum256(data)
			sum := hex.EncodeToString(h[:])
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/latest":
					fmt.Fprint(w, "1.2.3")
				case "/binaries/1.2.3/manifest.json":
					fmt.Fprintf(w, `{"version":"1.2.3","platforms":{%q:{"filename":"kimi-native","checksum":%q}}}`, tc.key, sum)
				default:
					w.Write(data)
				}
			}))
			defer s.Close()
			old := client
			client = s.Client()
			defer func() { client = old }()
			m, _ := fixture(t)
			m.OS, m.Arch = tc.os, tc.arch
			bin := "bin/tool"
			if tc.os == "windows" {
				bin += ".exe"
			}
			m.Manifest.Providers[0].Recipes["*"] = Recipe{Channel: "kimi", UpdateURL: s.URL, Bin: bin, BinDir: "bin"}
			result, e := m.Apply(context.Background(), "install", "tool")
			if e != nil || result.State != "installed" {
				t.Fatalf("official %s key: %+v %v", tc.key, result, e)
			}
		})
	}
}

func TestChangedNativeMethodDoesNotReuseLegacyReceipt(t *testing.T) {
	for _, channel := range []string{"kimi", "muse", "hermes"} {
		t.Run(channel, func(t *testing.T) {
			m, f := fixture(t)
			if _, e := m.Apply(context.Background(), "install", "tool"); e != nil {
				t.Fatal(e)
			}
			// A prior method left a valid-looking receipt. Legacy receipts lack channel.
			path := filepath.Join(m.dir("tool"), "receipt.json")
			b, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			var record map[string]any
			json.Unmarshal(b, &record)
			delete(record, "channel")
			b, _ = json.Marshal(record)
			os.WriteFile(path, b, 0600)
			r := m.Manifest.Providers[0].Recipes["*"]
			r.Channel = channel
			m.Manifest.Providers[0].Recipes["*"] = r
			before := len(f.calls)
			status, e := m.Status(context.Background(), "tool")
			if e != nil || status.State != "incomplete" || !strings.Contains(status.Reason, "method") {
				t.Fatalf("legacy installation misreported: %+v %v", status, e)
			}
			if len(f.calls) != before {
				t.Fatal("legacy runtime executed before method migration")
			}
		})
	}
}
