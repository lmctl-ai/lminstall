package main

import "testing"

func TestRealFailureDominatesUnavailable(t *testing.T) {
	if got := run([]string{"install", "unknown", "claudemock", "--root", t.TempDir(), "--json"}); got != 1 {
		t.Fatalf("real failure hidden by unavailable provider: %d", got)
	}
}
