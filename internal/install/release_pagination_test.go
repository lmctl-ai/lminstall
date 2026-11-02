package install

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestProductReleasePagination(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "synthetic-pagination-token")
	for _, mode := range []string{"next", "foreign", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Header.Get("Authorization") != "Bearer synthetic-pagination-token" {
					t.Error("missing release API authorization")
				}
				if r.URL.Query().Get("page") == "2" {
					fmt.Fprint(w, `[{"tag_name":"v2.0.0","assets":[]}]`)
					return
				}
				next := "https://api.github.com/repos/fixture/tool/releases?per_page=50&page=2"
				if mode == "foreign" {
					next = "https://untrusted.invalid/repos/fixture/tool/releases?page=2"
				}
				if mode == "cycle" {
					next = "https://api.github.com/repos/fixture/tool/releases?per_page=50"
				}
				w.Header().Set("Link", "<"+next+">; rel=\"next\"")
				fmt.Fprint(w, "["+strings.TrimSuffix(strings.Repeat(`{"tag_name":"sdk-v1","assets":[]},`, 50), ",")+"]")
			}))
			defer server.Close()
			old := client
			client = server.Client()
			tr := client.Transport
			client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.github.com" {
					t.Error("credential dispatched to a foreign pagination host")
					return nil, fmt.Errorf("unexpected host")
				}
				c := r.Clone(r.Context())
				u := *r.URL
				u.Host = strings.TrimPrefix(server.URL, "https://")
				c.URL = &u
				return tr.RoundTrip(c)
			})
			defer func() { client = old }()
			m, _ := fixture(t)
			release, err := m.matchingRelease(context.Background(), Recipe{Repository: "fixture/tool", TagPattern: `^v[0-9]`}, regexp.MustCompile(`^v[0-9]`))
			switch mode {
			case "next":
				if err != nil || release.Tag != "v2.0.0" || requests != 2 {
					t.Fatalf("pagination result tag=%q calls=%d error=%v", release.Tag, requests, err)
				}
			case "foreign":
				if err == nil || !strings.Contains(err.Error(), "untrusted release pagination") || requests != 1 {
					t.Fatalf("foreign URL not refused before dispatch: calls=%d error=%v", requests, err)
				}
			case "cycle":
				if err == nil || !strings.Contains(err.Error(), "pagination cycle") || requests != 1 {
					t.Fatalf("cycle not refused: calls=%d error=%v", requests, err)
				}
			}
		})
	}
}
