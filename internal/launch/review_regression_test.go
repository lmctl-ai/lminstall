package launch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A reflected Authorization header previously exposed the wire-trimmed token.
// Reject at both dispatch boundaries, before a process, socket or runtime exists.
func TestNoncanonicalCredentialRefusedBeforeDispatch(t *testing.T) {
	for _, key := range []string{" synthetic-token", "synthetic-token ", "\tsynthetic-token\t", "\u00a0synthetic-token\u00a0", "synthetic\ttoken", "synthetic\x7ftoken"} {
		t.Run(strings.ReplaceAll(key, "synthetic", "case"), func(t *testing.T) {
			t.Setenv("REVIEW_CREDENTIAL", key)
			called := false
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				io.WriteString(w, r.Header.Get("Authorization"))
			}))
			defer upstream.Close()
			m := manifest(t)
			dir := filepath.Join(t.TempDir(), "profile")
			_, err := Configure(m, Options{Provider: "claude", Backend: "mock", BaseURL: upstream.URL, Model: "m", CredentialEnv: "REVIEW_CREDENTIAL", ProfileDir: dir, InstallRoot: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			code, err := Run(context.Background(), m, "claude", dir, nil, IO{})
			if code != 1 || err == nil || err.Error() != "credential_environment_invalid" {
				t.Errorf("credential reached dispatch: code=%d error=%v", code, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "runs")); !os.IsNotExist(err) {
				t.Error("runtime created before credential validation")
			}
			p, _, err := Load(dir, "claude")
			if err != nil {
				t.Fatal(err)
			}
			b, err := startBridge(context.Background(), p, key, "placeholder", sensitiveEncodings(key))
			if err == nil {
				b.Close()
				t.Error("bridge accepted noncanonical credential")
			}
			if called {
				t.Error("invalid credential sent upstream")
			}
		})
	}
}

func TestUnsupportedBasePathsLeaveNoProfile(t *testing.T) {
	for _, id := range tools {
		for _, path := range []string{"/v1/v1", "/v1/v1/", "/prefix", "/prefix/v1", "/v1%2fv1"} {
			t.Run(id+path, func(t *testing.T) {
				dir := filepath.Join(t.TempDir(), "profile")
				_, err := Configure(manifest(t), Options{Provider: id, Backend: "mock", BaseURL: "http://127.0.0.1:8080" + path, Model: "m", ProfileDir: dir, InstallRoot: t.TempDir()})
				if err == nil {
					t.Error("unsupported base path accepted")
				}
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Error("invalid base path persisted a profile")
				}
			})
		}
	}
}

// Independent expected argv, not derived from the manifest under test. Empty
// settings sources and MCP JSON are required values, not optional decoration.
func TestRequiredLaunchArguments(t *testing.T) {
	expected := map[string][]string{
		"claude": {"--model", "chosen-model", "--bare", "--setting-sources", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`},
		"gemini": {"--model", "chosen-model"}, "copilot": {"--model", "chosen-model"}, "qwen": {"--model", "chosen-model"}, "agy": {"--model", "chosen-model"},
		"pi":         {"--provider", "mock", "--model", "chosen-model"},
		"miniplayer": {"run", "--model", "openai/chosen-model"},
		"hermes":     {"chat", "--native", "--provider", "custom", "--base-url", "http://127.0.0.1:1234/v1", "--model", "chosen-model", "--safe-mode"},
		"codex":      {}, "opencode": {}, "kimi": {}, "dsh": {}, "lmplayer": {},
	}
	for _, id := range tools {
		provider, _ := manifest(t).Provider(id)
		for backend, spec := range provider.LaunchConfig.Backends {
			if !spec.Supported {
				continue
			}
			t.Run(id+"/"+backend, func(t *testing.T) {
				want, ok := expected[id]
				if !ok {
					t.Fatal("missing independent argv contract")
				}
				_, _, got, err := Render(Profile{Provider: id, Model: "chosen-model", Spec: spec}, t.TempDir(), "http://127.0.0.1:1234", "placeholder")
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(got, want) {
					t.Errorf("required launch argv: got %q want %q", got, want)
				}
			})
		}
	}
}
