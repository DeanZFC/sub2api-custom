package service

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const (
	PrismExtraKey                      = "prism"
	PrismCookieCredentialKey           = "prism_cookie"
	PrismCookieConfiguredCredentialKey = "prism_cookie_configured"
	PrismDefaultTimeoutSeconds         = 180
	PrismMaxCookieBytes                = 32 * 1024
	PrismDefaultModel                  = "gpt-5.6-sol"
	PrismAuthModeAccount               = "account"
	PrismAuthModeCookie                = "cookie"
)

// PrismAccountConfig contains routing configuration only. Authentication remains
// in account credentials and is never included in scheduler projections.
type PrismAccountConfig struct {
	Enabled              bool
	Version              int
	AuthMode             string
	ConversationActionID string
	TimeoutSeconds       int
}

// IsPrismEnabled only accepts an explicit JSON boolean. Save-time validation
// rejects malformed flags instead of accidentally opting an account in.
func (a *Account) IsPrismEnabled() bool {
	if a == nil {
		return false
	}
	config, _ := a.Extra[PrismExtraKey].(map[string]any)
	enabled, _ := config["enabled"].(bool)
	return enabled
}

// UsesPrismAccountAuth distinguishes explicitly opted-in account authentication
// from legacy Prism configurations, which continue using their saved cookies.
func (a *Account) UsesPrismAccountAuth() bool {
	config, err := a.PrismConfig()
	return err == nil && config.Enabled && config.AuthMode == PrismAuthModeAccount
}

// PrismConfig parses non-secret configuration. An empty action ID delegates to
// the Prism client's built-in default. This is safe on a scheduler projection.
func (a *Account) PrismConfig() (PrismAccountConfig, error) {
	config := PrismAccountConfig{Version: 1, AuthMode: PrismAuthModeCookie, TimeoutSeconds: PrismDefaultTimeoutSeconds}
	if a == nil {
		return config, nil
	}
	raw, exists := a.Extra[PrismExtraKey]
	if !exists {
		return config, nil
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return config, invalidPrismConfig("extra.prism must be an object")
	}
	for key := range values {
		switch key {
		case "enabled", "version", "auth_mode", "conversation_action_id", "timeout_seconds":
		default:
			// Never echo unknown names/values: a misplaced cookie may be present.
			return config, invalidPrismConfig("extra.prism contains an unsupported field; store authentication in account credentials")
		}
	}
	if raw, exists := values["enabled"]; exists {
		config.Enabled, ok = raw.(bool)
		if !ok {
			return config, invalidPrismConfig("extra.prism.enabled must be a boolean")
		}
	}
	if raw, exists := values["version"]; exists {
		version, valid := prismConfigInteger(raw)
		if !valid || version != 1 {
			return config, invalidPrismConfig("extra.prism.version must be 1")
		}
	}
	if raw, exists := values["auth_mode"]; exists {
		mode, valid := raw.(string)
		if !valid || (mode != PrismAuthModeAccount && mode != PrismAuthModeCookie) {
			return config, invalidPrismConfig("extra.prism.auth_mode must be account or cookie")
		}
		config.AuthMode = mode
	}
	if raw, exists := values["conversation_action_id"]; exists {
		actionID, valid := raw.(string)
		if !valid {
			return config, invalidPrismConfig("extra.prism.conversation_action_id must be a string")
		}
		actionID = strings.TrimSpace(actionID)
		if actionID != "" {
			if _, err := hex.DecodeString(actionID); len(actionID) != 42 || err != nil {
				return config, invalidPrismConfig("extra.prism.conversation_action_id must be empty or 42 hexadecimal characters")
			}
		}
		config.ConversationActionID = strings.ToLower(actionID)
	}
	if raw, exists := values["timeout_seconds"]; exists {
		timeout, valid := prismConfigInteger(raw)
		if !valid || timeout < 30 || timeout > 600 {
			return config, invalidPrismConfig("extra.prism.timeout_seconds must be an integer between 30 and 600")
		}
		config.TimeoutSeconds = timeout
	}
	return config, nil
}

func invalidPrismConfig(message string) error {
	return infraerrors.BadRequest("INVALID_PRISM_CONFIGURATION", message)
}

func prismConfigInteger(value any) (int, bool) {
	var n float64
	switch v := value.(type) {
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	case float64:
		n = v
	case json.Number:
		var err error
		n, err = v.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	// All currently supported values are small; bound before converting to int.
	if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n < 0 || n > 600 {
		return 0, false
	}
	return int(n), true
}

// ValidatePrismAccountConfiguration checks the effective account before a write.
// Disabled accounts do not require Prism credentials or a supported account type.
func ValidatePrismAccountConfiguration(account *Account) error {
	if account != nil {
		if _, misplaced := account.Extra[PrismCookieCredentialKey]; misplaced {
			return invalidPrismConfig("Prism cookies must be stored in credentials.prism_cookie, not extra")
		}
	}
	config, err := account.PrismConfig()
	if err != nil || !config.Enabled {
		return err
	}
	if !account.IsOpenAIOAuthLike() || account.IsCredentialShadow() || account.AccountScope == "shared" {
		return invalidPrismConfig("Prism requires an independent OpenAI OAuth or setup-token account")
	}
	if config.AuthMode == PrismAuthModeAccount {
		return validatePrismAccountAuthCredentials(account)
	}
	cookie, ok := account.Credentials[PrismCookieCredentialKey].(string)
	if !ok || strings.TrimSpace(cookie) == "" {
		return invalidPrismConfig("credentials.prism_cookie is required when Prism is enabled")
	}
	if len(cookie) > PrismMaxCookieBytes {
		return invalidPrismConfig("credentials.prism_cookie exceeds the 32768-byte limit")
	}
	for _, c := range cookie {
		if c < 0x20 || c == 0x7f {
			return invalidPrismConfig("credentials.prism_cookie must not contain control characters")
		}
	}
	request := http.Request{Header: http.Header{"Cookie": []string{cookie}}}
	var access, session bool
	for _, c := range request.Cookies() {
		if strings.TrimSpace(c.Value) == "" {
			continue
		}
		switch c.Name {
		case "prism_oai_access_token":
			access = true
		case "prism_session_token":
			session = true
		}
	}
	if !access || !session {
		return invalidPrismConfig("credentials.prism_cookie must contain non-empty prism_oai_access_token and prism_session_token cookies")
	}
	return nil
}

func validatePrismAccountAuthCredentials(account *Account) error {
	if account.IsOpenAIPersonalAccessToken() {
		return invalidPrismConfig("Prism account authentication requires OpenAI OAuth credentials; Codex personal access tokens are not supported")
	}
	access, accessExists := account.Credentials["access_token"]
	accessToken, accessValid := access.(string)
	if accessExists && !accessValid {
		return invalidPrismConfig("credentials.access_token must be a string for Prism account authentication")
	}
	refresh, refreshExists := account.Credentials["refresh_token"]
	refreshToken, refreshValid := refresh.(string)
	if refreshExists && !refreshValid {
		return invalidPrismConfig("credentials.refresh_token must be a string for Prism account authentication")
	}
	if accessToken == "" && strings.TrimSpace(refreshToken) == "" {
		return invalidPrismConfig("Prism account authentication requires credentials.access_token or credentials.refresh_token")
	}
	if account.Type == AccountTypeSetupToken && accessToken == "" {
		return invalidPrismConfig("Prism setup-token account authentication requires credentials.access_token")
	}
	if len(accessToken) > PrismMaxCookieBytes {
		return invalidPrismConfig("credentials.access_token exceeds the 32768-byte limit for Prism account authentication")
	}
	// Account access tokens become one cookie value. Reject characters that
	// net/http would otherwise quote or silently strip before transmission.
	for i := 0; i < len(accessToken); i++ {
		c := accessToken[i]
		if c < 0x21 || c > 0x7e || c == '"' || c == ',' || c == ';' || c == '\\' {
			return invalidPrismConfig("credentials.access_token contains invalid characters for Prism account authentication")
		}
	}
	return nil
}

// PrismAccountModels advertises only the confirmed default unless an operator
// explicitly supplies a model mapping. Codex's catalog is a different backend.
func PrismAccountModels(account *Account) []openai.Model {
	ids := []string{PrismDefaultModel}
	if mapping := account.GetModelMapping(); len(mapping) > 0 {
		ids = make([]string, 0, len(mapping))
		for id := range mapping {
			ids = append(ids, id)
		}
		sort.Strings(ids)
	}
	models := make([]openai.Model, 0, len(ids))
	for _, id := range ids {
		models = append(models, openai.Model{ID: id, Object: "model", Type: "model", OwnedBy: "openai", DisplayName: id})
	}
	return models
}

// prismAccountWithMergedUpdates mirrors the top-level JSONB merge performed by
// extra and bulk saves, without mutating an account returned by a repository.
func prismAccountWithMergedUpdates(account *Account, credentials, extra map[string]any) *Account {
	if account == nil {
		return nil
	}
	updated := *account
	updated.Credentials = make(map[string]any, len(account.Credentials)+len(credentials))
	for key, value := range account.Credentials {
		updated.Credentials[key] = value
	}
	for key, value := range credentials {
		updated.Credentials[key] = value
	}
	updated.Extra = make(map[string]any, len(account.Extra)+len(extra))
	for key, value := range account.Extra {
		updated.Extra[key] = value
	}
	for key, value := range extra {
		updated.Extra[key] = value
	}
	return &updated
}
