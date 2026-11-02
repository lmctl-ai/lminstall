package launch

import (
	"bytes"
	"io"
	"net/http"
)

func sanitizeMetadata(headers http.Header, patterns [][]byte) {
	for name, values := range headers {
		if redactString(name, patterns) != name {
			delete(headers, name)
			continue
		}
		for i, v := range values {
			values[i] = redactString(v, patterns)
		}
	}
}

// Read upstream only when the downstream reader asks. Besides preserving stream
// backpressure, this lets HTTP trailers be sanitized synchronously after EOF,
// before ReverseProxy can copy them into the child's response metadata.
type redactingBody struct {
	source   io.ReadCloser
	output   bytes.Buffer
	filter   *redactingWriter
	finished error
	afterEOF func()
}

func newRedactingBody(source io.ReadCloser, patterns [][]byte, afterEOF func()) *redactingBody {
	body := &redactingBody{source: source, afterEOF: afterEOF}
	body.filter = redactor(&body.output, patterns)
	return body
}
func (b *redactingBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for b.output.Len() == 0 && b.finished == nil {
		var block [32 * 1024]byte
		n, e := b.source.Read(block[:])
		if n > 0 {
			if _, err := b.filter.Write(block[:n]); err != nil {
				return 0, err
			}
		}
		if e != nil {
			b.filter.Close()
			b.finished = e
			b.afterEOF()
		}
	}
	if b.output.Len() > 0 {
		return b.output.Read(p)
	}
	return 0, b.finished
}
func (b *redactingBody) Close() error { return b.source.Close() }
