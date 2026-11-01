package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestReviewManifestReplacesCatalog(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "catalog.json")
	data := `{"version":1,"providers":[{"id":"probe","binary":"probe","source":"https://example.invalid/review","recipes":{"*":{"unavailable":"fixture must not install"}}}]}`
	if err := os.WriteFile(manifest, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = w
	code := run([]string{"list", "probe", "--manifest", manifest, "--root", filepath.Join(root, "prefix"), "--json"})
	w.Close()
	os.Stdout = previous
	output, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct{ State, Channel string }
	if err = json.Unmarshal(output, &rows); err != nil {
		t.Fatal(err)
	}
	t.Logf("code=%d output=%s", code, output)
	if code != 0 || len(rows) != 1 {
		t.Fatal("fixture invalid")
	}
	if rows[0].State != "unavailable" {
		t.Fatal("BUG: custom wildcard unavailable inherits embedded npm platform recipe")
	}
}

func TestCLIExitCodes(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"install", "claudemock"}, 3},
		{[]string{"--not-a-real-flag"}, 2},
		{[]string{"install", "--root"}, 2},
		{[]string{"install"}, 2},
		{[]string{"unknown-operation"}, 2},
		{[]string{"--json"}, 2},
	} {
		args := append(append([]string{}, tc.args...), "--root", t.TempDir())
		// A missing flag value must remain the last argument.
		if len(tc.args) == 2 && tc.args[1] == "--root" {
			args = tc.args
		}
		if got := run(args); got != tc.want {
			t.Errorf("%v: got %d want %d", tc.args, got, tc.want)
		}
	}
}
