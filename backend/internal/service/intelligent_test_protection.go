package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const intelligentTestIdentityKey = "intelligent_test_identity"

type intelligentTestIdentity struct {
	sessionID string
	threadID  string
}

// Connectivity probes use the same account-scoped identity projection as the
// gateway. This is deliberately a no-op for accounts whose protection switch
// is off, so existing test traffic stays byte-for-byte compatible.
func prepareIntelligentTestProtection(c *gin.Context, account *Account, payload map[string]any) error {
	if c == nil || c.Request == nil || account == nil || !account.IdentityProtectionEnabled() {
		return nil
	}
	if err := validateRegisteredAntiDegrade(account); err != nil {
		return err
	}
	if !isOpenAIOAuthLike(account) || account.GetCodexFingerprintMode() == codexFingerprintOff {
		return nil
	}
	original, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	stageMode1Request(c, account, original)
	rawSession := uuid.NewString()
	ids := resolveCodexFingerprintIDs(account, rawSession, account.GetCodexFingerprintMode())
	if ids == nil {
		ids = resolveCodexFingerprintIDsFromRequest(account, http.Header{"Session-Id": []string{rawSession}})
	}
	stageCodexFingerprintIDs(c, ids)
	identity := intelligentTestIdentity{
		sessionID: isolateOpenAIUpstreamSessionID(0, account, rawSession),
		threadID:  scopeCodexAccountIdentityValue(account, 0, "thread", rawSession),
	}
	if ids != nil && ids.mode != codexFingerprintDevice && ids.sessionID != "" {
		identity.sessionID = ids.sessionID
		if ids.threadID != "" {
			identity.threadID = ids.threadID
		}
	}
	c.Request.Header.Set("session-id", rawSession)
	c.Set(intelligentTestIdentityKey, identity)
	applyStagedCodexFingerprintClientMetadata(c, account, payload)
	metadata, _ := payload["client_metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		payload["client_metadata"] = metadata
	}
	metadata["session_id"] = identity.sessionID
	metadata["thread_id"] = identity.threadID
	return nil
}

// Apply protection after account header overrides, matching the normal
// gateway's final outbound construction order, then validate the exact bytes
// sent by the probe.
func applyIntelligentTestProtection(c *gin.Context, account *Account, headers http.Header, payload []byte) error {
	if c == nil {
		return nil
	}
	value, exists := c.Get(intelligentTestIdentityKey)
	identity, ok := value.(intelligentTestIdentity)
	if !exists || !ok {
		return nil
	}
	applyStagedCodexFingerprintHeaders(c, account, headers)
	headers.Set("conversation_id", identity.sessionID)
	headers.Set("thread-id", identity.threadID)
	return validateMode1StagedRequest(c, account, payload)
}

func prepareIntelligentTestProtectionBytes(c *gin.Context, account *Account, raw []byte) ([]byte, error) {
	if c == nil || c.Request == nil || account == nil || !account.IdentityProtectionEnabled() {
		return raw, nil
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return raw, nil
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return raw, nil
	}
	if err := prepareIntelligentTestProtection(c, account, payload); err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}
