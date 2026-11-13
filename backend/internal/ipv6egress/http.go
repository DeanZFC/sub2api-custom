package ipv6egress

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (a *Agent) ControlHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Trust the socket peer only; forwarding headers cannot grant access.
		if !a.config.clientAllowed(r.RemoteAddr) {
			http.Error(w, "client not allowed", http.StatusForbidden)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+a.config.ControlToken)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if r.ContentLength > 4096 {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request too large"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/status":
			writeJSON(w, http.StatusOK, a.Status())
		case r.Method == http.MethodPost && r.URL.Path == "/v1/leases":
			ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			defer cancel()
			lease, err := a.Create(ctx)
			if err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "IPv6 allocation failed; inspect the private service status"})
				return
			}
			writeJSON(w, http.StatusOK, lease)
		default:
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/leases/"), "/")
			if !strings.HasPrefix(r.URL.Path, "/v1/leases/") {
				http.NotFound(w, r)
				return
			}
			if len(parts) == 2 && parts[1] == "activate" && r.Method == http.MethodPost {
				lease, err := a.Activate(parts[0])
				if err != nil {
					writeJSON(w, http.StatusConflict, map[string]string{"error": "lease unavailable or retired"})
					return
				}
				writeJSON(w, http.StatusOK, lease)
			} else if len(parts) == 1 && r.Method == http.MethodDelete {
				lease, err := a.Retire(r.Context(), parts[0])
				if err != nil {
					writeJSON(w, http.StatusConflict, map[string]string{"error": "unknown lease or storage unavailable"})
					return
				}
				writeJSON(w, http.StatusOK, lease)
			} else {
				http.NotFound(w, r)
			}
		}
	})
}

func proxyCredentials(r *http.Request) (string, string, bool) {
	value := r.Header.Get("Proxy-Authorization")
	if len(value) < 6 || !strings.EqualFold(value[:6], "Basic ") {
		return "", "", false
	}
	b, err := base64.StdEncoding.DecodeString(value[6:])
	if err != nil || len(b) > 1024 {
		return "", "", false
	}
	u, p, ok := strings.Cut(string(b), ":")
	return u, p, ok
}

func (a *Agent) ProxyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Trust the socket peer only; forwarding headers cannot grant access.
		if !a.config.clientAllowed(r.RemoteAddr) {
			http.Error(w, "client not allowed", http.StatusForbidden)
			return
		}
		u, p, ok := proxyCredentials(r)
		if !ok {
			w.Header().Set("Proxy-Authenticate", `Basic realm="ipv6-egress"`)
			http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
			return
		}
		_, source, release, ok := a.acquire(u, p)
		if !ok {
			http.Error(w, "proxy lease unavailable", http.StatusForbidden)
			return
		}
		defer release()
		if r.Method == http.MethodConnect {
			a.connect(w, r, source)
			return
		}
		if !a.config.hostAllowed(r.URL.Hostname()) {
			http.Error(w, "destination not allowed", http.StatusForbidden)
			return
		}
		if r.URL.Scheme != "http" || r.URL.Hostname() == "" || r.URL.User != nil || r.URL.Fragment != "" {
			http.Error(w, "only HTTP absolute requests or CONNECT are supported", http.StatusBadRequest)
			return
		}
		port := r.URL.Port()
		if port == "" {
			port = "80"
		}
		if port != "80" {
			http.Error(w, "HTTP destination port not allowed", http.StatusForbidden)
			return
		}
		if r.ContentLength > 32<<20 {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
		out := r.Clone(r.Context())
		out.RequestURI = ""
		out.Host = r.URL.Host
		removeHopHeaders(out.Header)
		tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(ctx context.Context, _, target string) (net.Conn, error) {
			return a.network.Connect(ctx, target, source)
		}, ResponseHeaderTimeout: 30 * time.Second, MaxResponseHeaderBytes: 64 << 10}
		defer tr.CloseIdleConnections()
		res, err := tr.RoundTrip(out)
		if err != nil {
			http.Error(w, "IPv6 upstream unavailable", http.StatusBadGateway)
			return
		}
		defer func() { _ = res.Body.Close() }()
		removeHopHeaders(res.Header)
		for key, values := range res.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	})
}

func removeHopHeaders(h http.Header) {
	for _, v := range h.Values("Connection") {
		for _, key := range strings.Split(v, ",") {
			h.Del(strings.TrimSpace(key))
		}
	}
	for _, key := range []string{"Proxy-Authorization", "Proxy-Authenticate", "Connection", "Proxy-Connection", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		h.Del(key)
	}
}

func (a *Agent) connect(w http.ResponseWriter, r *http.Request, source string) {
	if r.URL.User != nil || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		http.Error(w, "invalid CONNECT request", http.StatusBadRequest)
		return
	}
	target := r.Host
	host, _, err := net.SplitHostPort(target)
	if err != nil || !a.config.hostAllowed(host) {
		http.Error(w, "destination not allowed", http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	upstream, err := a.network.Connect(ctx, target, source)
	cancel()
	if err != nil {
		http.Error(w, "IPv6 upstream unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = upstream.Close() }()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "CONNECT unsupported", http.StatusInternalServerError)
		return
	}
	downstream, buffer, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = downstream.Close() }()
	_ = downstream.SetDeadline(time.Time{})
	if _, err = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err = buffer.Flush(); err != nil {
		return
	}
	done := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(upstream, buffer)
		if cw, ok := upstream.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	_, _ = io.Copy(downstream, upstream)
	// Upstream is finished; release a client that keeps its write side open.
	_ = downstream.Close()
	_ = upstream.Close()
	<-done
}
