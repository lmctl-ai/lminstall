package launch

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/lmctl-ai/lminstall/internal/install"
)

var tools = []string{"claude", "codex", "gemini", "copilot", "opencode", "kimi", "qwen", "pi", "dsh", "miniplayer", "lmplayer", "hermes", "agy"}

func manifest(t *testing.T) install.Manifest {
	t.Helper()
	m, e := install.LoadManifest()
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestProvenAdapterFileBytes(t *testing.T) {
	m := manifest(t)
	for _, id := range tools {
		t.Run(id, func(t *testing.T) {
			p, _ := m.Provider(id)
			profile := Profile{Provider: id, Model: "mock-agent", InstallRoot: "/INSTALL", Spec: p.LaunchConfig.Backends["mock"]}
			_, files, _, e := Render(profile, "/RUNTIME", "http://127.0.0.1:12345", "PLACEHOLDER")
			if e != nil {
				t.Fatal(e)
			}
			golden, e := os.ReadFile(filepath.Join("testdata", id+".json"))
			if e != nil {
				t.Fatal(e)
			}
			var expected map[string]string
			json.Unmarshal(golden, &expected)
			if len(files) != len(expected) {
				t.Fatalf("file count: %v vs %v", files, expected)
			}
			for name, want := range expected {
				if got := files[name]; got != want {
					t.Errorf("%s bytes\ngot %s\nwant %s", name, got, want)
				}
			}
		})
	}
}
func TestConfigureDoesNotResolveOrWriteCredential(t *testing.T) {
	key := "SYNTHETIC-WP138-unique+/?&<key>"
	t.Setenv("WP138_KEY", key)
	for _, id := range tools {
		dir := filepath.Join(t.TempDir(), "profile 雪 & literal")
		plan, e := Configure(manifest(t), Options{Provider: id, Backend: "mock", BaseURL: "http://127.0.0.1:12345/v1", Model: "mock-agent", CredentialEnv: "WP138_KEY", ProfileDir: dir, InstallRoot: t.TempDir()})
		// Origin-shaped adapters reject accidental /v1 rather than duplicating it.
		if e != nil {
			plan, e = Configure(manifest(t), Options{Provider: id, Backend: "mock", BaseURL: "http://127.0.0.1:12345", Model: "mock-agent", CredentialEnv: "WP138_KEY", ProfileDir: dir, InstallRoot: t.TempDir()})
		}
		if e != nil {
			t.Fatalf("%s: %v", id, e)
		}
		b, _ := json.Marshal(plan)
		assertAbsent(t, b, key)
		assertAbsent(t, []byte(GitHubEnv(plan)), key)
		filepath.WalkDir(dir, func(path string, d os.DirEntry, e error) error {
			if e != nil {
				t.Fatal(e)
			}
			if !d.IsDir() {
				b, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				assertAbsent(t, b, key)
			}
			return nil
		})
	}
}
func assertAbsent(t *testing.T, b []byte, key string) {
	t.Helper()
	for _, pattern := range independentEncodings(key) {
		if bytes.Contains(b, pattern) {
			t.Error("credential leaked in a persisted/output encoding")
			return
		}
	}
}
func TestUnsupportedAndForeignProfileRefused(t *testing.T) {
	for _, tc := range []struct{ provider, backend string }{{"muse", "mock"}, {"codex", "ollama-cloud"}, {"claude", "ollama-local"}, {"claudemock", "mock"}} {
		if _, e := Configure(manifest(t), Options{Provider: tc.provider, Backend: tc.backend, BaseURL: "http://127.0.0.1:12345", Model: "m", ProfileDir: t.TempDir(), InstallRoot: t.TempDir()}); e == nil {
			t.Fatal(tc)
		}
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "user-config"), []byte("unchanged"), 0600)
	if _, e := Configure(manifest(t), Options{Provider: "claude", Backend: "mock", BaseURL: "http://127.0.0.1:12345", Model: "m", ProfileDir: dir, InstallRoot: t.TempDir()}); e == nil {
		t.Fatal("adopted foreign files")
	}
}
func TestRedactionSpansWritesWithoutOutputCap(t *testing.T) {
	key := "SYNTHETIC-+/?<&>key-138"
	patterns := sensitiveEncodings(key)
	for _, encoded := range independentEncodings(key) {
		var out bytes.Buffer
		w := redactor(&out, patterns)
		for _, c := range encoded {
			w.Write([]byte{c})
		}
		w.Close()
		assertAbsent(t, out.Bytes(), key)
	}
	payload := strings.Repeat("x", 1200000)
	var out bytes.Buffer
	w := redactor(&out, patterns)
	w.Write([]byte(payload))
	w.Close()
	if out.String() != payload {
		t.Fatal("nonsecret output clipped or changed")
	}
}
func TestBridgeBindsOnlyInMemoryAndRedactsResponses(t *testing.T) {
	key := "SYNTHETIC-bridge+/key-138"
	nonce := "invocation-placeholder"
	patterns := sensitiveEncodings(key)
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("wrong upstream binding")
		}
		w.Header().Set("X-Echo", key)
		for _, p := range independentEncodings(key) {
			w.Write(p)
			w.Write([]byte("\n"))
		}
	}))
	defer upstream.Close()
	p := Profile{BaseURL: upstream.URL, Spec: install.LaunchSpec{Headers: map[string]string{"Authorization": "Bearer {credential}"}}}
	b, e := startBridge(context.Background(), p, key, nonce, patterns)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	req, _ := http.NewRequest("POST", b.url+"/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+nonce)
	response, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	body, e := io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil {
		t.Fatal(e)
	}
	assertAbsent(t, body, key)
	assertAbsent(t, []byte(response.Header.Get("X-Echo")), key)
	if !called {
		t.Fatal("no real HTTP request")
	}
}

func independentEncodings(key string) [][]byte {
	raw := []byte(key)
	out := [][]byte{raw, []byte(base64.StdEncoding.EncodeToString(raw)), []byte(base64.RawStdEncoding.EncodeToString(raw)), []byte(base64.URLEncoding.EncodeToString(raw)), []byte(base64.RawURLEncoding.EncodeToString(raw)), []byte(hex.EncodeToString(raw)), []byte(strings.ToUpper(hex.EncodeToString(raw))), []byte(url.QueryEscape(key)), []byte(url.PathEscape(key)), []byte(url.QueryEscape(url.QueryEscape(key)))}
	b, _ := json.Marshal(key)
	out = append(out, b[1:len(b)-1])
	var pct, pctLower, uni, uniUpper strings.Builder
	var le, be []byte
	for _, v := range raw {
		fmt.Fprintf(&pct, "%%%02X", v)
		fmt.Fprintf(&pctLower, "%%%02x", v)
	}
	for _, v := range utf16.Encode([]rune(key)) {
		fmt.Fprintf(&uni, `\u%04x`, v)
		fmt.Fprintf(&uniUpper, `\u%04X`, v)
		le = append(le, byte(v), byte(v>>8))
		be = append(be, byte(v>>8), byte(v))
	}
	return append(out, []byte(pct.String()), []byte(pctLower.String()), []byte(uni.String()), []byte(uniUpper.String()), le, be)
}
func TestClaudeSlotsAndRoutingAreManifestDriven(t *testing.T) {
	p, _ := manifest(t).Provider("claude")
	spec := p.LaunchConfig.Backends["mock"]
	env, _, argv, e := Render(Profile{Provider: "claude", Model: "hosted-id", Spec: spec}, "/RUNTIME", "http://127.0.0.1:1234", "placeholder")
	if e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"} {
		if env[key] != "hosted-id" {
			t.Errorf("slot %s not configured", key)
		}
	}
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:1234" || env["ANTHROPIC_API_KEY"] != "" {
		t.Fatal("wrong Anthropic base/key")
	}
	if len(argv) < 2 || argv[0] != "--model" || argv[1] != "hosted-id" {
		t.Fatal("model argv missing")
	}
}
