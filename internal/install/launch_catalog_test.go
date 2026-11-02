package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLaunchCatalogAdmitsOnlyQualifiedBackends(t *testing.T) {
	var m struct {
		Providers []struct {
			ID     string
			Launch json.RawMessage `json:"launch_config"`
		}
	}
	if e := json.Unmarshal(catalog, &m); e != nil {
		t.Fatal(e)
	}
	for _, p := range m.Providers {
		var config struct {
			Version  int
			Backends map[string]struct {
				Supported                            bool
				Reason, Protocol, BaseShape, Adapter string
				AdapterVersion                       int `json:"adapter_version"`
			}
		}
		if e := json.Unmarshal(p.Launch, &config); e != nil {
			t.Fatal(e)
		}
		if config.Version != 1 {
			t.Errorf("%s missing versioned launch data", p.ID)
			continue
		}
		for _, backend := range []string{"mock", "ollama-cloud", "ollama-local"} {
			route, ok := config.Backends[backend]
			if !ok {
				t.Errorf("%s missing explicit backend %s", p.ID, backend)
				continue
			}
			want := (backend == "mock" && p.ID != "muse" && p.ID != "claudemock") || (backend == "ollama-cloud" && p.ID == "claude")
			if route.Supported != want {
				t.Errorf("%s/%s support mismatch", p.ID, backend)
			}
			if !want && route.Reason == "" {
				t.Errorf("%s/%s must explain unsupported", p.ID, backend)
			}
		}
	}
}

func TestWindowsNPMEntryPreservesLiteralArgsAndNativeExecutable(t *testing.T) {
	root := t.TempDir()
	pkg := filepath.Join(root, "node_modules", "@vendor", "tool")
	os.MkdirAll(filepath.Join(pkg, "bin"), 0700)
	os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"bin":{"tool":"bin/tool.exe"}}`), 0600)
	os.WriteFile(filepath.Join(pkg, "bin", "tool.exe"), []byte("fixture"), 0600)
	args := []string{"", "literal a b & $() 雪"}
	got, e := ResolveNPMEntry(filepath.Join(root, "tool.cmd"), "tool", "@vendor/tool", args)
	if e != nil || len(got) != 3 || got[0] != filepath.Join(pkg, "bin", "tool.exe") || got[1] != "" || got[2] != args[1] {
		t.Fatalf("native entry routed through a shell/Node: %v %v", got, e)
	}
}
