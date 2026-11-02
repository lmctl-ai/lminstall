package install

import (
	"strings"
	"testing"
)

func TestOfficialCatalogMethods(t *testing.T) {
	m, e := LoadManifest()
	if e != nil {
		t.Fatal(e)
	}
	expected := map[string]string{"claude": "claude", "codex": "release", "gemini": "npm", "copilot": "release", "opencode": "release", "kimi": "kimi", "qwen": "release", "pi": "npm", "dsh": "npm", "miniplayer": "npm", "lmplayer": "release", "hermes": "hermes", "agy": "update-manifest", "muse": "muse"}
	for id, channel := range expected {
		t.Run(id, func(t *testing.T) {
			p, e := m.Provider(id)
			if e != nil {
				t.Fatal(e)
			}
			for _, os := range []string{"linux", "darwin", "windows"} {
				r := p.Recipe(os)
				if r.Channel != channel {
					t.Errorf("%s: got %s want official %s", os, r.Channel, channel)
				}
				for _, step := range r.Steps {
					if step.URL != "" {
						t.Errorf("%s still downloads an installer script", os)
					}
					for _, a := range step.Args {
						if strings.HasSuffix(a, ".sh") || strings.HasSuffix(a, ".ps1") {
							t.Errorf("installer execution: %v", step.Args)
						}
					}
				}
			}
		})
	}
	packages := map[string]string{"gemini": "@google/gemini-cli@latest", "pi": "@earendil-works/pi-coding-agent@latest", "dsh": "@deepseek-ai/dsh@latest"}
	for id, pkg := range packages {
		p, _ := m.Provider(id)
		for _, os := range []string{"linux", "darwin", "windows"} {
			if p.Recipe(os).Package != pkg {
				t.Errorf("%s/%s package mismatch", id, os)
			}
		}
	}
	dsh, _ := m.Provider("dsh")
	if dsh.Source != "https://github.com/deepseek-ai/deepseek-harness" {
		t.Error("DSH points to community docs rather than vendor")
	}
}
