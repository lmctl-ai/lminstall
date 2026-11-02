// One explicit live qualification. Raw provider output stays in RAM and is
// never included in error messages or artifacts.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const reply = "LMINSTALL_CLOUD_OK_138"

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "live_qualification_failed:", e)
		os.Exit(1)
	}
}
func valid(body []byte) bool {
	var r struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}
	return json.Unmarshal(body, &r) == nil && !r.IsError && r.Result == reply
}
func run() error {
	tool := flag.String("lminstall", "lminstall", "native installer")
	invalid := flag.Bool("invalid-auth", false, "synthetic bearer must fail")
	flag.Parse()
	credential := os.Getenv("OLLAMA_API_KEY")
	if *invalid {
		credential = "synthetic-invalid-wp138"
	} else if credential == "" {
		return errors.New("credential_missing")
	}
	root, e := os.MkdirTemp("", "lminstall-live-")
	if e != nil {
		return errors.New("scratch_failed")
	}
	defer os.RemoveAll(root)
	profile := filepath.Join(root, "profile")
	configure := exec.Command(*tool, "configure", "claude", "--backend", "ollama-cloud", "--base-url", "https://ollama.com", "--model", "glm-5.3-flash", "--credential-env", "WP138_LIVE_KEY", "--profile-dir", profile)
	var plan bytes.Buffer
	configure.Stdout = &plan
	if configure.Run() != nil {
		return errors.New("configure_failed")
	}
	if bytes.Contains(plan.Bytes(), []byte(credential)) {
		return errors.New("credential_in_plan")
	}
	cmd := exec.Command(*tool, "run", "claude", "--profile-dir", profile, "--", "--no-session-persistence", "--tools", "", "-p", "Reply with exactly "+reply+". Do not call any tools.", "--output-format", "json")
	cmd.Env = append(os.Environ(), "WP138_LIVE_KEY="+credential)
	var out, errout bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errout
	err := cmd.Run()
	if *invalid {
		combined := bytes.ToLower(append(out.Bytes(), errout.Bytes()...))
		if err == nil || (!bytes.Contains(combined, []byte("401")) && !bytes.Contains(combined, []byte("unauthorized")) && !bytes.Contains(combined, []byte("authentication"))) {
			return errors.New("invalid_auth_control_not_rejected")
		}
		fmt.Println(`{"control":"invalid_auth_rejected"}`)
		return nil
	}
	if err != nil {
		return errors.New("provider_exit_failed")
	}
	if !valid(out.Bytes()) {
		return errors.New("exact_reply_failed")
	}
	if valid([]byte(`{"result":"WRONG"}`)) {
		return errors.New("wrong_reply_control_survived")
	}
	fmt.Println(`{"provider":"claude","backend":"ollama-cloud","reply":"` + reply + `","control":"wrong_reply_rejected"}`)
	return nil
}
