package main

import (
	"github.com/lmctl-ai/lminstall/internal/install"
	"github.com/lmctl-ai/lminstall/internal/launch"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExactReplyAndFailingControl(t *testing.T) {
	if oracle(expected, expected) != nil {
		t.Fatal("positive rejected")
	}
	if oracle(expected, "WRONG") == nil {
		t.Fatal("wrong expectation survived")
	}
	if oracle(expected+" extra", expected) == nil {
		t.Fatal("substring oracle")
	}
}
func TestStructuredReplyAdapters(t *testing.T) {
	for _, tc := range []struct{ id, body string }{{"claude", `{"result":"OK"}`}, {"gemini", `{"response":"OK"}`}, {"qwen", `[{"type":"result","result":"OK"}]`}, {"kimi", `{"role":"assistant","content":"OK"}`}, {"codex", `{"type":"item.completed","item":{"type":"agent_message","text":"OK"}}`}, {"opencode", `{"type":"text","part":{"text":"OK"}}`}} {
		got, e := extract(tc.id, []byte(tc.body))
		if e != nil || strings.TrimSpace(got) != "OK" {
			t.Fatalf("%s: %q %v", tc.id, got, e)
		}
	}
}

func TestInspectionEvidenceNeverPersistsUpstreamCredential(t *testing.T) {
	b, e := inspectionEvidence([]byte(`{"method":"POST","url":"/v1/messages","body":"{hello}","headers":{"authorization":"Bearer SYNTHETIC-SECRET-138","x-api-key":"SYNTHETIC-SECRET-138"}}`))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "SYNTHETIC-SECRET-138") || strings.Contains(string(b), "headers") {
		t.Fatal("upstream credential persisted by evidence collector")
	}
	if !strings.Contains(string(b), "/v1/messages") || !strings.Contains(string(b), "{hello}") {
		t.Fatal("request oracle evidence lost")
	}
}

func TestAuditEvidenceDoesNotRetainGoogleCredential(t *testing.T) {
	b, e := auditEvidence([]byte(`{"method":"POST","url":"/v1beta/models/x","status":200,"headers":{"X-Goog-Api-Key":["SYNTHETIC-SECRET-138"]}}` + "\n"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(b), "SYNTHETIC-SECRET-138") || strings.Contains(string(b), "headers") {
		t.Fatal("upstream credential in released mock audit artifact")
	}
	if !strings.Contains(string(b), `"status":200`) {
		t.Fatal("audit oracle lost")
	}
}

func TestCompositeProfileIsConsumedBeforePortBinding(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(t.TempDir(), "profile")
	m, e := install.LoadManifest()
	if e != nil {
		t.Fatal(e)
	}
	_, e = launch.Configure(m, launch.Options{Provider: "claude", Backend: "mock", BaseURL: "http://127.0.0.1:8080", Model: "mock-agent", CredentialEnv: "PROVIDER_MOCK_KEY", ProfileDir: dir, InstallRoot: root})
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("LMINSTALL_ROOT", root)
	t.Setenv("LMINSTALL_PROFILE_DIR", dir)
	t.Setenv("LMINSTALL_PROVIDER", "claude")
	t.Setenv("LMINSTALL_BACKEND", "mock")
	t.Setenv("LMINSTALL_MODEL", "mock-agent")
	if e = validateActionProfile(dir, "claude", "mock-agent"); e != nil {
		t.Fatal(e)
	}
	t.Setenv("LMINSTALL_MODEL", "WRONG")
	if validateActionProfile(dir, "claude", "mock-agent") == nil {
		t.Fatal("wrong Action export silently replaced by CLI configuration")
	}
	t.Setenv("LMINSTALL_MODEL", "mock-agent")
	other := t.TempDir()
	t.Setenv("LMINSTALL_PROFILE_DIR", other)
	if validateActionProfile(dir, "claude", "mock-agent") == nil {
		t.Fatal("unused Action profile accepted")
	}
	t.Setenv("LMINSTALL_PROFILE_DIR", dir)
	if e = os.Remove(filepath.Join(dir, "profile.json")); e != nil {
		t.Fatal(e)
	}
	if validateActionProfile(dir, "claude", "mock-agent") == nil {
		t.Fatal("missing Action profile accepted")
	}
}
