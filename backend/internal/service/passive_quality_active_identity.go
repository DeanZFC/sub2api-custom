package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Recovery has a private session per batch. Existing account fingerprint
// headers stay authoritative; only otherwise absent session headers are set.
// Reuse the first prepared Anthropic identity rather than minting a new one
// for every request or overriding the account's device/account fields.
func applyPassiveRecoveryRequestIdentity(req *http.Request, session string) error {
	if session == "" {
		return nil
	}
	// AWS signing authenticates the payload; do not modify a signed body.
	if strings.HasPrefix(req.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
		return nil
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	for _, key := range []string{"session-id", "session_id"} {
		if req.Header.Get(key) == "" {
			req.Header.Set(key, session)
		}
	}
	if req.Body == nil {
		return nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}
	_ = req.Body.Close()
	var payload map[string]any
	if err = json.Unmarshal(body, &payload); err != nil {
		return err
	}
	changed := false
	// Keep optional provider parameters exactly as the protocol builder chose.
	// A stable prefix/session does not imply prompt_cache_key is supported.
	if metadata, ok := payload["metadata"].(map[string]any); ok {
		if userID, ok := metadata["user_id"].(string); ok {
			if recovery, ok := req.Context().Value(passiveRecoveryRequestKey{}).(*passiveRecoveryRequest); ok && recovery != nil {
				recovery.identityMu.Lock()
				if recovery.metadataUserID == "" {
					recovery.metadataUserID = userID
				}
				metadata["user_id"] = recovery.metadataUserID
				recovery.identityMu.Unlock()
				changed = true
			}
		}
	}
	if changed {
		body, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	return nil
}
