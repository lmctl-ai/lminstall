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
