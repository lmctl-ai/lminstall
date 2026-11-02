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
