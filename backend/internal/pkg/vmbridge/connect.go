package vmbridge

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// Exact names only: no wildcard, IP literal, arbitrary port, or generic relay.
var allowedConnectHosts = map[string]bool{
	"api.openai.com": true, "auth.openai.com": true, "chatgpt.com": true, "chat.openai.com": true, "platform.openai.com": true,
}

func allowedConnectTarget(target string) bool {
	host, port, err := net.SplitHostPort(target)
	if err != nil || port != "443" || net.ParseIP(host) != nil {
		return false
	}
	return allowedConnectHosts[strings.ToLower(host)]
}
func parseUpstreamProxy(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, nil
	}
	if strings.TrimSpace(raw) != raw || len(raw) > 4096 {
		return nil, errors.New("invalid proxy")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("invalid proxy")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("unsupported proxy")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port <= 0 || port > 65535 {
			return nil, errors.New("invalid proxy port")
		}
	}
	return u, nil
}

func (m *Manager) serveConnect(w http.ResponseWriter, r *http.Request) {
	if !allowedConnectTarget(r.Host) {
		writeError(w, bridgeError(403, "target_denied", "CONNECT target is not allowed"))
		return
	}
	const prefix = "Basic "
	raw := r.Header.Get("Proxy-Authorization")
	if !strings.HasPrefix(raw, prefix) {
		w.Header().Set("Proxy-Authenticate", "Basic realm=\"vm2api-bridge\"")
		writeError(w, bridgeError(407, "proxy_auth_required", "proxy authentication required"))
		return
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, prefix))
	if err != nil {
		writeError(w, bridgeError(407, "proxy_auth_required", "proxy authentication required"))
		return
	}
	username, password, ok := strings.Cut(string(decoded), ":")
	id, parseErr := strconv.ParseInt(username, 10, 64)
	if !ok || parseErr != nil || id <= 0 || strconv.FormatInt(id, 10) != username || !constantEqual(password, ProxyPassword(m.cfg.Secret, id)) {
		writeError(w, bridgeError(407, "proxy_auth_required", "proxy authentication required"))
		return
	}
	_, _, proxyURL, release, err := m.acquire(id)
	if err != nil {
		writeError(w, err)
		return
	}
	defer release()
	dialCtx, cancel := context.WithTimeout(r.Context(), m.cfg.DialTimeout)
	defer cancel()
	upstream, err := dialAccountTunnel(dialCtx, r.Host, proxyURL)
	if err != nil {
		writeError(w, bridgeError(502, "proxy_unavailable", "account exit is unavailable"))
		return
	}
	defer func() { _ = upstream.Close() }()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, bridgeError(500, "connect_not_supported", "CONNECT requires HTTP/1.1"))
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err = buffered.Flush(); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	copyStream := func(dst net.Conn, src io.Reader) { _, _ = io.Copy(dst, src); done <- struct{}{} }
	go copyStream(upstream, buffered.Reader)
	go copyStream(client, upstream)
	completed := 0
	select {
	case <-done:
		completed++
	case <-r.Context().Done():
	}
	_ = client.Close()
	_ = upstream.Close()
	for completed < 2 {
		<-done
		completed++
	}
}

// A configured upstream proxy never falls back to local networking. SOCKS sends
// the destination hostname to the proxy; HTTP uses CONNECT and verified TLS
// when talking to an HTTPS proxy. No HTTP proxy environment variables are used.
func dialAccountTunnel(ctx context.Context, target, rawProxy string) (net.Conn, error) {
	u, err := parseUpstreamProxy(rawProxy)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	if u == nil {
		return dialer.DialContext(ctx, "tcp", target)
	}
	if u.Scheme == "socks5" || u.Scheme == "socks5h" {
		address := u.Host
		if u.Port() == "" {
			address = net.JoinHostPort(u.Hostname(), "1080")
		}
		var auth *proxy.Auth
		if u.User != nil {
			password, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: password}
		}
		socks, err := proxy.SOCKS5("tcp", address, auth, dialer)
		if err != nil {
			return nil, err
		}
		contextDialer, ok := socks.(proxy.ContextDialer)
		if !ok {
			return nil, errors.New("proxy cannot honor cancellation")
		}
		return contextDialer.DialContext(ctx, "tcp", target)
	}
	address := u.Host
	if u.Port() == "" {
		port := "80"
		if u.Scheme == "https" {
			port = "443"
		}
		address = net.JoinHostPort(u.Hostname(), port)
	}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = conn.Close()
		}
	}()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	// Capture the underlying socket rather than the variable later wrapped in
	// TLS, so cancellation and that assignment cannot race.
	socket := conn
	stop := context.AfterFunc(ctx, func() { _ = socket.Close() })
	defer stop()
	if u.Scheme == "https" {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
		if err = tlsConn.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		conn = tlsConn
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if u.User != nil {
		password, _ := u.User.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(u.User.Username()+":"+password)))
	}
	if err = req.Write(conn); err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(conn, 32<<10)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("proxy CONNECT rejected with status %d", resp.StatusCode)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	_ = conn.SetDeadline(time.Time{})
	success = true
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
