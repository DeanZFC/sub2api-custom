package admin

import (
	"fmt"
	"time"
)

// CodexCredentialEntry contains parsed credentials only, with no account lookup
// or mutation. Shared-pool imports must never use the admin dedup/update path.
type CodexCredentialEntry struct {
	Index       int            `json:"index"`
	Name        string         `json:"name"`
	Credentials map[string]any `json:"credentials,omitempty"`
	Extra       map[string]any `json:"extra,omitempty"`
	ExpiresAt   *time.Time     `json:"expires_at,omitempty"`
	Error       string         `json:"error,omitempty"`
}

func ParseCodexCredentials(content, name string) ([]CodexCredentialEntry, error) {
	req := CodexSessionImportRequest{Content: content, Name: name}
	entries, err := parseCodexSessionImportEntries(req)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 || len(entries) > 100 {
		return nil, fmt.Errorf("每次请输入 1 到 100 个账号")
	}
	result := make([]CodexCredentialEntry, 0, len(entries))
	seen := map[string]codexSeenIdentity{}
	for _, entry := range entries {
		parsed := CodexCredentialEntry{Index: entry.Index}
		item, err := normalizeCodexImportEntry(entry)
		if err != nil {
			parsed.Error = err.Error()
			result = append(result, parsed)
			continue
		}
		parsed.Name = buildCodexCreateAccountName(name, item, entry.Index, len(entries))
		if !item.IsAgentIdentity && item.RefreshToken == "" && item.TokenExpiresAt == nil {
			parsed.Error = "无法确定令牌过期时间，请导入包含 refresh_token 或有效过期时间的 auth.json"
			result = append(result, parsed)
			continue
		}
		expiry, credentialExpiry, _, _, err := resolveCodexImportExpiry(req, item)
		if err != nil {
			parsed.Error = err.Error()
			result = append(result, parsed)
			continue
		}
		if duplicate, ok := firstSeenCodexIdentity(seen, item.IdentityKeys, item.UserID); ok {
			parsed.Error = fmt.Sprintf("与第 %d 条导入项重复", duplicate)
			result = append(result, parsed)
			continue
		}
		markCodexIdentitySeen(seen, item.IdentityKeys, entry.Index, item.UserID)
		if credentialExpiry != nil {
			item.Credentials["expires_at"] = credentialExpiry.Format(time.RFC3339)
		}
		if expiry != nil {
			value := time.Unix(*expiry, 0).UTC()
			parsed.ExpiresAt = &value
		}
		parsed.Credentials, parsed.Extra = item.Credentials, item.Extra
		result = append(result, parsed)
	}
	return result, nil
}
