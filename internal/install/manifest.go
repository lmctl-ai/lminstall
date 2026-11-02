package install

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
)

//go:embed providers.json
var catalog []byte

type Manifest struct {
	Version   int        `json:"version"`
	Providers []Provider `json:"providers"`
}
type Provider struct {
	ID      string            `json:"id"`
	Binary  string            `json:"binary"`
	Source  string            `json:"source"`
	Recipes map[string]Recipe `json:"recipes"`
}
type Recipe struct {
	Checksums     string            `json:"checksums"`
	UpdateURL     string            `json:"update_url"`
	ArchiveBinary string            `json:"archive_binary"`
	Channel       string            `json:"channel"`
	Package       string            `json:"package"`
	Python        string            `json:"python"`
	Repository    string            `json:"repository"`
	Asset         string            `json:"asset"`
	Bin           string            `json:"bin"`
	BinDir        string            `json:"bin_dir"`
	Args          []string          `json:"args"`
	Env           map[string]string `json:"env"`
	Steps         []Step            `json:"steps"`
	Replace       []string          `json:"replace_on_upgrade"`
	Unavailable   string            `json:"unavailable"`
}
type Step struct {
	URL  string   `json:"url"`
	File string   `json:"file"`
	Args []string `json:"args"`
	Cwd  string   `json:"cwd"`
}

var safeID = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func LoadManifest() (Manifest, error) {
	var m Manifest
	err := json.Unmarshal(catalog, &m)
	if err != nil {
		return m, err
	}
	return m, Validate(m)
}
func Validate(m Manifest) error {
	seen := map[string]bool{}
	for _, p := range m.Providers {
		if !safeID.MatchString(p.ID) || seen[p.ID] || p.Binary == "" || len(p.Recipes) == 0 {
			return fmt.Errorf("invalid provider %q", p.ID)
		}

		for _, recipe := range p.Recipes {
			for _, step := range recipe.Steps {
				if step.URL != "" {
					return fmt.Errorf("%s: downloaded installer steps are unsupported; use native Go channels", p.ID)
				}
			}
		}
		seen[p.ID] = true
	}
	return nil
}
func (m Manifest) Provider(id string) (Provider, error) {
	for _, p := range m.Providers {
		if p.ID == id {
			return p, nil
		}
	}
	return Provider{}, fmt.Errorf("unknown provider %q; run lminstall list", id)
}
func (p Provider) Recipe(os string) Recipe {
	if r, ok := p.Recipes[os]; ok {
		return r
	}
	if r, ok := p.Recipes["*"]; ok {
		return r
	}
	return Recipe{Unavailable: "No official install recipe for " + os + "; see " + p.Source}
}
