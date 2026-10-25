package vmbridge

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const AccessTokenHeader = "X-VM2API-Access-Token"
const AccountIdentityHeader = "X-VM2API-ChatGPT-Account-ID"

// Handler serves only the documented management API and an allowlisted CONNECT
// proxy. It deliberately has no generic URL forwarding or credential logging.
func (m *Manager) Handler() http.Handler { return http.HandlerFunc(m.serveHTTP) }
func (m *Manager) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodConnect {
		m.serveConnect(w, r)
		return
	}
	if r.URL.Path == "/health" && r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]string{"status": "ok"})
		return
	}
	if !constantEqual(r.Header.Get("Authorization"), "Bearer "+m.cfg.Secret) {
		writeError(w, bridgeError(401, "unauthorized", "authentication required"))
		return
	}
	if r.URL.Path == "/v1/health" && r.Method == http.MethodGet && r.URL.RawQuery == "" {
		writeJSON(w, 200, map[string]string{"status": "ok"})
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 || len(parts) > 5 || parts[0] != "v1" || parts[1] != "accounts" {
		writeError(w, bridgeError(404, "not_found", "endpoint not found"))
		return
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != parts[2] {
		writeError(w, bridgeError(400, "invalid_account_id", "account id must be a positive integer"))
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, bridgeError(400, "invalid_query", "query parameters are not supported"))
		return
	}
	if len(parts) >= 4 {
		path := strings.Join(parts[3:], "/")
		if (r.Method == http.MethodPost && (path == "responses" || path == "responses/compact" || path == "images/generations" || path == "images/edits" || path == "alpha/search")) || (r.Method == http.MethodGet && path == "responses") {
			m.serveInference(w, r, id, "/v1/"+path)
			return
		}
	}
	if len(parts) == 5 {
		writeError(w, bridgeError(404, "not_found", "endpoint not found"))
		return
	}
	if len(parts) == 4 {
		switch parts[3] {
		case "identity":
			if r.Method != http.MethodPut {
				break
			}
			var identity Identity
			if !decodeBody(w, r, &identity) {
				return
			}
			status, err := m.UpdateIdentity(r.Context(), id, identity)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, 200, status)
			return
		}
		writeError(w, bridgeError(405, "method_not_allowed", "method is not supported"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		status, err := m.StatusContext(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, 200, status)
	case http.MethodPost:
		var input ProvisionRequest
		if !decodeBody(w, r, &input) {
			return
		}
		input.AccountID = id
		status, created, err := m.Provision(r.Context(), input)
		if err != nil {
			writeError(w, err)
			return
		}
		code := 200
		if created {
			code = 201
		}
		writeJSON(w, code, status)
	case http.MethodDelete:
		if err := m.Delete(r.Context(), id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, bridgeError(405, "method_not_allowed", "method is not supported"))
	}
}
func constantEqual(a, b string) bool {
	x := sha256.Sum256([]byte(a))
	y := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		writeError(w, bridgeError(400, "invalid_json", "invalid JSON or unsupported field"))
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeError(w, bridgeError(400, "invalid_json", "exactly one JSON object is required"))
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func writeError(w http.ResponseWriter, err error) {
	var be *Error
	if !errors.As(err, &be) {
		be = &Error{500, "internal_error", "request could not be completed"}
	}
	writeJSON(w, be.Status, map[string]any{"error": map[string]string{"code": be.Code, "message": be.Message}})
}

func (m *Manager) serveInference(w http.ResponseWriter, r *http.Request, id int64, path string) {
	var identity *Identity
	token, accountID := r.Header.Get(AccessTokenHeader), r.Header.Get(AccountIdentityHeader)
	if token != "" || accountID != "" {
		identity = &Identity{token, accountID}
	}
	out := r.Clone(r.Context())
	out.Header = r.Header.Clone()
	out.Header.Del("Authorization")
	out.Header.Del(AccessTokenHeader)
	out.Header.Del(AccountIdentityHeader)
	out.Header.Del("Proxy-Authorization")
	stripHopHeaders(out.Header)
	upgrade := r.Method == http.MethodGet && path == "/v1/responses" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	if upgrade {
		out.Header.Set("Connection", "Upgrade")
		out.Header.Set("Upgrade", "websocket")
	}
	out.RequestURI = ""
	out.URL.Path = path
	out.URL.RawPath = ""
	out.URL.RawQuery = ""
	out.Host = ""
	out.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	response, err := m.Forward(r.Context(), id, out, identity)
	if err != nil {
		writeError(w, err)
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusSwitchingProtocols {
		if !upgrade {
			writeError(w, bridgeError(502, "invalid_upstream_upgrade", "unexpected upstream upgrade"))
			return
		}
		serveUpgradedTunnel(w, r, response)
		return
	}
	headers := response.Header.Clone()
	stripHopHeaders(headers)
	headers.Del("Set-Cookie")
	headers.Del("Authorization")
	headers.Del(AccessTokenHeader)
	headers.Del(AccountIdentityHeader)
	for key, values := range headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(response.StatusCode)
	controller := http.NewResponseController(w)
	_ = controller.Flush()
	buf := make([]byte, 32<<10)
	for {
		n, readErr := response.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			if flushErr := controller.Flush(); flushErr != nil && !errors.Is(flushErr, http.ErrNotSupported) {
				return
			}
		}
		if readErr != nil {
			return
		}
	}
}

func stripHopHeaders(headers http.Header) {
	for _, entry := range headers.Values("Connection") {
		for _, name := range strings.Split(entry, ",") {
			headers.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
		headers.Del(name)
	}
}
