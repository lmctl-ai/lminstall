package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var authWords = regexp.MustCompile(`(?i)(401|invalid[ _-]?(x-)?api[ _-]?key|authenticat|unauthori[sz]ed|incorrect api key|api key)`)

// invalidKeyCheck runs the same client command with a wrong credential. The mock
// (configured with an apiKey) must answer 401, and the real CLI must not produce
// the expected reply: it has to fail or print an authentication error.
func invalidKeyCheck(tool string, args []string, root string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Env = append(os.Environ(), "PROVIDER_MOCK_KEY="+wrongKey)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return errors.New("invalid_key_run_hung")
	}
	out := stdout.String() + "\n" + stderr.String()
	// Never persist the credentials, even though they are synthetic.
	evidence := strings.ReplaceAll(strings.ReplaceAll(out, wrongKey, "<wrong-key>"), validKey, "<valid-key>")
	if len(evidence) > 8000 {
		evidence = evidence[:8000]
	}
	os.WriteFile(filepath.Join(root, "invalid-key-output.txt"), []byte(evidence), 0600)
	if strings.Contains(out, expected) {
		return errors.New("wrong_key_got_real_reply")
	}
	audit, _ := os.ReadFile(filepath.Join(root, "audit.jsonl"))
	if !(bytes.Contains(audit, []byte(`"status":401`)) || bytes.Contains(audit, []byte(`"status":403`))) {
		return errors.New("mock_did_not_reject_wrong_key")
	}
	if runErr == nil && !authWords.MatchString(out) {
		return errors.New("wrong_key_not_reported_as_error")
	}
	if !authWords.MatchString(out) {
		fmt.Printf("::warning title=%s::exit failed but output has no recognizable authentication wording\n", "invalid key")
	}
	return nil
}
