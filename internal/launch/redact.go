package launch

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
)

// Sensitive encodings are redacted at the transport and output boundary. The
// retained suffix is only a possible partial match, never an output-size cap.
func sensitiveEncodings(secret string) [][]byte {
	if secret == "" {
		return nil
	}
	set := map[string]bool{secret: true}
	percent := func(s string, lower bool) string {
		var b strings.Builder
		for _, c := range []byte(s) {
			if lower {
				fmt.Fprintf(&b, "%%%02x", c)
			} else {
				fmt.Fprintf(&b, "%%%02X", c)
			}
		}
		return b.String()
	}
	unicode := func(s string) string {
		var b strings.Builder
		for _, v := range utf16.Encode([]rune(s)) {
			fmt.Fprintf(&b, `\u%04x`, v)
		}
		return b.String()
	}
	for _, s := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), percent(secret, false), percent(secret, true)} {
		set[s] = true
		set[url.QueryEscape(s)] = true
		set[url.PathEscape(s)] = true
		b, _ := json.Marshal(s)
		set[string(b[1:len(b)-1])] = true
	}
	set[unicode(secret)] = true
	var upper strings.Builder
	for _, value := range utf16.Encode([]rune(secret)) {
		fmt.Fprintf(&upper, `\u%04X`, value)
	}
	set[upper.String()] = true
	set[strings.ToUpper(unicode(secret))] = true
	raw := []byte(secret)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		set[enc.EncodeToString(raw)] = true
	}
	set[hex.EncodeToString(raw)] = true
	set[strings.ToUpper(hex.EncodeToString(raw))] = true
	var le, be []byte
	for _, v := range utf16.Encode([]rune(secret)) {
		le = append(le, byte(v), byte(v>>8))
		be = append(be, byte(v>>8), byte(v))
	}
	set[string(le)] = true
	set[string(be)] = true
	out := [][]byte{}
	for s := range set {
		if s != "" {
			out = append(out, []byte(s))
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

type redactingWriter struct {
	mu       sync.Mutex
	writer   io.Writer
	patterns [][]byte
	pending  []byte
	err      error
}

func redactor(w io.Writer, patterns [][]byte) *redactingWriter {
	return &redactingWriter{writer: w, patterns: patterns}
}
func (w *redactingWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	w.pending = append(w.pending, b...)
	w.flush(false)
	return len(b), w.err
}
func (w *redactingWriter) flush(final bool) {
	var out []byte
	for len(w.pending) > 0 {
		found := 0
		partial := false
		for _, p := range w.patterns {
			if len(w.pending) >= len(p) && bytes.Equal(w.pending[:len(p)], p) {
				if found == 0 {
					found = len(p)
				}
			}
			if len(w.pending) < len(p) && bytes.Equal(w.pending, p[:len(w.pending)]) {
				partial = true
			}
		}
		if partial && !final {
			break
		}
		if found > 0 {
			out = append(out, []byte("[REDACTED]")...)
			w.pending = w.pending[found:]
		} else {
			out = append(out, w.pending[0])
			w.pending = w.pending[1:]
		}
	}
	if len(out) > 0 && w.err == nil {
		_, w.err = w.writer.Write(out)
	}
}
func (w *redactingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flush(true)
	return w.err
}
func redactString(s string, p [][]byte) string {
	var b bytes.Buffer
	w := redactor(&b, p)
	w.Write([]byte(s))
	w.Close()
	return b.String()
}
