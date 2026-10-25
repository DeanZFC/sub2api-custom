package vmbridge

import (
	"io"
	"net/http"
	"strings"
)

// Copy the negotiated wire stream, including interrupt/control/close frames.
// No message parser or turn serialization is inserted between the two peers.
func serveUpgradedTunnel(w http.ResponseWriter, r *http.Request, response *http.Response) {
	upstream, ok := response.Body.(io.ReadWriteCloser)
	if !ok || !strings.EqualFold(response.Header.Get("Upgrade"), "websocket") {
		writeError(w, bridgeError(502, "invalid_upstream_upgrade", "upstream returned an invalid upgrade"))
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, bridgeError(500, "upgrade_not_supported", "WebSocket requires HTTP/1.1"))
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	defer func() { _ = upstream.Close() }()
	headers := make(http.Header)
	headers.Set("Connection", "Upgrade")
	headers.Set("Upgrade", "websocket")
	for _, name := range []string{"Sec-WebSocket-Accept", "Sec-WebSocket-Protocol", "Sec-WebSocket-Extensions", "X-Codex-Turn-State", "X-Request-Id"} {
		if values := response.Header.Values(name); len(values) > 0 {
			headers[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
	}
	if _, err = buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\n"); err != nil {
		return
	}
	if err = headers.Write(buffered); err != nil {
		return
	}
	if _, err = buffered.WriteString("\r\n"); err != nil {
		return
	}
	if err = buffered.Flush(); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, buffered.Reader); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
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
