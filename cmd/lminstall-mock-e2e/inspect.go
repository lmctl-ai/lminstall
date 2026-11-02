package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

type gap struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
	Kind   string `json:"kind"`
}

// coverageGaps lists requests the real client made that the mock did not serve
// as a normal mocked 2xx/3xx response. These are the routes to add to lmmock.
func coverageGaps(audit []byte) []gap {
	var out []gap
	seen := map[string]bool{}
	for _, line := range bytes.Split(audit, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row struct {
			Kind, Method, URL string
			Status            int
		}
		if json.Unmarshal(line, &row) != nil {
			continue
		}
		path := row.URL
		if u, e := url.Parse(row.URL); e == nil {
			path = u.Path
		}
		if strings.HasPrefix(path, "/__") || (row.Kind == "mocked" && row.Status < 400) {
			continue
		}
		key := fmt.Sprint(row.Method, path, row.Status)
		if !seen[key] {
			seen[key] = true
			out = append(out, gap{row.Method, path, row.Status, row.Kind})
		}
	}
	return out
}

// startInspector puts lminspect (a recording reverse proxy) between the real
// CLI and the mock. Flows are written as JSONL for protocol discovery.
func startInspector(binary, upstream, record string) (func(), string, error) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return nil, "", e
	}
	addr := l.Addr().String()
	l.Close()
	cmd := exec.Command(binary, "run", "--listen", addr, "--mode", "reverse:"+upstream, "--record", record)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if e = cmd.Start(); e != nil {
		return nil, "", e
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	stop := func() { cmd.Process.Kill(); <-exited }
	for {
		select {
		case <-exited:
			return nil, "", fmt.Errorf("lminspect exited before listening")
		default:
		}
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			return stop, "http://" + addr, nil
		}
	}
}
