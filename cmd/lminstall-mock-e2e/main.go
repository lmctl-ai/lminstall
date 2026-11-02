// Real provider qualification against released lmmock; no provider emulators,
// process deadlines, sleeps or output truncation.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const expected = "LMINSTALL_CONFIGURE_OK_138"
const prompt = "Reply with exactly " + expected + ". Do not call any tools."

type settings struct{ Binary, Program string }

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "mock_qualification_failed:", e)
		os.Exit(1)
	}
}
func run() error {
	provider := flag.String("provider", "", "provider")
	tool := flag.String("lminstall", "", "native lminstall")
	settingsPath := flag.String("settings", "", "released fixture settings")
	out := flag.String("out", "artifacts", "evidence")
	prepareDir := flag.String("prepare", "", "download released fixture into absolute directory")
	compiler := flag.String("compiler", "", "private compiler JavaScript entry")
	child := flag.Bool("child", false, "mock child")
	flag.Parse()
	if *prepareDir != "" {
		return prepare(*prepareDir, *compiler)
	}
	root, e := filepath.Abs(*out)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		return e
	}
	if !*child {
		b, e := os.ReadFile(*settingsPath)
		if e != nil {
			return e
		}
		var s settings
		if e = json.Unmarshal(b, &s); e != nil {
			return e
		}
		config := filepath.Join(root, "mock-config.json")
		b, _ = json.Marshal(map[string]string{"text": expected})
		os.WriteFile(config, b, 0600)
		exe, _ := os.Executable()
		args := []string{"exec", "--lmcode", s.Program, "--config", config, "--audit-log", filepath.Join(root, "audit.jsonl"), "--", exe, "--child", "--provider", *provider, "--lminstall", *tool, "--out", root}
		cmd := exec.Command(s.Binary, args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		runErr := cmd.Run()
		auditPath := filepath.Join(root, "audit.jsonl")
		b, e = os.ReadFile(auditPath)
		if e == nil {
			clean, err := auditEvidence(b)
			if err != nil {
				os.Remove(auditPath)
				return err
			}
			if err = os.WriteFile(auditPath, clean, 0600); err != nil {
				os.Remove(auditPath)
				return err
			}
		}
		if runErr != nil {
			return runErr
		}
		b, e = os.ReadFile(auditPath)
		if e != nil {
			return e
		}
		posts := 0
		for _, line := range bytes.Split(b, []byte("\n")) {
			var row map[string]any
			if json.Unmarshal(line, &row) == nil && row["method"] == "POST" && row["status"] == float64(200) {
				posts++
			}
		}
		if posts == 0 {
			return errors.New("no_successful_real_POST")
		}
		fmt.Printf("{\"provider\":%q,\"audit_posts\":%d}\n", *provider, posts)
		return nil
	}
	base := os.Getenv("LMMOCK_URL")
	if !strings.HasPrefix(base, "http://127.0.0.1:") {
		return errors.New("loopback_fixture_required")
	}
	model := "mock-agent"
	if *provider == "gemini" {
		model = "gemini-2.5-flash"
	}
	if *provider == "agy" {
		model = "gemini-3.8-flash"
	}
	profile := filepath.Join(root, "profile")
	configure := exec.Command(*tool, "configure", *provider, "--backend", "mock", "--base-url", base, "--model", model, "--credential-env", "PROVIDER_MOCK_KEY", "--profile-dir", profile, "--format", "plan")
	plan, e := configure.Output()
	if e != nil {
		return fmt.Errorf("configure_failed: %w", e)
	}
	os.WriteFile(filepath.Join(root, "plan.json"), plan, 0600)
	userArgs := map[string][]string{
		"claude": {"--no-session-persistence", "--tools", "", "-p", prompt, "--output-format", "json"},
		"codex":  {"exec", "--skip-git-repo-check", "--json", prompt},
		"gemini": {"-p", prompt, "--output-format", "json"}, "qwen": {"-p", prompt, "--output-format", "json"},
		"copilot":  {"-p", prompt, "--silent", "--no-ask-user"},
		"opencode": {"run", "--format", "json", prompt}, "lmplayer": {"run", "--format", "json", prompt},
		"pi": {"--no-session", "--no-tools", "-p", prompt}, "kimi": {"-p", prompt, "--output-format", "stream-json"},
		"miniplayer": {prompt}, "agy": {"-p", prompt}, "hermes": {"-Q", "-q", prompt}, "dsh": {"headless", prompt},
	}[*provider]
	if len(userArgs) == 0 {
		return errors.New("unqualified_provider")
	}
	args := append([]string{"run", *provider, "--profile-dir", profile, "--"}, userArgs...)
	cmd := exec.Command(*tool, args...)
	cmd.Env = append(os.Environ(), "PROVIDER_MOCK_KEY=synthetic-mock-key-129")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	e = cmd.Run()
	os.WriteFile(filepath.Join(root, "stdout.txt"), stdout.Bytes(), 0600)
	os.WriteFile(filepath.Join(root, "stderr.txt"), stderr.Bytes(), 0600)
	if e != nil {
		return fmt.Errorf("run_failed: %w; diagnostic=%s", e, stderr.String())
	}
	reply, e := extract(*provider, stdout.Bytes())
	if e != nil {
		return e
	}
	if e = oracle(reply, expected); e != nil {
		return e
	}
	if oracle(reply, "DELIBERATELY_WRONG_138") == nil {
		return errors.New("wrong_expectation_control_survived")
	}
	response, e := http.Get(base + "/__inspect")
	if e != nil {
		return e
	}
	b, e := io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil {
		return e
	}
	evidence, err := inspectionEvidence(b)
	if err != nil {
		return err
	}
	os.WriteFile(filepath.Join(root, "request.json"), evidence, 0600)
	var observed struct{ Method, URL, Body string }
	if e = json.Unmarshal(b, &observed); e != nil {
		return e
	}
	paths := map[string]string{"claude": "/v1/messages", "dsh": "/v1/messages", "codex": "/v1/responses", "miniplayer": "/v1/responses", "gemini": "/v1beta/models/", "agy": "/v1beta/models/"}
	path := paths[*provider]
	if path == "" {
		path = "/v1/chat/completions"
	}
	if observed.Method != "POST" || !strings.HasPrefix(observed.URL, path) || !strings.Contains(observed.Body, prompt) {
		return fmt.Errorf("request_oracle_failed: %s %s", observed.Method, observed.URL)
	}
	if *provider == "gemini" || *provider == "agy" {
		if !strings.Contains(observed.URL, "/models/"+model+":") {
			return errors.New("requested_model_missing")
		}
	} else {
		var payload struct{ Model string }
		if json.Unmarshal([]byte(observed.Body), &payload) != nil || payload.Model != model {
			return errors.New("requested_model_mismatch")
		}
	}
	result := map[string]any{"provider": *provider, "exit": 0, "reply": reply, "control": "CAUGHT wrong-expectation", "configured_with": "lminstall configure/run", "profile": profile}
	b, _ = json.MarshalIndent(result, "", "  ")
	os.WriteFile(filepath.Join(root, "result.json"), b, 0600)
	fmt.Println(string(b))
	return nil
}
func extract(provider string, b []byte) (string, error) {
	if provider == "claude" || provider == "gemini" {
		var row map[string]any
		if e := json.Unmarshal(b, &row); e != nil {
			return "", e
		}
		key := "result"
		if provider == "gemini" {
			key = "response"
		}
		s, ok := row[key].(string)
		if !ok {
			return "", errors.New("missing_reply")
		}
		return s, nil
	}
	if provider == "qwen" {
		var rows []map[string]any
		if e := json.Unmarshal(b, &rows); e != nil {
			return "", e
		}
		answer := ""
		count := 0
		for _, r := range rows {
			if r["type"] == "result" && r["is_error"] != true {
				answer, _ = r["result"].(string)
				count++
			}
		}
		if count != 1 {
			return "", errors.New("result_count")
		}
		return answer, nil
	}
	if provider == "codex" || provider == "kimi" || provider == "opencode" || provider == "lmplayer" {
		answer := ""
		for _, line := range bytes.Split(b, []byte("\n")) {
			var r map[string]any
			if json.Unmarshal(line, &r) != nil {
				continue
			}
			switch provider {
			case "codex":
				if r["type"] == "item.completed" {
					item, _ := r["item"].(map[string]any)
					if item["type"] == "agent_message" {
						s, _ := item["text"].(string)
						answer += s
					}
				}
			case "kimi":
				if r["role"] == "assistant" {
					s, _ := r["content"].(string)
					answer += s
				}
			default:
				if r["type"] == "text" {
					part, _ := r["part"].(map[string]any)
					s, _ := part["text"].(string)
					answer += s
				}
			}
		}
		return answer, nil
	}
	return strings.TrimSpace(string(b)), nil
}

func oracle(observed, wanted string) error {
	if observed != wanted {
		return errors.New("wrong_reply")
	}
	return nil
}

func inspectionEvidence(b []byte) ([]byte, error) {
	// The mock inspect route sees upstream authentication. Retain only fields
	// needed for the route/model/prompt oracle; never persist its headers.
	var evidence struct {
		Method string `json:"method"`
		URL    string `json:"url"`
		Body   string `json:"body"`
	}
	if e := json.Unmarshal(b, &evidence); e != nil {
		return nil, e
	}
	return json.Marshal(evidence)
}

func auditEvidence(b []byte) ([]byte, error) {
	// Released lmmock masks several auth headers, but not x-goog-api-key.
	// Whitelist only the request facts used by this oracle before upload.
	var out bytes.Buffer
	for _, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row struct {
			Method string `json:"method"`
			URL    string `json:"url"`
			Status int    `json:"status"`
		}
		if e := json.Unmarshal(line, &row); e != nil {
			return nil, e
		}
		if e := json.NewEncoder(&out).Encode(row); e != nil {
			return nil, e
		}
	}
	return out.Bytes(), nil
}
