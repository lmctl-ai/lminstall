package main

import (
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
