package launch

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

type bridge struct {
	server    *http.Server
	listener  net.Listener
	url       string
	transport *http.Transport
}

func startBridge(ctx context.Context, p Profile, credential, placeholder string, patterns [][]byte) (*bridge, error) {
	target, e := url.Parse(p.BaseURL)
	if e != nil {
		return nil, errors.New("invalid_profile_base")
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return nil, errors.New("loopback_listener_failed")
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true, DisableCompression: true}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.Host = target.Host
		req.Header.Del("Cookie")
		req.Header.Set("Accept-Encoding", "identity")
		for _, name := range p.Spec.RemoveHeaders {
			req.Header.Del(name)
		}
		for name, value := range p.Spec.Headers {
			req.Header.Set(name, strings.ReplaceAll(value, "{credential}", credential))
		}
		query := req.URL.Query()
		if query.Has("key") {
			query.Del("key")
			req.URL.RawQuery = query.Encode()
		}
	}
	proxy.Transport = transport
	proxy.FlushInterval = -1
	proxy.ErrorLog = log.New(io.Discard, "", 0)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "upstream_request_failed", http.StatusBadGateway)
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
			resp.Body.Close()
			return errors.New("unsupported_upstream_content_encoding")
		}
		resp.Header.Del("Set-Cookie")
		resp.Header.Del("Content-Length")
		resp.ContentLength = -1
		for name, values := range resp.Header {
			for i, v := range values {
				values[i] = redactString(v, patterns)
			}
			resp.Header[name] = values
		}
		original := resp.Body
		r, w := io.Pipe()
		resp.Body = r
		go func() {
			filter := redactor(w, patterns)
			_, err := io.Copy(filter, original)
			closeErr := filter.Close()
			original.Close()
			if err == nil {
				err = closeErr
			}
			w.CloseWithError(err)
		}()
		return nil
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Discovery without authentication is allowed only without upstream credential
		// binding; all billable/action requests need this invocation's random token.
		authorized := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ") == placeholder || req.Header.Get("x-api-key") == placeholder || req.Header.Get("x-goog-api-key") == placeholder || req.URL.Query().Get("key") == placeholder
		if !authorized {
			if req.Method == "GET" {
				http.NotFound(w, req)
			} else {
				http.Error(w, "invocation_auth_required", http.StatusUnauthorized)
			}
			return
		}
		proxy.ServeHTTP(w, req.WithContext(ctx))
	})
	server := &http.Server{Handler: handler}
	b := &bridge{server: server, listener: listener, url: "http://" + listener.Addr().String(), transport: transport}
	go server.Serve(listener)
	return b, nil
}
func (b *bridge) Close() { b.server.Close(); b.listener.Close(); b.transport.CloseIdleConnections() }
