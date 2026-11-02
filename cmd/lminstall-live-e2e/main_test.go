package main

import "testing"

func TestLiveOracleRejectsExtraAndError(t *testing.T) {
	for _, body := range []string{`{"result":"WRONG"}`, `{"result":"LMINSTALL_CLOUD_OK_138 extra"}`, `{"result":"LMINSTALL_CLOUD_OK_138","is_error":true}`, `not-json`} {
		if valid([]byte(body)) {
			t.Fatal("false success")
		}
	}
	if !valid([]byte(`{"result":"LMINSTALL_CLOUD_OK_138","is_error":false}`)) {
		t.Fatal("positive rejected")
	}
}
